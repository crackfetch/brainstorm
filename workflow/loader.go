package workflow

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var envVarPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

// SplitActionNames splits a comma-separated action name string into individual
// action names, filtering out empty strings from leading/trailing/double commas.
func SplitActionNames(s string) []string {
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// Load reads and parses a workflow YAML file.
func Load(path string) (*Workflow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read workflow %s: %w", path, err)
	}

	var w Workflow
	if err := yaml.Unmarshal(data, &w); err != nil {
		return nil, fmt.Errorf("parse workflow %s: %w", path, err)
	}

	if w.Name == "" {
		return nil, fmt.Errorf("workflow %s: missing 'name' field", path)
	}
	if len(w.Actions) == 0 {
		return nil, fmt.Errorf("workflow %s: no actions defined", path)
	}

	if _, err := resolveAliases(&w, filepath.Dir(path)); err != nil {
		return nil, fmt.Errorf("workflow %s: %w", path, err)
	}

	return &w, nil
}

// LoadFromBytes parses workflow YAML from a byte slice.
func LoadFromBytes(data []byte) (*Workflow, error) {
	var w Workflow
	if err := yaml.Unmarshal(data, &w); err != nil {
		return nil, fmt.Errorf("parse workflow: %w", err)
	}

	if w.Name == "" {
		return nil, fmt.Errorf("workflow: missing 'name' field")
	}
	if len(w.Actions) == 0 {
		return nil, fmt.Errorf("workflow: no actions defined")
	}

	if _, err := resolveAliases(&w, ""); err != nil {
		return nil, fmt.Errorf("parse workflow: %w", err)
	}

	return &w, nil
}

// LoadStrictFromBytes parses workflow YAML with KnownFields enforcement —
// any field name that doesn't match the Workflow / Action / Step schema
// returns an error with a YAML line number. This catches typos like
// `save_too:` (close to `save_to:`) at validate time instead of at run
// time when the workflow silently misbehaves.
//
// Default Load / LoadFromBytes stays lenient so existing workflows that
// rely on yaml.v3's default unknown-field behavior keep working. Callers
// opt into strict by reaching for this function (driven by `validate
// --strict` from the CLI).
func LoadStrictFromBytes(data []byte) (*Workflow, error) {
	var w Workflow
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&w); err != nil {
		// Decorate with field-name suggestions before wrapping. The
		// inner err message is the only signal yaml.v3 gives us about
		// which struct field was rejected, so we keep it as the source
		// of truth and just append "Did you mean?" hints.
		return nil, fmt.Errorf("parse workflow: %w", decorateStrictError(err))
	}

	if w.Name == "" {
		return nil, fmt.Errorf("workflow: missing 'name' field")
	}
	if len(w.Actions) == 0 {
		return nil, fmt.Errorf("workflow: no actions defined")
	}

	if _, err := resolveAliases(&w, ""); err != nil {
		return nil, fmt.Errorf("parse workflow: %w", err)
	}

	return &w, nil
}

// LoadStrict reads a YAML file and parses with KnownFields enforcement.
// File-system equivalent of LoadStrictFromBytes. Aliases are resolved
// against the file's directory so workflow-relative `aliases_from`
// paths work; LoadStrictFromBytes can't do that and is restricted to
// absolute / `~` paths.
func LoadStrict(path string) (*Workflow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read workflow %s: %w", path, err)
	}
	// Mirror LoadStrictFromBytes but with a known workflowDir for alias
	// path resolution. We can't simply call LoadStrictFromBytes because
	// it has no way to learn the source directory.
	var w Workflow
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&w); err != nil {
		return nil, fmt.Errorf("workflow %s: parse workflow: %w", path, decorateStrictError(err))
	}
	if w.Name == "" {
		return nil, fmt.Errorf("workflow %s: missing 'name' field", path)
	}
	if len(w.Actions) == 0 {
		return nil, fmt.Errorf("workflow %s: no actions defined", path)
	}
	if _, err := resolveAliases(&w, filepath.Dir(path)); err != nil {
		return nil, fmt.Errorf("workflow %s: %w", path, err)
	}
	return &w, nil
}

