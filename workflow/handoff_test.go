package workflow

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Tests for the handoff step. Three layers:
//   - YAML parsing (no browser).
//   - Validation: setting neither wait_url/wait_eval, or both, errors at
//     execution time (not at parse time, because lenient mode would let
//     it through).
//   - E2E: pause workflow → simulate the resume signal arriving on a
//     real httptest page → confirm the workflow continues.

func TestHandoffStep_Parsing(t *testing.T) {
	yamlSrc := `
name: t
actions:
  do:
    steps:
      - handoff:
          message: "solve the captcha"
          wait_url: "/dashboard"
          timeout: "5m"
`
	w, err := LoadFromBytes([]byte(yamlSrc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	h := w.Actions["do"].Steps[0].Handoff
	if h == nil {
		t.Fatal("expected handoff step")
	}
	if h.Message != "solve the captcha" {
		t.Errorf("Message: got %q", h.Message)
	}
	if h.WaitURL != "/dashboard" {
		t.Errorf("WaitURL: got %q", h.WaitURL)
	}
	if h.Timeout != "5m" {
		t.Errorf("Timeout: got %q", h.Timeout)
	}
}

func TestHandoffStep_StepTypeAndResolve(t *testing.T) {
	s := Step{Handoff: &HandoffStep{Message: "x", WaitURL: "/y"}}
	if got := stepType(s); got != "handoff" {
		t.Errorf("stepType: got %q", got)
	}
	rs := ResolveSteps(Action{Steps: []Step{s}}, nil)
	if rs[0].Type != "handoff" {
		t.Errorf("ResolveSteps type: got %q", rs[0].Type)
	}
	if rs[0].Match != "/y" {
		t.Errorf("ResolveSteps Match: got %q", rs[0].Match)
	}
}

func TestDoHandoff_RejectsMissingSignal(t *testing.T) {
	skipIfNoChrome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	w := &Workflow{Name: "t", Actions: map[string]Action{
		"do": {URL: srv.URL, Steps: []Step{{Handoff: &HandoffStep{Message: "no signal"}}}},
	}}
	exec := NewExecutor(w, WithHeaded(true))
	if err := exec.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer exec.Close()
	res := exec.RunAction("do")
	if res.OK {
		t.Fatal("expected handoff with no signal to error")
	}
	if !strings.Contains(res.Error, "wait_url or wait_eval") {
		t.Errorf("error should explain the missing signal; got: %s", res.Error)
	}
}

func TestDoHandoff_RejectsBothSignals(t *testing.T) {
	skipIfNoChrome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	w := &Workflow{Name: "t", Actions: map[string]Action{
		"do": {URL: srv.URL, Steps: []Step{{Handoff: &HandoffStep{
			Message: "x", WaitURL: "/a", WaitEval: "() => true",
		}}}},
	}}
	exec := NewExecutor(w, WithHeaded(true))
	if err := exec.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer exec.Close()
	res := exec.RunAction("do")
	if res.OK {
		t.Fatal("expected handoff with both signals to error")
	}
	if !strings.Contains(res.Error, "wait_url OR wait_eval") {
		t.Errorf("error should explain the conflicting signals; got: %s", res.Error)
	}
}

// E2E: handoff that resumes via wait_url. We can't simulate a human
// solving a captcha, but we CAN drive the page from a goroutine that
// triggers the resume condition mid-handoff. Same effect for tests.

func TestDoHandoff_WaitURL_ResumesWhenURLMatches(t *testing.T) {
	skipIfNoChrome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/dashboard") {
			w.Write([]byte(`<html><body>dashboard</body></html>`))
			return
		}
		w.Write([]byte(`<html><body>start <a id="go" href="/dashboard">go</a></body></html>`))
	}))
	defer srv.Close()

	w := &Workflow{
		Name: "handoff-url",
		Actions: map[string]Action{
			"do": {
				URL: srv.URL + "/start",
				Steps: []Step{
					{Handoff: &HandoffStep{
						Message:  "navigate to /dashboard",
						WaitURL:  "/dashboard",
						Timeout:  "10s",
					}},
				},
			},
		},
	}
	exec := NewExecutor(w, WithHeaded(true))
	if err := exec.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer exec.Close()

	// Spawn a "human" that clicks the link 1.5s into the handoff.
	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(1500 * time.Millisecond)
		// Best-effort — if the click fails the handoff times out and the
		// test fails, which is the right diagnostic.
		_, _ = exec.page.Eval(`() => document.getElementById('go').click()`)
	}()

	res := exec.RunAction("do")
	<-done
	if !res.OK {
		t.Fatalf("handoff should have resumed; got error: %s", res.Error)
	}
}

