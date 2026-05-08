package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/crackfetch/brainstorm/internal/cassette"
	"github.com/crackfetch/brainstorm/internal/config"
	"github.com/crackfetch/brainstorm/workflow"
	"golang.org/x/term"
)

// cmdRecord runs a workflow action like `brz run`, but with a CDP Fetch
// interceptor attached so every (request, response) pair is captured to a
// JSON cassette on disk. See `brz replay` for the matching playback path.
func cmdRecord(args []string) {
	fs := flag.NewFlagSet("record", flag.ExitOnError)
	bf := addBrowserFlags(fs)
	var envs envFlag
	fs.Var(&envs, "env", "Set workflow env var (repeatable): --env KEY=VAL")
	cassettePath := fs.String("cassette", "", "Output cassette path (default: <workflow>.cassette.json)")
	mode := fs.String("mode", "all", "Record mode: all (default) or new (append-only, dedup by key)")
	noBodyCap := fs.Bool("no-body-cap", false, "Disable the 5MB-per-response body cap")
	fs.Usage = func() { printRecordUsage() }
	fs.Parse(args)

	if fs.NArg() < 2 {
		printRecordUsage()
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

	recMode := cassette.RecordMode(*mode)
	if !cassette.IsValidMode(recMode) {
		outputError(useJSON, exitWorkflowError, fmt.Sprintf("invalid --mode %q; want all|new", *mode))
		return
	}

	w, err := workflow.Load(workflowPath)
	if err != nil {
		outputError(useJSON, exitWorkflowError, err.Error())
		return
	}

	out := *cassettePath
	if out == "" {
		base := strings.TrimSuffix(filepath.Base(workflowPath), filepath.Ext(workflowPath))
		out = filepath.Join(filepath.Dir(workflowPath), base+".cassette.json")
	}

	// Seed from existing cassette in --mode new so we can dedup. Surface
	// any read error other than "file does not exist": silently overwriting
	// a corrupt or future-versioned cassette would lose the recording.
	var existing *cassette.Cassette
	if recMode == cassette.ModeNew {
		c, err := cassette.LoadCassetteForRecord(out)
		if err != nil {
			outputError(useJSON, exitWorkflowError, fmt.Sprintf("read existing cassette %s: %v", out, err))
			return
		}
		existing = c
	}

	rec := cassette.NewRecorder(existing, cassette.RecorderOptions{
		Mode:         recMode,
		BodyCapBytes: bodyCap(*noBodyCap),
		Stderr:       os.Stderr,
		Workflow:     filepath.Base(workflowPath),
		BrzVersion:   Version,
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

	if err := rec.Attach(exec.Browser()); err != nil {
		outputError(useJSON, exitBrowserError, fmt.Sprintf("attach recorder: %v", err))
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

	// Drain pending intercepts cleanly before saving.
	if err := rec.Stop(); err != nil {
		fmt.Fprintf(os.Stderr, "recorder: stop: %v\n", err)
	}
	// Give any in-flight fulfill calls a moment to complete. Without this
	// brief grace, an entry can race with shutdown and drop.
	time.Sleep(100 * time.Millisecond)

	c := rec.Cassette()
	c.RecordedAt = time.Now().UTC()
	if err := cassette.Save(c, out); err != nil {
		outputError(useJSON, exitWorkflowError, fmt.Sprintf("save cassette: %v", err))
		return
	}

	// lastResult should always be set (we validated len(names) > 0 earlier),
	// but guard anyway so a future code-path change can't panic during output.
	if lastResult == nil {
		outputError(useJSON, exitWorkflowError, "no action result produced")
		return
	}

	if useJSON {
		encodeJSON(map[string]interface{}{
			"ok":       lastResult.OK,
			"action":   lastResult.Action,
			"cassette": out,
			"entries":  len(c.Entries),
		})
	} else {
		fmt.Printf("OK  %s  cassette=%s entries=%d\n", lastResult.Action, out, len(c.Entries))
	}

	if !lastResult.OK {
		os.Exit(exitActionFailed)
	}
}

func bodyCap(disabled bool) int {
	if disabled {
		return -1
	}
	return cassette.DefaultBodyCapBytes
}

// buildExecutor mirrors cmdRun's Executor construction.
func buildExecutor(w *workflow.Workflow, bf *browserFlags) *workflow.Executor {
	cfg := config.Load()
	headed, autoHeaded := resolveHeaded(bf.headed, cfg)
	opts := []workflow.Option{
		workflow.WithHeaded(headed),
		workflow.WithAutoHeaded(autoHeaded),
		workflow.WithDebug(bf.debug || cfg.Debug),
	}
	if bf.ephemeral {
		dir, _ := os.MkdirTemp("", "brz-ephemeral-*")
		opts = append(opts, workflow.WithProfileDir(dir))
	} else if bf.profile != "" {
		opts = append(opts, workflow.WithProfileDir(bf.profile))
	} else {
		opts = append(opts, workflow.WithProfileDir(cfg.ProfileDir))
	}
	return workflow.NewExecutor(w, opts...)
}

func printRecordUsage() {
	fmt.Print(`Brainstorm (brz) record — capture network traffic to a cassette

Usage: brz record <workflow.yaml> <action> [flags]

Records every HTTP request the page makes via CDP Fetch interception, saving
(method, url, body) → (status, headers, body) pairs to a JSON cassette. Replay
the cassette later with "brz replay --from <cassette>" for deterministic CI runs.

Arguments:
  workflow.yaml   Path to a YAML workflow file
  action          Name of the action to execute

Flags:
  --cassette FILE   Output path (default: <workflow-basename>.cassette.json next to the workflow)
  --mode MODE       all (record everything; default) or new (append-only, dedup by match key)
  --no-body-cap     Disable the 5MB-per-response body cap (cassette may grow large)
  --json            Force JSON output
  --headed          Show the browser window
  --debug           Verbose logging
  --profile DIR     Chrome profile directory
  --ephemeral       Use a fresh temp profile
  --env KEY=VAL     Set a workflow env var (repeatable)

Match key (used by replay):
  (method, canonical-URL, sha256(body))
  URL is canonicalized — host lowercased, query keys sorted, fragment stripped.

Limitations:
  - WebSocket frames are NOT captured in v1.
  - data: URLs pass through without recording.
  - iframe network state may be incomplete.
`)
}
