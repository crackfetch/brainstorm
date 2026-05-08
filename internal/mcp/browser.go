package mcp

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/crackfetch/brainstorm/workflow"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// BrowserOptions configure how the lazy browser is launched.
type BrowserOptions struct {
	Headed     bool
	ProfileDir string
	Debug      bool
}

// Browser wraps a single rod browser/page pair, providing a serialized API
// for tool calls. It lazy-launches Chrome on first use and can be torn down
// idempotently from any goroutine (EOF, idle timer, signal).
type Browser struct {
	mu   sync.Mutex
	opts BrowserOptions

	exec *workflow.Executor
	page *rod.Page

	closed bool
}

// NewBrowser returns an unstarted handle. Chrome is not launched until the
// first tool call that needs it (e.g. browser_goto).
func NewBrowser(opts BrowserOptions) *Browser {
	return &Browser{opts: opts}
}

// Close tears down the browser if it was started. Safe to call multiple times.
func (b *Browser) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	if b.exec != nil {
		b.exec.Close()
		b.exec = nil
		b.page = nil
	}
}

// withLock runs fn while holding the browser mutex. All tool entry points
// funnel through this so concurrent JSON-RPC requests are serialized — the
// rod browser is not safe for concurrent navigation/eval anyway.
func (b *Browser) withLock(fn func() error) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return errors.New("browser is closed")
	}
	return fn()
}

// ensurePage starts the browser (if needed) and returns the active page,
// creating a fresh blank page on first use. Caller must hold b.mu.
func (b *Browser) ensurePage() (*rod.Page, error) {
	if b.exec == nil {
		opts := []workflow.Option{
			workflow.WithHeaded(b.opts.Headed),
			workflow.WithDebug(b.opts.Debug),
		}
		if b.opts.ProfileDir != "" {
			opts = append(opts, workflow.WithProfileDir(b.opts.ProfileDir))
		}
		exec := workflow.NewExecutor(nil, opts...)
		if err := exec.Start(); err != nil {
			return nil, fmt.Errorf("launch browser: %w", err)
		}
		b.exec = exec
	}
	if b.page == nil {
		page, err := b.exec.Browser().Page(proto.TargetCreateTarget{URL: "about:blank"})
		if err != nil {
			return nil, fmt.Errorf("create page: %w", err)
		}
		b.page = page
	}
	return b.page, nil
}

// ---- Tool implementations ------------------------------------------------

// Goto navigates the active page to url. wait_for_load defaults to true.
func (b *Browser) Goto(rawURL string, waitForLoad bool) (map[string]any, error) {
	if rawURL == "" {
		return nil, errors.New("url is required")
	}
	if !strings.Contains(rawURL, "://") {
		// Help the LLM out: bare hosts get https.
		rawURL = "https://" + rawURL
	}
	if _, err := url.Parse(rawURL); err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}
	var out map[string]any
	err := b.withLock(func() error {
		page, err := b.ensurePage()
		if err != nil {
			return err
		}
		if err := page.Navigate(rawURL); err != nil {
			return fmt.Errorf("navigate: %w", err)
		}
		if waitForLoad {
			if err := page.WaitLoad(); err != nil {
				// non-fatal; report partial success
				out = map[string]any{
					"ok":          false,
					"final_url":   safePageURL(page),
					"title":       safePageTitle(page),
					"wait_error":  err.Error(),
				}
				return nil
			}
		}
		out = map[string]any{
			"ok":        true,
			"final_url": safePageURL(page),
			"title":     safePageTitle(page),
		}
		return nil
	})
	return out, err
}