func TestDoHandoff_WaitEval_ResumesWhenEvalTruthy(t *testing.T) {
	skipIfNoChrome(t)
	// Page sets a sentinel global after 1s.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body>
<script>setTimeout(() => { window.__handoff_done = true; }, 1000);</script>
</body></html>`))
	}))
	defer srv.Close()

	w := &Workflow{
		Name: "handoff-eval",
		Actions: map[string]Action{
			"do": {
				URL: srv.URL,
				Steps: []Step{
					{Handoff: &HandoffStep{
						Message:  "wait for the sentinel",
						WaitEval: "() => window.__handoff_done === true",
						Timeout:  "10s",
					}},
				},
			},
		},
	}
	exec := NewExecutor(w, WithHeaded(true))
	if err := exec.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer exec.Close()
	res := exec.RunAction("do")
	if !res.OK {
		t.Fatalf("handoff should have resumed via eval; got error: %s", res.Error)
	}
}

func TestDoHandoff_TimesOut(t *testing.T) {
	skipIfNoChrome(t)
	// A wait_url that will never match within the timeout.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body>static</body></html>`))
	}))
	defer srv.Close()

	w := &Workflow{
		Name: "handoff-timeout",
		Actions: map[string]Action{
			"do": {
				URL: srv.URL,
				Steps: []Step{
					{Handoff: &HandoffStep{
						Message: "won't happen",
						WaitURL: "/never-arrives-here",
						Timeout: "1500ms",
					}},
				},
			},
		},
	}
	exec := NewExecutor(w, WithHeaded(true))
	if err := exec.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer exec.Close()
	res := exec.RunAction("do")
	if res.OK {
		t.Fatal("handoff should have timed out")
	}
	if !strings.Contains(res.Error, "did not match") {
		t.Errorf("error should explain the timeout; got: %s", res.Error)
	}
}

func TestDoHandoff_HeadlessEscalation_RestoresPage(t *testing.T) {
	skipIfNoChrome(t)
	// Pin the page-restoration fix: when a workflow runs HEADLESS and
	// hits a handoff, restart() replaces the rod browser handle. Without
	// nilling e.page + re-navigating, the resume loop polls a stale
	// handle that points at the closed-headless browser — the eval/info
	// calls silently fail and the handoff times out instead of resuming.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body>
<script>setTimeout(() => { window.__handoff_done = true; }, 800);</script>
</body></html>`))
	}))
	defer srv.Close()

	w := &Workflow{
		Name: "handoff-from-headless",
		Actions: map[string]Action{
			"do": {
				URL: srv.URL,
				Steps: []Step{
					{Handoff: &HandoffStep{
						Message:  "page-restoration regression guard",
						WaitEval: "() => window.__handoff_done === true",
						Timeout:  "10s",
					}},
				},
			},
		},
	}
	exec := NewExecutor(w) // NOT WithHeaded — start headless to exercise escalation
	if err := exec.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer exec.Close()

	res := exec.RunAction("do")
	if !res.OK {
		t.Fatalf("handoff from headless should have resumed via eval; got error: %s", res.Error)
	}
	if !exec.headed {
		t.Errorf("after handoff escalation, executor should be headed; got headed=%v", exec.headed)
	}
}

func TestDoHandoff_WaitEval_HandlesJSTruthy(t *testing.T) {
	skipIfNoChrome(t)
	// wait_eval must accept JS-truthy values, not just strict bool true.
	// "ready" is a string literal — without the Boolean wrap, rod would
	// see the string and Bool() coercion behavior would be ambiguous.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body>
<script>setTimeout(() => { window.__signal = "ready"; }, 800);</script>
</body></html>`))
	}))
	defer srv.Close()

	w := &Workflow{
		Name: "handoff-truthy",
		Actions: map[string]Action{
			"do": {
				URL: srv.URL,
				Steps: []Step{
					{Handoff: &HandoffStep{
						Message:  "string truthy test",
						WaitEval: `() => window.__signal`, // returns "ready", a JS-truthy string
						Timeout:  "5s",
					}},
				},
			},
		},
	}
	exec := NewExecutor(w, WithHeaded(true))
	if err := exec.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer exec.Close()
	res := exec.RunAction("do")
	if !res.OK {
		t.Fatalf("wait_eval returning a truthy string should have resumed; got: %s", res.Error)
	}
}

func TestStrictSuggest_HandoffFieldsRegistered(t *testing.T) {
	// HandoffStep must be in the strict-suggest registry so a typo
	// like wait_urll gets a "Did you mean wait_url?" hint.
	tags, ok := fieldRegistry["workflow.HandoffStep"]
	if !ok {
		t.Fatal("HandoffStep missing from fieldRegistry — strict mode won't surface field-name suggestions for it")
	}
	have := make(map[string]bool)
	for _, t := range tags {
		have[t] = true
	}
	for _, want := range []string{"message", "wait_url", "wait_eval", "timeout"} {
		if !have[want] {
			t.Errorf("HandoffStep registry missing %q (got %v)", want, tags)
		}
	}
}
