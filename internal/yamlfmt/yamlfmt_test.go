package yamlfmt

import (
	"strings"
	"testing"
)

func TestFormat_Idempotent(t *testing.T) {
	inputs := []string{
		`name: x
actions:
  a:
    url: https://example.com
    steps:
      - click: { selector: '#go' }
`,
		`actions:
  login:
    steps:
      - fill:
          selector: '#email'
          value: '${E}'
        label: enter email
        optional: true
name: form
env:
  E: a@b.com
`,
		`# Top-level comment
name: commented
actions:
  a:
    # this action does X
    url: https://example.com
    steps:
      - label: step one  # inline
        click: { selector: '#go' }
`,
	}
	for i, in := range inputs {
		out1, err := Format([]byte(in))
		if err != nil {
			t.Fatalf("case %d: format err: %v", i, err)
		}
		out2, err := Format(out1)
		if err != nil {
			t.Fatalf("case %d: re-format err: %v", i, err)
		}
		if string(out1) != string(out2) {
			t.Errorf("case %d: not idempotent\nfirst:\n%s\nsecond:\n%s", i, out1, out2)
		}
	}
}

func TestFormat_KeyOrdering(t *testing.T) {
	in := `actions:
  a:
    steps:
      - click: { selector: '#go' }
    url: https://example.com
name: x
env:
  K: v
`
	out, err := Format([]byte(in))
	if err != nil {
		t.Fatalf("format err: %v", err)
	}
	got := string(out)
	// name should come before env, env before actions.
	iName := strings.Index(got, "name:")
	iEnv := strings.Index(got, "env:")
	iActs := strings.Index(got, "actions:")
	if !(iName < iEnv && iEnv < iActs) {
		t.Errorf("bad top-level order\n%s", got)
	}
	// Within action, url before steps.
	iURL := strings.Index(got, "url:")
	iSteps := strings.Index(got, "steps:")
	if !(iURL < iSteps) {
		t.Errorf("url should come before steps:\n%s", got)
	}
}

func TestFormat_StepKeyOrdering(t *testing.T) {
	in := `name: x
actions:
  a:
    steps:
      - click:
          selector: '#go'
        label: do it
`
	out, err := Format([]byte(in))
	if err != nil {
		t.Fatalf("format err: %v", err)
	}
	got := string(out)
	iLabel := strings.Index(got, "label:")
	iClick := strings.Index(got, "click:")
	if !(iLabel >= 0 && iClick > iLabel) {
		t.Errorf("label should come before click:\n%s", got)
	}
}

func TestFormat_PreservesComments(t *testing.T) {
	in := `# header comment
name: x  # inline name
actions:
  a:
    # action body
    steps:
      - click: { selector: '#go' }
`
	out, err := Format([]byte(in))
	if err != nil {
		t.Fatalf("format err: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "# header comment") {
		t.Errorf("lost head comment:\n%s", got)
	}
	if !strings.Contains(got, "# action body") {
		t.Errorf("lost action body comment:\n%s", got)
	}
}

func TestFormat_TerminalNewline(t *testing.T) {
	in := "name: x\nactions:\n  a:\n    steps: []\n"
	out, err := Format([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 || out[len(out)-1] != '\n' {
		t.Errorf("missing terminal newline: %q", out)
	}
}

func TestFormat_BadYAML(t *testing.T) {
	_, err := Format([]byte("name: x\n  bad indent\n: [\n"))
	if err == nil {
		t.Errorf("expected parse error")
	}
}

func TestFormat_Empty(t *testing.T) {
	out, err := Format([]byte(""))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Errorf("expected empty output, got %q", out)
	}
}
