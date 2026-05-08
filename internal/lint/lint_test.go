package lint

import (
	"encoding/json"
	"strings"
	"testing"
)

func findingsByCode(res Result) map[string]int {
	m := map[string]int{}
	for _, f := range res.Findings {
		m[f.Code]++
	}
	return m
}

func TestRule_BrittleSelector_CSSinJS(t *testing.T) {
	yml := `name: x
actions:
  a:
    steps:
      - click: { selector: '.css-1abc23' }
`
	res := Lint("t.yaml", []byte(yml))
	if findingsByCode(res)["W101-brittle-selector"] == 0 {
		t.Errorf("expected W101 for css-in-js hash; got: %+v", res.Findings)
	}
}

func TestRule_BrittleSelector_Negative(t *testing.T) {
	yml := `name: x
actions:
  a:
    steps:
      - click: { selector: '#login-button' }
`
	res := Lint("t.yaml", []byte(yml))
	if findingsByCode(res)["W101-brittle-selector"] > 0 {
		t.Errorf("false positive on stable selector")
	}
}

func TestRule_NthChild(t *testing.T) {
	yml := `name: x
actions:
  a:
    steps:
      - click: { selector: 'div.list > :nth-child(3)' }
`
	res := Lint("t.yaml", []byte(yml))
	if findingsByCode(res)["W102-nth-child"] == 0 {
		t.Errorf("expected W102; got: %+v", res.Findings)
	}
}

func TestRule_DeepCombinators(t *testing.T) {
	yml := `name: x
actions:
  a:
    steps:
      - click: { selector: 'body > div > div > div > div > div > a' }
`
	res := Lint("t.yaml", []byte(yml))
	if findingsByCode(res)["W103-deep-combinators"] == 0 {
		t.Errorf("expected W103; got: %+v", res.Findings)
	}
}

func TestRule_Sleep(t *testing.T) {
	yml := `name: x
actions:
  a:
    steps:
      - sleep: { duration: '5s' }
`
	res := Lint("t.yaml", []byte(yml))
	if findingsByCode(res)["I201-sleep-instead-of-wait"] == 0 {
		t.Errorf("expected I201; got: %+v", res.Findings)
	}
}

func TestRule_Sleep_Negative(t *testing.T) {
	yml := `name: x
actions:
  a:
    steps:
      - wait_visible: { selector: '#ok' }
`
	res := Lint("t.yaml", []byte(yml))
	if findingsByCode(res)["I201-sleep-instead-of-wait"] > 0 {
		t.Errorf("false positive on wait_visible")
	}
}

func TestRule_EvalNonStrictBool(t *testing.T) {
	yml := `name: x
actions:
  a:
    steps:
      - eval: "document.querySelector('#ready')"
`
	res := Lint("t.yaml", []byte(yml))
	if findingsByCode(res)["W301-eval-non-strict-bool"] == 0 {
		t.Errorf("expected W301; got: %+v", res.Findings)
	}
}

func TestRule_EvalNonStrictBool_Negative(t *testing.T) {
	yml := `name: x
actions:
  a:
    steps:
      - eval: "!!document.querySelector('#ready')"
`
	res := Lint("t.yaml", []byte(yml))
	if findingsByCode(res)["W301-eval-non-strict-bool"] > 0 {
		t.Errorf("false positive on coerced eval")
	}
}

func TestRule_DownloadNoTimeout(t *testing.T) {
	yml := `name: x
actions:
  a:
    steps:
      - download: {}
`
	res := Lint("t.yaml", []byte(yml))
	if findingsByCode(res)["I401-download-no-timeout"] == 0 {
		t.Errorf("expected I401; got: %+v", res.Findings)
	}
}

func TestRule_DownloadNoTimeout_Negative(t *testing.T) {
	yml := `name: x
actions:
  a:
    steps:
      - download: { timeout: '5m' }
`
	res := Lint("t.yaml", []byte(yml))
	if findingsByCode(res)["I401-download-no-timeout"] > 0 {
		t.Errorf("false positive on timed download")
	}
}

func TestRule_DuplicateStepName(t *testing.T) {
	yml := `name: x
actions:
  a:
    steps:
      - label: do thing
        click: { selector: '#a' }
      - label: do thing
        click: { selector: '#b' }
`
	res := Lint("t.yaml", []byte(yml))
	if findingsByCode(res)["W501-duplicate-step-name"] == 0 {
		t.Errorf("expected W501; got: %+v", res.Findings)
	}
}

func TestRule_UndeclaredEnvVar(t *testing.T) {
	yml := `name: x
actions:
  a:
    steps:
      - fill: { selector: '#email', value: '${SOMETHING_VERY_UNLIKELY_TO_EXIST_xyz}' }
`
	res := Lint("t.yaml", []byte(yml))
	if findingsByCode(res)["I601-undeclared-env-var"] == 0 {
		t.Errorf("expected W601; got: %+v", res.Findings)
	}
}

func TestRule_UndeclaredEnvVar_Negative(t *testing.T) {
	yml := `name: x
env:
  E: a@b.com
actions:
  a:
    steps:
      - fill: { selector: '#email', value: '${E}' }
`
	res := Lint("t.yaml", []byte(yml))
	if findingsByCode(res)["I601-undeclared-env-var"] > 0 {
		t.Errorf("false positive on declared env var")
	}
}

func TestSchemaErrorEmittedAsError(t *testing.T) {
	yml := `name: x
actions:
  a:
    steps:
      - click: { selector: '#x', save_too: 'oops' }
`
	res := Lint("t.yaml", []byte(yml))
	hasSchema := false
	for _, f := range res.Findings {
		if f.Code == "E002-schema" {
			hasSchema = true
		}
	}
	if !hasSchema {
		t.Errorf("expected E002-schema; got: %+v", res.Findings)
	}
}

func TestParseError(t *testing.T) {
	yml := "name: x\n  bad: : indent\n: [\n"
	res := Lint("t.yaml", []byte(yml))
	if res.ParseOK {
		t.Errorf("expected parse failure")
	}
	if len(res.Findings) != 1 || res.Findings[0].Code != "E001-parse" {
		t.Errorf("expected single parse finding; got: %+v", res.Findings)
	}
}

func TestJSONSerializable(t *testing.T) {
	yml := `name: x
actions:
  a:
    steps:
      - click: { selector: '.css-1abc23' }
`
	res := Lint("t.yaml", []byte(yml))
	for _, f := range res.Findings {
		b, err := json.Marshal(f)
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		for _, want := range []string{"file", "line", "col", "severity", "code", "message"} {
			if !strings.Contains(s, `"`+want+`"`) {
				t.Errorf("finding JSON missing %q: %s", want, s)
			}
		}
	}
}
