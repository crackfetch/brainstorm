package session

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// ExportOptions controls what Export reads from the live browser.
type ExportOptions struct {
	// Include selects scopes by name. Unknown scopes are ignored
	// (forward-compat with future scope identifiers).
	Include []string
	// DomainPatterns filters cookies and storage origins. Empty means
	// "export everything visible to the browser."
	DomainPatterns []string
	// BrzVersion is stamped into the bundle for debuggability.
	BrzVersion string
}

// Export reads cookies and per-origin storage from a live, attached
// browser and returns a Bundle ready to be encoded.
//
// The browser is expected to already be running against the profile
// the caller wants to export. Export does not modify profile state.
func Export(b *rod.Browser, opts ExportOptions) (*Bundle, error) {
	bundle := &Bundle{
		Version:    FormatVersion,
		ExportedAt: time.Now().UTC(),
		BrzVersion: opts.BrzVersion,
	}

	include := normalizeScopes(opts.Include)

	// --- Cookies (always cheap; gate on scope flag) -------------------
	if include[ScopeCookies] {
		// Use Storage.getCookies (browser-level) rather than
		// Network.getAllCookies (page-level) — calling
		// Network.getAllCookies on a *rod.Browser yields a CDP
		// "method not found" error because Network is a per-target
		// domain. Storage.getCookies works on the browser endpoint
		// when scoped to its BrowserContext.
		raw, err := (proto.StorageGetCookies{BrowserContextID: b.BrowserContextID}).Call(b)
		if err != nil {
			return nil, fmt.Errorf("get all cookies: %w", err)
		}
		for _, c := range raw.Cookies {
			if !MatchDomain(c.Domain, opts.DomainPatterns) {
				continue
			}
			bundle.Cookies = append(bundle.Cookies, Cookie{
				Name:     c.Name,
				Value:    c.Value,
				Domain:   c.Domain,
				Path:     c.Path,
				Secure:   c.Secure,
				HTTPOnly: c.HTTPOnly,
				SameSite: string(c.SameSite),
				Expires:  float64(c.Expires),
				Session:  c.Session,
			})
		}
		sort.Slice(bundle.Cookies, func(i, j int) bool {
			if bundle.Cookies[i].Domain != bundle.Cookies[j].Domain {
				return bundle.Cookies[i].Domain < bundle.Cookies[j].Domain
			}
			return bundle.Cookies[i].Name < bundle.Cookies[j].Name
		})
	}

	// Derive the origin set from cookies + the user's domain filter.
	origins := originsFromCookies(bundle.Cookies)

	// --- localStorage / sessionStorage (per-origin, requires nav) -----
	if include[ScopeLocalStorage] {
		bundle.LocalStorage = map[string]map[string]string{}
		for _, origin := range origins {
			data, err := readWebStorage(b, origin, "localStorage")
			if err != nil || len(data) == 0 {
				continue
			}
			bundle.LocalStorage[origin] = data
		}
	}
	if include[ScopeSessionStorage] {
		bundle.SessionStorage = map[string]map[string]string{}
		for _, origin := range origins {
			data, err := readWebStorage(b, origin, "sessionStorage")
			if err != nil || len(data) == 0 {
				continue
			}
			bundle.SessionStorage[origin] = data
		}
	}

	// IndexedDB scope is reserved (heavy, not yet implemented).
	bundle.IndexedDB = nil
	bundle.Domains = uniqueDomains(bundle.Cookies)
	return bundle, nil
}

// ImportOptions controls how Import writes state to the live browser.
type ImportOptions struct {
	// Replace clears all cookies before applying the bundle. By default
	// import is additive (overlapping name/domain/path tuples are
	// overwritten because that's how Network.setCookies behaves).
	Replace bool

	// PageForFlush, if non-nil, is used as a scratch page for
	// per-origin navigations after Storage.setCookies. Visiting each
	// host warms Chrome's per-host network context, which on some
	// builds is what causes the in-memory cookie store to flush to
	// disk. When nil, Import skips the warm-up navigations.
	// (Page-level Network.setCookies has been tried as an alternative
	// to Storage.setCookies; it does not improve cross-process
	// persistence in practice.)
	PageForFlush *rod.Page
}

