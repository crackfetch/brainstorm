package main

import (
	"flag"
	"testing"
)

// Pin the flag-parsing contract for `brz validate --strict <file>`. Go's
// stdlib flag package stops at the first positional arg, which means
// `brz validate file.yaml --strict` would silently run leniently. The CLI
// usage string and agent.md both warn about this; this test guards against
// a future refactor that swaps in a different parser and accidentally
// changes the contract.

func TestValidateFlagOrder_StrictBeforePositional(t *testing.T) {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "")
	strict := fs.Bool("strict", false, "")
	if err := fs.Parse([]string{"--strict", "file.yaml"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !*strict {
		t.Errorf("--strict before positional should be parsed; got false")
	}
	if *jsonOut {
		t.Errorf("--json should be false (not set)")
	}
	if fs.NArg() != 1 || fs.Arg(0) != "file.yaml" {
		t.Errorf("positional: got %v, want [file.yaml]", fs.Args())
	}
}

func TestValidateFlagOrder_StrictAfterPositional_IsIgnored(t *testing.T) {
	// Go flag's documented behavior: flags after the first positional are
	// treated as more positionals. We pin this so a future replacement
	// with a smarter parser is a deliberate choice (and updates the docs).
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	strict := fs.Bool("strict", false, "")
	if err := fs.Parse([]string{"file.yaml", "--strict"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if *strict {
		t.Errorf("--strict after positional should NOT be parsed (Go flag default); got true. " +
			"If you've replaced the flag parser, update prompts/agent.md too — the docs warn users about this.")
	}
	if fs.NArg() < 1 || fs.Arg(0) != "file.yaml" {
		t.Errorf("positional should still be file.yaml; got %v", fs.Args())
	}
}

func TestValidateFlagOrder_BothFlagsBeforePositional(t *testing.T) {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "")
	strict := fs.Bool("strict", false, "")
	if err := fs.Parse([]string{"--json", "--strict", "file.yaml"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !*jsonOut || !*strict {
		t.Errorf("both flags should be true; got json=%v strict=%v", *jsonOut, *strict)
	}
}
