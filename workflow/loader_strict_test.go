package workflow

import (
	"strings"
	"testing"
)

// LoadStrictFromBytes catches typos that yaml.v3's lenient default
// silently drops. Tests pin both the happy path (valid workflow parses
// identically to LoadFromBytes) and the strict-mode rejections (typos,
// nested-step typos, action-level typos) with line-number context.

func TestLoadStrictFromBytes_ValidWorkflow_AcceptedSameAsLenient(t *testing.T) {
	yamlSrc := `
name: t
actions:
  do:
    steps:
      - click: { selector: '.btn' }
      - download: { save_to: '/tmp/x.csv', return_to: previous }
      - wait_enabled: { selector: 'button[type=submit]', timeout: '30s' }
`
	wStrict, err := LoadStrictFromBytes([]byte(yamlSrc))
	if err != nil {
		t.Fatalf("strict rejected a valid workflow: %v", err)
	}
	wLenient, err := LoadFromBytes([]byte(yamlSrc))
	if err != nil {
		t.Fatalf("lenient rejected a valid workflow: %v", err)
	}
	if wStrict.Name != wLenient.Name {
		t.Errorf("strict and lenient produced different Name fields")
	}
	if len(wStrict.Actions["do"].Steps) != len(wLenient.Actions["do"].Steps) {
		t.Errorf("strict step count differs from lenient")
	}
}

func TestLoadStrictFromBytes_TypoInStep_Rejected(t *testing.T) {
	// `save_too` is a one-character typo of `save_to`. The lenient parser
	// silently drops it; strict catches it. This is the canonical bug
	// `--strict` exists to surface.
	yamlSrc := `
name: t
actions:
  do:
    steps:
      - click: { selector: '.btn' }
      - download:
          save_too: "/tmp/oops.csv"
`
	_, err := LoadStrictFromBytes([]byte(yamlSrc))
	if err == nil {
		t.Fatal("strict accepted a workflow with a typo'd field; want error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "save_too") {
		t.Errorf("error should name the offending field; got %q", msg)
	}
	// yaml.v3 includes a line: prefix when KnownFields is on. Just verify
	// some location info is present so users can find the typo.
	if !strings.Contains(msg, "line") {
		t.Errorf("error should include line-number context; got %q", msg)
	}

	// Lenient must still accept it — backward compat with the existing
	// loader. If this changes we'd be silently breaking older workflows.
	if _, err := LoadFromBytes([]byte(yamlSrc)); err != nil {
		t.Errorf("lenient rejected a typo'd field — backward compat break: %v", err)
	}
}

func TestLoadStrictFromBytes_TypoInAction_Rejected(t *testing.T) {
	// Action-level typo: `urll` instead of `url`. Should also be caught.
	yamlSrc := `
name: t
actions:
  do:
    urll: https://example.com
    steps:
      - click: { selector: '.btn' }
`
	_, err := LoadStrictFromBytes([]byte(yamlSrc))
	if err == nil {
		t.Fatal("strict accepted an action-level typo; want error")
	}
	if !strings.Contains(err.Error(), "urll") {
		t.Errorf("error should name the offending field; got %q", err.Error())
	}
}

func TestLoadStrictFromBytes_TypoInWorkflow_Rejected(t *testing.T) {
	// Top-level typo: `viewports` instead of `viewport`.
	yamlSrc := `
name: t
viewports: { width: 1280, height: 800 }
actions:
  do:
    steps:
      - click: { selector: '.btn' }
`
	_, err := LoadStrictFromBytes([]byte(yamlSrc))
	if err == nil {
		t.Fatal("strict accepted a top-level typo; want error")
	}
	if !strings.Contains(err.Error(), "viewports") {
		t.Errorf("error should name the offending field; got %q", err.Error())
	}
}

func TestLoadStrictFromBytes_MissingName_Rejected(t *testing.T) {
	// Strict still enforces the required-fields contract that lenient does.
	yamlSrc := `
actions:
  do:
    steps:
      - click: { selector: '.btn' }
`
	_, err := LoadStrictFromBytes([]byte(yamlSrc))
	if err == nil || !strings.Contains(err.Error(), "name") {
		t.Errorf("strict should reject missing name like lenient does; got %v", err)
	}
}

func TestLoadStrictFromBytes_NoActions_Rejected(t *testing.T) {
	yamlSrc := `name: t`
	_, err := LoadStrictFromBytes([]byte(yamlSrc))
	if err == nil || !strings.Contains(err.Error(), "actions") {
		t.Errorf("strict should reject empty actions like lenient does; got %v", err)
	}
}

func TestLoadFromBytes_LenientStillSilentlyAcceptsTypos(t *testing.T) {
	// Backward-compat guard: a future contributor MUST NOT accidentally
	// flip lenient mode to strict — that would break every workflow with
	// a previously-tolerated unknown field.
	yamlSrc := `
name: t
unknown_top_level_field: ignored
actions:
  do:
    unknown_action_field: ignored
    steps:
      - click:
          selector: '.btn'
          unknown_step_field: ignored
`
	w, err := LoadFromBytes([]byte(yamlSrc))
	if err != nil {
		t.Fatalf("lenient must still accept unknown fields silently; got %v", err)
	}
	if w.Name != "t" {
		t.Errorf("Name parsed wrong: %q", w.Name)
	}
}