// Import applies a bundle to a live, attached browser. The browser is
// expected to be running against the profile the caller wants to
// populate; rod's launcher persists cookies + storage to the profile
// dir on close, so the resulting profile will be "warm" on next use.
func Import(b *rod.Browser, bundle *Bundle, opts ImportOptions) error {
	if bundle.Version > FormatVersion {
		return fmt.Errorf("bundle version %d is newer than this brz supports (max %d)", bundle.Version, FormatVersion)
	}

	// --replace is destructive (it wipes cookies the user already
	// has). To avoid corrupting an existing profile when the
	// subsequent set step fails, snapshot the current cookies first
	// and restore them on any error after the clear.
	var restoreOnFail []*proto.NetworkCookieParam
	if opts.Replace {
		prev, err := (proto.StorageGetCookies{BrowserContextID: b.BrowserContextID}).Call(b)
		if err != nil {
			return fmt.Errorf("snapshot existing cookies before --replace: %w", err)
		}
		restoreOnFail = make([]*proto.NetworkCookieParam, 0, len(prev.Cookies))
		for _, c := range prev.Cookies {
			restoreOnFail = append(restoreOnFail, &proto.NetworkCookieParam{
				Name: c.Name, Value: c.Value, Domain: c.Domain, Path: c.Path,
				Secure: c.Secure, HTTPOnly: c.HTTPOnly,
				SameSite: c.SameSite, Expires: c.Expires,
			})
		}
		if err := (proto.StorageClearCookies{BrowserContextID: b.BrowserContextID}).Call(b); err != nil {
			return fmt.Errorf("clear cookies: %w", err)
		}
	}
	// restore is a closure used in error paths after Clear has run.
	restore := func() {
		if len(restoreOnFail) == 0 {
			return
		}
		_ = (proto.StorageSetCookies{
			Cookies: restoreOnFail, BrowserContextID: b.BrowserContextID,
		}).Call(b)
	}

	// --- Cookies ------------------------------------------------------
	//
	// We use Storage.setCookies (browser-level) rather than per-page
	// Network.setCookies because:
	//   1. It accepts a batch in one round-trip.
	//   2. It doesn't require navigating to each cookie's origin
	//      (which would be slow and could trigger anti-bot detection
	//      on a fresh, profile-level CDP attach — exactly the cold-CDP
	//      scenario session import is meant to avoid).
	// We also stamp a synthetic URL hint via the Domain/Path on each
	// param so Chrome accepts the cookie even when no page has been
	// loaded for that origin yet.
	params := make([]*proto.NetworkCookieParam, 0, len(bundle.Cookies))
	for _, c := range bundle.Cookies {
		p := &proto.NetworkCookieParam{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Secure:   c.Secure,
			HTTPOnly: c.HTTPOnly,
		}
		if c.SameSite != "" {
			p.SameSite = proto.NetworkCookieSameSite(c.SameSite)
		}
		// Session cookies omit Expires; persistent cookies keep the value.
		if !c.Session && c.Expires > 0 {
			p.Expires = proto.TimeSinceEpoch(c.Expires)
		}
		params = append(params, p)
	}
	if len(params) > 0 {
		// Stamp a synthetic URL hint so Chrome accepts cookies for
		// origins we may not have navigated to.
		for _, p := range params {
			p.URL = cookieURLHint(p)
		}
		if err := (proto.StorageSetCookies{
			Cookies:          params,
			BrowserContextID: b.BrowserContextID,
		}).Call(b); err != nil {
			restore()
			return fmt.Errorf("set cookies: %w", err)
		}
		// Force a per-origin page navigation so Chrome's network
		// context loads + persists the cookies for each host. Without
		// this, cookies set via CDP often live only in the in-memory
		// store and are dropped on profile close. We navigate
		// concurrently is not supported — rod pages serialize per
		// browser, so we walk origins sequentially.
		if opts.PageForFlush != nil {
			for _, origin := range originsFromCookieParams(params) {
				_ = opts.PageForFlush.Navigate(origin)
				_ = opts.PageForFlush.WaitLoad()
			}
		}
	}

	// --- Storage ------------------------------------------------------
	if err := writeAllStorage(b, bundle.LocalStorage, "localStorage", opts.Replace); err != nil {
		return err
	}
	if err := writeAllStorage(b, bundle.SessionStorage, "sessionStorage", opts.Replace); err != nil {
		return err
	}
	return nil
}

