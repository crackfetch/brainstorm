package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/crackfetch/brainstorm/internal/lint"
)

// cmdLint implements `brz lint`. Schema validation runs first (delegating
// to workflow.LoadStrict), so lint is a strict superset of `brz validate
// --strict` — every error validate would emit shows up here too.
func cmdLint(args []string) {
	fs := flag.NewFlagSet("lint", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "Emit JSONL stream of findings (one JSON object per line)")
	strict := fs.Bool("strict", false, "Treat warnings as failures (exit 1 on any warn)")
	fs.Usage = func() { printLintUsage() }
	fs.Parse(args)

	if fs.NArg() < 1 {
		printLintUsage()
		os.Exit(exitWorkflowError)
	}

	totalErrs, totalWarns, totalInfos := 0, 0, 0
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)

	for _, path := range fs.Args() {
		res := lint.LintFile(path)
		errs, warns, infos := res.CountBySeverity()
		totalErrs += errs
		totalWarns += warns
		totalInfos += infos

		if *jsonOut {
			for _, f := range res.Findings {
				_ = enc.Encode(f)
			}
		} else {
			if len(res.Findings) > 0 {
				fmt.Print(lint.FormatHuman(res))
			}
		}
	}

	if !*jsonOut {
		// Human summary on stderr so stdout stays clean for piping.
		fmt.Fprintf(os.Stderr, "%d error(s), %d warning(s), %d info\n", totalErrs, totalWarns, totalInfos)
	}

	switch {
	case totalErrs > 0:
		os.Exit(2)
	case totalWarns > 0 && *strict:
		os.Exit(1)
	default:
		os.Exit(0)
	}
}

func printLintUsage() {
	fmt.Print(`Brainstorm (brz) lint — schema-check + smell-check workflow YAML

Usage: brz lint [flags] <file>...

Flags:
  --json     Emit JSONL findings (one JSON object per line) for editor/CI integration
  --strict   Exit non-zero on warnings (default: only errors fail)

Severities:
  error  Schema-invalid or unparseable YAML. Always fails (exit 2).
  warn   Smell — likely brittle. Fails only with --strict (exit 1).
  info   Suggestion. Never fails.

Rules:
  W101  Brittle selector: CSS-in-JS hashed class (.css-XXXX)
  W102  :nth-child selector — brittle to DOM reordering
  W103  Deep child-combinator chain (>4)
  I201  sleep step where wait_visible / wait_enabled / wait_url fits
  W301  eval used as condition without strict-bool coercion (Boolean(...) / !!)
  I401  download step without explicit timeout
  W501  duplicate step labels within one action
  I601  ${VAR} reference not declared in workflow env: and not in host env

JSONL output schema:
  {"file":"...","line":N,"col":N,"severity":"warn","code":"W101-...","message":"..."}

Exit codes:
  0  Clean (or warnings without --strict)
  1  Warnings with --strict
  2  Errors (schema-invalid or unparseable)
`)
}
