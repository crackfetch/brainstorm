package baseline

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBaselineRoundTrip(t *testing.T) {
	in := New("fetch-orders.yaml", "export", "0.13.0", time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC), []StepObservation{
		{Name: "goto", Action: "navigate", Target: "https://x.test/a", OK: true},
		{Name: "click row", Action: "click", Selector: ".row", MatchedCount: IntPtr(12), OK: true, SampleTextHash: HashText("Hello")},
		{Name: "extract", Action: "wait_visible", Selector: ".name", MatchedCount: IntPtr(0), OK: false},
	})

	var buf bytes.Buffer
	if err := in.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}

	out, err := Read(&buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if out.Version != SchemaVersion {
		t.Errorf("Version: got %d want %d", out.Version, SchemaVersion)
	}
	if out.Workflow != "fetch-orders.yaml" {
		t.Errorf("Workflow: %q", out.Workflow)
	}
	if len(out.Steps) != 3 {
		t.Fatalf("Steps len: %d", len(out.Steps))
	}
	if out.Steps[1].MatchedCount == nil || *out.Steps[1].MatchedCount != 12 {
		t.Errorf("MatchedCount[1]: %+v", out.Steps[1].MatchedCount)
	}
	// MatchedCount=0 must round-trip as 0, not nil.
	if out.Steps[2].MatchedCount == nil || *out.Steps[2].MatchedCount != 0 {
		t.Errorf("MatchedCount[2]: %+v (expected pointer to 0)", out.Steps[2].MatchedCount)
	}
	if !strings.HasPrefix(out.Steps[1].SampleTextHash, "sha256:") {
		t.Errorf("SampleTextHash prefix: %q", out.Steps[1].SampleTextHash)
	}
}

func TestBaselineUnknownFieldsIgnored(t *testing.T) {
	// A baseline written by a future brz with a new top-level field and
	// a new per-step field. Decoding must not fail.
	js := `{
		"version": 99,
		"workflow": "x.yaml",
		"captured_at": "2030-01-01T00:00:00Z",
		"brz_version": "9.9.9",
		"future_top_level_field": {"weird": [1,2,3]},
		"steps": [
			{"name":"a","action":"click","selector":".x","matched_count":3,"future_step_field":"yes"}
		]
	}`
	b, err := Read(strings.NewReader(js))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if b.Version != 99 {
		t.Errorf("Version: %d", b.Version)
	}
	if len(b.Steps) != 1 {
		t.Fatalf("Steps: %d", len(b.Steps))
	}
	if b.Steps[0].MatchedCount == nil || *b.Steps[0].MatchedCount != 3 {
		t.Errorf("MatchedCount: %+v", b.Steps[0].MatchedCount)
	}
}

func TestHashText(t *testing.T) {
	a := HashText("  Hello World  ")
	b := HashText("hello world")
	if a != b {
		t.Errorf("HashText not trim+lower-stable: %q vs %q", a, b)
	}
	if HashText("") != "" {
		t.Errorf("HashText(\"\") should be empty")
	}
	if !strings.HasPrefix(a, "sha256:") {
		t.Errorf("HashText prefix: %q", a)
	}
}

func TestResolvePath(t *testing.T) {
	cases := []struct {
		wf, arg, want string
	}{
		{"workflows/fetch.yaml", "auto", filepath.Join("workflows", ".brz-baselines", "fetch.baseline.json")},
		{"/abs/path/wf.yml", "auto", filepath.Join("/abs/path", ".brz-baselines", "wf.baseline.json")},
		{"any.yaml", "/explicit/path.json", "/explicit/path.json"},
		{"any.yaml", "rel/path.json", "rel/path.json"},
	}
	for _, c := range cases {
		got := ResolvePath(c.wf, c.arg)
		if got != c.want {
			t.Errorf("ResolvePath(%q, %q) = %q; want %q", c.wf, c.arg, got, c.want)
		}
	}
}