// InterpolateEnv replaces ${VAR_NAME} patterns with environment variable values.
// Checks the workflow's env map first, then os.Getenv.
func InterpolateEnv(s string, workflowEnv map[string]string) string {
	return envVarPattern.ReplaceAllStringFunc(s, func(match string) string {
		varName := strings.TrimSuffix(strings.TrimPrefix(match, "${"), "}")

		// Check workflow-level env first
		if v, ok := workflowEnv[varName]; ok {
			return v
		}

		// Fall back to OS environment
		if v := os.Getenv(varName); v != "" {
			return v
		}

		// Return original if not found (don't crash on missing vars)
		return match
	})
}

// ResolveSteps resolves all env vars in an action's steps and returns
// a flat representation suitable for JSON output (used by --dry-run).
func ResolveSteps(action Action, env map[string]string) []ResolvedStep {
	var result []ResolvedStep
	for _, step := range action.Steps {
		rs := ResolvedStep{
			Label:    step.Label,
			Optional: step.Optional,
		}
		switch {
		case step.Navigate != "":
			rs.Type = "navigate"
			rs.URL = InterpolateEnv(step.Navigate, env)
		case step.Click != nil:
			rs.Type = "click"
			rs.Selector = step.Click.Selector
			rs.Text = step.Click.Text
			rs.Nth = step.Click.Nth
			rs.Visible = step.Click.Visible
			rs.Timeout = step.Click.Timeout
		case step.Fill != nil:
			rs.Type = "fill"
			rs.Selector = step.Fill.Selector
			rs.Value = InterpolateEnv(step.Fill.Value, env)
			rs.Clear = step.Fill.Clear
		case step.Select != nil:
			rs.Type = "select"
			rs.Selector = step.Select.Selector
			rs.Value = InterpolateEnv(step.Select.Value, env)
			rs.Text = InterpolateEnv(step.Select.Text, env)
			rs.Timeout = step.Select.Timeout
		case step.Upload != nil:
			rs.Type = "upload"
			rs.Selector = step.Upload.Selector
			rs.Source = InterpolateEnv(step.Upload.Source, env)
		case step.Download != nil:
			rs.Type = "download"
			rs.Timeout = step.Download.Timeout
			saveTarget := step.Download.SaveAs
			if saveTarget == "" {
				saveTarget = step.Download.SaveTo
			}
			rs.SaveTo = InterpolateEnv(saveTarget, env)
			rs.ReturnTo = InterpolateEnv(step.Download.ReturnTo, env)
		case step.WaitVisible != nil:
			rs.Type = "wait_visible"
			rs.Selector = step.WaitVisible.Selector
			rs.Timeout = step.WaitVisible.Timeout
		case step.WaitText != nil:
			rs.Type = "wait_text"
			rs.Text = step.WaitText.Text
			rs.Timeout = step.WaitText.Timeout
		case step.WaitURL != nil:
			rs.Type = "wait_url"
			rs.Match = step.WaitURL.Match
			rs.Timeout = step.WaitURL.Timeout
		case step.WaitEnabled != nil:
			rs.Type = "wait_enabled"
			rs.Selector = step.WaitEnabled.Selector
			rs.Timeout = step.WaitEnabled.Timeout
		case step.Handoff != nil:
			rs.Type = "handoff"
			rs.Match = step.Handoff.WaitURL
			rs.Expr = InterpolateEnv(step.Handoff.WaitEval, env)
			rs.Text = step.Handoff.Message
			rs.Timeout = step.Handoff.Timeout
		case step.Screenshot != "":
			rs.Type = "screenshot"
			rs.Expr = step.Screenshot
		case step.Sleep != nil:
			rs.Type = "sleep"
			rs.Duration = step.Sleep.Duration
		case step.Eval != "":
			rs.Type = "eval"
			rs.Expr = InterpolateEnv(step.Eval, env)
		}
		result = append(result, rs)
	}
	return result
}
