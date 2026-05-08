package workflow

// "Did you mean?" decoration for KnownFields(true) errors emitted by
// LoadStrict / LoadStrictFromBytes. yaml.v3's raw message names the
// rejected field and its target type; we use reflection to build a
// registry of valid YAML tags per type, then pick the closest match
// by Levenshtein distance and embed the suggestion in the error.
//
// Without this layer, a user typo'ing `save_too:` in a download step
// gets:
//   yaml: unmarshal errors:
//     line 7: field save_too not found in type workflow.DownloadStep
// and has to go look up DownloadStep's valid fields. With it:
//   config error at line 7: unknown field 'save_too' in download step.
//     Did you mean 'save_to'? Valid fields: timeout, save_as, save_to, return_to.

import (
	"reflect"
	"regexp"
	"sort"
	"strings"
)

// fieldRegistry maps "<package>.<TypeName>" to the list of yaml tag names
// declared on that type. Built once at package init so per-error
// suggestions are O(1) lookups.
var fieldRegistry = buildFieldRegistry()

// strictErrorLine matches one yaml.v3 "field X not found in type Y" line.
// The leading "  line N:" prefix is captured so the decorated output
// keeps the line number; type Y is fully qualified by yaml.v3.
var strictErrorLine = regexp.MustCompile(`^(\s*line \d+: )field (\S+) not found in type (\S+)$`)

// buildFieldRegistry inspects every workflow struct that yaml.v3 will
// decode into and builds the type → yaml-tag-list registry. Adding a
// new type to the workflow schema requires adding it here so its
// suggestions show up; keeps the suggestion table honest about what's
// actually in the schema.
func buildFieldRegistry() map[string][]string {
	types := []any{
		Workflow{},
		Viewport{},
		Action{},
		EvalAssert{},
		Step{},
		ClickStep{},
		FillStep{},
		SelectStep{},
		UploadStep{},
		DownloadStep{},
		WaitStep{},
		WaitURLStep{},
		SleepStep{},
		HandoffStep{},
		RetryStep{},
	}
	out := make(map[string][]string, len(types))
	for _, v := range types {
		t := reflect.TypeOf(v)
		key := t.PkgPath() + "." + t.Name()
		// yaml.v3 uses the short package name (workflow.DownloadStep);
		// store under that key shape.
		short := t.String() // e.g. "workflow.DownloadStep"
		out[short] = yamlTagsFor(t)
		out[key] = out[short]
	}
	return out
}

// yamlTagsFor returns every yaml tag name on a struct type, ignoring
// "-" (skip) tags and inline anonymous structs. Lowercase comparison
// happens at lookup time; we keep the original-case tag here.
func yamlTagsFor(t reflect.Type) []string {
	if t.Kind() != reflect.Struct {
		return nil
	}
	var tags []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		raw := f.Tag.Get("yaml")
		if raw == "" || raw == "-" {
			continue
		}
		// Strip ",omitempty" / ",inline" suffixes — the field name is
		// the first comma-separated chunk.
		name := strings.SplitN(raw, ",", 2)[0]
		if name == "" {
			continue
		}
		tags = append(tags, name)
	}
	sort.Strings(tags)
	return tags
}

// decorateStrictError walks each line of err.Error(), and for every
// "field X not found in type Y" line that yaml.v3 emitted, appends a
// Levenshtein-based suggestion ("Did you mean Z?") plus the full list
// of valid fields for Y. Lines that don't match the pattern are passed
// through verbatim, so non-strict yaml errors stay readable.
//
// Returns err unchanged if it's nil or contains no decoratable line.
// The returned wrapper preserves the original error in its Unwrap()
// chain so callers using errors.As / errors.Is on the underlying
// yaml.v3 error type continue to work.
func decorateStrictError(err error) error {
	if err == nil {
		return nil
	}
	in := err.Error()
	lines := strings.Split(in, "\n")
	changed := false
	for i, line := range lines {
		m := strictErrorLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		prefix, fieldName, typeName := m[1], m[2], m[3]
		valid, ok := fieldRegistry[typeName]
		if !ok {
			continue
		}
		suggestion := bestSuggestion(fieldName, valid)
		var b strings.Builder
		b.WriteString(prefix)
		b.WriteString("unknown field '")
		b.WriteString(fieldName)
		b.WriteString("' in ")
		b.WriteString(humanTypeName(typeName))
		b.WriteString(".")
		if suggestion != "" {
			b.WriteString(" Did you mean '")
			b.WriteString(suggestion)
			b.WriteString("'?")
		}
		if len(valid) > 0 {
			b.WriteString(" Valid fields: ")
			b.WriteString(strings.Join(valid, ", "))
			b.WriteString(".")
		}
		lines[i] = b.String()
		changed = true
	}
	if !changed {
		return err
	}
	return &decoratedYAMLError{orig: err, decorated: strings.Join(lines, "\n")}
}

