package workflow

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crackfetch/brainstorm/internal/baseline"
)

// driftServer flips the served HTML based on an atomic flag. Variant A
// has 3 .row elements; variant B has 1. This is the cheapest possible
// "the page changed" simulator.
func driftServer(useB *atomic.Bool) *httptest.Server {
	htmlA := `<html><body>
		<div class="row" id="r1">Order #1001</div>
		<div class="row" id="r2">Order #1002</div>
		<div class="row" id="r3">Order #1003</div>
	</body></html>`
	htmlB := `<html><body>
		<div class="row" id="rZ">Site is down for maintenance</div>
	</body></html>`
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if useB.Load() {
			_, _ = w.Write([]byte(htmlB))
		} else {
			_, _ = w.Write([]byte(htmlA))
		}
	}))
}

// TestDriftDetection_E2E records a baseline against variant A, then
// switches the server to variant B and verifies that a fresh run with
// the recorded baseline surfaces both count_changed and the
// no_longer_matches/text_pattern_changed signals appropriately.
func TestDriftDetection_E2E(t *testing.T) {
	skipIfNoChrome(t)

	var useB atomic.Bool
	srv := driftServer(&useB)
	defer srv.Close()

	wf := &Workflow{
		Name: "drift-e2e",
		Actions: map[string]Action{
			"go": {
				URL: srv.URL,
				Steps: []Step{
					{WaitVisible: &WaitStep{Selector: ".row", Timeout: "5s"}, Label: "see row"},
				},
			},
		},
	}

	// Pass 1: variant A. Capture baseline.
	rec1 := NewRecordingObserver(nil, nil)
	exec1 := NewExecutor(wf, WithObserver(rec1))
	if err := exec1.Start(); err != nil {
		t.Fatalf("start1: %v", err)
	}
	r1 := exec1.RunAction("go")
	exec1.Close()
	if !r1.OK {
		t.Fatalf("pass 1 failed: %s", r1.Error)
	}

	steps := rec1.Steps()
	if len(steps) != 1 {
		t.Fatalf("expected 1 recorded step, got %d", len(steps))
	}
	if steps[0].MatchedCount == nil || *steps[0].MatchedCount != 3 {
		t.Fatalf("baseline matched_count: got %+v, want 3", steps[0].MatchedCount)
	}
	if steps[0].SampleTextHash == "" {
		t.Errorf("expected sample text hash on baseline")
	}

	// Pass 2: variant B with comparator.
	useB.Store(true)
	b := baseline.New("drift-e2e.yaml", "go", "test", anyTime(), steps)
	cmp := baseline.NewComparator(b)
	var drifts []baseline.Drift
	rec2 := NewRecordingObserver(cmp, func(d baseline.Drift) { drifts = append(drifts, d) })
	exec2 := NewExecutor(wf, WithObserver(rec2))
	if err := exec2.Start(); err != nil {
		t.Fatalf("start2: %v", err)
	}
	r2 := exec2.RunAction("go")
	exec2.Close()
	if !r2.OK {
		t.Fatalf("pass 2 (variant B) action itself failed: %s", r2.Error)
	}

	// We expect at least one count_changed (3 -> 1) AND text_pattern_changed.
	var sawCount, sawText bool
	for _, d := range drifts {
		switch d.Kind {
		case baseline.DriftSelectorCountChanged:
			if d.BaselineHits == 3 && d.CurrentHits == 1 {
				sawCount = true
			}
		case baseline.DriftTextPatternChanged:
			sawText = true
		}
	}
	if !sawCount {
		t.Errorf("expected count_changed drift (3 → 1), got %v", drifts)
	}
	if !sawText {
		t.Errorf("expected text_pattern_changed drift, got %v", drifts)
	}
}

// TestDriftDetection_NoLongerMatches uses a selector that disappears on B.
func TestDriftDetection_NoLongerMatches(t *testing.T) {
	skipIfNoChrome(t)

	htmlA := `<html><body><span class="present">Hi</span><div class="row">a</div></body></html>`
	htmlB := `<html><body><div class="row">a</div></body></html>` // .present gone, .row remains
	var useB atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if useB.Load() {
			_, _ = w.Write([]byte(htmlB))
		} else {
			_, _ = w.Write([]byte(htmlA))
		}
	}))
	defer srv.Close()

	// Use a click step against .row (so the action stays green on both
	// variants), then a wait_visible against .present which only B has
	// missing. Mark .present as optional so the action succeeds on B.
	wf := &Workflow{
		Name: "drift-gone",
		Actions: map[string]Action{
			"go": {
				URL: srv.URL,
				Steps: []Step{
					{Click: &ClickStep{Selector: ".row", Timeout: "5s"}, Label: "click row"},
					{WaitVisible: &WaitStep{Selector: ".present", Timeout: "2s"}, Label: "see present", Optional: true},
				},
			},
		},
	}

	rec1 := NewRecordingObserver(nil, nil)
	exec1 := NewExecutor(wf, WithObserver(rec1))
	if err := exec1.Start(); err != nil {
		t.Fatalf("start1: %v", err)
	}
	r1 := exec1.RunAction("go")
	exec1.Close()
	if !r1.OK {
		t.Fatalf("pass 1 failed: %s", r1.Error)
	}

	useB.Store(true)
	b := baseline.New("x", "go", "test", anyTime(), rec1.Steps())
	cmp := baseline.NewComparator(b)
	var drifts []baseline.Drift
	rec2 := NewRecordingObserver(cmp, func(d baseline.Drift) { drifts = append(drifts, d) })
	exec2 := NewExecutor(wf, WithObserver(rec2))
	if err := exec2.Start(); err != nil {
		t.Fatalf("start2: %v", err)
	}
	_ = exec2.RunAction("go")
	exec2.Close()

	// Expect a no_longer_matches event for the second step (.present).
	var sawGone bool
	for _, d := range drifts {
		if d.Kind == baseline.DriftSelectorNoLongerMatches && d.Selector == ".present" {
			sawGone = true
		}
	}
	if !sawGone {
		t.Errorf("expected no_longer_matches drift for .present, got %v", drifts)
	}
}

// anyTime is just a stable timestamp for tests that need one.
func anyTime() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
