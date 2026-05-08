package workflow

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/proto"
)

// Tests for step.retry and action.on_error.
//
// Step retry: pure-Go test of computeBackoff math + an E2E test that
// exercises a real httptest server returning failure-then-success so we
// pin "retry actually retries and eventually succeeds."
//
// Action on_error: a workflow with a primary action that fails plus a
// recovery action; verify the failResult reflects the recovery outcome.

func TestRetryStep_Parsing(t *testing.T) {
	yamlSrc := `
name: t
actions:
  do:
    steps:
      - click:
          selector: '.x'
        retry:
          count: 3
          backoff: exponential
          initial_delay: "500ms"
`
	w, err := LoadFromBytes([]byte(yamlSrc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	r := w.Actions["do"].Steps[0].Retry
	if r == nil {
		t.Fatal("expected retry config")
	}
	if r.Count != 3 || r.Backoff != "exponential" || r.InitialDelay != "500ms" {
		t.Errorf("retry parsed wrong: %+v", r)
	}
}

func TestComputeBackoff(t *testing.T) {
	initial := 100 * time.Millisecond
	tests := []struct {
		strategy string
		attempt  int
		want     time.Duration
	}{
		{"none", 1, 0},
		{"", 1, 0},                              // empty == none
		{"linear", 1, 100 * time.Millisecond},
		{"linear", 2, 200 * time.Millisecond},
		{"linear", 3, 300 * time.Millisecond},
		{"exponential", 1, 200 * time.Millisecond}, // initial * 2^1
		{"exponential", 2, 400 * time.Millisecond},
		{"exponential", 3, 800 * time.Millisecond},
		{"unknown_strategy", 1, 100 * time.Millisecond}, // fallback to linear
		{"linear", 0, 0},                                 // attempt < 1 → 0
	}
	for _, tc := range tests {
		got := computeBackoff(tc.strategy, initial, tc.attempt)
		if got != tc.want {
			t.Errorf("computeBackoff(%q, %v, %d) = %v, want %v", tc.strategy, initial, tc.attempt, got, tc.want)
		}
	}
}

func TestStep_RetrySucceedsOnSecondAttempt(t *testing.T) {
	skipIfNoChrome(t)
	// Pin that retry actually retries: the page injects #go AFTER a
	// 600ms delay. The click step has a 200ms timeout, so the first
	// attempt fails (no element). Retry waits 100ms backoff and tries
	// again — by attempt 2 or 3 the button has appeared. Without retry,
	// a single 200ms click attempt would fail outright; success here
	// means the retry loop ran at least once.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body>
<div id="root"></div>
<script>
  setTimeout(() => {
    const btn = document.createElement('button');
    btn.id = 'go';
    btn.textContent = 'Go';
    document.getElementById('root').appendChild(btn);
  }, 600);
</script>
</body></html>`))
	}))
	defer srv.Close()

	w := &Workflow{
		Name: "retry-success",
		Actions: map[string]Action{
			"do": {
				URL: srv.URL,
				Steps: []Step{
					{
						Click: &ClickStep{Selector: "#go", Timeout: "200ms"},
						Retry: &RetryStep{Count: 5, Backoff: "linear", InitialDelay: "150ms"},
					},
				},
			},
		},
	}
	exec := NewExecutor(w)
	if err := exec.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer exec.Close()

	start := time.Now()
	res := exec.RunAction("do")
	elapsed := time.Since(start)

	if !res.OK {
		t.Fatalf("retry should have made the click succeed eventually; got error: %s", res.Error)
	}
	// Sanity: we must have spent at least one timeout + one backoff
	// before succeeding. If the test ran in <300ms, the click somehow
	// passed first try and retry wasn't actually exercised.
	if elapsed < 300*time.Millisecond {
		t.Errorf("retry path likely not exercised — completed in %s (expected at least one 200ms timeout + 150ms backoff)", elapsed)
	}
}

func TestStep_RetryRunsOutAndReturnsFailure(t *testing.T) {
	skipIfNoChrome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body>no button ever</body></html>`))
	}))
	defer srv.Close()

	w := &Workflow{
		Name: "retry-exhaust",
		Actions: map[string]Action{
			"do": {
				URL: srv.URL,
				Steps: []Step{
					{
						Click: &ClickStep{Selector: "#never-here", Timeout: "300ms"},
						Retry: &RetryStep{Count: 3, Backoff: "none"},
					},
				},
			},
		},
	}
	exec := NewExecutor(w)
	if err := exec.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer exec.Close()
	res := exec.RunAction("do")
	if res.OK {
		t.Fatal("expected retry to exhaust and return failure")
	}
	if !strings.Contains(res.Error, "after 3 attempts") {
		t.Errorf("error should name the attempt count; got: %s", res.Error)
	}
}