// Click clicks the nth match of selector. nth defaults to 0.
func (b *Browser) Click(selector string, nth int, timeoutMs int) (map[string]any, error) {
	if selector == "" {
		return nil, errors.New("selector is required")
	}
	if nth < 0 {
		nth = 0
	}
	timeout := timeoutMsToDuration(timeoutMs, 5*time.Second)
	var out map[string]any
	err := b.withLock(func() error {
		page, err := b.ensurePage()
		if err != nil {
			return err
		}
		els, err := page.Timeout(timeout).Elements(selector)
		if err != nil {
			return fmt.Errorf("query %q: %w", selector, err)
		}
		if len(els) == 0 {
			return fmt.Errorf("no elements match %q", selector)
		}
		if nth >= len(els) {
			return fmt.Errorf("nth=%d but only %d elements match %q", nth, len(els), selector)
		}
		if err := els[nth].Click(proto.InputMouseButtonLeft, 1); err != nil {
			return fmt.Errorf("click: %w", err)
		}
		out = map[string]any{"ok": true, "matched_count": len(els)}
		return nil
	})
	return out, err
}

// Type focuses selector and types text. clear_first wipes existing input first.
// submit presses Enter after typing.
func (b *Browser) Type(selector, text string, clearFirst, submit bool) (map[string]any, error) {
	if selector == "" {
		return nil, errors.New("selector is required")
	}
	var out map[string]any
	err := b.withLock(func() error {
		page, err := b.ensurePage()
		if err != nil {
			return err
		}
		el, err := page.Element(selector)
		if err != nil {
			return fmt.Errorf("find %q: %w", selector, err)
		}
		if err := el.Focus(); err != nil {
			return fmt.Errorf("focus: %w", err)
		}
		if clearFirst {
			if err := el.SelectAllText(); err == nil {
				_ = page.Keyboard.Type('\b')
			}
		}
		if err := el.Input(text); err != nil {
			return fmt.Errorf("input: %w", err)
		}
		if submit {
			if err := page.Keyboard.Type('\n'); err != nil {
				return fmt.Errorf("submit: %w", err)
			}
		}
		out = map[string]any{"ok": true}
		return nil
	})
	return out, err
}

// Extract returns matches for selector. all=false returns at most one match.
// attribute, if set, returns the named attribute instead of text/html.
func (b *Browser) Extract(selector, attribute string, all bool) (map[string]any, error) {
	if selector == "" {
		return nil, errors.New("selector is required")
	}
	var out map[string]any
	err := b.withLock(func() error {
		page, err := b.ensurePage()
		if err != nil {
			return err
		}
		els, err := page.Elements(selector)
		if err != nil {
			return fmt.Errorf("query %q: %w", selector, err)
		}
		if !all && len(els) > 1 {
			els = els[:1]
		}
		matches := make([]map[string]any, 0, len(els))
		for _, el := range els {
			m := map[string]any{}
			if attribute != "" {
				v, _ := el.Attribute(attribute)
				if v != nil {
					m["attribute"] = *v
				} else {
					m["attribute"] = nil
				}
			}
			if t, err := el.Text(); err == nil {
				m["text"] = t
			}
			if h, err := el.HTML(); err == nil {
				m["html"] = h
			}
			matches = append(matches, m)
		}
		out = map[string]any{"matches": matches}
		return nil
	})
	return out, err
}

// Screenshot captures either the full page or a single selector.
// Returns base64-encoded PNG.
func (b *Browser) Screenshot(selector string, fullPage bool) (string, error) {
	var b64 string
	err := b.withLock(func() error {
		page, err := b.ensurePage()
		if err != nil {
			return err
		}
		if selector != "" {
			el, err := page.Element(selector)
			if err != nil {
				return fmt.Errorf("find %q: %w", selector, err)
			}
			img, err := el.Screenshot(proto.PageCaptureScreenshotFormatPng, 0)
			if err != nil {
				return fmt.Errorf("element screenshot: %w", err)
			}
			b64 = base64.StdEncoding.EncodeToString(img)
			return nil
		}
		req := &proto.PageCaptureScreenshot{
			Format:      proto.PageCaptureScreenshotFormatPng,
			CaptureBeyondViewport: fullPage,
		}
		img, err := page.Screenshot(fullPage, req)
		if err != nil {
			return fmt.Errorf("screenshot: %w", err)
		}
		b64 = base64.StdEncoding.EncodeToString(img)
		return nil
	})
	return b64, err
}

