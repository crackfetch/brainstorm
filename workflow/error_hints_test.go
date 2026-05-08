package workflow

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Tests for "nearby elements" hints surfaced inside step-failure error
// strings. Two layers:
//   - summarizeNearbyElements: pure unit tests for the formatting.
//   - E2E (skipIfNoChrome): force a click failure on a real page and assert
//     the hint appears in result.Error.

func TestSummarizeNearbyElements_Empty(t *testing.T) {
	if got := summarizeNearbyElements(nil, 5); got != "" {
		t.Errorf("nil → got %q, want empty", got)
	}
	if got := summarizeNearbyElements([]ElementInfo{}, 5); got != "" {
		t.Errorf("empty slice → got %q, want empty", got)
	}
	if got := summarizeNearbyElements([]ElementInfo{{Selector: "x"}}, 0); got != "" {
		t.Errorf("zero limit → got %q, want empty", got)
	}
}

func TestSummarizeNearbyElements_Format(t *testing.T) {
	els := []ElementInfo{
		{Selector: "button.primary", Text: "Sign In"},
		{Selector: "input[type=submit]", Value: "Submit"},
		{Selector: "a.cta", Text: "Continue"},
	}
	got := summarizeNearbyElements(els, 5)
	for _, want := range []string{
		"Nearby visible elements:",
		`button.primary (Sign In)`,
		`input[type=submit] (Submit)`,
		`a.cta (Continue)`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in summary: %s", want, got)
		}
	}
	// Single line — no embedded newline.
	if strings.Contains(got, "\n") {
		t.Errorf("summary should be a single line; got %q", got)
	}
}

func TestSummarizeNearbyElements_RespectsLimit(t *testing.T) {
	els := []ElementInfo{
		{Selector: "a", Text: "1"},
		{Selector: "b", Text: "2"},
		{Selector: "c", Text: "3"},
		{Selector: "d", Text: "4"},
		{Selector: "e", Text: "5"},
	}
	got := summarizeNearbyElements(els, 3)
	for _, want := range []string{"a (1)", "b (2)", "c (3)"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in summary: %s", want, got)
		}
	}
	for _, unwanted := range []string{"d (4)", "e (5)"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("limit not respected — found %q in: %s", unwanted, got)
		}
	}
}

func TestSummarizeNearbyElements_SkipsHidden(t *testing.T) {
	// A hint that says "click .foo (hidden)" sends the user chasing the
	// wrong fix. Hidden elements aren't candidates for a fix, so they
	// must not appear in the summary.
	els := []ElementInfo{
		{Selector: "button.visible", Text: "OK"},
		{Selector: "button.hidden", Text: "should-not-appear", Hidden: true},
		{Selector: "button.also-visible", Text: "Cancel"},
	}
	got := summarizeNearbyElements(els, 5)
	if strings.Contains(got, "should-not-appear") {
		t.Errorf("hidden element leaked into summary: %s", got)
	}
	if !strings.Contains(got, "button.visible") {
		t.Errorf("visible elements should appear: %s", got)
	}
}

func TestSummarizeNearbyElements_TruncatesLongText(t *testing.T) {
	els := []ElementInfo{
		{
			Selector: "button.long",
			Text:     "This text is far too long to show inline in an error message and must be truncated",
		},
	}
	got := summarizeNearbyElements(els, 1)
	// Should contain the first 27 chars + "..."
	if !strings.Contains(got, "This text is far too long t...") {
		t.Errorf("long text should be truncated to 30 chars; got: %s", got)
	}
}

