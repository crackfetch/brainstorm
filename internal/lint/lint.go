// Package lint runs structural and stylistic checks against brz workflow YAML.
//
// Layered design:
//
//   - Schema errors come from workflow.LoadStrict (the same validator used
//     by `brz validate --strict`). These are reported as severity "error"
//     and force exit code 2 — they make the workflow unrunnable.
//
//   - Smell rules walk the parsed yaml.Node tree and emit "warn" / "info"
//     findings. Warnings only fail the run when the caller passes --strict.
//
// Adding a new rule = add to ruleFns. Each rule receives the document
// node and a reporter that knows how to produce file:line:col entries
// pulled straight from yaml.Node positions.
package lint

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/crackfetch/brainstorm/workflow"
	"gopkg.in/yaml.v3"
)

// Severity levels.
type Severity string

const (
	SevError Severity = "error"
	SevWarn  Severity = "warn"
	SevInfo  Severity = "info"
)

// Finding is one issue found by lint.
type Finding struct {
	File     string   `json:"file"`
	Line     int      `json:"line"`
	Col      int      `json:"col"`
	Severity Severity `json:"severity"`
	Code     string   `json:"code"`
	Message  string   `json:"message"`
}

// Result is the full set of findings for one file plus parse status.
type Result struct {
	File     string
	Findings []Finding
	// ParseOK indicates whether YAML could be parsed at all. If false,
	// only one Finding (a parse error) is emitted.
	ParseOK bool
}

// CountBySeverity returns counts of {error, warn, info}.
func (r *Result) CountBySeverity() (errs, warns, infos int) {
	for _, f := range r.Findings {
		switch f.Severity {
		case SevError:
			errs++
		case SevWarn:
			warns++
		case SevInfo:
			infos++
		}
	}
	return
}

// LintFile reads and lints a single file path. Errors during file IO are
// returned as a Finding with severity error and ParseOK=false.
func LintFile(path string) Result {
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{
			File:    path,
			ParseOK: false,
			Findings: []Finding{{
				File: path, Line: 0, Col: 0, Severity: SevError, Code: "E000-read",
				Message: err.Error(),
			}},
		}
	}
	return Lint(path, data)
}

// Lint lints the given YAML bytes attributed to `file` (used in finding paths).
func Lint(file string, data []byte) Result {
	res := Result{File: file}

	// Stage 1: parse.
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		res.Findings = append(res.Findings, Finding{
			File: file, Line: extractLine(err), Col: extractCol(err),
			Severity: SevError, Code: "E001-parse",
			Message:  err.Error(),
		})
		return res
	}
	res.ParseOK = true

	// Stage 2: schema validation via the existing strict loader. Reuses
	// brz validate --strict for free — fields the schema rejects show up
	// here as schema errors so lint is a true superset of validate.
	if _, err := workflow.LoadStrictFromBytes(data); err != nil {
		res.Findings = append(res.Findings, Finding{
			File: file, Line: extractLine(err), Col: extractCol(err),
			Severity: SevError, Code: "E002-schema",
			Message:  err.Error(),
		})
		// Continue anyway — schema can be invalid yet still have rules
		// worth surfacing (e.g. a typo plus a brittle selector). Skip
		// only the rules that need a typed Workflow.
	}

	// Stage 3: smell rules over the node tree.
	for _, rule := range ruleFns {
		rule(file, &root, &res)
	}

	// Sort findings by line then col for stable output.
	sort.SliceStable(res.Findings, func(i, j int) bool {
		a, b := res.Findings[i], res.Findings[j]
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Col < b.Col
	})

	return res
}

// ---- rule plumbing ----

type ruleFn func(file string, root *yaml.Node, res *Result)

var ruleFns = []ruleFn{
	ruleBrittleSelectors,
	ruleSleepWhereWaitFits,
	ruleEvalNonStrictBool,
	ruleDownloadNoTimeout,
	ruleDuplicateStepNames,
	ruleUndeclaredEnvVars,
}

// emit appends a finding pinned to a Node's position.
func emit(res *Result, n *yaml.Node, sev Severity, code, msg string) {
	line, col := 0, 0
	if n != nil {
		line, col = n.Line, n.Column
	}
	res.Findings = append(res.Findings, Finding{
		File: res.File, Line: line, Col: col,
		Severity: sev, Code: code, Message: msg,
	})
}

