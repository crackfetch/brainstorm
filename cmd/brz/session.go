package main

// `brz session capture` — opens Chrome via the WithLoginURL flow (no CDP
// attached during the user's typing, so hCaptcha doesn't see automation),
// waits for the success URL, attaches CDP, dumps Network.getAllCookies via
// rod, and writes a portable cookie bundle to disk. After this, callers can
// drive plain HTTP with the captured cookies — no Chrome needed.
//
// This is the primitive proposed in
// https://github.com/crackfetch/brainstorm/issues/40 — a "headed-login →
// quiet-HTTP" pattern any tool can adopt without re-implementing Chrome
// cookie decryption per platform.

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/crackfetch/brainstorm/workflow"
	"github.com/go-rod/rod/lib/proto"
	"golang.org/x/term"
)

func cmdSession(args []string) {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printSessionUsage()
		os.Exit(exitWorkflowError)
	}
	switch args[0] {
	case "capture":
		cmdSessionCapture(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown session subcommand: %s\n\n", args[0])
		printSessionUsage()
		os.Exit(exitWorkflowError)
	}
}

func printSessionUsage() {
	fmt.Print(`Usage: brz session <subcommand> [flags]

Subcommands:
  capture    Run a headed login flow and dump the resulting cookies as JSON or
             Netscape (curl) format. After capture, drive plain HTTP from any
             tool using the saved cookies — no Chrome needed.

Run "brz session <subcommand> --help" for flags.
`)
}

// CookieRecord is the portable, language-agnostic cookie shape we serialize.
// Times are ISO 8601 (RFC3339); session cookies omit "expires".
type CookieRecord struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Domain   string `json:"domain"`
	Path     string `json:"path"`
	Expires  string `json:"expires,omitempty"` // RFC3339; empty = session cookie
	Secure   bool   `json:"secure"`
	HTTPOnly bool   `json:"httpOnly"`
	SameSite string `json:"sameSite,omitempty"` // "Strict", "Lax", "None", or empty
}

// SessionBundle is the on-disk JSON shape for `brz session capture --format json`.
type SessionBundle struct {
	Version    int            `json:"version"`     // 1
	CapturedAt string         `json:"captured_at"` // RFC3339
	BrzVersion string         `json:"brz_version"`
	LoginURL   string         `json:"login_url"`
	SuccessURL string         `json:"success_url"`
	Domains    []string       `json:"domains"`
	Cookies    []CookieRecord `json:"cookies"`
}