func TestStep_NoRetry_DefaultBehaviorPreserved(t *testing.T) {
	skipIfNoChrome(t)
	// Backward-compat: a step without retry config keeps single-attempt
	// behavior. The error message should NOT contain "after N attempts."
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body>x</body></html>`))
	}))
	defer srv.Close()

	w := &Workflow{
		Name: "no-retry",
		Actions: map[string]Action{
			"do": {
				URL: srv.URL,
				Steps: []Step{
					{Click: &ClickStep{Selector: "#missing", Timeout: "300ms"}},
				},
			},
		},
	}
	exec := NewExecutor(w)
	if err := exec.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer exec.Close()
	res := exec.RunAction("do")
	if res.OK {
		t.Fatal("expected single-attempt failure")
	}
	if strings.Contains(res.Error, "after") && strings.Contains(res.Error, "attempt") {
		t.Errorf("non-retry failure shouldn't reference attempt counts; got: %s", res.Error)
	}
}

func TestAction_OnError_RecoverySucceeds(t *testing.T) {
	skipIfNoChrome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body>start</body></html>`))
	}))
	defer srv.Close()

	w := &Workflow{
		Name: "on-error-success",
		Actions: map[string]Action{
			"primary": {
				URL:     srv.URL,
				OnError: "cleanup",
				Steps: []Step{
					{Click: &ClickStep{Selector: "#missing", Timeout: "300ms"}},
				},
			},
			"cleanup": {
				URL: srv.URL,
				Steps: []Step{
					// Navigate-only "recovery" — succeeds trivially.
					{Eval: `() => 'cleanup-ran'`},
				},
			},
		},
	}
	exec := NewExecutor(w)
	if err := exec.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer exec.Close()
	res := exec.RunAction("primary")
	if res.OK {
		t.Fatal("primary action should still fail after recovery (recovery doesn't change the outcome of primary)")
	}
	if !strings.Contains(res.Error, "on_error 'cleanup' recovery succeeded") {
		t.Errorf("error should note the recovery succeeded; got: %s", res.Error)
	}
}

func TestAction_OnError_RecoveryFails(t *testing.T) {
	skipIfNoChrome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body>start</body></html>`))
	}))
	defer srv.Close()

	w := &Workflow{
		Name: "on-error-fail",
		Actions: map[string]Action{
			"primary": {
				URL:     srv.URL,
				OnError: "cleanup",
				Steps: []Step{
					{Click: &ClickStep{Selector: "#missing-primary", Timeout: "300ms"}},
				},
			},
			"cleanup": {
				URL: srv.URL,
				Steps: []Step{
					{Click: &ClickStep{Selector: "#missing-cleanup", Timeout: "300ms"}},
				},
			},
		},
	}
	exec := NewExecutor(w)
	if err := exec.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer exec.Close()
	res := exec.RunAction("primary")
	if res.OK {
		t.Fatal("expected primary to fail")
	}
	if !strings.Contains(res.Error, "on_error 'cleanup' also failed") {
		t.Errorf("error should note both failures; got: %s", res.Error)
	}
}

func TestAction_OnError_RecoveryActionNotFound(t *testing.T) {
	skipIfNoChrome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body>x</body></html>`))
	}))
	defer srv.Close()

	w := &Workflow{
		Name: "on-error-missing",
		Actions: map[string]Action{
			"primary": {
				URL:     srv.URL,
				OnError: "no_such_action",
				Steps: []Step{
					{Click: &ClickStep{Selector: "#missing", Timeout: "300ms"}},
				},
			},
		},
	}
	exec := NewExecutor(w)
	if err := exec.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer exec.Close()
	res := exec.RunAction("primary")
	if res.OK {
		t.Fatal("expected primary to fail")
	}
	if !strings.Contains(res.Error, `on_error action "no_such_action" not found`) {
		t.Errorf("error should explain the missing recovery; got: %s", res.Error)
	}
}

