package examples

import (
	"strings"
	"testing"
)

func TestNames_NonEmpty(t *testing.T) {
	names := Names()
	if len(names) == 0 {
		t.Fatal("expected bundled examples; got 0")
	}
	// Sanity: no .yaml suffix, no empty entries, sorted ascending.
	for i, n := range names {
		if n == "" {
			t.Errorf("empty name at index %d", i)
		}
		if strings.HasSuffix(n, ".yaml") {
			t.Errorf("name %q includes .yaml suffix; should be stripped", n)
		}
		if i > 0 && names[i-1] > n {
			t.Errorf("names not sorted: %q before %q", names[i-1], n)
		}
	}
}

func TestNames_IncludesNewExamples(t *testing.T) {
	// Pin that the new bead-7 examples are surfaced. If a future contributor
	// renames or removes them, the discoverability they were filed for
	// vanishes — this guard catches that silently.
	want := []string{
		"captcha-gated-form",
		"click-visible-among-duplicates",
		"modal-export-with-save-and-return",
	}
	have := make(map[string]bool)
	for _, n := range Names() {
		have[n] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("missing bundled example %q (run `brz examples list` to see what's there)", w)
		}
	}
}

func TestRead_ReturnsBytesForKnownName(t *testing.T) {
	data, _, err := Read("captcha-gated-form")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("returned 0 bytes")
	}
	// First line is a "# Example:" comment by convention.
	first := strings.SplitN(string(data), "\n", 2)[0]
	if !strings.HasPrefix(first, "# Example:") {
		t.Errorf("expected first line to start with '# Example:'; got %q", first)
	}
}

func TestRead_UnknownNameReturnsError(t *testing.T) {
	_, filename, err := Read("definitely-not-real")
	if err == nil {
		t.Fatal("expected error for unknown name; got nil")
	}
	if !strings.Contains(filename, "definitely-not-real.yaml") {
		t.Errorf("filename should be the looked-up path for the error message; got %q", filename)
	}
}

func TestSummary_ExtractsDemonstratesLine(t *testing.T) {
	yaml := []byte(`# Example: do a thing
#
# Demonstrates: foo, bar, baz

name: t
`)
	got := Summary(yaml)
	if got != "foo, bar, baz" {
		t.Errorf("Summary should return the Demonstrates list; got %q", got)
	}
}

func TestSummary_FallsBackToFirstCommentWhenNoDemonstrates(t *testing.T) {
	yaml := []byte(`# Just a brief example

name: t
`)
	got := Summary(yaml)
	if got != "Just a brief example" {
		t.Errorf("Summary fallback: got %q", got)
	}
}

func TestSummary_EmptyYamlReturnsEmpty(t *testing.T) {
	got := Summary([]byte(""))
	if got != "" {
		t.Errorf("empty yaml → got %q, want empty", got)
	}
	got = Summary([]byte("name: noheader\n"))
	if got != "" {
		t.Errorf("yaml with no comments → got %q, want empty", got)
	}
}

func TestEachExample_HasExampleHeaderAndDemonstrates(t *testing.T) {
	// Convention guard: every bundled YAML must follow the "# Example: ..."
	// + "# Demonstrates: ..." pattern so `brz examples list` surfaces a
	// useful summary line for each one.
	for _, name := range Names() {
		t.Run(name, func(t *testing.T) {
			data, _, err := Read(name)
			if err != nil {
				t.Fatal(err)
			}
			s := string(data)
			if !strings.Contains(s, "# Example:") {
				t.Errorf("%s missing '# Example:' header", name)
			}
			if !strings.Contains(s, "# Demonstrates:") {
				t.Errorf("%s missing '# Demonstrates:' header (needed for `examples list` summary)", name)
			}
		})
	}
}
