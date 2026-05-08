package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/crackfetch/brainstorm/internal/cassette"
	"github.com/crackfetch/brainstorm/workflow"
	"golang.org/x/term"
)

// cmdReplay runs a workflow action with the network served from a cassette
// instead of the live internet. Strict mode fails on any unmatched request,
// which is what you want in CI: a regression in workflow behavior surfaces as
// a clear "request X is not in the cassette" error.
func cmdReplay(args []string) {
	fs := flag.NewFlagSet("replay", flag.ExitOnError)
	bf := addBrowserFlags(fs)
	var envs envFlag
	fs.Var(&envs, "env", "Set workflow env var (repeatable): --env KEY=VAL")
	from := fs.String("from", "", "Cassette file to replay (required)")
	strict := fs.Bool("strict", false, "Fail run when a request is not in the cassette (recommended for CI)")
	fs.Usage = func() { printReplayUsage() }
	fs.Parse(args)

	if fs.NArg() < 2 || *from == "" {
		printReplayUsage()
		os.Exit(exitWorkflowError)
	}

	workflowPath := fs.Arg(0)
	actionArg := fs.Arg(1)
	names := workflow.SplitActionNames(actionArg)
	useJSON := bf.json || !term.IsTerminal(int(os.Stdout.Fd()))

	if len(names) == 0 {
		outputError(useJSON, exitWorkflowError, "no action name provided")
		return
	}

	w, err := workflow.Load(workflowPath)
	if err != nil {
		outputError(useJSON, exitWorkflowError, err.Error())
		return
	}

	c, err := cassette.Load(*from)
	if err != nil {
		outputError(useJSON, exitWorkflowError, err.Error())
		return
	}
	idx := cassette.NewIndex(c)
	rep := cassette.NewReplayer(idx, cassette.ReplayerOptions{
		Strict: *strict,
		Stderr: os.Stderr,
	})

	exec := buildExecutor(w, bf)
	for k, v := range parseEnvFlags(envs) {
		exec.SetEnv(k, v)
	}
	if err := exec.Start(); err != nil {
		outputError(useJSON, exitBrowserError, err.Error())
		return
	}
	defer exec.Close()

	if err := rep.Attach(exec.Browser()); err != nil {
		outputError(useJSON, exitBrowserError, fmt.Sprintf("attach replayer: %v", err))
		return
	}

	var lastResult *workflow.ActionResult
	for i, name := range names {
		result := exec.RunAction(name)
		lastResult = result
		if !result.OK {
			break
		}
		if len(names) > 1 && i < len(names)-1 {
			if useJSON {
				encodeJSON(result)
			} else {
				fmt.Printf("OK  %s  %d steps  %dms\n", result.Action, result.Steps, result.DurationMs)
			}
		}
	}

	_ = rep.Stop()

	if lastResult == nil {
		outputError(useJSON, exitWorkflowError, "no action result produced")
		return
	}

	misses := rep.Misses()
	if useJSON {
		encodeJSON(map[string]interface{}{
			"ok":       lastResult.OK,
			"action":   lastResult.Action,
			"cassette": *from,
			"misses":   len(misses),
		})
	} else {
		printHumanResult(lastResult)
		if len(misses) > 0 {
			fmt.Fprintf(os.Stderr, "replay: %d unmatched request(s)\n", len(misses))
		}
	}

	if *strict && len(misses) > 0 {
		// Strict mode: even if the action reported OK, surface the cassette mismatch.
		os.Exit(exitActionFailed)
	}
	if !lastResult.OK {
		os.Exit(exitActionFailed)
	}
}

func printReplayUsage() {
	fmt.Print(`Brainstorm (brz) replay — run a workflow against a recorded cassette

Usage: brz replay <workflow.yaml> <action> --from <cassette> [flags]

Serves browser network requests from a cassette file recorded by "brz record".
In --strict mode, any request not in the cassette fails the run — useful for CI.
In default (non-strict) mode, unmatched requests pass through to the real network
with a stderr warning.

Arguments:
  workflow.yaml   Path to a YAML workflow file
  action          Name of the action to execute

Flags:
  --from FILE       Cassette file (required)
  --strict          Fail unmatched requests (recommended for CI)
  --json            Force JSON output
  --headed          Show the browser window
  --debug           Verbose logging
  --profile DIR     Chrome profile directory
  --ephemeral       Use a fresh temp profile
  --env KEY=VAL     Set a workflow env var (repeatable)

Match key:
  (method, canonical-URL, sha256(body)). Querystring matters; cosmetic
  differences (host casing, query-param order, fragment) are normalized.
`)
}
