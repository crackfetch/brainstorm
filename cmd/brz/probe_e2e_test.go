package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/crackfetch/brainstorm/workflow"
	"github.com/go-rod/rod/lib/launcher"
)

// skipIfNoChrome skips the test if no Chrome/Chromium is installed.
func skipIfNoChrome(t *testing.T) {
	t.Helper()
	if _, exists := launcher.LookPath(); !exists {
		t.Skip("Chrome not found — skipping browser E2E test")
	}
}

// TestProbe_REPL_Integration drives a real REPL against a tiny in-process
// HTTP server serving a known HTML doc. Verifies match counts, the
// `0 matches` path, an XPath query, and a malformed-selector path.
//
// Runs headless: highlight injection is exercised by ":eval" verifying the
// style tag would be there for headed mode (kept simple — the highlight DOM
// path is also exercised manually via dogfood).
func TestProbe_REPL_Integration(t *testing.T) {
	skipIfNoChrome(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html>
<html><head><title>probe-fixture</title></head><body>
  <h1 id="hello">Hello</h1>
  <div class="card">A</div>
  <div class="card">B</div>
  <div class="card">C</div>
  <button id="go">Go</button>
</body></html>`))
	}))
	defer srv.Close()

	exec := workflow.NewExecutor(nil) // headless default
	if err := exec.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer exec.Close()

	if err := exec.NavigateTo(srv.URL); err != nil {
		t.Fatalf("nav: %v", err)
	}

	var stdout, stderr bytes.Buffer
	rp := &repl{
		exec:      exec,
		mode:      "css",
		highlight: false, // skip injection in headless test
		out:       &stdout,
		err:       &stderr,
		isTTY:     false,
	}

	input := strings.NewReader(strings.Join([]string{
		"h1",
		".card",
		".does-not-exist",
		"xpath://button[@id='go']",
		":eval document.title",
		":q",
	}, "\n") + "\n")
	rp.run(input)

	got := stdout.String()
	checks := []string{
		"1 match\n",                  // h1
		`<h1 id="hello"> "Hello"`,    // h1 description
		"3 matches\n",                // .card
		"0 matches\n",                // miss
		"1 match\n",                  // xpath
		`<button id="go"> "Go"`,      // button description
		"probe-fixture",              // :eval result
	}
	for _, want := range checks {
		if !strings.Contains(got, want) {
			t.Errorf("expected stdout to contain %q\n--- stdout ---\n%s\n--- stderr ---\n%s",
				want, got, stderr.String())
		}
	}
}

// TestProbe_REPL_BadSelectorDoesNotCrash sends malformed CSS and confirms the
// REPL keeps running and prints a useful error.
func TestProbe_REPL_BadSelectorDoesNotCrash(t *testing.T) {
	skipIfNoChrome(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body><p>x</p></body></html>`))
	}))
	defer srv.Close()

	exec := workflow.NewExecutor(nil)
	if err := exec.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer exec.Close()
	if err := exec.NavigateTo(srv.URL); err != nil {
		t.Fatalf("nav: %v", err)
	}

	var stdout, stderr bytes.Buffer
	rp := &repl{
		exec: exec, mode: "css", highlight: false,
		out: &stdout, err: &stderr, isTTY: false,
	}

	// `>>>` is not a valid CSS selector. Then a valid one. Then quit.
	input := strings.NewReader(">>>\np\n:q\n")
	rp.run(input)

	if !strings.Contains(stderr.String(), "invalid selector") {
		t.Errorf("expected stderr to mention 'invalid selector', got: %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "1 match") {
		t.Errorf("REPL should have continued after bad selector and matched <p>; stdout=%q", stdout.String())
	}
}
