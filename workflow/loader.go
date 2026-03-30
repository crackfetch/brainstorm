package workflow

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var envVarPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

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