func TestAction_OnError_DepthLimitPreventsChainedRecovery(t *testing.T) {
	skipIfNoChrome(t)
	// Recovery actions can't themselves chain on_error. If we allowed
	// it: a chain that always fails would recurse indefinitely. The
	// depth limit (max 1) means the recovery action's own on_error is
	// ignored; we just report its failure.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body>x</body></html>`))
	}))
	defer srv.Close()

	w := &Workflow{
		Name: "on-error-chain",
		Actions: map[string]Action{
			"primary": {
				URL:     srv.URL,
				OnError: "level1",
				Steps:   []Step{{Click: &ClickStep{Selector: "#a", Timeout: "200ms"}}},
			},
			"level1": {
				URL:     srv.URL,
				OnError: "level2",
				Steps:   []Step{{Click: &ClickStep{Selector: "#b", Timeout: "200ms"}}},
			},
			"level2": {
				URL:   srv.URL,
				Steps: []Step{{Eval: `() => true`}},
			},
		},
	}
	exec := NewExecutor(w)
	if err := exec.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer exec.Close()
	res := exec.RunAction("primary")
	if res.OK {
		t.Fatal("expected primary to fail")
	}
	if !strings.Contains(res.Error, "chain limit hit") {
		t.Errorf("level1's nested on_error should hit the depth limit; got: %s", res.Error)
	}
}

func TestAction_OnError_FiresOnEvalAssertionFailure(t *testing.T) {
	skipIfNoChrome(t)
	// Codex caught that the post-action eval-failure path bypassed
	// maybeRunOnError. Pin that recovery now fires when the steps all
	// pass but a post-action assertion vetoes the result.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body>start</body></html>`))
	}))
	defer srv.Close()

	w := &Workflow{
		Name: "on-error-eval",
		Actions: map[string]Action{
			"primary": {
				URL:     srv.URL,
				OnError: "cleanup",
				Steps: []Step{
					{Eval: `() => 'step-passed'`},
				},
				// Post-action assertion fails: this URL won't ever match.
				Eval: []EvalAssert{
					{URLContains: "/never-arrives"},
				},
			},
			"cleanup": {
				URL:   srv.URL,
				Steps: []Step{{Eval: `() => 'recovery-ran'`}},
			},
		},
	}
	exec := NewExecutor(w)
	if err := exec.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer exec.Close()
	res := exec.RunAction("primary")
	if res.OK {
		t.Fatal("expected primary to fail on eval assertion")
	}
	if !strings.Contains(res.Error, "on_error 'cleanup' recovery succeeded") {
		t.Errorf("eval-assertion failure should still trigger on_error; got: %s", res.Error)
	}
}

func TestMaybeRunOnError_ClearsStalePendingDownload(t *testing.T) {
	// Codex caught that a failed primary action could leave
	// pendingDownloadWait set, polluting any subsequent action.
	// Pin that maybeRunOnError clears it even when no recovery is set.
	exec := &Executor{
		workflow: &Workflow{Actions: map[string]Action{}},
		// Simulate a primary that pre-registered a download wait but
		// then failed before doDownload consumed it.
		pendingDownloadWait: func() *proto.PageDownloadWillBegin { return nil },
		pendingDownloadDir:  "/tmp/xyz",
		pendingDownloadURL:  "https://example.com/foo",
	}
	failResult := &ActionResult{OK: false, Error: "step failed"}

	out := exec.maybeRunOnError("primary", Action{}, failResult, 0)

	if out.OK {
		t.Errorf("ok should still be false after no-op recovery cleanup")
	}
	if exec.pendingDownloadWait != nil || exec.pendingDownloadDir != "" || exec.pendingDownloadURL != "" {
		t.Errorf("stale pending* should be cleared; got wait=%v dir=%q url=%q",
			exec.pendingDownloadWait != nil, exec.pendingDownloadDir, exec.pendingDownloadURL)
	}
}

func TestAction_NoOnError_DefaultBehaviorPreserved(t *testing.T) {
	skipIfNoChrome(t)
	// Backward-compat: an action without on_error fails normally with
	// no "on_error" decoration in the error string.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body>x</body></html>`))
	}))
	defer srv.Close()

	w := &Workflow{
		Name: "no-on-error",
		Actions: map[string]Action{
			"primary": {
				URL:   srv.URL,
				Steps: []Step{{Click: &ClickStep{Selector: "#missing", Timeout: "200ms"}}},
			},
		},
	}
	exec := NewExecutor(w)
	if err := exec.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer exec.Close()
	res := exec.RunAction("primary")
	if res.OK {
		t.Fatal("expected failure")
	}
	if strings.Contains(res.Error, "on_error") {
		t.Errorf("error should not reference on_error when none was set; got: %s", res.Error)
	}
}