func cmdSessionCapture(args []string) {
	fs := flag.NewFlagSet("session capture", flag.ExitOnError)
	loginURL := fs.String("login", "", "Login URL — Chrome opens to this with no CDP attached.")
	successURL := fs.String("success-url", "", "Substring expected in the URL after login completes (e.g. \"example.com/admin\").")
	profileDir := fs.String("profile", "", "Chrome user-data-dir to use. Required.")
	out := fs.String("out", "", "Output path (default: stdout).")
	format := fs.String("format", "json", "Output format: json | netscape (curl-compatible).")
	domainGlob := fs.String("domain", "", "Comma-separated host globs (e.g. \"*.example.com,api.other.com\"). Empty = all cookies.")
	loginTimeout := fs.Duration("login-timeout", 5*time.Minute, "How long to wait for the user to complete login.")

	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `Usage: brz session capture --login URL --success-url SUBSTR --profile DIR [flags]

Opens a headed Chrome at the login URL with no CDP attached (so hCaptcha and
similar bot detectors don't see automation). The user logs in normally. Once
the URL contains --success-url, brz attaches CDP, calls Network.getAllCookies,
serializes the result, and exits.

Output formats:
  json      Versioned JSON (default). Includes all fields per cookie.
  netscape  Tab-separated cookies.txt usable with curl -b cookies.txt.

Filtering:
  --domain "*.tcgplayer.com"          Only cookies whose domain matches.
  --domain "tcgplayer.com,hcaptcha.com"  Multiple globs (comma-separated).

Examples:
  brz session capture \
    --login https://store.tcgplayer.com/oauth/login \
    --success-url store.tcgplayer.com/admin \
    --profile ~/.config/brz/chrome-profile \
    --domain "*.tcgplayer.com" \
    --out tcg-cookies.json

  brz session capture --login URL --success-url SUBSTR --profile DIR --format netscape > cookies.txt
  curl -b cookies.txt https://example.com/api/whatever`)
		fs.PrintDefaults()
	}
	fs.Parse(args)

	if *loginURL == "" || *successURL == "" || *profileDir == "" {
		fs.Usage()
		os.Exit(exitWorkflowError)
	}
	if *format != "json" && *format != "netscape" {
		fmt.Fprintf(os.Stderr, "unsupported --format %q (json|netscape)\n", *format)
		os.Exit(exitWorkflowError)
	}

	abs, err := filepath.Abs(*profileDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve profile path: %v\n", err)
		os.Exit(exitWorkflowError)
	}

	// Bare-bones workflow — the executor only exists for its WithLoginURL plumbing.
	// We never call RunAction, so the action map just needs to satisfy the loader.
	wf := &workflow.Workflow{
		Name: "brz-session-capture",
		Actions: map[string]workflow.Action{
			"_noop": {URL: "about:blank"},
		},
	}
	exec := workflow.NewExecutor(wf,
		workflow.WithHeaded(true),
		workflow.WithProfileDir(abs),
		workflow.WithLoginURL(*loginURL, *successURL),
	)

	if err := exec.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "launch Chrome: %v\n", err)
		os.Exit(exitBrowserError)
	}
	defer exec.Close()

	if exec.NeedsLogin() {
		fmt.Fprintln(os.Stderr, "Chrome opened to login URL. Sign in normally; this tool will detect /admin (or your --success-url) and capture cookies.")
		if err := waitForCaptureLogin(exec, abs, *loginTimeout); err != nil {
			fmt.Fprintf(os.Stderr, "login wait: %v\n", err)
			os.Exit(exitBrowserError)
		}
		if err := exec.ConnectAfterLogin(); err != nil {
			fmt.Fprintf(os.Stderr, "attach CDP: %v\n", err)
			os.Exit(exitBrowserError)
		}
	} else {
		fmt.Fprintln(os.Stderr, "Already logged in (cookies present in the profile dir). Capturing immediately.")
	}

	browser := exec.Browser()
	if browser == nil {
		fmt.Fprintln(os.Stderr, "Browser handle unavailable — CDP not connected")
		os.Exit(exitBrowserError)
	}
	rawCookies, err := browser.GetCookies()
	if err != nil {
		fmt.Fprintf(os.Stderr, "get cookies via CDP: %v\n", err)
		os.Exit(exitBrowserError)
	}
	records := convertCookies(rawCookies)
	records = filterCookies(records, *domainGlob)

	domains := uniqueDomains(records)
	bundle := SessionBundle{
		Version:    1,
		CapturedAt: time.Now().UTC().Format(time.RFC3339),
		BrzVersion: Version,
		LoginURL:   *loginURL,
		SuccessURL: *successURL,
		Domains:    domains,
		Cookies:    records,
	}

	var output []byte
	switch *format {
	case "json":
		output, err = json.MarshalIndent(bundle, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "encode JSON: %v\n", err)
			os.Exit(exitBrowserError)
		}
		output = append(output, '\n')
	case "netscape":
		output = []byte(toNetscape(bundle))
	}

	if *out == "" {
		// Cookies are auth credentials. If we're emitting to a TTY,
		// the user's scrollback / clipboard / shell-redirected file
		// (controlled by umask, often 0644) will hold them. Refuse
		// rather than silently leak.
		if term.IsTerminal(int(os.Stdout.Fd())) {
			fmt.Fprintln(os.Stderr, "refusing to dump cookies to a TTY (auth credentials). use --out FILE or pipe to a file with restrictive perms.")
			os.Exit(exitWorkflowError)
		}
		if _, err := os.Stdout.Write(output); err != nil {
			fmt.Fprintf(os.Stderr, "write to stdout: %v\n", err)
			os.Exit(exitBrowserError)
		}
	} else {
		if err := writeFile0600(*out, output); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", *out, err)
			os.Exit(exitBrowserError)
		}
		fmt.Fprintf(os.Stderr, "wrote %s — %d cookies across %d domain(s)\n", *out, len(records), len(domains))
	}
}

// writeFile0600 atomically replaces path with the given bytes, ensuring
// the resulting file has 0600 permissions even if a broader-permissioned
// file already existed at that path. os.WriteFile only applies the mode
// argument when creating the file; we want hard 0600 always.
func writeFile0600(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".brz-cookies-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op if rename succeeded
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// waitForCaptureLogin polls Chrome's HTTP debug API for a page URL that
// contains the success substring. Re-reads DevToolsActivePort each tick
// because brz can return a stale port if a previous Chrome run's file is
// still around (see brainstorm#41).
func waitForCaptureLogin(exec *workflow.Executor, profileDir string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	port := exec.DebugPort()
	portFile := filepath.Join(profileDir, "DevToolsActivePort")
	successURL := exec.LoginSuccessURL()

	httpClient := &http.Client{Timeout: 2 * time.Second}
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()

	for {
		<-tick.C
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s", timeout)
		}
		if data, err := os.ReadFile(portFile); err == nil {
			lines := strings.SplitN(string(data), "\n", 2)
			if p := strings.TrimSpace(lines[0]); p != "" {
				port = p
			}
		}
		req, err := http.NewRequest("GET", "http://127.0.0.1:"+port+"/json", nil)
		if err != nil {
			continue
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			continue
		}
		var pages []struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&pages)
		resp.Body.Close()
		for _, p := range pages {
			if p.Type == "page" && strings.Contains(p.URL, successURL) {
				return nil
			}
		}
	}
}

