package workflow

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestInlineAliasesSubstitution(t *testing.T) {
	src := []byte(`
name: aliases-inline
aliases:
  cart_button: "#header .cart"
  product_card: ".product-grid > .card"
actions:
  open:
    steps:
      - click: { selector: "${aliases.cart_button}" }
      - wait_visible: { selector: "${aliases.product_card}" }
`)
	w, err := LoadFromBytes(src)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	steps := w.Actions["open"].Steps
	if got, want := steps[0].Click.Selector, "#header .cart"; got != want {
		t.Errorf("click selector = %q, want %q", got, want)
	}
	if got, want := steps[0].Click.AliasName, "cart_button"; got != want {
		t.Errorf("click alias name = %q, want %q", got, want)
	}
	if got, want := steps[1].WaitVisible.Selector, ".product-grid > .card"; got != want {
		t.Errorf("wait_visible selector = %q, want %q", got, want)
	}
	// LoadStrictFromBytes must accept the new keys, not reject them.
	if _, err := LoadStrictFromBytes(src); err != nil {
		t.Fatalf("strict load rejected new keys: %v", err)
	}
}

func TestAliasesFromExternalFileMergeOrderAndOverride(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.yaml")
	b := filepath.Join(dir, "b.yaml")
	wf := filepath.Join(dir, "w.yaml")
	if err := os.WriteFile(a, []byte("cart: \".a-cart\"\nshared: \".a-shared\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("shared: \".b-shared\"\nproduct: \".b-product\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wfBody := []byte(`name: aliases-from
aliases_from:
  - ./a.yaml
  - ./b.yaml
actions:
  go:
    steps:
      - click: { selector: "${aliases.cart}" }
      - click: { selector: "${aliases.shared}" }
      - click: { selector: "${aliases.product}" }
`)
	if err := os.WriteFile(wf, wfBody, 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := Load(wf)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	steps := w.Actions["go"].Steps
	cases := []struct{ got, want string }{
		{steps[0].Click.Selector, ".a-cart"},
		{steps[1].Click.Selector, ".b-shared"}, // later file wins
		{steps[2].Click.Selector, ".b-product"},
	}
	for i, c := range cases {
		if c.got != c.want {
			t.Errorf("step[%d] selector = %q, want %q", i, c.got, c.want)
		}
	}

	// Merge warning surface (call resolver again on a fresh workflow to inspect warnings).
	var fresh Workflow
	if err := yaml.Unmarshal(wfBody, &fresh); err != nil {
		t.Fatal(err)
	}
	warnings, err := resolveAliases(&fresh, dir)
	if err != nil {
		t.Fatalf("resolveAliases: %v", err)
	}
	foundOverride := false
	for _, w := range warnings {
		if w.Name == "shared" && strings.HasSuffix(w.From, "b.yaml") {
			foundOverride = true
		}
	}
	if !foundOverride {
		t.Errorf("expected merge override warning for 'shared', got %#v", warnings)
	}
}

func TestInlineOverridesAliasesFrom(t *testing.T) {
	dir := t.TempDir()
	ext := filepath.Join(dir, "ext.yaml")
	if err := os.WriteFile(ext, []byte("cart: \".external\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wfBody := []byte(`name: inline-wins
aliases_from:
  - ` + ext + `
aliases:
  cart: ".inline-wins"
actions:
  go:
    steps:
      - click: { selector: "${aliases.cart}" }
`)
	w, err := LoadFromBytes(wfBody)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := w.Actions["go"].Steps[0].Click.Selector; got != ".inline-wins" {
		t.Errorf("inline override failed: got %q", got)
	}
}

func TestUndefinedAliasErrorWithDidYouMean(t *testing.T) {
	src := []byte(`name: undef
aliases:
  cart_button: ".cart"
  product_card: ".card"
actions:
  go:
    steps:
      - click: { selector: "${aliases.cart_buton}" }
`)
	_, err := LoadFromBytes(src)
	if err == nil {
		t.Fatal("expected error for undefined alias")
	}
	msg := err.Error()
	if !strings.Contains(msg, "aliases.cart_buton is not defined") {
		t.Errorf("missing 'is not defined' phrasing: %s", msg)
	}
	if !strings.Contains(msg, "did you mean: cart_button") {
		t.Errorf("missing did-you-mean: %s", msg)
	}
	if !strings.Contains(msg, "defined aliases: [cart_button, product_card]") {
		t.Errorf("missing defined-aliases list: %s", msg)
	}
}

func TestAliasCycleDetection(t *testing.T) {
	src := []byte(`name: cycle
aliases:
  a: "${aliases.b}"
  b: "${aliases.a}"
actions:
  go:
    steps:
      - click: { selector: "${aliases.a}" }
`)
	_, err := LoadFromBytes(src)
	if err == nil {
		t.Fatal("expected cycle error")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("expected 'cycle' in error, got: %v", err)
	}
}

func TestAliasChainResolves(t *testing.T) {
	// alias-of-alias chains must resolve transitively.
	src := []byte(`name: chain
aliases:
  base: ".real"
  mid: "${aliases.base}"
  top: "${aliases.mid}"
actions:
  go:
    steps:
      - click: { selector: "${aliases.top}" }
`)
	w, err := LoadFromBytes(src)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := w.Actions["go"].Steps[0].Click.Selector; got != ".real" {
		t.Errorf("chained alias unresolved: %q", got)
	}
}

func TestNoAliasesIsByteIdenticalThroughParser(t *testing.T) {
	// Backwards-compat smoke: a workflow that uses no alias machinery
	// should round-trip with no surprises and no AliasName fields set.
	src := []byte(`name: plain
actions:
  go:
    steps:
      - click: { selector: ".btn" }
      - fill: { selector: "input", value: "hi" }
`)
	w, err := LoadFromBytes(src)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if w.Actions["go"].Steps[0].Click.AliasName != "" {
		t.Errorf("AliasName populated for non-aliased click")
	}
	if w.Aliases != nil || w.AliasesFrom != nil {
		t.Errorf("alias fields populated unexpectedly: %v %v", w.Aliases, w.AliasesFrom)
	}
	if w.ResolvedAliases() != nil {
		t.Errorf("ResolvedAliases() should be nil for no-alias workflow")
	}
	// Also: re-marshaling the workflow YAML should not emit aliases keys.
	out, err := yaml.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("aliases:")) || bytes.Contains(out, []byte("aliases_from:")) {
		t.Errorf("re-marshaled YAML leaked alias keys: %s", out)
	}
}

func TestAliasesFromHomeDirSymlinkEscapeRejected(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	// Create a temp directory inside $HOME with a symlink that points
	// outside $HOME. The path resolver should refuse to read through it.
	scratch, err := os.MkdirTemp(home, "brz-alias-test-")
	if err != nil {
		t.Skipf("cannot create temp under home: %v", err)
	}
	defer os.RemoveAll(scratch)
	outside := t.TempDir()
	target := filepath.Join(outside, "evil.yaml")
	if err := os.WriteFile(target, []byte("x: y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(scratch, "link.yaml")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	if _, err := resolveAliasPath(link, ""); err == nil {
		t.Errorf("expected symlink-escape rejection, got nil")
	}
}

func TestLiteralAliasRefIgnoredWhenNoOptIn(t *testing.T) {
	// A workflow that doesn't declare `aliases:` or `aliases_from:` must
	// pass through any literal `${aliases.foo}` substring untouched —
	// this is the strict backwards-compat guarantee. The selector might
	// be junk CSS at runtime, but parsing must not error out.
	src := []byte(`name: nope
actions:
  go:
    steps:
      - click: { selector: "${aliases.foo}" }
`)
	w, err := LoadFromBytes(src)
	if err != nil {
		t.Fatalf("non-opt-in workflow rejected: %v", err)
	}
	if got := w.Actions["go"].Steps[0].Click.Selector; got != "${aliases.foo}" {
		t.Errorf("literal preserved when no opt-in; got %q", got)
	}
}

func TestRelativeAliasesFromRequiresWorkflowDir(t *testing.T) {
	// LoadFromBytes has no source directory; relative aliases_from must
	// be rejected with a clear error rather than silently using cwd.
	src := []byte(`name: rel
aliases_from:
  - ./selectors.yaml
actions:
  go:
    steps:
      - click: { selector: "${aliases.x}" }
`)
	_, err := LoadFromBytes(src)
	if err == nil {
		t.Fatal("expected error for relative aliases_from in LoadFromBytes")
	}
	if !strings.Contains(err.Error(), "relative path") {
		t.Errorf("error doesn't mention relative path: %v", err)
	}
}

func TestRuntimeErrorIncludesAliasName(t *testing.T) {
	// formatSelectorForError is the load-bearing piece of "alias name in
	// runtime error". Unit-test it directly so we don't need a browser.
	w := &Workflow{aliasOrigin: map[string]string{"cart_button": "<inline>", "x": "/tmp/sel.yaml"}}
	got := formatSelectorForError(".foo", "cart_button", w)
	if !strings.Contains(got, "alias cart_button") {
		t.Errorf("missing alias name: %s", got)
	}
	got = formatSelectorForError(".foo", "x", w)
	if !strings.Contains(got, "/tmp/sel.yaml") {
		t.Errorf("missing origin file: %s", got)
	}
	got = formatSelectorForError(".foo", "", nil)
	if got != `".foo"` {
		t.Errorf("plain path changed: %s", got)
	}
}
