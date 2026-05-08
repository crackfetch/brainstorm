package workflow

import (
	"errors"
	"strings"
	"testing"
)

func TestLevenshtein_Cases(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"abc", "", 3},
		{"", "xyz", 3},
		{"save_too", "save_to", 1},  // the canonical typo this bead fixes
		{"urll", "url", 1},
		{"save_to", "save_as", 2},   // two substitutions: t→a, o→s
		{"foo", "bar", 3},
	}
	for _, tc := range tests {
		got := levenshtein(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("levenshtein(%q,%q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestBestSuggestion(t *testing.T) {
	candidates := []string{"timeout", "save_as", "save_to", "return_to"}
	tests := []struct {
		needle string
		want   string
	}{
		{"save_too", "save_to"},
		{"save_t", "save_to"},
		{"reutrn_to", "return_to"},
		{"timeo", "timeout"},
		{"completely_different_field", ""}, // too far → no suggestion
		{"", ""},                           // empty needle → no suggestion
	}
	for _, tc := range tests {
		got := bestSuggestion(tc.needle, candidates)
		if got != tc.want {
			t.Errorf("bestSuggestion(%q) = %q, want %q", tc.needle, got, tc.want)
		}
	}
}

func TestBestSuggestion_NoCandidatesReturnsEmpty(t *testing.T) {
	if got := bestSuggestion("x", nil); got != "" {
		t.Errorf("expected empty for no candidates; got %q", got)
	}
	if got := bestSuggestion("x", []string{}); got != "" {
		t.Errorf("expected empty for empty slice; got %q", got)
	}
}

func TestFieldRegistry_KnownTypesPresent(t *testing.T) {
	// Pin that every workflow type yaml.v3 might reject into has an
	// entry in the registry. Adding a type without registering it would
	// drop the suggestion silently — this test fails CI before that
	// regression ships.
	want := []string{
		"workflow.Workflow",
		"workflow.Action",
		"workflow.Step",
		"workflow.ClickStep",
		"workflow.FillStep",
		"workflow.SelectStep",
		"workflow.UploadStep",
		"workflow.DownloadStep",
		"workflow.WaitStep",
		"workflow.WaitURLStep",
		"workflow.SleepStep",
		"workflow.EvalAssert",
		"workflow.Viewport",
	}
	for _, w := range want {
		if _, ok := fieldRegistry[w]; !ok {
			t.Errorf("registry missing entry for %q (add to buildFieldRegistry's types slice)", w)
		}
	}
}

func TestFieldRegistry_DownloadStepHasSaveTo(t *testing.T) {
	// Sanity: the registry actually picked up the new save_to/return_to
	// fields added in earlier beads. If a refactor drops the yaml tag,
	// this test catches it.
	got, ok := fieldRegistry["workflow.DownloadStep"]
	if !ok {
		t.Fatal("no entry for DownloadStep")
	}
	want := []string{"timeout", "save_as", "save_to", "return_to"}
	for _, w := range want {
		found := false
		for _, g := range got {
			if g == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("DownloadStep registry missing yaml field %q (got %v)", w, got)
		}
	}
}

func TestDecorateStrictError_AddsSuggestion(t *testing.T) {
	in := errors.New("yaml: unmarshal errors:\n  line 7: field save_too not found in type workflow.DownloadStep")
	out := decorateStrictError(in).Error()
	for _, want := range []string{
		"line 7:",
		"unknown field 'save_too'",
		"download step",
		"Did you mean 'save_to'?",
		"Valid fields:",
		"timeout",
		"return_to",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("decorated error missing %q\n  got: %s", want, out)
		}
	}
}

func TestDecorateStrictError_NoSuggestionWhenFar(t *testing.T) {
	// Field too far from any valid name: still call out unknown +
	// list valid options, but skip the "Did you mean" line.
	in := errors.New("yaml: unmarshal errors:\n  line 7: field totally_unrelated_garbage not found in type workflow.DownloadStep")
	out := decorateStrictError(in).Error()
	if strings.Contains(out, "Did you mean") {
		t.Errorf("far-from-anything field should not trigger a suggestion; got: %s", out)
	}
	if !strings.Contains(out, "Valid fields:") {
		t.Errorf("should still list valid fields even without a suggestion; got: %s", out)
	}
}

func TestDecorateStrictError_HandlesMultipleTyposInOneError(t *testing.T) {
	in := errors.New(`yaml: unmarshal errors:
  line 7: field save_too not found in type workflow.DownloadStep
  line 11: field urll not found in type workflow.Action`)
	out := decorateStrictError(in).Error()
	if !strings.Contains(out, "Did you mean 'save_to'?") {
		t.Errorf("first typo not decorated; got: %s", out)
	}
	if !strings.Contains(out, "Did you mean 'url'?") {
		t.Errorf("second typo not decorated; got: %s", out)
	}
}

func TestDecorateStrictError_LeavesUnrelatedLinesAlone(t *testing.T) {
	in := errors.New("yaml: line 3: did not find expected key")
	out := decorateStrictError(in).Error()
	if out != in.Error() {
		t.Errorf("non-strict error was modified:\n  in:  %q\n  out: %q", in.Error(), out)
	}
}

func TestDecorateStrictError_NilSafe(t *testing.T) {
	if got := decorateStrictError(nil); got != nil {
		t.Errorf("nil input should return nil; got %v", got)
	}
}

func TestDecorateStrictError_PreservesUnwrapChain(t *testing.T) {
	// errors.As/errors.Is consumers must still reach the underlying
	// error after decoration. Without an Unwrap() method on the wrapper,
	// agents introspecting the error type would silently start failing
	// once strict mode is on.
	orig := &sentinelError{msg: "yaml: unmarshal errors:\n  line 7: field save_too not found in type workflow.DownloadStep"}
	wrapped := decorateStrictError(orig)
	if wrapped == nil {
		t.Fatal("decoration returned nil for non-nil input")
	}
	var got *sentinelError
	if !errorsAs(wrapped, &got) {
		t.Errorf("errors.As should reach the original error through the wrapper")
	}
	// And Error() text is the decorated form.
	if !strings.Contains(wrapped.Error(), "Did you mean 'save_to'?") {
		t.Errorf("decorated Error() should include suggestion; got: %s", wrapped.Error())
	}
}

// sentinelError is a tiny test-only error type so we can prove the
// decoration wrapper preserves identity through errors.As. Production
// code wraps yaml.TypeError; using a sentinel keeps the test honest
// without depending on yaml.v3's internals.
type sentinelError struct{ msg string }

func (e *sentinelError) Error() string { return e.msg }

// errorsAs is a thin alias around errors.As to keep the test concise.
// (The stdlib errors package is imported in this file already.)
func errorsAs(err error, target any) bool { return errors.As(err, target) }

func TestHumanTypeName(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"workflow.DownloadStep", "download step"},
		{"workflow.ClickStep", "click step"},
		{"workflow.WaitURLStep", "wait_url step"},
		{"workflow.WaitStep", "wait step"},
		{"workflow.Action", "Action"},
		{"workflow.Workflow", "Workflow"},
		{"workflow.SleepStep", "sleep step"},
	}
	for _, tc := range tests {
		got := humanTypeName(tc.in)
		if got != tc.want {
			t.Errorf("humanTypeName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestLoadStrictFromBytes_ProducesDecoratedError(t *testing.T) {
	yamlSrc := `
name: t
actions:
  do:
    steps:
      - download:
          save_too: "/tmp/oops.csv"
`
	_, err := LoadStrictFromBytes([]byte(yamlSrc))
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	for _, want := range []string{
		"unknown field 'save_too'",
		"download step",
		"Did you mean 'save_to'?",
		"save_to",
		"return_to",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("LoadStrictFromBytes error missing %q\n  got: %s", want, msg)
		}
	}
}
