package workflow

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadValidWorkflow(t *testing.T) {
	yaml := `
name: test-workflow
actions:
  login:
    url: https://example.com/login
    steps:
      - fill: { selector: 'input[name="email"]', value: '${EMAIL}' }
      - fill: { selector: 'input[name="password"]', value: '${PASSWORD}' }
      - click: { selector: 'button[type="submit"]' }
      - wait_url: { match: 'example.com/dashboard', timeout: '30s' }
  export:
    url: https://example.com/export
    steps:
      - click: { selector: '#export-btn' }
      - download: { timeout: '60s' }
`
	path := writeTempYAML(t, yaml)

	w, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w.Name != "test-workflow" {
		t.Errorf("expected name=test-workflow, got %s", w.Name)
	}
	if len(w.Actions) != 2 {
		t.Errorf("expected 2 actions, got %d", len(w.Actions))
	}

	login := w.Actions["login"]
	if login.URL != "https://example.com/login" {
		t.Errorf("expected login URL, got %s", login.URL)
	}
	if len(login.Steps) != 4 {
		t.Errorf("expected 4 login steps, got %d", len(login.Steps))
	}

	// Verify fill step parsed correctly
	step := login.Steps[0]
	if step.Fill == nil {
		t.Fatal("expected fill step")
	}
	if step.Fill.Selector != `input[name="email"]` {
		t.Errorf("expected email selector, got %s", step.Fill.Selector)
	}
	if step.Fill.Value != "${EMAIL}" {
		t.Errorf("expected ${EMAIL} value, got %s", step.Fill.Value)
	}

	// Verify click step
	step = login.Steps[2]
	if step.Click == nil {
		t.Fatal("expected click step")
	}
	if step.Click.Selector != `button[type="submit"]` {
		t.Errorf("expected submit selector, got %s", step.Click.Selector)
	}

	// Verify download step
	export := w.Actions["export"]
	if export.Steps[1].Download == nil {
		t.Fatal("expected download step")
	}
	if export.Steps[1].Download.Timeout != "60s" {
		t.Errorf("expected 60s timeout, got %s", export.Steps[1].Download.Timeout)
	}
}

func TestLoadMissingName(t *testing.T) {
	yaml := `
actions:
  login:
    steps:
      - click: { selector: '#btn' }
`
	path := writeTempYAML(t, yaml)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestLoadNoActions(t *testing.T) {
	yaml := `
name: empty
actions: {}
`
	path := writeTempYAML(t, yaml)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for no actions")
	}
}

func TestInterpolateEnv(t *testing.T) {
	os.Setenv("TEST_EMAIL", "user@example.com")
	defer os.Unsetenv("TEST_EMAIL")

	result := InterpolateEnv("${TEST_EMAIL}", nil)
	if result != "user@example.com" {
		t.Errorf("expected user@example.com, got %s", result)
	}
}

func TestInterpolateEnvWorkflowOverride(t *testing.T) {
	os.Setenv("MY_VAR", "from-os")
	defer os.Unsetenv("MY_VAR")

	wEnv := map[string]string{"MY_VAR": "from-workflow"}
	result := InterpolateEnv("${MY_VAR}", wEnv)
	if result != "from-workflow" {
		t.Errorf("expected from-workflow, got %s", result)
	}
}

func TestInterpolateEnvMissing(t *testing.T) {
	result := InterpolateEnv("${NONEXISTENT_VAR_12345}", nil)
	if result != "${NONEXISTENT_VAR_12345}" {
		t.Errorf("expected unchanged placeholder, got %s", result)
	}
}

func TestParseTimeout(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"30s", "30s"},
		{"2m", "2m0s"},
		{"", "30s"},
		{"garbage", "30s"},
		{"120s", "2m0s"},
	}

	for _, tt := range tests {
		d := ParseTimeout(tt.input)
		if d.String() != tt.expected {
			t.Errorf("ParseTimeout(%q) = %s, want %s", tt.input, d.String(), tt.expected)
		}
	}
}

func writeTempYAML(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "workflow.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}
