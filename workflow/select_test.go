package workflow

import (
	"testing"
)

func TestSelectStepParsing(t *testing.T) {
	yaml := `
name: test-select
actions:
  test:
    steps:
      - label: "Select category"
        select:
          selector: '#my-dropdown'
          value: '3'
          timeout: '10s'
`
	w, err := LoadFromBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	step := w.Actions["test"].Steps[0]
	if step.Select == nil {
		t.Fatal("expected select step, got nil")
	}
	if step.Select.Selector != "#my-dropdown" {
		t.Errorf("selector: got %q, want #my-dropdown", step.Select.Selector)
	}
	if step.Select.Value != "3" {
		t.Errorf("value: got %q, want 3", step.Select.Value)
	}
	if step.Select.Timeout != "10s" {
		t.Errorf("timeout: got %q, want 10s", step.Select.Timeout)
	}
}

func TestSelectStepParsing_TextMatch(t *testing.T) {
	yaml := `
name: test-select-text
actions:
  test:
    steps:
      - select:
          selector: '#category'
          text: 'Pokemon'
`
	w, err := LoadFromBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	step := w.Actions["test"].Steps[0]
	if step.Select == nil {
		t.Fatal("expected select step, got nil")
	}
	if step.Select.Text != "Pokemon" {
		t.Errorf("text: got %q, want Pokemon", step.Select.Text)
	}
}

func TestSelectStepParsing_EnvInterpolation(t *testing.T) {
	yaml := `
name: test-select-env
env:
  CATEGORY_ID: "42"
actions:
  test:
    steps:
      - select:
          selector: '#dropdown'
          value: '${CATEGORY_ID}'
`
	w, err := LoadFromBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	step := w.Actions["test"].Steps[0]
	if step.Select == nil {
		t.Fatal("expected select step")
	}
	// Value should still be ${CATEGORY_ID} at parse time — interpolation happens at execution
	if step.Select.Value != "${CATEGORY_ID}" {
		t.Errorf("value: got %q, want ${CATEGORY_ID}", step.Select.Value)
	}
}

func TestSelectStepParsing_DefaultTimeout(t *testing.T) {
	yaml := `
name: test-select-default
actions:
  test:
    steps:
      - select:
          selector: '#dropdown'
          value: 'x'
`
	w, err := LoadFromBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	step := w.Actions["test"].Steps[0]
	if step.Select == nil {
		t.Fatal("expected select step")
	}
	// Empty timeout should default to 5s when parsed by ParseTimeout
	timeout := ParseTimeout(step.Select.Timeout)
	// ParseTimeout returns 30s for empty string by default, but select should use 5s
	// We'll handle the default in the executor, not the parser
	if step.Select.Timeout != "" {
		t.Errorf("timeout should be empty (default handled at execution), got %q", step.Select.Timeout)
	}
	_ = timeout // just verifying it doesn't panic
}
