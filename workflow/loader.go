package workflow

import (
	"fmt"
	"os"
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