// walkSteps yields each step node along with its parent action name.
func walkSteps(root *yaml.Node, fn func(actionName string, step *yaml.Node)) {
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return
	}
	actions := mapGet(doc, "actions")
	if actions == nil || actions.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(actions.Content); i += 2 {
		actionName := actions.Content[i].Value
		actionNode := actions.Content[i+1]
		if actionNode.Kind != yaml.MappingNode {
			continue
		}
		steps := mapGet(actionNode, "steps")
		if steps == nil || steps.Kind != yaml.SequenceNode {
			continue
		}
		for _, step := range steps.Content {
			fn(actionName, step)
		}
	}
}

// mapGet returns the value node for key in a mapping, or nil.
func mapGet(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// ---- selector smell rules ----

var (
	// CSS-in-JS hashed class names: .css-abc123 or .Component_name_a1b2c3.
	reCSSInJSHash    = regexp.MustCompile(`\.css-[a-z0-9]{4,}`)
	reUnderscoreHash = regexp.MustCompile(`\.[a-zA-Z][a-zA-Z0-9]*_[a-zA-Z0-9]{6,}`)
	reNthChild       = regexp.MustCompile(`>\s*:nth-child\(\d+\)`)
)

// ruleBrittleSelectors flags selectors likely to break across deploys.
func ruleBrittleSelectors(file string, root *yaml.Node, res *Result) {
	walkSteps(root, func(_ string, step *yaml.Node) {
		if step.Kind != yaml.MappingNode {
			return
		}
		for i := 0; i+1 < len(step.Content); i += 2 {
			body := step.Content[i+1]
			selNode := mapGet(body, "selector")
			if selNode == nil {
				continue
			}
			sel := selNode.Value
			switch {
			case reCSSInJSHash.MatchString(sel):
				emit(res, selNode, SevWarn, "W101-brittle-selector",
					fmt.Sprintf("selector %q matches CSS-in-JS hashed class (.css-XXXX); will break on rebuild", sel))
			case reUnderscoreHash.MatchString(sel):
				emit(res, selNode, SevWarn, "W101-brittle-selector",
					fmt.Sprintf("selector %q looks like a hashed module class; will break on rebuild", sel))
			case reNthChild.MatchString(sel):
				emit(res, selNode, SevWarn, "W102-nth-child",
					fmt.Sprintf("selector %q uses :nth-child; brittle to DOM reordering", sel))
			}
			// Deep combinator chains (>4 child combinators).
			if strings.Count(sel, ">") > 4 {
				emit(res, selNode, SevWarn, "W103-deep-combinators",
					fmt.Sprintf("selector %q uses %d child combinators; consider a single id/data-attr", sel, strings.Count(sel, ">")))
			}
		}
	})
}

// ruleSleepWhereWaitFits flags `sleep:` steps where wait_* would do.
func ruleSleepWhereWaitFits(file string, root *yaml.Node, res *Result) {
	walkSteps(root, func(_ string, step *yaml.Node) {
		sleep := mapGet(step, "sleep")
		if sleep == nil {
			return
		}
		emit(res, sleep, SevInfo, "I201-sleep-instead-of-wait",
			"sleep step is fragile; prefer wait_visible/wait_enabled/wait_url/wait_text when the goal is waiting for a state change")
	})
}

// ruleEvalNonStrictBool flags eval used as a wait condition that doesn't
// look like a strict-boolean expression. handoff.wait_eval is wrapped in
// Boolean(...) at execution time, so it's safe; the real risk is when an
// eval step's return value is read by a caller assuming === true.
//
// We flag eval expressions that look like they're being used for
// existence checks (e.g. `document.querySelector('x')`) without an
// explicit `!!` / `=== ` / `Boolean(...)` coercion — that's the JS-truthy
// gotcha documented in reference_brz_rod_quirks.md.
func ruleEvalNonStrictBool(file string, root *yaml.Node, res *Result) {
	walkSteps(root, func(_ string, step *yaml.Node) {
		evalN := mapGet(step, "eval")
		if evalN == nil {
			return
		}
		expr := strings.TrimSpace(evalN.Value)
		if expr == "" {
			return
		}
		// Only flag short one-liners that look like existence checks.
		if len(expr) > 200 {
			return
		}
		looksLikeQuery := strings.Contains(expr, "querySelector") ||
			strings.Contains(expr, "getElementById") ||
			strings.Contains(expr, "getElementsBy")
		hasBoolCoerce := strings.Contains(expr, "!!") ||
			strings.Contains(expr, "===") ||
			strings.Contains(expr, "!==") ||
			strings.Contains(expr, "Boolean(") ||
			strings.Contains(expr, ".length") ||
			strings.Contains(expr, "true") ||
			strings.Contains(expr, "false")
		if looksLikeQuery && !hasBoolCoerce {
			emit(res, evalN, SevWarn, "W301-eval-non-strict-bool",
				"eval result used as a condition is JS-truthy; rod expects strict bool — wrap in Boolean(...) or use !!")
		}
	})
}

// ruleDownloadNoTimeout flags download steps with no timeout.
func ruleDownloadNoTimeout(file string, root *yaml.Node, res *Result) {
	walkSteps(root, func(_ string, step *yaml.Node) {
		dl := mapGet(step, "download")
		if dl == nil {
			return
		}
		// As of v0.13.0, download honors its Timeout field with 60s
		// default. Still worth flagging because slow servers exceed 60s
		// and the implicit default isn't visible at the call site.
		if mapGet(dl, "timeout") == nil {
			emit(res, dl, SevInfo, "I401-download-no-timeout",
				"download step has no explicit timeout (default 60s); set timeout: 5m or longer for slow exports")
		}
	})
}

// ruleDuplicateStepNames flags duplicate step labels within an action.
func ruleDuplicateStepNames(file string, root *yaml.Node, res *Result) {
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return
	}
	doc := root.Content[0]
	actions := mapGet(doc, "actions")
	if actions == nil || actions.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(actions.Content); i += 2 {
		actionName := actions.Content[i].Value
		actionNode := actions.Content[i+1]
		steps := mapGet(actionNode, "steps")
		if steps == nil || steps.Kind != yaml.SequenceNode {
			continue
		}
		seen := make(map[string]*yaml.Node)
		for _, step := range steps.Content {
			labelN := mapGet(step, "label")
			if labelN == nil || labelN.Value == "" {
				continue
			}
			if prev, dup := seen[labelN.Value]; dup {
				emit(res, labelN, SevWarn, "W501-duplicate-step-name",
					fmt.Sprintf("duplicate step label %q in action %q (first at line %d); labels should be unique within an action", labelN.Value, actionName, prev.Line))
			} else {
				seen[labelN.Value] = labelN
			}
		}
	}
}

