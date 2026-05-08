// Package yamlfmt canonicalizes brz workflow YAML files.
//
// Goals:
//   - Stable 2-space indentation, terminal newline, no trailing whitespace.
//   - Deterministic key ordering for known structural keys (workflow,
//     action, step). Unknown keys keep their original relative order.
//   - Preserve comments. yaml.v3's Node API carries Head/Line/Foot
//     comments through round-trips; we lean on that and avoid the
//     typed-unmarshal path that drops them.
//   - Idempotent: Format(Format(x)) == Format(x).
//
// Non-goals:
//   - Reformatting flow-style step bodies like `click: { selector: ..., timeout: ... }`.
//     Sorting inside a flow mapping rewrites the line and risks dropping
//     line comments attached to the original key order, so we leave inner
//     flow mappings alone. Top-level (block-style) maps are sorted.
package yamlfmt

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// Canonical key order for the workflow root.
var workflowOrder = []string{
	"name",
	"env",
	"viewport",
	"debug_screenshots",
	"actions",
}

// Canonical key order for an action.
var actionOrder = []string{
	"url",
	"force_navigate",
	"headed",
	"viewport",
	"steps",
	"eval",
	"on_error",
}

// Canonical key order for a step (block-style only — we don't touch flow).
var stepOrder = []string{
	"label",
	"optional",
	"navigate",
	"click",
	"fill",
	"select",
	"upload",
	"download",
	"wait_visible",
	"wait_text",
	"wait_url",
	"wait_enabled",
	"handoff",
	"screenshot",
	"sleep",
	"eval",
	"retry",
}

// Format reads YAML bytes and returns a canonicalized version.
// Returns an error if the input is not parseable YAML; the error is
// the raw yaml.v3 error which carries file:line:col.
func Format(in []byte) ([]byte, error) {
	if len(bytes.TrimSpace(in)) == 0 {
		return []byte{}, nil
	}
	var root yaml.Node
	if err := yaml.Unmarshal(in, &root); err != nil {
		return nil, err
	}

	canonicalize(&root)

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&root); err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("close encoder: %w", err)
	}
	return buf.Bytes(), nil
}

// canonicalize mutates the node tree to canonical form.
func canonicalize(n *yaml.Node) {
	if n == nil {
		return
	}
	switch n.Kind {
	case yaml.DocumentNode:
		for _, c := range n.Content {
			canonicalizeRoot(c)
		}
	}
}

// canonicalizeRoot handles the workflow document root.
func canonicalizeRoot(n *yaml.Node) {
	if n.Kind != yaml.MappingNode {
		return
	}
	// Force block style at the root so the formatter output is stable.
	n.Style = 0
	sortMapping(n, workflowOrder)

	// Walk into actions: <name>.steps[].
	for i := 0; i+1 < len(n.Content); i += 2 {
		key := n.Content[i].Value
		val := n.Content[i+1]
		if key == "actions" && val.Kind == yaml.MappingNode {
			val.Style = 0
			for j := 0; j+1 < len(val.Content); j += 2 {
				canonicalizeAction(val.Content[j+1])
			}
		}
	}
}

func canonicalizeAction(n *yaml.Node) {
	if n.Kind != yaml.MappingNode {
		return
	}
	n.Style = 0
	sortMapping(n, actionOrder)
	for i := 0; i+1 < len(n.Content); i += 2 {
		key := n.Content[i].Value
		val := n.Content[i+1]
		if key == "steps" && val.Kind == yaml.SequenceNode {
			val.Style = 0
			for _, step := range val.Content {
				canonicalizeStep(step)
			}
		}
	}
}

func canonicalizeStep(n *yaml.Node) {
	if n.Kind != yaml.MappingNode {
		return
	}
	// Only touch block-style step maps. Flow-style ({a: 1, b: 2}) bodies
	// stay untouched to avoid disturbing inline comments + author intent.
	if n.Style == yaml.FlowStyle {
		return
	}
	sortMapping(n, stepOrder)
}

// sortMapping reorders n.Content (key,val pairs) by `order`. Keys not in
// `order` keep their relative order and are appended after the known
// keys. Comments stay attached to their keys/values because yaml.v3
// stores them on Node, not on the parent's slice position.
func sortMapping(n *yaml.Node, order []string) {
	if n.Kind != yaml.MappingNode {
		return
	}
	rank := make(map[string]int, len(order))
	for i, k := range order {
		rank[k] = i
	}
	type pair struct {
		key, val *yaml.Node
		idx      int // original position, used as tiebreak
	}
	pairs := make([]pair, 0, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		pairs = append(pairs, pair{key: n.Content[i], val: n.Content[i+1], idx: i})
	}
	// Stable sort: known keys first by rank, unknown keys keep original order after.
	// We implement it manually to be explicit (avoid sort import for clarity).
	sortStable(pairs, func(a, b pair) bool {
		ra, oka := rank[a.key.Value]
		rb, okb := rank[b.key.Value]
		switch {
		case oka && okb:
			if ra != rb {
				return ra < rb
			}
			return a.idx < b.idx
		case oka && !okb:
			return true
		case !oka && okb:
			return false
		default:
			return a.idx < b.idx
		}
	})
	out := make([]*yaml.Node, 0, len(n.Content))
	for _, p := range pairs {
		out = append(out, p.key, p.val)
	}
	n.Content = out
}

// sortStable is a tiny insertion sort to keep the package dep-free.
// n is small (mapping size), so insertion sort is fine and stable.
func sortStable[T any](xs []T, less func(a, b T) bool) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && less(xs[j], xs[j-1]); j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}
