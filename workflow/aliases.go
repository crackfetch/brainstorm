package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// aliasRefPrefix is the literal prefix every alias reference in YAML must
// start with. We accept exactly the shape `${aliases.NAME}` (and only that
// — bare `${NAME}` continues to mean an env var, see InterpolateEnv) so
// the two substitution systems stay non-overlapping.
const aliasRefPrefix = "${aliases."

// AliasMergeWarning is a soft signal emitted when two alias source files
// (inline + external, or two `aliases_from` entries) define the same key.
// The later definition wins; the warning lets brz lint surface it without
// failing the parse.
type AliasMergeWarning struct {
	Name      string
	From      string
	OverridesFrom string
}

func (w AliasMergeWarning) String() string {
	return fmt.Sprintf("alias %q from %s overrides earlier definition from %s", w.Name, w.From, w.OverridesFrom)
}

// resolveAliases is the parse-time entry point. It loads any external
// alias files referenced by `aliases_from`, merges them in declaration
// order, then layers the inline `aliases:` map on top (inline always
// wins). After the merge it walks every step in every action and rewrites
// `${aliases.NAME}` references in selector positions, recording the alias
// name on the step so runtime errors can name it.
//
// workflowDir is the directory containing the workflow file; relative
// `aliases_from` paths resolve against it. May be empty for in-memory /
// test loads, in which case only absolute and `~`-prefixed paths work.
func resolveAliases(w *Workflow, workflowDir string) ([]AliasMergeWarning, error) {
	// Backwards-compat: workflows that declare neither `aliases:` nor
	// `aliases_from:` are completely untouched. A literal selector that
	// happens to contain `${aliases.foo}` (no opt-in declared) flows
	// through unchanged — the executor will treat it as a literal CSS
	// selector, same as before this feature existed.
	if len(w.Aliases) == 0 && len(w.AliasesFrom) == 0 {
		return nil, nil
	}
	var warnings []AliasMergeWarning

	// origin tracks where each alias was last defined, for warning text.
	merged := map[string]string{}
	origin := map[string]string{}

	for _, ref := range w.AliasesFrom {
		path, err := resolveAliasPath(ref, workflowDir)
		if err != nil {
			return nil, fmt.Errorf("aliases_from %q: %w", ref, err)
		}
		ext, err := loadExternalAliases(path)
		if err != nil {
			return nil, fmt.Errorf("aliases_from %q: %w", ref, err)
		}
		// Sort keys so the merge order is deterministic across runs.
		keys := make([]string, 0, len(ext))
		for k := range ext {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if prior, ok := origin[k]; ok && prior != path {
				warnings = append(warnings, AliasMergeWarning{Name: k, From: path, OverridesFrom: prior})
			}
			merged[k] = ext[k]
			origin[k] = path
		}
	}
	// Inline aliases override aliases_from. Track origin as "<inline>" so
	// the warning string reads naturally.
	inlineKeys := make([]string, 0, len(w.Aliases))
	for k := range w.Aliases {
		inlineKeys = append(inlineKeys, k)
	}
	sort.Strings(inlineKeys)
	for _, k := range inlineKeys {
		if prior, ok := origin[k]; ok {
			warnings = append(warnings, AliasMergeWarning{Name: k, From: "<inline>", OverridesFrom: prior})
		}
		merged[k] = w.Aliases[k]
		origin[k] = "<inline>"
	}

	// Resolve alias-of-alias chains once, up front. Cycles error here so
	// we never leave a half-resolved value for runtime to trip over.
	resolved, err := resolveAliasChains(merged)
	if err != nil {
		return nil, err
	}

	// Stash the fully-resolved map back on the workflow so callers (and
	// future lint passes) can see what was actually used.
	w.resolvedAliases = resolved
	w.aliasOrigin = origin

	// Walk every step, rewriting ${aliases.NAME} in selector fields.
	for actionName, action := range w.Actions {
		for i := range action.Steps {
			if err := rewriteStepAliases(&action.Steps[i], resolved); err != nil {
				return nil, fmt.Errorf("action %q step %d: %w", actionName, i+1, err)
			}
		}
		w.Actions[actionName] = action
	}
	return warnings, nil
}

