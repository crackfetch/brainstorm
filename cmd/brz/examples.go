package main

// brz examples — surface bundled workflow YAML samples so users (and LLM
// agents) discover patterns without having to read upstream repos. The
// embed is in workflows/examples/embed.go; this file is just the CLI
// surface around it.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/crackfetch/brainstorm/workflows/examples"
)

func cmdExamples(args []string) {
	if len(args) == 0 {
		printExamplesUsage()
		os.Exit(exitWorkflowError)
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "list":
		cmdExamplesList(rest)
	case "cat":
		cmdExamplesCat(rest)
	case "scaffold":
		cmdExamplesScaffold(rest)
	case "help", "--help", "-h":
		printExamplesUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown examples subcommand: %s\n\n", sub)
		printExamplesUsage()
		os.Exit(exitWorkflowError)
	}
}

func cmdExamplesList(args []string) {
	fs := flag.NewFlagSet("examples list", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "JSON output (auto when piped)")
	fs.Parse(args)
	useJSON := *jsonOut || !isStdoutTTY()

	type entry struct {
		Name    string `json:"name"`
		Summary string `json:"summary"`
	}
	var rows []entry
	for _, name := range examples.Names() {
		data, _, err := examples.Read(name)
		if err != nil {
			continue
		}
		rows = append(rows, entry{Name: name, Summary: examples.Summary(data)})
	}

	if useJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rows)
		return
	}
	if len(rows) == 0 {
		fmt.Println("(no examples bundled)")
		return
	}
	maxName := 0
	for _, r := range rows {
		if len(r.Name) > maxName {
			maxName = len(r.Name)
		}
	}
	for _, r := range rows {
		fmt.Printf("  %-*s  %s\n", maxName, r.Name, r.Summary)
	}
	fmt.Println()
	fmt.Printf("Run `brz examples cat <name>` to print one. `brz examples scaffold <name> [<dir>]` to copy it.\n")
}

func cmdExamplesCat(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: brz examples cat <name>")
		os.Exit(exitWorkflowError)
	}
	name := args[0]
	data, filename, err := examples.Read(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "example %q not found (looked for %s). Run `brz examples list` for available names.\n", name, filename)
		os.Exit(exitWorkflowError)
	}
	os.Stdout.Write(data)
}

func cmdExamplesScaffold(args []string) {
	fs := flag.NewFlagSet("examples scaffold", flag.ExitOnError)
	overwrite := fs.Bool("force", false, "Overwrite an existing file")
	fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: brz examples scaffold [--force] <name> [<dir>]")
		os.Exit(exitWorkflowError)
	}
	name := fs.Arg(0)
	dir := "."
	if fs.NArg() >= 2 {
		dir = fs.Arg(1)
	}
	data, filename, err := examples.Read(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "example %q not found (looked for %s). Run `brz examples list`.\n", name, filename)
		os.Exit(exitWorkflowError)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "create dir %q: %v\n", dir, err)
		os.Exit(exitBrowserError)
	}
	target := filepath.Join(dir, filename)
	// Without --force, refuse to overwrite an existing file. Use
	// O_CREATE|O_EXCL so the existence check + create are atomic — a
	// stat-then-write pattern would race with a concurrent writer
	// (TOCTOU) and could clobber a file that appeared between the two
	// syscalls. With --force, plain WriteFile (truncate) is intentional.
	if *overwrite {
		if err := os.WriteFile(target, data, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", target, err)
			os.Exit(exitBrowserError)
		}
	} else {
		f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			if os.IsExist(err) {
				fmt.Fprintf(os.Stderr, "%s already exists. Use --force to overwrite.\n", target)
				os.Exit(exitWorkflowError)
			}
			fmt.Fprintf(os.Stderr, "create %s: %v\n", target, err)
			os.Exit(exitBrowserError)
		}
		if _, werr := f.Write(data); werr != nil {
			_ = f.Close()
			fmt.Fprintf(os.Stderr, "write %s: %v\n", target, werr)
			os.Exit(exitBrowserError)
		}
		if cerr := f.Close(); cerr != nil {
			fmt.Fprintf(os.Stderr, "close %s: %v\n", target, cerr)
			os.Exit(exitBrowserError)
		}
	}
	fmt.Println(target)
}

func printExamplesUsage() {
	bundled := strings.Join(examples.Names(), ", ")
	fmt.Printf(`brz examples — bundled workflow YAML patterns

Usage:
  brz examples list              Print bundled examples + one-line summaries
  brz examples cat <name>        Print one example to stdout
  brz examples scaffold <name> [<dir>]  Write the example file into <dir> (default: cwd)

The bundled examples cover common patterns: form login, click-then-download,
captcha-gated forms (wait_enabled), modal disambiguation (click.visible/nth),
download save_to + return_to, multi-step uploads, and more.

Bundled: %s
`, bundled)
}
