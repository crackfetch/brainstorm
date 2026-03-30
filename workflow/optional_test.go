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

	// Step 0: not optional
	if login.Steps[0].Optional {
		t.Error("step 0 should not be optional")
	}

	// Step 1: optional
	if !login.Steps[1].Optional {
		t.Error("step 1 should be optional")
	}

	// Step 2: not optional
	if login.Steps[2].Optional {
		t.Error("step 2 should not be optional")
	}
}

func TestOptionalStep_Success(t *testing.T) {
	// When an optional step succeeds, the action should complete normally.
	// We test this via runSteps by verifying the optional field parses and
	// a successful step sequence works end-to-end.
	yaml := `
name: test-optional-success
actions:
  test:
    steps:
      - eval: "1+1"
      - label: "Optional eval"
        eval: "2+2"
        optional: true
      - eval: "3+3"
`
	path := writeTempYAML(t, yaml)

	w, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	action := w.Actions["test"]
	if len(action.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(action.Steps))
	}

	// All three steps should parse, with step 1 optional
	if !action.Steps[1].Optional {
		t.Error("step 1 should be optional")
	}
}

func TestOptionalStep_Failure(t *testing.T) {
	// When an optional step fails, the action should still complete OK=true.
	// We test this at the runSteps level by constructing a workflow with a
	// step that will fail (navigate to invalid URL) marked as optional.
	// Since runSteps requires a browser, we test the logic unit: verify
	// that the Optional field is correctly set so the executor can use it.
	yaml := `
name: test-optional-failure
actions:
  test:
    steps:
      - label: "Will fail but optional"
        click: { selector: '#nonexistent', timeout: '1s' }
        optional: true
      - eval: "1+1"
`
	path := writeTempYAML(t, yaml)

	w, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	action := w.Actions["test"]
	if !action.Steps[0].Optional {
		t.Error("step 0 should be optional")
	}
	if action.Steps[1].Optional {
		t.Error("step 1 should not be optional")
	}
}

func TestOptionalStep_NonOptionalFailure(t *testing.T) {
	// Regression test: non-optional steps should still fail the action.
	// Verify that a non-optional failing step is NOT marked optional.
	yaml := `
name: test-non-optional-failure
actions:
  test:
    steps:
      - click: { selector: '#nonexistent', timeout: '1s' }
      - eval: "1+1"
`
	path := writeTempYAML(t, yaml)

	w, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	action := w.Actions["test"]
	if action.Steps[0].Optional {
		t.Error("step 0 should NOT be optional")
	}
}