// resolveAliasPath expands ~, makes relative paths workflow-relative, and
// guards against symlink traversal out of the user's home directory. The
// security guard is intentionally narrow: if the resolved path is INSIDE
// $HOME we re-evaluate symlinks and reject any link that points outside.
// Paths that never enter $HOME (system-wide configs in /etc, project-
// local files) are unaffected.
func resolveAliasPath(ref, workflowDir string) (string, error) {
	if ref == "" {
		return "", fmt.Errorf("empty path")
	}
	expanded, err := expandHomeDir(ref)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(expanded) {
		if workflowDir == "" {
			// In-memory loads (LoadFromBytes / LoadStrictFromBytes) have
			// no source directory to anchor a relative path against.
			// Refuse rather than silently use process cwd, because the
			// parse result would otherwise depend on where the caller
			// happens to run brz from — surprising and untestable.
			return "", fmt.Errorf("relative path %q requires a workflow file (use absolute or ~ path with LoadFromBytes)", ref)
		}
		expanded = filepath.Join(workflowDir, expanded)
	}
	clean := filepath.Clean(expanded)

	home, _ := os.UserHomeDir()
	if home != "" && strings.HasPrefix(clean, home+string(filepath.Separator)) {
		// Inside $HOME: resolve symlinks and require the final target to
		// also be inside $HOME. Symlinks pointing into /etc or /tmp from
		// a user-controlled selectors dir would otherwise let a workflow
		// trick brz into reading anywhere on disk.
		real, err := filepath.EvalSymlinks(clean)
		if err == nil {
			if !strings.HasPrefix(real, home+string(filepath.Separator)) && real != home {
				return "", fmt.Errorf("symlink escapes home directory: %s", clean)
			}
			clean = real
		}
	}
	return clean, nil
}

func loadExternalAliases(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	if err := yaml.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	return out, nil
}

// resolveAliasChains walks the alias map and follows any `${aliases.X}`
// references inside alias *values*. Supports arbitrary depth bounded by
// cycle detection — alias-of-alias-of-alias is fine, but A → B → A errors.
func resolveAliasChains(in map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(in))
	for name := range in {
		val, err := resolveOne(name, in, map[string]bool{})
		if err != nil {
			return nil, err
		}
		out[name] = val
	}
	return out, nil
}

