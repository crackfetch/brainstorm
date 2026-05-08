package workflow

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestAliases_E2E_RunsAgainstHTTPServer verifies that a workflow using
// `${aliases.X}` actually drives the browser end-to-end: an alias-only
// click against an in-process server lands on the success page.
func TestAliases_E2E_RunsAgainstHTTPServer(t *testing.T) {
	skipIfNoChrome(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/done":
			fmt.Fprint(w, `<html><body><h1 id="ok">DONE</h1></body></html>`)
		default:
			fmt.Fprint(w, `<html><body><a id="cart" href="/done">Go</a></body></html>`)
		}
	}))
	defer srv.Close()

	src := []byte(`name: alias-e2e
aliases:
  cart_button: "#cart"
actions:
  go:
    url: ` + srv.URL + `
    steps:
      - click: { selector: "${aliases.cart_button}" }
      - wait_url: { match: "/done", timeout: "10s" }
`)
	w, err := LoadFromBytes(src)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// Sanity: alias was substituted.
	if got := w.Actions["go"].Steps[0].Click.Selector; got != "#cart" {
		t.Fatalf("alias not substituted: %q", got)
	}
	if got := w.Actions["go"].Steps[0].Click.AliasName; got != "cart_button" {
		t.Fatalf("alias name not recorded: %q", got)
	}

	exec := NewExecutor(w)
	if err := exec.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer exec.Close()

	res := exec.RunAction("go")
	if res == nil || !res.OK {
		t.Fatalf("action failed: %#v", res)
	}
}