func TestComparator_NilBaseline(t *testing.T) {
	c := NewComparator(nil)
	got := c.Compare(StepObservation{}, StepObservation{Selector: ".x", MatchedCount: IntPtr(1)})
	if len(got) != 0 {
		t.Errorf("nil baseline: expected no drift, got %v", got)
	}
}

func TestComparator_CountChanged(t *testing.T) {
	c := NewComparator(&Baseline{Version: SchemaVersion})
	base := StepObservation{Selector: ".row", MatchedCount: IntPtr(12)}
	live := StepObservation{Name: "click row", Selector: ".row", MatchedCount: IntPtr(13), Action: "click"}
	got := c.Compare(base, live)
	if len(got) != 1 || got[0].Kind != DriftSelectorCountChanged {
		t.Fatalf("expected count-changed drift, got %#v", got)
	}
	if got[0].BaselineHits != 12 || got[0].CurrentHits != 13 {
		t.Errorf("hits wrong: %+v", got[0])
	}
}

func TestComparator_NoLongerMatches(t *testing.T) {
	c := NewComparator(&Baseline{Version: SchemaVersion})
	base := StepObservation{Selector: ".row", MatchedCount: IntPtr(12)}
	live := StepObservation{Name: "click row", Selector: ".row", MatchedCount: IntPtr(0), Action: "click"}
	got := c.Compare(base, live)
	if len(got) != 1 || got[0].Kind != DriftSelectorNoLongerMatches {
		t.Fatalf("expected no-longer-matches, got %#v", got)
	}
}

func TestComparator_TextChanged(t *testing.T) {
	c := NewComparator(&Baseline{Version: SchemaVersion})
	base := StepObservation{Selector: ".name", MatchedCount: IntPtr(1), SampleTextHash: HashText("Order #1234")}
	live := StepObservation{Name: "extract", Selector: ".name", MatchedCount: IntPtr(1), SampleTextHash: HashText("Site is down for maintenance"), Action: "wait_visible"}
	got := c.Compare(base, live)
	if len(got) != 1 || got[0].Kind != DriftTextPatternChanged {
		t.Fatalf("expected text-changed, got %#v", got)
	}
}

func TestComparator_NoDriftWhenSame(t *testing.T) {
	c := NewComparator(&Baseline{Version: SchemaVersion})
	hash := HashText("same")
	base := StepObservation{Selector: ".x", MatchedCount: IntPtr(3), SampleTextHash: hash}
	live := StepObservation{Selector: ".x", MatchedCount: IntPtr(3), SampleTextHash: hash}
	got := c.Compare(base, live)
	if len(got) != 0 {
		t.Errorf("expected no drift, got %v", got)
	}
}

func TestComparator_TextDriftNotFiredWhenOneMissing(t *testing.T) {
	// If the baseline didn't capture a hash (e.g. selector matched but
	// element had empty innerText), and the current run does have one,
	// we should NOT fire text drift — that would be a false positive.
	c := NewComparator(&Baseline{Version: SchemaVersion})
	base := StepObservation{Selector: ".x", MatchedCount: IntPtr(1), SampleTextHash: ""}
	live := StepObservation{Selector: ".x", MatchedCount: IntPtr(1), SampleTextHash: HashText("Hello")}
	got := c.Compare(base, live)
	if len(got) != 0 {
		t.Errorf("expected no text drift when baseline hash empty, got %v", got)
	}
}

func TestComparator_ObservationFor_SelectorMismatchSkipped(t *testing.T) {
	b := &Baseline{
		Version: SchemaVersion,
		Steps: []StepObservation{
			{Name: "a", Selector: ".old", MatchedCount: IntPtr(1)},
		},
	}
	c := NewComparator(b)
	if _, ok := c.ObservationFor(0, ".new"); ok {
		t.Errorf("ObservationFor should return false when baseline selector differs (workflow edited)")
	}
	if _, ok := c.ObservationFor(0, ".old"); !ok {
		t.Errorf("ObservationFor should match same selector")
	}
	if _, ok := c.ObservationFor(99, ".old"); ok {
		t.Errorf("ObservationFor should return false for out-of-range index")
	}
}
