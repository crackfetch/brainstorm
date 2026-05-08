package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSelector(t *testing.T) {
	cases := []struct {
		in       string
		mode     string
		wantType string
		wantSel  string
	}{
		{".foo", "css", "css", ".foo"},
		{".foo", "xpath", "xpath", ".foo"},
		{"xpath://a", "css", "xpath", "//a"},
		{"XPATH:  //a[@id='x']", "css", "xpath", "//a[@id='x']"},
		{"css:div.thing", "xpath", "css", "div.thing"},
		{"  div  ", "css", "css", "  div  "}, // we don't trim non-prefix; that's fine
	}
	for _, tc := range cases {
		gotType, gotSel := parseSelector(tc.in, tc.mode)
		if gotType != tc.wantType || gotSel != tc.wantSel {
			t.Errorf("parseSelector(%q, %q) = (%q, %q) want (%q, %q)",
				tc.in, tc.mode, gotType, gotSel, tc.wantType, tc.wantSel)
		}
	}
}

func TestSaveSelector_CreatesAndAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "selectors.yaml")

	if err := saveSelector(path, "submit", "css", "#submit-btn"); err != nil {
		t.Fatalf("save 1: %v", err)
	}
	if err := saveSelector(path, "go", "xpath", "//button[@id='go']"); err != nil {
		t.Fatalf("save 2: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(data)

	for _, want := range []string{
		"selectors:",
		`submit: "#submit-btn"`,
		"go:",
		"xpath://button",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected file to contain %q, got:\n%s", want, got)
		}
	}
}

// TestRunCommand_Quit confirms `:q` returns false and other commands return true.
func TestRunCommand_Quit(t *testing.T) {
	rp := &repl{out: os.Stderr, err: os.Stderr}
	for _, cmd := range []string{":q", ":quit", ":exit"} {
		if rp.runCommand(cmd) {
			t.Errorf("runCommand(%q) should return false", cmd)
		}
	}
	for _, cmd := range []string{":help", ":mode css", ":mode xpath"} {
		if !rp.runCommand(cmd) {
			t.Errorf("runCommand(%q) should return true", cmd)
		}
	}
}

func TestRunCommand_ModeChange(t *testing.T) {
	rp := &repl{out: os.Stderr, err: os.Stderr, mode: "css"}
	rp.runCommand(":mode xpath")
	if rp.mode != "xpath" {
		t.Errorf("mode = %q want xpath", rp.mode)
	}
	rp.runCommand(":mode css")
	if rp.mode != "css" {
		t.Errorf("mode = %q want css", rp.mode)
	}
	// invalid mode should not change current value
	rp.runCommand(":mode bogus")
	if rp.mode != "css" {
		t.Errorf("invalid :mode should not mutate; got %q", rp.mode)
	}
}

// TestParseSelector_EmptyInputs hardens against degenerate input.
func TestParseSelector_EmptyInputs(t *testing.T) {
	gotType, gotSel := parseSelector("xpath:", "css")
	if gotType != "xpath" || gotSel != "" {
		t.Errorf("parseSelector empty xpath: (%q, %q)", gotType, gotSel)
	}
	gotType, gotSel = parseSelector("", "css")
	if gotType != "css" || gotSel != "" {
		t.Errorf("parseSelector empty: (%q, %q)", gotType, gotSel)
	}
}