// originsFromCookieParams returns a deduplicated set of https://host
// origins from cookie params. Used to force a per-host navigation so
// Chrome flushes cookies to disk.
func originsFromCookieParams(params []*proto.NetworkCookieParam) []string {
	seen := map[string]struct{}{}
	for _, p := range params {
		host := strings.TrimPrefix(p.Domain, ".")
		if host == "" {
			continue
		}
		seen["https://"+host] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for o := range seen {
		out = append(out, o)
	}
	sort.Strings(out)
	return out
}

// cookieURLHint synthesizes a URL that satisfies the Domain/Secure
// constraints of a cookie param so Chrome accepts it via Network.setCookies
// without an actual page navigation. Leading-dot domains are stripped;
// secure cookies get https, others http.
func cookieURLHint(p *proto.NetworkCookieParam) string {
	host := strings.TrimPrefix(p.Domain, ".")
	scheme := "http"
	if p.Secure {
		scheme = "https"
	}
	path := p.Path
	if path == "" {
		path = "/"
	}
	return scheme + "://" + host + path
}

// --- helpers --------------------------------------------------------

func normalizeScopes(in []string) map[string]bool {
	if len(in) == 0 {
		return map[string]bool{
			ScopeCookies:        true,
			ScopeLocalStorage:   true,
			ScopeSessionStorage: true,
		}
	}
	out := make(map[string]bool, len(in))
	for _, s := range in {
		out[strings.ToLower(strings.TrimSpace(s))] = true
	}
	return out
}

func uniqueDomains(cs []Cookie) []string {
	seen := map[string]struct{}{}
	for _, c := range cs {
		d := strings.TrimPrefix(c.Domain, ".")
		if d == "" {
			continue
		}
		seen[d] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// originsFromCookies derives an https://<host> origin per unique
// cookie domain. We assume https because every site that warrants a
// session bundle in 2026 is on TLS, and Secure cookies require it.
func originsFromCookies(cs []Cookie) []string {
	seen := map[string]struct{}{}
	for _, c := range cs {
		d := strings.TrimPrefix(c.Domain, ".")
		if d == "" {
			continue
		}
		seen["https://"+d] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for o := range seen {
		out = append(out, o)
	}
	sort.Strings(out)
	return out
}

// readWebStorage navigates to origin and serializes the chosen
// Storage object. Returns an empty map (not an error) if storage is
// unavailable for the origin.
func readWebStorage(b *rod.Browser, origin, which string) (map[string]string, error) {
	page, err := b.Page(proto.TargetCreateTarget{URL: origin})
	if err != nil {
		return nil, err
	}
	defer page.Close()
	if err := page.WaitLoad(); err != nil {
		// Best-effort: if the page didn't load (offline, blocked) we
		// just skip storage for this origin rather than fail the whole
		// export.
		return nil, nil
	}
	js := fmt.Sprintf(`() => { try { const s = window.%s; const o = {}; for (let i = 0; i < s.length; i++) { const k = s.key(i); o[k] = s.getItem(k); } return JSON.stringify(o); } catch (e) { return "{}"; } }`, which)
	res, err := page.Eval(js)
	if err != nil {
		return nil, nil
	}
	var data map[string]string
	if err := json.Unmarshal([]byte(res.Value.Str()), &data); err != nil {
		return nil, nil
	}
	return data, nil
}

func writeAllStorage(b *rod.Browser, data map[string]map[string]string, which string, replace bool) error {
	for origin, kv := range data {
		if err := writeWebStorage(b, origin, which, kv, replace); err != nil {
			// Continue on error: a single broken origin shouldn't
			// fail a bulk import. Diagnostics go to stderr so they
			// never contaminate stdout, which the caller may be
			// piping into another tool.
			fmt.Fprintf(os.Stderr, "session import: warning: write %s for %s: %v\n", which, origin, err)
		}
	}
	return nil
}

func writeWebStorage(b *rod.Browser, origin, which string, kv map[string]string, replace bool) error {
	page, err := b.Page(proto.TargetCreateTarget{URL: origin})
	if err != nil {
		return err
	}
	defer page.Close()
	if err := page.WaitLoad(); err != nil {
		return nil // skip silently — origin may be unreachable
	}
	payload, err := json.Marshal(kv)
	if err != nil {
		return err
	}
	js := fmt.Sprintf(`(payload) => { try { const s = window.%s; if (%v) s.clear(); const o = JSON.parse(payload); for (const k in o) s.setItem(k, o[k]); return true; } catch (e) { return String(e); } }`, which, replace)
	if _, err := page.Eval(js, string(payload)); err != nil {
		return err
	}
	return nil
}