// Eval evaluates a JS expression and returns its JSON-encoded result.
//
// The expression is evaluated verbatim. To get a strict-bool, wrap it
// yourself: `Boolean(document.querySelector('a'))`. This avoids the rod
// MustEval bool gotcha by never assuming a type.
func (b *Browser) Eval(expression string) (map[string]any, error) {
	if expression == "" {
		return nil, errors.New("expression is required")
	}
	var out map[string]any
	err := b.withLock(func() error {
		page, err := b.ensurePage()
		if err != nil {
			return err
		}
		// Wrap in an IIFE so multi-line expressions work.
		wrapped := "(() => { return (" + expression + "); })()"
		res, err := page.Eval(wrapped)
		if err != nil {
			return fmt.Errorf("eval: %w", err)
		}
		out = map[string]any{"result_json": res.Value.Raw}
		return nil
	})
	return out, err
}

// WaitFor blocks until either selector becomes visible or expression returns
// truthy, with a timeout. Exactly one of selector/expression must be set.
func (b *Browser) WaitFor(selector, expression string, timeoutMs int) (map[string]any, error) {
	if selector == "" && expression == "" {
		return nil, errors.New("selector or expression is required")
	}
	if selector != "" && expression != "" {
		return nil, errors.New("set selector OR expression, not both")
	}
	timeout := timeoutMsToDuration(timeoutMs, 10*time.Second)
	start := time.Now()
	var out map[string]any
	err := b.withLock(func() error {
		page, err := b.ensurePage()
		if err != nil {
			return err
		}
		ctx := page.Timeout(timeout)
		if selector != "" {
			el, err := ctx.Element(selector)
			if err != nil {
				return fmt.Errorf("wait for selector %q: %w", selector, err)
			}
			if err := el.WaitVisible(); err != nil {
				return fmt.Errorf("wait visible: %w", err)
			}
		} else {
			// Wrap with Boolean() so the LLM doesn't have to remember rod's
			// strict-bool quirk.
			wrapped := "() => Boolean((" + expression + "))"
			if err := ctx.Wait(rod.Eval(wrapped)); err != nil {
				return fmt.Errorf("wait for expression: %w", err)
			}
		}
		out = map[string]any{
			"ok":         true,
			"elapsed_ms": time.Since(start).Milliseconds(),
		}
		return nil
	})
	return out, err
}

// GetURL returns the current URL and title.
func (b *Browser) GetURL() (map[string]any, error) {
	var out map[string]any
	err := b.withLock(func() error {
		page, err := b.ensurePage()
		if err != nil {
			return err
		}
		out = map[string]any{
			"url":   safePageURL(page),
			"title": safePageTitle(page),
		}
		return nil
	})
	return out, err
}

// SessionInfo reports cookie count, URL, viewport, and UA.
func (b *Browser) SessionInfo() (map[string]any, error) {
	var out map[string]any
	err := b.withLock(func() error {
		page, err := b.ensurePage()
		if err != nil {
			return err
		}
		cookies, _ := b.exec.Browser().GetCookies()
		ua := b.exec.UserAgent()
		viewport := map[string]any{}
		if vp, err := page.Eval("() => ({ width: window.innerWidth, height: window.innerHeight })"); err == nil {
			viewport["raw"] = vp.Value.Raw
		}
		out = map[string]any{
			"cookies_count": len(cookies),
			"current_url":   safePageURL(page),
			"viewport":      viewport,
			"user_agent":    ua,
		}
		return nil
	})
	return out, err
}

// ---- helpers -------------------------------------------------------------

func safePageURL(p *rod.Page) string {
	info, err := p.Info()
	if err != nil || info == nil {
		return ""
	}
	return info.URL
}

func safePageTitle(p *rod.Page) string {
	info, err := p.Info()
	if err != nil || info == nil {
		return ""
	}
	return info.Title
}

func timeoutMsToDuration(ms int, fallback time.Duration) time.Duration {
	if ms <= 0 {
		return fallback
	}
	return time.Duration(ms) * time.Millisecond
}