// convertCookies maps rod's *proto.NetworkCookie to our portable shape.
// Expires (TimeSinceEpoch float, in seconds since epoch; 0 or negative = session)
// is rendered as RFC3339 UTC, omitted for session cookies.
func convertCookies(in []*proto.NetworkCookie) []CookieRecord {
	out := make([]CookieRecord, 0, len(in))
	for _, c := range in {
		rec := CookieRecord{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Secure:   c.Secure,
			HTTPOnly: c.HTTPOnly,
			SameSite: string(c.SameSite),
		}
		if c.Expires > 0 {
			rec.Expires = time.Unix(int64(c.Expires), 0).UTC().Format(time.RFC3339)
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Domain != out[j].Domain {
			return out[i].Domain < out[j].Domain
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// filterCookies keeps cookies whose Domain matches any of the comma-separated
// globs. Empty filter returns all cookies. A leading "." in cookie domains is
// trimmed before matching (cookie convention).
func filterCookies(in []CookieRecord, globsCSV string) []CookieRecord {
	globsCSV = strings.TrimSpace(globsCSV)
	if globsCSV == "" {
		return in
	}
	globs := []string{}
	for _, g := range strings.Split(globsCSV, ",") {
		if g = strings.TrimSpace(g); g != "" {
			globs = append(globs, g)
		}
	}
	if len(globs) == 0 {
		return in
	}
	out := make([]CookieRecord, 0, len(in))
	for _, c := range in {
		host := strings.TrimPrefix(c.Domain, ".")
		for _, g := range globs {
			if matchHostGlob(host, g) {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// matchHostGlob is a tiny shell-style matcher: "*" matches any run of chars
// inside one host component, leading "*." is treated as "any subdomain or
// exact match." Strict enough for the cookie filter use case; not a generic
// glob. Returns true on match.
func matchHostGlob(host, glob string) bool {
	host = strings.ToLower(host)
	glob = strings.ToLower(glob)
	if glob == host {
		return true
	}
	if strings.HasPrefix(glob, "*.") {
		suffix := glob[1:] // ".example.com"
		if strings.HasSuffix(host, suffix) || host == suffix[1:] {
			return true
		}
	}
	if strings.Contains(glob, "*") {
		// Crude generic glob — split on "*" and require parts in order.
		parts := strings.Split(glob, "*")
		idx := 0
		for i, p := range parts {
			if p == "" {
				continue
			}
			off := strings.Index(host[idx:], p)
			if off < 0 {
				return false
			}
			if i == 0 && off != 0 {
				return false
			}
			idx += off + len(p)
		}
		if !strings.HasSuffix(glob, "*") && idx != len(host) {
			return false
		}
		return true
	}
	return false
}

func uniqueDomains(cookies []CookieRecord) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, c := range cookies {
		d := strings.TrimPrefix(c.Domain, ".")
		if _, ok := seen[d]; ok {
			continue
		}
		seen[d] = struct{}{}
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// toNetscape emits the classic cookies.txt format that curl -b reads.
// Format reference: https://curl.se/docs/http-cookies.html
//
//	domain  includeSubdomains  path  secure  expires  name  value
//
// Preserves Chrome's host scoping: a leading "." in c.Domain means the
// cookie was set as a domain cookie (apply to subdomains); a bare domain
// means host-only (apply to that exact host only). Forcing "." everywhere
// would broaden cookie scope and could leak auth cookies to sibling
// subdomains of the same registrable domain (codex review MED).
func toNetscape(b SessionBundle) string {
	var sb strings.Builder
	sb.WriteString("# Netscape HTTP Cookie File — generated by brz session capture\n")
	sb.WriteString("# https://curl.se/docs/http-cookies.html\n\n")
	for _, c := range b.Cookies {
		includeSubdomains := strings.HasPrefix(c.Domain, ".")
		var expires int64
		if c.Expires != "" {
			if t, err := time.Parse(time.RFC3339, c.Expires); err == nil {
				expires = t.Unix()
			}
		}
		fmt.Fprintf(&sb, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			c.Domain,
			boolStrTRUE(includeSubdomains),
			cookiePath(c.Path),
			boolStrTRUE(c.Secure),
			expires,
			c.Name,
			c.Value,
		)
	}
	return sb.String()
}

func boolStrTRUE(b bool) string {
	if b {
		return "TRUE"
	}
	return "FALSE"
}

func cookiePath(p string) string {
	if p == "" {
		return "/"
	}
	return p
}