// decoratedYAMLError wraps a yaml.v3 strict error so the user-visible
// Error() text shows the "Did you mean?" suggestions while errors.As /
// errors.Is consumers can still reach the underlying yaml.v3 error type
// via Unwrap().
type decoratedYAMLError struct {
	orig      error
	decorated string
}

func (e *decoratedYAMLError) Error() string { return e.decorated }
func (e *decoratedYAMLError) Unwrap() error { return e.orig }

// humanTypeName renders "workflow.DownloadStep" as "download step",
// "workflow.WaitURLStep" as "wait_url step", etc. Falls back to the raw
// type name if the suffix isn't "Step".
func humanTypeName(t string) string {
	short := t
	if dot := strings.LastIndex(t, "."); dot >= 0 {
		short = t[dot+1:]
	}
	if !strings.HasSuffix(short, "Step") {
		return short
	}
	stem := strings.TrimSuffix(short, "Step")
	// Convert CamelCase to snake-ish for the human form. WaitURL → wait_url,
	// Download → download, EvalAssert → eval_assert.
	var b strings.Builder
	for i, r := range stem {
		if i > 0 && r >= 'A' && r <= 'Z' {
			// Avoid double-underscore for runs of caps (URL stays as one chunk).
			prev := []rune(stem[:i])[len([]rune(stem[:i]))-1]
			if !(prev >= 'A' && prev <= 'Z') {
				b.WriteByte('_')
			}
		}
		if r >= 'A' && r <= 'Z' {
			b.WriteRune(r + 32)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String() + " step"
}

// bestSuggestion picks the candidate with the lowest Levenshtein distance
// to needle. Returns "" when no candidate is close enough — the threshold
// scales with needle length so "save_too" ↔ "save_to" (distance 1) wins
// but "x" ↔ "completely_different" (distance 18) doesn't.
func bestSuggestion(needle string, candidates []string) string {
	if len(candidates) == 0 || needle == "" {
		return ""
	}
	// Threshold scales with needle length so longer typos can be matched
	// (a 6-char field with 2 substitutions is a real typo), but a floor
	// of 2 ensures short typos and truncations like "timeo" → "timeout"
	// still get suggestions. The far-from-anything case (e.g. "x" vs
	// "completely_different_name") is still rejected because the actual
	// edit distance exceeds the threshold by a wide margin.
	threshold := len(needle) / 3
	if threshold < 2 {
		threshold = 2
	}
	best := ""
	bestDist := threshold + 1
	for _, c := range candidates {
		d := levenshtein(strings.ToLower(needle), strings.ToLower(c))
		if d < bestDist {
			bestDist = d
			best = c
		}
	}
	return best
}

// levenshtein returns the edit distance between a and b. Standard DP
// implementation, two rows of memory. Adequate for short YAML field
// names; we never feed it strings longer than a few dozen runes.
func levenshtein(a, b string) int {
	ar, br := []rune(a), []rune(b)
	if len(ar) == 0 {
		return len(br)
	}
	if len(br) == 0 {
		return len(ar)
	}
	prev := make([]int, len(br)+1)
	curr := make([]int, len(br)+1)
	for j := 0; j <= len(br); j++ {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		curr[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			curr[j] = minInt3(
				prev[j]+1,        // deletion
				curr[j-1]+1,      // insertion
				prev[j-1]+cost,   // substitution
			)
		}
		prev, curr = curr, prev
	}
	return prev[len(br)]
}

func minInt3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}
