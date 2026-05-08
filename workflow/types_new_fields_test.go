package workflow

import "testing"

// Parsing tests for the new optional fields added to ClickStep, DownloadStep,
// and the new WaitEnabled step. These run without a browser — they only
// exercise YAML → struct decoding plus the dispatch helpers (stepType,
// ResolveSteps). Behavior tests for each feature live in their dedicated
// test files (click_visible_test.go, download_save_test.go, etc.).

func TestClickStep_VisibleField(t *testing.T) {
	yaml := `
name: t
actions:
  test:
    steps:
      - click:
          selector: '.btn'
          visible: true
          nth: -1
`
	w, err := LoadFromBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	c := w.Actions["test"].Steps[0].Click
	if c == nil {
		t.Fatal("expected click step")
	}
	if !c.Visible {
		t.Error("Visible should be true")
	}
	if c.Nth != -1 {
		t.Errorf("Nth: got %d, want -1", c.Nth)
	}
}

func TestClickStep_DefaultsPreserved(t *testing.T) {
	// Backward-compat guard: a click step that does NOT specify Visible
	// or Nth must keep their zero-value defaults so existing workflows
	// in any downstream consumer get identical behavior.
	yaml := `
name: t
actions:
  test:
    steps:
      - click:
          selector: '.btn'
`
	w, _ := LoadFromBytes([]byte(yaml))
	c := w.Actions["test"].Steps[0].Click
	if c.Visible {
		t.Error("Visible should default to false")
	}
	if c.Nth != 0 {
		t.Errorf("Nth should default to 0, got %d", c.Nth)
	}
}

func TestDownloadStep_SaveAsAndSaveToParse(t *testing.T) {
	yaml := `
name: t
actions:
  a:
    steps:
      - download:
          save_as: "~/Downloads/old.csv"
  b:
    steps:
      - download:
          save_to: "/tmp/new.csv"
          return_to: previous
`
	w, err := LoadFromBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	a := w.Actions["a"].Steps[0].Download
	if a.SaveAs != "~/Downloads/old.csv" {
		t.Errorf("SaveAs: got %q", a.SaveAs)
	}
	if a.SaveTo != "" {
		t.Errorf("SaveTo should be empty when save_as used, got %q", a.SaveTo)
	}
	b := w.Actions["b"].Steps[0].Download
	if b.SaveTo != "/tmp/new.csv" {
		t.Errorf("SaveTo: got %q", b.SaveTo)
	}
	if b.ReturnTo != "previous" {
		t.Errorf("ReturnTo: got %q", b.ReturnTo)
	}
}

func TestStep_WaitEnabledParse(t *testing.T) {
	yaml := `
name: t
actions:
  test:
    steps:
      - wait_enabled:
          selector: 'button[type="submit"]'
          timeout: '120s'
`
	w, err := LoadFromBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	we := w.Actions["test"].Steps[0].WaitEnabled
	if we == nil {
		t.Fatal("expected wait_enabled step")
	}
	if we.Selector != `button[type="submit"]` {
		t.Errorf("selector: got %q", we.Selector)
	}
	if we.Timeout != "120s" {
		t.Errorf("timeout: got %q", we.Timeout)
	}
}

func TestStepType_RecognizesWaitEnabled(t *testing.T) {
	s := Step{WaitEnabled: &WaitStep{Selector: ".x"}}
	if got := stepType(s); got != "wait_enabled" {
		t.Errorf("stepType: got %q, want wait_enabled", got)
	}
}

func TestResolveSteps_WaitEnabled(t *testing.T) {
	a := Action{Steps: []Step{{WaitEnabled: &WaitStep{Selector: ".x", Timeout: "10s"}}}}
	rs := ResolveSteps(a, nil)
	if len(rs) != 1 {
		t.Fatalf("expected 1 resolved step, got %d", len(rs))
	}
	if rs[0].Type != "wait_enabled" {
		t.Errorf("type: got %q", rs[0].Type)
	}
	if rs[0].Selector != ".x" {
		t.Errorf("selector: got %q", rs[0].Selector)
	}
	if rs[0].Timeout != "10s" {
		t.Errorf("timeout: got %q", rs[0].Timeout)
	}
}

func TestResolveSteps_DownloadFields(t *testing.T) {
	env := map[string]string{"NAME": "Pokemon"}
	a := Action{Steps: []Step{
		{Download: &DownloadStep{SaveAs: "/tmp/${NAME}.csv", ReturnTo: "previous"}},
		{Download: &DownloadStep{SaveTo: "/tmp/alt-${NAME}.csv"}},
	}}
	rs := ResolveSteps(a, env)
	if rs[0].SaveTo != "/tmp/Pokemon.csv" {
		t.Errorf("save_as → save_to interpolation: got %q", rs[0].SaveTo)
	}
	if rs[0].ReturnTo != "previous" {
		t.Errorf("return_to: got %q", rs[0].ReturnTo)
	}
	if rs[1].SaveTo != "/tmp/alt-Pokemon.csv" {
		t.Errorf("save_to interpolation: got %q", rs[1].SaveTo)
	}
}

func TestResolveSteps_ClickVisible(t *testing.T) {
	a := Action{Steps: []Step{{Click: &ClickStep{Selector: ".btn", Visible: true, Nth: -1}}}}
	rs := ResolveSteps(a, nil)
	if !rs[0].Visible {
		t.Error("Visible should be carried into resolved step")
	}
	if rs[0].Nth != -1 {
		t.Errorf("Nth: got %d, want -1", rs[0].Nth)
	}
}
