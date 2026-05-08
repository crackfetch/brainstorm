package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/crackfetch/brainstorm/internal/yamlfmt"
)

// cmdFmt implements `brz fmt`. Default behavior writes back in place;
// --diff prints a unified diff and exits non-zero if changes are needed
// (CI use); --stdout writes the result to stdout (pipeline use). Stdin
// support: a single arg of "-" reads from stdin and always writes to
// stdout.
func cmdFmt(args []string) {
	fs := flag.NewFlagSet("fmt", flag.ExitOnError)
	diff := fs.Bool("diff", false, "Print unified diff; exit non-zero if changes would be made (CI mode)")
	stdout := fs.Bool("stdout", false, "Write result to stdout instead of in-place")
	fs.Usage = func() { printFmtUsage() }
	fs.Parse(args)

	if fs.NArg() < 1 {
		printFmtUsage()
		os.Exit(exitWorkflowError)
	}

	// Stdin: brz fmt -
	if fs.NArg() == 1 && fs.Arg(0) == "-" {
		in, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: read stdin: %v\n", err)
			os.Exit(exitWorkflowError)
		}
		out, err := yamlfmt.Format(in)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			os.Exit(exitWorkflowError)
		}
		os.Stdout.Write(out)
		return
	}

	anyChanged := false
	for _, path := range fs.Args() {
		in, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %s: %v\n", path, err)
			os.Exit(exitWorkflowError)
		}
		out, err := yamlfmt.Format(in)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %s: %v\n", path, err)
			os.Exit(exitWorkflowError)
		}
		changed := !bytes.Equal(in, out)

		switch {
		case *diff:
			if changed {
				anyChanged = true
				printUnifiedDiff(path, in, out)
			}
		case *stdout:
			os.Stdout.Write(out)
		default:
			if changed {
				anyChanged = true
				if err := os.WriteFile(path, out, 0644); err != nil {
					fmt.Fprintf(os.Stderr, "ERROR: write %s: %v\n", path, err)
					os.Exit(exitWorkflowError)
				}
				fmt.Fprintf(os.Stderr, "fmt %s\n", path)
			}
		}
	}

	if *diff && anyChanged {
		os.Exit(1) // CI signal: changes pending
	}
}

// printUnifiedDiff shells out to `diff -u` for a real unified diff. If
// `diff` isn't on PATH, falls back to a naive "removed/added" listing.
// Worth the shellout because writing a correct LCS diff is more code
// than this whole feature warrants.
func printUnifiedDiff(path string, a, b []byte) {
	if _, err := exec.LookPath("diff"); err == nil {
		cmd := exec.Command("diff", "-u",
			"--label", path,
			"--label", path+" (formatted)",
			"-", "/dev/stdin")
		// diff(1) doesn't support two stdin streams; use temp files.
		fa, _ := os.CreateTemp("", "brz-fmt-a-*.yaml")
		fb, _ := os.CreateTemp("", "brz-fmt-b-*.yaml")
		defer os.Remove(fa.Name())
		defer os.Remove(fb.Name())
		fa.Write(a)
		fa.Close()
		fb.Write(b)
		fb.Close()
		cmd = exec.Command("diff", "-u",
			"--label", path,
			"--label", path+" (formatted)",
			fa.Name(), fb.Name())
		out, _ := cmd.Output() // exit 1 means "differ" — that's expected
		os.Stdout.Write(out)
		return
	}
	// Fallback: dumb listing.
	fmt.Printf("--- %s\n+++ %s (formatted)\n", path, path)
	for _, line := range bytes.Split(a, []byte("\n")) {
		fmt.Printf("-%s\n", line)
	}
	for _, line := range bytes.Split(b, []byte("\n")) {
		fmt.Printf("+%s\n", line)
	}
}

func printFmtUsage() {
	fmt.Print(`Brainstorm (brz) fmt — canonicalize workflow YAML

Usage: brz fmt [flags] <file>...
       brz fmt -          (read stdin, write stdout)

Flags:
  --diff      Print unified diff; exit 1 if changes would be made (CI use)
  --stdout    Write formatted result to stdout instead of editing in place

Behavior:
  - 2-space indentation, terminal newline, no trailing whitespace.
  - Top-level keys sorted: name, env, viewport, debug_screenshots, actions.
  - Action keys sorted: url, force_navigate, headed, viewport, steps, eval, on_error.
  - Step keys sorted: label, optional, navigate, click, fill, ..., retry.
  - Comments are preserved when attached to keys/values. Comments inside
    flow-style step bodies (e.g. ` + "`click: { selector: x }`" + `) are kept as-is
    because flow content is not reformatted.
  - Idempotent: running fmt twice produces identical output.

Exit codes:
  0  No changes (or in-place write succeeded)
  1  --diff: changes would be made
  2  Parse error or IO error
`)
}
