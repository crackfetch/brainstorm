package workflow

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// E2E tests for the wait_enabled step. The page renders a button that is
// initially `disabled` (or `aria-disabled="true"`) and re-enables itself
// after a delay. wait_enabled must block until the button is enabled,
// not return early when it's still disabled, and time out cleanly when
// the button never enables.

const waitEnabledHTML = `<!doctype html>
<html><body>
<button id="submit" disabled>Sign In</button>
<button id="aria" aria-disabled="true">Aria Sign In</button>
<button id="never" disabled>Never Enables</button>
<script>
  // Re-enable #submit after 800ms, #aria after 1100ms — gives wait_enabled
  // a real signal to wait for. #never stays disabled forever.
  setTimeout(() => { document.getElementById('submit').disabled = false; }, 800);
  setTimeout(() => { document.getElementById('aria').setAttribute('aria-disabled', 'false'); }, 1100);
</script>
</body></html>`

func waitEnabledServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(waitEnabledHTML))
	}))
}

func runWaitEnabled(t *testing.T, ws *WaitStep) (elapsed time.Duration, err error) {
	t.Helper()
	srv := waitEnabledServer()
	t.Cleanup(srv.Close)

	w := &Workflow{
		Name: "wait-enabled",
		Actions: map[string]Action{
			"do": {
				URL:   srv.URL,
				Steps: []Step{{WaitEnabled: ws}},
			},
		},
	}
	exec := NewExecutor(w)
	if err := exec.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { exec.Close() })

	start := time.Now()
	res := exec.RunAction("do")
	elapsed = time.Since(start)
	if !res.OK {
		return elapsed, &actionError{res.Error}
	}
	return elapsed, nil
}

func TestWaitEnabled_BlocksUntilEnabled(t *testing.T) {
	skipIfNoChrome(t)
	// #submit re-enables at 800ms. With a 5s timeout, wait_enabled must
	// return successfully somewhere around that mark — not before
	// (would mean we returned while still disabled), not at the timeout
	// (would mean we never detected the change).
	elapsed, err := runWaitEnabled(t, &WaitStep{Selector: "#submit", Timeout: "5s"})
	if err != nil {
		t.Fatalf("wait_enabled: %v", err)
	}
	if elapsed < 700*time.Millisecond {
		t.Errorf("returned too early at %s (button still disabled until ~800ms)", elapsed)
	}
	if elapsed > 3*time.Second {
		t.Errorf("returned too late at %s (button enabled at ~800ms)", elapsed)
	}
}

func TestWaitEnabled_HandlesAriaDisabled(t *testing.T) {
	skipIfNoChrome(t)
	// #aria uses aria-disabled="true" → "false" rather than the disabled
	// property. Both conventions must be honored.
	elapsed, err := runWaitEnabled(t, &WaitStep{Selector: "#aria", Timeout: "5s"})
	if err != nil {
		t.Fatalf("wait_enabled: %v", err)
	}
	if elapsed < 1*time.Second {
		t.Errorf("returned too early at %s (aria-disabled flipped at ~1100ms)", elapsed)
	}
	if elapsed > 3*time.Second {
		t.Errorf("returned too late at %s (aria-disabled flipped at ~1100ms)", elapsed)
	}
}

func TestWaitEnabled_TimesOut(t *testing.T) {
	skipIfNoChrome(t)
	// #never is disabled forever → wait_enabled must error with a clear
	// message naming the selector and the elapsed timeout.
	_, err := runWaitEnabled(t, &WaitStep{Selector: "#never", Timeout: "1500ms"})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestWaitEnabled_SelectorMissing_TimesOut(t *testing.T) {
	skipIfNoChrome(t)
	// Selector that never matches must time out cleanly (not panic, not
	// return early). Same path as a disabled element — caller can't
	// distinguish, which is fine; the message should be diagnostic enough.
	_, err := runWaitEnabled(t, &WaitStep{Selector: "#does-not-exist", Timeout: "1500ms"})
	if err == nil {
		t.Fatal("expected timeout error for missing selector, got nil")
	}
}
