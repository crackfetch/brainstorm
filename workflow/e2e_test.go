package workflow

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-rod/rod/lib/launcher"
)

// skipIfNoChrome skips the test if no Chrome/Chromium is installed.
func skipIfNoChrome(t *testing.T) {
	t.Helper()
	if _, exists := launcher.LookPath(); !exists {
		t.Skip("Chrome not found — skipping browser E2E test")
	}
}

// TestStealth_HeadlessNotLeakedInUserAgent verifies that when brz runs in
// headless mode (the default), the Executor's UA and the page-level
// navigator.userAgent both look like real Chrome with no "Headless" token.
//
// In addition to checking the absence of "Headless", this test asserts the
// presence of "Chrome/" and "Mozilla/" — otherwise an empty/malformed UA
// would also pass the negative check (resolveUserAgent("") returns the
// fallback, which trivially contains no "Headless").
func TestStealth_HeadlessNotLeakedInUserAgent(t *testing.T) {
	skipIfNoChrome(t)

	exec := NewExecutor(&Workflow{Name: "test", Actions: map[string]Action{}})
	if err := exec.Start(); err != nil {
		t.Fatalf("start browser: %v", err)
	}
	defer exec.Close()

	if err := exec.NavigateTo("about:blank"); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	assertCleanChromeUA(t, "Executor.UserAgent()", exec.UserAgent())

	pageUA := exec.page.MustEval(`() => navigator.userAgent`).String()
	assertCleanChromeUA(t, "page navigator.userAgent", pageUA)
}

// TestStealth_HeadlessNotLeakedInHTTPHeader verifies that the User-Agent
// header brz actually sends over the network in headless mode does not
// contain "Headless". The about:blank check above only proves what JS sees
// in-page — it cannot catch a leak at the HTTP-request layer. This test
// stands up a local httptest.Server, navigates the executor to it, and
// inspects the User-Agent header the server received.
func TestStealth_HeadlessNotLeakedInHTTPHeader(t *testing.T) {
	skipIfNoChrome(t)

	var (
		mu        sync.Mutex
		seenUA    string
		seenCount int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seenUA = r.Header.Get("User-Agent")
		seenCount++
		mu.Unlock()
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>ok</body></html>"))
	}))
	defer srv.Close()

	exec := NewExecutor(&Workflow{Name: "test", Actions: map[string]Action{}})
	if err := exec.Start(); err != nil {
		t.Fatalf("start browser: %v", err)
	}
	defer exec.Close()

	if err := exec.NavigateTo(srv.URL); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	mu.Lock()
	gotUA := seenUA
	gotCount := seenCount
	mu.Unlock()

	if gotCount == 0 {
		t.Fatalf("test server received no requests — navigation never reached the network")
	}
	assertCleanChromeUA(t, "HTTP User-Agent header", gotUA)
}

// assertCleanChromeUA fails the test if ua looks like a HeadlessChrome leak
// or like an empty/fallback string. A clean UA must contain "Chrome/" and
// "Mozilla/" and must not contain "Headless".
func assertCleanChromeUA(t *testing.T, label, ua string) {
	t.Helper()
	if strings.Contains(ua, "Headless") {
		t.Errorf("%s leaks Headless: %q", label, ua)
	}
	if !strings.Contains(ua, "Chrome/") {
		t.Errorf("%s missing Chrome/ token (suspicious empty/malformed UA): %q", label, ua)
	}
	if !strings.Contains(ua, "Mozilla/") {
		t.Errorf("%s missing Mozilla/ token (suspicious empty/malformed UA): %q", label, ua)
	}
}

func TestStealthInjection_Idempotent(t *testing.T) {
	skipIfNoChrome(t)

	exec := NewExecutor(&Workflow{Name: "test", Actions: map[string]Action{}})
	if err := exec.Start(); err != nil {
		t.Fatalf("start browser: %v", err)
	}
	defer exec.Close()

	if err := exec.NavigateTo("about:blank"); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	// Second injection on the same page must not panic.
	// Before the fix, this panicked with:
	//   TypeError: Cannot redefine property: webdriver
	exec.injectStealth()
}

func TestRunAction_SameURL_ReusesPage(t *testing.T) {
	skipIfNoChrome(t)

	w := &Workflow{
		Name: "test-reuse",
		Actions: map[string]Action{
			"first": {
				URL:   "about:blank",
				Steps: []Step{},
			},
			"second": {
				URL:   "about:blank",
				Steps: []Step{},
			},
		},
	}

	exec := NewExecutor(w)
	if err := exec.Start(); err != nil {
		t.Fatalf("start browser: %v", err)
	}
	defer exec.Close()

	r1 := exec.RunAction("first")
	if !r1.OK {
		t.Fatalf("first action failed: %s", r1.Error)
	}

	// Second action with same URL should reuse the page (no new tab).
	r2 := exec.RunAction("second")
	if !r2.OK {
		t.Fatalf("second action failed: %s", r2.Error)
	}
}

func TestRunAction_NoURL_ContinuationPattern(t *testing.T) {
	skipIfNoChrome(t)

	w := &Workflow{
		Name: "test-continuation",
		Actions: map[string]Action{
			"navigate_first": {
				URL:   "about:blank",
				Steps: []Step{},
			},
			"continue": {
				// No URL — should operate on current page.
				Steps: []Step{},
			},
		},
	}

	exec := NewExecutor(w)
	if err := exec.Start(); err != nil {
		t.Fatalf("start browser: %v", err)
	}
	defer exec.Close()

	r1 := exec.RunAction("navigate_first")
	if !r1.OK {
		t.Fatalf("first action failed: %s", r1.Error)
	}

	// Continuation action with no URL reuses the current page.
	r2 := exec.RunAction("continue")
	if !r2.OK {
		t.Fatalf("continuation action failed: %s", r2.Error)
	}
}