func TestElementDisplayText_TruncatesByRunesNotBytes(t *testing.T) {
	// 30 multi-byte runes (Japanese hiragana). Byte-based truncation would
	// cut mid-character and produce mojibake; rune-based truncation keeps
	// each character intact.
	const oneRune = "あ"
	const fortyRunes = oneRune + oneRune + oneRune + oneRune + oneRune +
		oneRune + oneRune + oneRune + oneRune + oneRune +
		oneRune + oneRune + oneRune + oneRune + oneRune +
		oneRune + oneRune + oneRune + oneRune + oneRune +
		oneRune + oneRune + oneRune + oneRune + oneRune +
		oneRune + oneRune + oneRune + oneRune + oneRune +
		oneRune + oneRune + oneRune + oneRune + oneRune +
		oneRune + oneRune + oneRune + oneRune + oneRune
	got := elementDisplayText(ElementInfo{Text: fortyRunes})
	if !strings.HasSuffix(got, "...") {
		t.Errorf("expected trailing ...; got %q", got)
	}
	// 27 runes + 3 dots = 30 runes total. Each rune is multi-byte but
	// counting visible characters: should be exactly 30.
	if cnt := len([]rune(got)); cnt != 30 {
		t.Errorf("expected 30 runes after truncation, got %d (%q)", cnt, got)
	}
}

func TestElementDisplayText_FallbackChain(t *testing.T) {
	tests := []struct {
		name string
		el   ElementInfo
		want string
	}{
		{"text wins", ElementInfo{Text: "T", Value: "V", Placeholder: "P"}, "T"},
		{"value when text empty", ElementInfo{Value: "V", Placeholder: "P"}, "V"},
		{"placeholder when value empty", ElementInfo{Placeholder: "P", Name: "N"}, "P"},
		{"name when others empty", ElementInfo{Name: "N", Role: "R"}, "N"},
		{"role last resort", ElementInfo{Role: "button"}, "button"},
		{"all empty returns empty", ElementInfo{}, ""},
		{"whitespace-only treated as empty", ElementInfo{Text: "   ", Value: "V"}, "V"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := elementDisplayText(tc.el)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// E2E: a click that fails on a real page must include the hint in the
// returned ActionResult.Error string.

func TestRunAction_ClickFails_ErrorIncludesNearbyHint(t *testing.T) {
	skipIfNoChrome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><html><body>
<button class="present-button">Click Me</button>
<button class="other-button">Or Me</button>
<input type="submit" value="Submit"/>
</body></html>`))
	}))
	defer srv.Close()

	w := &Workflow{
		Name: "click-error-hint",
		Actions: map[string]Action{
			"do": {
				URL: srv.URL,
				Steps: []Step{
					// Click a selector that does NOT exist — failure path.
					// Page has present-button, other-button, input[type=submit];
					// the captureSimilarElements logic should surface them.
					{Click: &ClickStep{Selector: "button.absolutely-not-here", Timeout: "1s"}},
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
		t.Fatal("expected action to fail; got success")
	}
	if !strings.Contains(res.Error, "Nearby visible elements:") {
		t.Errorf("error should include 'Nearby visible elements:' hint; got: %s", res.Error)
	}
	if len(res.PageElements) == 0 {
		t.Errorf("expected PageElements to also be populated; got 0")
	}
	// At least one of the real buttons should appear in the hint.
	if !strings.Contains(res.Error, "present-button") &&
		!strings.Contains(res.Error, "other-button") &&
		!strings.Contains(res.Error, "submit") {
		t.Errorf("hint should reference at least one real button; got: %s", res.Error)
	}
}

func TestRunAction_NoStepSelector_NoHint(t *testing.T) {
	skipIfNoChrome(t)
	// A step that doesn't target a selector (e.g. sleep) shouldn't append
	// the hint when it fails — there's nothing to compare to. Backward-
	// compat guard for non-selector failures.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><html><body><p>x</p></body></html>`))
	}))
	defer srv.Close()
	w := &Workflow{
		Name: "no-selector-fail",
		Actions: map[string]Action{
			"do": {
				URL: srv.URL,
				Steps: []Step{
					// Force an error from a selector-less step path: an eval
					// that throws. eval has no selector so StepSelector("") →
					// no PageElements capture → no hint appended.
					{Eval: `throw new Error('intentional')`},
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
		t.Fatal("expected action to fail")
	}
	if strings.Contains(res.Error, "Nearby visible elements:") {
		t.Errorf("non-selector failure should NOT include nearby hint; got: %s", res.Error)
	}
}