func resolveOne(name string, in map[string]string, visiting map[string]bool) (string, error) {
	if visiting[name] {
		// Build a deterministic cycle path for the error message.
		keys := make([]string, 0, len(visiting))
		for k := range visiting {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return "", fmt.Errorf("alias cycle detected involving %q (chain: %s)", name, strings.Join(keys, " -> "))
	}
	val, ok := in[name]
	if !ok {
		return "", fmt.Errorf("alias %q is not defined", name)
	}
	if !strings.Contains(val, aliasRefPrefix) {
		return val, nil
	}
	visiting[name] = true
	defer delete(visiting, name)

	var resErr error
	out := envVarPattern.ReplaceAllStringFunc(val, func(match string) string {
		inner := strings.TrimSuffix(strings.TrimPrefix(match, "${"), "}")
		if !strings.HasPrefix(inner, "aliases.") {
			return match // not an alias reference; leave for env interpolation later
		}
		ref := strings.TrimPrefix(inner, "aliases.")
		sub, err := resolveOne(ref, in, visiting)
		if err != nil {
			resErr = err
			return match
		}
		return sub
	})
	if resErr != nil {
		return "", resErr
	}
	return out, nil
}

// rewriteStepAliases substitutes alias references in every selector-bearing
// field of a step. Each substitution also records the alias name on the
// nested step struct so runtime errors can mention it.
func rewriteStepAliases(step *Step, resolved map[string]string) error {
	subst := func(s string) (string, string, error) {
		return substituteAliasRef(s, resolved)
	}
	if step.Click != nil {
		v, name, err := subst(step.Click.Selector)
		if err != nil {
			return err
		}
		step.Click.Selector = v
		step.Click.AliasName = name
	}
	if step.Fill != nil {
		v, name, err := subst(step.Fill.Selector)
		if err != nil {
			return err
		}
		step.Fill.Selector = v
		step.Fill.AliasName = name
	}
	if step.Select != nil {
		v, name, err := subst(step.Select.Selector)
		if err != nil {
			return err
		}
		step.Select.Selector = v
		step.Select.AliasName = name
	}
	if step.Upload != nil {
		v, name, err := subst(step.Upload.Selector)
		if err != nil {
			return err
		}
		step.Upload.Selector = v
		step.Upload.AliasName = name
	}
	if step.WaitVisible != nil {
		v, name, err := subst(step.WaitVisible.Selector)
		if err != nil {
			return err
		}
		step.WaitVisible.Selector = v
		step.WaitVisible.AliasName = name
	}
	if step.WaitText != nil {
		v, name, err := subst(step.WaitText.Selector)
		if err != nil {
			return err
		}
		step.WaitText.Selector = v
		step.WaitText.AliasName = name
	}
	if step.WaitEnabled != nil {
		v, name, err := subst(step.WaitEnabled.Selector)
		if err != nil {
			return err
		}
		step.WaitEnabled.Selector = v
		step.WaitEnabled.AliasName = name
	}
	return nil
}

// substituteAliasRef replaces a `${aliases.NAME}` token with its resolved
// value. Returns (resolvedValue, aliasName, err). aliasName is non-empty
// only when the input was a single full-string alias reference, which is
// the typical case (`selector: "${aliases.cart_button}"`); embedded uses
// like `".wrap ${aliases.x}"` still resolve but don't carry an alias name
// into runtime errors because there's no single name to attribute it to.
//
// An undefined alias produces a parse error decorated with the defined
// alias list and a fuzzy "did you mean" suggestion (reusing the existing
// Levenshtein helper from strict_suggest.go).
func substituteAliasRef(s string, resolved map[string]string) (string, string, error) {
	if s == "" || !strings.Contains(s, aliasRefPrefix) {
		return s, "", nil
	}
	// Detect single-token form for clean alias-name attribution.
	trimmed := strings.TrimSpace(s)
	singleAlias := ""
	if strings.HasPrefix(trimmed, aliasRefPrefix) && strings.HasSuffix(trimmed, "}") {
		inner := trimmed[len("${") : len(trimmed)-1]
		if strings.HasPrefix(inner, "aliases.") && !strings.ContainsAny(inner, " \t}{") {
			singleAlias = strings.TrimPrefix(inner, "aliases.")
		}
	}

	var firstErr error
	out := envVarPattern.ReplaceAllStringFunc(s, func(match string) string {
		inner := strings.TrimSuffix(strings.TrimPrefix(match, "${"), "}")
		if !strings.HasPrefix(inner, "aliases.") {
			return match // env var, not an alias; leave for InterpolateEnv
		}
		name := strings.TrimPrefix(inner, "aliases.")
		v, ok := resolved[name]
		if !ok {
			if firstErr == nil {
				firstErr = undefinedAliasError(name, resolved)
			}
			return match
		}
		return v
	})
	if firstErr != nil {
		return "", "", firstErr
	}
	return out, singleAlias, nil
}

func undefinedAliasError(name string, resolved map[string]string) error {
	defined := make([]string, 0, len(resolved))
	for k := range resolved {
		defined = append(defined, k)
	}
	sort.Strings(defined)
	suggestion := bestSuggestion(name, defined)
	msg := fmt.Sprintf("aliases.%s is not defined", name)
	if len(defined) > 0 {
		msg += "; defined aliases: [" + strings.Join(defined, ", ") + "]"
	} else {
		msg += "; no aliases defined"
	}
	if suggestion != "" {
		msg += "; did you mean: " + suggestion + "?"
	}
	return fmt.Errorf("%s", msg)
}

// formatSelectorForError renders a selector for error messages, embedding
// the alias name and source file when available. Examples:
//   `.foo`                                       (no alias)
//   `.foo (alias cart_button)`                   (inline alias)
//   `.foo (alias cart_button from selectors.yml)` (external alias)
func formatSelectorForError(selector, aliasName string, w *Workflow) string {
	if aliasName == "" {
		return fmt.Sprintf("%q", selector)
	}
	origin := ""
	if w != nil {
		origin = w.AliasOrigin(aliasName)
	}
	if origin == "" || origin == "<inline>" {
		return fmt.Sprintf("%q (alias %s)", selector, aliasName)
	}
	return fmt.Sprintf("%q (alias %s from %s)", selector, aliasName, origin)
}

// AliasOrigin returns the source file for an alias, or "" if unknown.
// Exposed for runtime error formatting and future lint tooling.
func (w *Workflow) AliasOrigin(name string) string {
	if w == nil {
		return ""
	}
	return w.aliasOrigin[name]
}

// ResolvedAliases returns a copy of the fully-resolved alias map.
// Exposed for tests and future `brz lint` integration. Returns nil if
// the workflow had no aliases.
//
// TODO: brz lint should call this together with a step-walk to flag
// `unused alias` warnings (defined but never referenced from any step).
// Hooking that up requires the lint subcommand from PR #30, which isn't
// merged yet — leaving this surface ready for it.
func (w *Workflow) ResolvedAliases() map[string]string {
	if w == nil || len(w.resolvedAliases) == 0 {
		return nil
	}
	out := make(map[string]string, len(w.resolvedAliases))
	for k, v := range w.resolvedAliases {
		out[k] = v
	}
	return out
}