// reEnvVar matches ${NAME} in a string.
var reEnvVar = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// ruleUndeclaredEnvVars flags ${VAR} references that aren't in the
// workflow `env:` map AND aren't in the host process environment at
// lint time. Host-env presence is a soft signal — a workflow that
// works for one author may break for another, but agents typically
// run lint in CI where neither side is set, so flagging is useful.
func ruleUndeclaredEnvVars(file string, root *yaml.Node, res *Result) {
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return
	}
	doc := root.Content[0]
	envMap := map[string]bool{}
	if env := mapGet(doc, "env"); env != nil && env.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(env.Content); i += 2 {
			envMap[env.Content[i].Value] = true
		}
	}

	// Walk all string values in the doc, find ${VAR}.
	var visit func(n *yaml.Node)
	visit = func(n *yaml.Node) {
		if n == nil {
			return
		}
		switch n.Kind {
		case yaml.ScalarNode:
			for _, m := range reEnvVar.FindAllStringSubmatch(n.Value, -1) {
				name := m[1]
				if envMap[name] {
					continue
				}
				if _, ok := os.LookupEnv(name); ok {
					continue
				}
				emit(res, n, SevInfo, "I601-undeclared-env-var",
					fmt.Sprintf("env var ${%s} is referenced but not declared in workflow env:; ensure it's passed via --env or the host environment at run time", name))
			}
		default:
			for _, c := range n.Content {
				visit(c)
			}
		}
	}
	visit(root)
}

// ---- error position extraction ----

// extractLine pulls a line number out of a yaml.v3 error message of the
// form "yaml: line N: ..." or "yaml: line N: column M: ...". Returns 0
// if no match.
var reYAMLPos = regexp.MustCompile(`yaml: line (\d+)`)
var reYAMLCol = regexp.MustCompile(`column (\d+)`)

func extractLine(err error) int {
	if err == nil {
		return 0
	}
	if m := reYAMLPos.FindStringSubmatch(err.Error()); m != nil {
		var n int
		fmt.Sscanf(m[1], "%d", &n)
		return n
	}
	return 0
}

func extractCol(err error) int {
	if err == nil {
		return 0
	}
	if m := reYAMLCol.FindStringSubmatch(err.Error()); m != nil {
		var n int
		fmt.Sscanf(m[1], "%d", &n)
		return n
	}
	return 0
}

// FormatHuman renders findings as one-per-line human-readable output.
func FormatHuman(res Result) string {
	var buf bytes.Buffer
	for _, f := range res.Findings {
		fmt.Fprintf(&buf, "%s:%d:%d: %s [%s] %s\n", f.File, f.Line, f.Col, f.Severity, f.Code, f.Message)
	}
	return buf.String()
}
