package workflow

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestResolveSteps_InterpolatesEnv(t *testing.T) {
	action := Action{
		URL: "https://${HOST}/login",
		Steps: []Step{
			{Fill: &FillStep{Selector: "#email", Value: "${EMAIL}"}},
			{Click: &ClickStep{Selector: "#submit"}},
		},
	}
	env := map[string]string{"HOST": "example.com", "EMAIL": "test@co.com"}

	resolved := ResolveSteps(action, env)
	if len(resolved) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(resolved))
	}

	// Fill step should have interpolated value
	if resolved[0].Type != "fill" {
		t.Errorf("expected type=fill, got %s", resolved[0].Type)
	}
	if resolved[0].Value != "test@co.com" {
		t.Errorf("expected interpolated email, got %s", resolved[0].Value)
	}
	if resolved[0].Selector != "#email" {
		t.Errorf("expected selector #email, got %s", resolved[0].Selector)
	}

	// Click step
	if resolved[1].Type != "click" {
		t.Errorf("expected type=click, got %s", resolved[1].Type)
	}
	if resolved[1].Selector != "#submit" {
		t.Errorf("expected selector #submit, got %s", resolved[1].Selector)
	}
}

func TestResolveSteps_PreservesUnresolved(t *testing.T) {
	action := Action{
		Steps: []Step{
			{Fill: &FillStep{Selector: "#pw", Value: "${MISSING_VAR}"}},
		},
	}
	resolved := ResolveSteps(action, nil)
	if resolved[0].Value != "${MISSING_VAR}" {
		t.Errorf("expected unresolved var preserved, got %s", resolved[0].Value)
	}
}

func TestResolveSteps_AllStepTypes(t *testing.T) {
	action := Action{
		Steps: []Step{
			{Navigate: "https://example.com"},
			{Click: &ClickStep{Selector: "#btn", Text: "Go", Timeout: "10s"}},
			{Fill: &FillStep{Selector: "#input", Value: "hello", Clear: true}},
			{Select: &SelectStep{Selector: "#dropdown", Value: "opt1"}},
			{Upload: &UploadStep{Selector: "#file", Source: "/tmp/file.csv"}},
			{Download: &DownloadStep{Timeout: "60s"}},
			{WaitVisible: &WaitStep{Selector: "#loader", Timeout: "5s"}},
			{WaitText: &WaitStep{Text: "Success", Timeout: "5s"}},
			{WaitURL: &WaitURLStep{Match: "/dashboard", Timeout: "10s"}},
			{Screenshot: "page.png"},
			{Sleep: &SleepStep{Duration: "2s"}},
			{Eval: "document.title"},
		},
	}

	resolved := ResolveSteps(action, nil)
	if len(resolved) != 12 {
		t.Fatalf("expected 12 steps, got %d", len(resolved))
	}

	expectedTypes := []string{
		"navigate", "click", "fill", "select", "upload", "download",
		"wait_visible", "wait_text", "wait_url", "screenshot", "sleep", "eval",
	}
	for i, rt := range resolved {
		if rt.Type != expectedTypes[i] {
			t.Errorf("step %d: expected type=%s, got %s", i, expectedTypes[i], rt.Type)
		}
	}
}

func TestResolveSteps_EnvFromOSAndWorkflow(t *testing.T) {
	os.Setenv("TEST_BRZ_OS_VAR", "from-os")
	defer os.Unsetenv("TEST_BRZ_OS_VAR")

	action := Action{
		Steps: []Step{
			{Fill: &FillStep{Selector: "#a", Value: "${TEST_BRZ_OS_VAR}"}},
			{Fill: &FillStep{Selector: "#b", Value: "${WF_VAR}"}},
		},
	}
	env := map[string]string{"WF_VAR": "from-workflow"}

	resolved := ResolveSteps(action, env)
	if resolved[0].Value != "from-os" {
		t.Errorf("expected OS env, got %s", resolved[0].Value)
	}
	if resolved[1].Value != "from-workflow" {
		t.Errorf("expected workflow env, got %s", resolved[1].Value)
	}
}

func TestResolvedStep_JSONShape(t *testing.T) {
	step := ResolvedStep{
		Type:     "fill",
		Selector: "#email",
		Value:    "test@example.com",
	}
	data, err := json.Marshal(step)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	// Should have type and selector and value
	if !strings.Contains(s, `"type":"fill"`) {
		t.Errorf("missing type in JSON: %s", s)
	}
	if !strings.Contains(s, `"selector":"#email"`) {
		t.Errorf("missing selector in JSON: %s", s)
	}
	// Empty fields should be omitted
	step2 := ResolvedStep{Type: "navigate", URL: "https://example.com"}
	data2, _ := json.Marshal(step2)
	s2 := string(data2)
	if strings.Contains(s2, `"selector"`) {
		t.Errorf("empty selector should be omitted: %s", s2)
	}
}
