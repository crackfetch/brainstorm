// Package examples embeds all bundled workflow YAML files so brz examples
// can surface them at runtime without a separate filesystem dependency.
// New YAML files added to this directory are picked up automatically by
// the //go:embed directive on the next build.
package examples

import (
	"embed"
	"sort"
	"strings"
)

//go:embed *.yaml
var FS embed.FS

// Names returns the example names (filename without the .yaml suffix),
// sorted alphabetically. Excludes the embed.go file itself.
func Names() []string {
	entries, err := FS.ReadDir(".")
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if !strings.HasSuffix(n, ".yaml") {
			continue
		}
		names = append(names, strings.TrimSuffix(n, ".yaml"))
	}
	sort.Strings(names)
	return names
}

// Read returns the raw YAML bytes for an example by name (no .yaml suffix).
// Returns the second value as the discovered filename for error messages.
func Read(name string) ([]byte, string, error) {
	filename := name + ".yaml"
	data, err := FS.ReadFile(filename)
	return data, filename, err
}

// Summary extracts the first non-empty comment line from a YAML file as a
// one-line description. Convention: bundled examples start with
// "# Example: <description>" then "#" then "# Demonstrates: <list>". This
// helper surfaces the second line of usable text — the Demonstrates list —
// because that's the highest-information one for a `list` view.
//
// Falls back to the first comment line if no Demonstrates: marker exists.
func Summary(yaml []byte) string {
	lines := strings.Split(string(yaml), "\n")
	var firstComment string
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if !strings.HasPrefix(ln, "#") {
			break // header block ended
		}
		body := strings.TrimSpace(strings.TrimPrefix(ln, "#"))
		if body == "" {
			continue
		}
		if firstComment == "" {
			firstComment = body
		}
		if strings.HasPrefix(body, "Demonstrates:") {
			return strings.TrimSpace(strings.TrimPrefix(body, "Demonstrates:"))
		}
	}
	return firstComment
}
