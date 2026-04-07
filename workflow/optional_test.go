package workflow

import (
	"testing"
)

func TestOptionalStepParsing(t *testing.T) {
	yaml := `
name: test-optional
actions:
  login:
    url: https://example.com/login
    steps:
      - fill: { selector: 'input[name="email"]', value: '${EMAIL}' }
      - label: "Check Remember Me"
        click: { selector: '#RememberMe', timeout: '3s' }
        optional: true
      - click: { selector: 'button[type="submit"]' }
`
	path := writeTempYAML(t, yaml)

	w, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	login := w.Actions["login"]
	if len(login.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(login.Steps))
	}

	if login.Steps[0].Optional {
		t.Error("step 0 should not be optional")
	}
	if !login.Steps[1].Optional {
		t.Error("step 1 should be optional")
	}
	if login.Steps[1].Label != "Check Remember Me" {
		t.Errorf("step 1 label wrong: %q", login.Steps[1].Label)
	}
	if login.Steps[2].Optional {
		t.Error("step 2 should not be optional")
	}
}

// Note: testing that optional steps are actually skipped on failure requires
// a running browser (executor.runSteps). That's covered by manual E2E testing
// with `brz run`. Adding a mock browser to test this would be testing the mock,
// not the behavior.
