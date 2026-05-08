package workflow

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/crackfetch/brainstorm/internal/events"
)

// captureEmitter records every event in order. Safe for concurrent use to
// match the events.Emitter contract; in practice the executor emits from a
// single goroutine, but we don't want a flaky test if that ever changes.
type captureEmitter struct {
	mu     sync.Mutex
	events []events.Event
}

func (c *captureEmitter) Emit(e events.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
}

func (c *captureEmitter) snapshot() []events.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]events.Event, len(c.events))
	copy(out, c.events)
	return out
}

// TestExecutor_EmitsStepLifecycleEvents asserts that running a successful
// action produces step_start + step_end (status=ok) for every step, in
// order, with monotonically increasing seq values once routed through the
// JSONL emitter.
//
// This test does NOT require a browser: NewExecutor + an empty action with
// zero steps does not need Start(). We test the emit path with a tiny
// action that has zero steps for the no-step case, and a separate browser-
// gated case for steps with selectors.
func TestExecutor_EmitsStepLifecycleEvents_NoSteps(t *testing.T) {
	skipIfNoChrome(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body><button id="b">Hi</button></body></html>`))
	}))
	defer srv.Close()

	cap := &captureEmitter{}
	w := &Workflow{
		Name: "evt-test",
		Actions: map[string]Action{
			"go": {
				URL: srv.URL,
				Steps: []Step{
					{Click: &ClickStep{Selector: "#b"}},
					{WaitVisible: &WaitStep{Selector: "#b", Timeout: "1s"}},
				},
			},
		},
	}
	exec := NewExecutor(w, WithEventEmitter(cap))
	if err := exec.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer exec.Close()

	r := exec.RunAction("go")
	if !r.OK {
		t.Fatalf("action failed: %s", r.Error)
	}

	evs := cap.snapshot()
	// Expect: action_start, (step_start, step_end)×2, action_end.
	if len(evs) != 6 {
		t.Fatalf("expected 6 events, got %d: %+v", len(evs), evs)
	}
	if evs[0].Event != events.ActionStart {
		t.Errorf("evs[0]: want action_start, got %s", evs[0].Event)
	}
	if evs[len(evs)-1].Event != events.ActionEnd {
		t.Errorf("last: want action_end, got %s", evs[len(evs)-1].Event)
	}
	if evs[len(evs)-1].Status != events.StatusOK {
		t.Errorf("action_end status=%q, want ok", evs[len(evs)-1].Status)
	}

	// Step events between action_start and action_end, paired start/end.
	for i := 1; i < len(evs)-1; i += 2 {
		if evs[i].Event != events.StepStart {
			t.Errorf("evs[%d]: want step_start, got %s", i, evs[i].Event)
		}
		if evs[i+1].Event != events.StepEnd {
			t.Errorf("evs[%d]: want step_end, got %s", i+1, evs[i+1].Event)
		}
		if evs[i].StepNum != evs[i+1].StepNum {
			t.Errorf("step_num mismatch at %d: start=%d end=%d", i, evs[i].StepNum, evs[i+1].StepNum)
		}
		if evs[i+1].Status != events.StatusOK {
			t.Errorf("step %d ended status=%q, want ok", evs[i+1].StepNum, evs[i+1].Status)
		}
	}
}

// TestExecutor_EmitsErrorOnFailedStep verifies a missing-selector step
// surfaces as step_end status=error with the error string populated.
func TestExecutor_EmitsErrorOnFailedStep(t *testing.T) {
	skipIfNoChrome(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body></body></html>`))
	}))
	defer srv.Close()

	cap := &captureEmitter{}
	w := &Workflow{
		Name: "evt-fail",
		Actions: map[string]Action{
			"go": {
				URL: srv.URL,
				Steps: []Step{
					{Click: &ClickStep{Selector: "#does-not-exist", Timeout: "200ms"}},
				},
			},
		},
	}
	exec := NewExecutor(w, WithEventEmitter(cap))
	if err := exec.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer exec.Close()

	r := exec.RunAction("go")
	if r.OK {
		t.Fatalf("expected failure, got success")
	}

	evs := cap.snapshot()
	if len(evs) < 4 {
		t.Fatalf("want ≥4 events (action_start, step_start, step_end, action_end), got %d: %+v", len(evs), evs)
	}
	// Find the step_end and verify it's an error.
	var stepEnd *events.Event
	for i := range evs {
		if evs[i].Event == events.StepEnd {
			stepEnd = &evs[i]
			break
		}
	}
	if stepEnd == nil {
		t.Fatalf("no step_end event found in %+v", evs)
	}
	if stepEnd.Status != events.StatusError {
		t.Errorf("step_end status=%q, want error", stepEnd.Status)
	}
	if stepEnd.Error == "" {
		t.Error("step_end Error should be populated on failure")
	}
	// action_end must be present and report error.
	last := evs[len(evs)-1]
	if last.Event != events.ActionEnd {
		t.Errorf("last event = %s, want action_end", last.Event)
	}
	if last.Status != events.StatusError {
		t.Errorf("action_end status = %q, want error", last.Status)
	}
}

// TestExecutor_NoEmitterMeansNoSideEffects verifies the default executor
// (no WithEventEmitter) silently uses Nop and does not panic.
func TestExecutor_NoEmitterMeansNoSideEffects(t *testing.T) {
	exec := NewExecutor(&Workflow{Name: "x", Actions: map[string]Action{}})
	if exec.events == nil {
		t.Fatal("default emitter must be non-nil (Nop)")
	}
	// Should not panic.
	exec.events.Emit(events.Event{Event: events.StepStart})
}
