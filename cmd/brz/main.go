package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/crackfetch/brainstorm/internal/config"
	"github.com/crackfetch/brainstorm/prompts"
	"github.com/crackfetch/brainstorm/workflow"
	"golang.org/x/term"
)

// promptContent returns the embedded agent prompt.
func promptContent() string {
	return prompts.AgentPrompt
}

// Version is set at build time via ldflags.
var Version = "dev"

// Exit codes — deterministic, so agents can branch on them.
const (
	exitOK            = 0
	exitActionFailed  = 1
	exitWorkflowError = 2
	exitBrowserError  = 3
)

// envFlag collects repeatable --env KEY=VAL flags.
type envFlag []string

func (e *envFlag) String() string { return strings.Join(*e, ", ") }
func (e *envFlag) Set(v string) error {
	*e = append(*e, v)
	return nil
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(exitWorkflowError)
	}

	cmd := os.Args[1]
	switch cmd {
	case "run":
		cmdRun(os.Args[2:])
	case "inspect":
		cmdInspect(os.Args[2:])
	case "screenshot":
		cmdScreenshot(os.Args[2:])
	case "eval":
		cmdEval(os.Args[2:])
	case "validate":
		cmdValidate(os.Args[2:])
	case "actions":
		cmdActions(os.Args[2:])
	case "prompt":
		cmdPrompt()
	case "version":
		fmt.Println(Version)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		printUsage()
		os.Exit(exitWorkflowError)
	}
}

// ---------------------------------------------------------------------------
// brz prompt
// ---------------------------------------------------------------------------

func cmdPrompt() {
	fmt.Print(promptContent())
}

// ---------------------------------------------------------------------------
// Shared browser setup
// ---------------------------------------------------------------------------

type browserFlags struct {
	json      bool
	headed    bool
	debug     bool
	profile   string
	ephemeral bool
}

// addBrowserFlags registers common browser flags on a FlagSet.
func addBrowserFlags(fs *flag.FlagSet) *browserFlags {
	bf := &browserFlags{}
	fs.BoolVar(&bf.json, "json", false, "Force JSON output (default when piped)")
	fs.BoolVar(&bf.headed, "headed", false, "Show browser window")
	fs.BoolVar(&bf.debug, "debug", false, "Verbose logging + failure screenshots")
	fs.StringVar(&bf.profile, "profile", "", "Chrome profile directory")
	fs.BoolVar(&bf.ephemeral, "ephemeral", false, "Use a temporary profile (no session persistence)")
	return bf
}

// resolveHeaded returns (headed, autoHeaded) from flags + config.
func resolveHeaded(flagHeaded bool, cfg *config.Config) (bool, bool) {
	if flagHeaded || cfg.Headed == "1" {
		return true, false
	}
	if cfg.Headed == "auto" {
		return false, true
	}
	return false, false
}

// startBrowser creates and starts an Executor from flags + config.
// Returns the executor and whether JSON output is requested.
func startBrowser(bf *browserFlags) (*workflow.Executor, bool) {
	useJSON := bf.json || !term.IsTerminal(int(os.Stdout.Fd()))
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

	exec := workflow.NewExecutor(nil, opts...)
	if err := exec.Start(); err != nil {
		outputError(useJSON, exitBrowserError, err.Error())
	}
	return exec, useJSON
}

// ---------------------------------------------------------------------------
// brz run
// ---------------------------------------------------------------------------

func cmdRun(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	bf := addBrowserFlags(fs)
	var envs envFlag
	fs.Var(&envs, "env", "Set workflow env var (repeatable): --env KEY=VAL")
	dryRun := fs.Bool("dry-run", false, "Show resolved steps without executing (no browser needed)")

	fs.Usage = func() { printRunUsage() }
	fs.Parse(args)

	if fs.NArg() < 2 {
		printRunUsage()
		os.Exit(exitWorkflowError)
	}

	workflowPath := fs.Arg(0)
	actionArg := fs.Arg(1)
	names := workflow.SplitActionNames(actionArg)
	useJSON := bf.json || !term.IsTerminal(int(os.Stdout.Fd()))

	// Load workflow
	w, err := workflow.Load(workflowPath)
	if err != nil {
		outputError(useJSON, exitWorkflowError, err.Error())
		return
	}

	// --dry-run: resolve steps and print without launching a browser
	if *dryRun {
		// Build merged env: workflow defaults + --env overrides
		mergedEnv := make(map[string]string)
		for k, v := range w.Env {
			mergedEnv[k] = v
		}
		for _, kv := range envs {
			parts := strings.SplitN(kv, "=", 2)
			if len(parts) == 2 {
				mergedEnv[parts[0]] = parts[1]
			}
		}

		type dryRunOutput struct {
			Action string                  `json:"action"`
			URL    string                  `json:"url,omitempty"`
			Steps  []workflow.ResolvedStep `json:"steps"`
		}

		var output []dryRunOutput
		for _, name := range names {
			action, ok := w.Actions[name]
			if !ok {
				outputError(useJSON, exitWorkflowError, fmt.Sprintf("action %q not found", name))
				return
			}
			resolved := workflow.ResolveSteps(action, mergedEnv)
			resolvedURL := workflow.InterpolateEnv(action.URL, mergedEnv)
			output = append(output, dryRunOutput{Action: name, URL: resolvedURL, Steps: resolved})
		}

		if useJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetEscapeHTML(false)
			if len(output) == 1 {
				enc.Encode(output[0])
			} else {
				enc.Encode(output)
			}
		} else {
			for _, o := range output {
				fmt.Printf("Action: %s\n", o.Action)
				if o.URL != "" {
					fmt.Printf("  URL: %s\n", o.URL)
				}
				for i, s := range o.Steps {
					fmt.Printf("  Step %d: %s", i+1, s.Type)
					if s.Selector != "" {
						fmt.Printf("  %s", s.Selector)
					}
					if s.Value != "" {
						fmt.Printf("  value=%s", s.Value)
					}
					fmt.Println()
				}
			}
		}
		return
	}

	// Load env config
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

	exec := workflow.NewExecutor(w, opts...)

	// Inject --env flags into workflow env
	for _, kv := range envs {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 2 {
			exec.SetEnv(parts[0], parts[1])
		}
	}

	if err := exec.Start(); err != nil {
		outputError(useJSON, exitBrowserError, err.Error())
		return
	}
	defer exec.Close()

	var lastResult *workflow.ActionResult
	for _, name := range names {
		result := exec.RunAction(name)
		if len(names) > 1 && result.OK {
			if useJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetEscapeHTML(false)
				enc.Encode(result)
			} else {
				fmt.Printf("OK  %s  %d steps  %dms\n", result.Action, result.Steps, result.DurationMs)
			}
		}
		lastResult = result
		if !result.OK {
			break // fail-fast
		}
	}

	// Output final result (or the only result for single-action)
	if len(names) == 1 {
		if useJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetEscapeHTML(false)
			enc.Encode(lastResult)
		} else {
			printHumanResult(lastResult)
		}
	} else if !lastResult.OK {
		// Multi-action: print the failing result
		if useJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetEscapeHTML(false)
			enc.Encode(lastResult)
		} else {
			printHumanResult(lastResult)
		}
	}

	if !lastResult.OK {
		exec.WaitOnFailure()
		os.Exit(exitActionFailed)
	}
}

// ---------------------------------------------------------------------------
// brz inspect
// ---------------------------------------------------------------------------

func cmdInspect(args []string) {
	fs := flag.NewFlagSet("inspect", flag.ExitOnError)
	bf := addBrowserFlags(fs)
	full := fs.Bool("full", false, "Return all visible elements (default: actionable only)")
	tagFilter := fs.String("tag", "", "Filter by tag name (comma-separated, e.g. input,button)")
	nameFilter := fs.String("name", "", "Filter by name attribute (comma-separated, e.g. email,password)")
	compact := fs.Bool("compact", false, "Compact output: only selector, tag, type, name, text")
	screenshotFlag := fs.Bool("screenshot", false, "Also capture a screenshot")
	evalFlag := fs.String("eval", "", "Also evaluate a JS expression")
	fs.Usage = func() { printInspectUsage() }
	fs.Parse(args)

	if fs.NArg() < 1 {
		printInspectUsage()
		os.Exit(exitWorkflowError)
	}

	url := fs.Arg(0)
	exec, useJSON := startBrowser(bf)
	defer exec.Close()

	start := time.Now()
	if err := exec.NavigateTo(url); err != nil {
		outputError(useJSON, exitActionFailed, err.Error())
		return
	}

	// Set full mode flag for the inspect JS
	if *full {
		exec.Page().MustEval(`() => { window.__brz_full = true; }`)
	}

	res, err := exec.Page().Eval(workflow.InspectJS)
	if err != nil {
		outputError(useJSON, exitActionFailed, fmt.Sprintf("inspect eval: %v", err))
		return
	}

	// Parse the JS result into our struct
	var result workflow.InspectResult
	if err := json.Unmarshal([]byte(res.Value.JSON("", "")), &result); err != nil {
		outputError(useJSON, exitActionFailed, fmt.Sprintf("parse inspect result: %v", err))
		return
	}
	result.OK = true
	result.DurationMs = time.Since(start).Milliseconds()

	// Apply filters
	if *tagFilter != "" {
		tags := strings.Split(*tagFilter, ",")
		result.Elements = workflow.FilterByTag(result.Elements, tags)
		result.Total = len(result.Elements)
	}
	if *nameFilter != "" {
		names := strings.Split(*nameFilter, ",")
		result.Elements = workflow.FilterByName(result.Elements, names)
		result.Total = len(result.Elements)
	}

	// Compact mode
	if *compact {
		result.Elements = workflow.CompactElements(result.Elements)
	}

	// Screenshot combo
	if *screenshotFlag {
		sdata, serr := exec.Page().Screenshot(true, nil)
		if serr != nil {
			result.Error = fmt.Sprintf("screenshot: %v", serr)
		} else {
			f, ferr := os.CreateTemp("", "brz-inspect-screenshot-*.png")
			if ferr == nil {
				f.Write(sdata)
				f.Close()
				result.Screenshot = f.Name()
			}
		}
	}

	// Eval combo
	if *evalFlag != "" {
		js := fmt.Sprintf(`() => { return %s; }`, *evalFlag)
		evalRes, evalErr := exec.Page().Eval(js)
		if evalErr == nil {
			result.EvalResult = evalRes.Value.Val()
		}
	}

	if useJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		enc.Encode(result)
	} else {
		fmt.Printf("OK  %s  %d elements  %dms\n", result.URL, result.Total, result.DurationMs)
		for _, el := range result.Elements {
			line := fmt.Sprintf("  %-10s %-40s", el.Tag, el.Selector)
			if el.Name != "" {
				line += fmt.Sprintf("  name=%s", el.Name)
			}
			if el.Type != "" {
				line += fmt.Sprintf("  type=%s", el.Type)
			}
			if el.Text != "" {
				line += fmt.Sprintf("  %q", el.Text)
			}
			if el.Hidden {
				line += "  [hidden]"
			}
			fmt.Println(line)
		}
		if result.Screenshot != "" {
			fmt.Printf("  screenshot: %s\n", result.Screenshot)
		}
		if result.EvalResult != nil {
			fmt.Printf("  eval: %v\n", result.EvalResult)
		}
	}
}

// ---------------------------------------------------------------------------
// brz screenshot
// ---------------------------------------------------------------------------

func cmdScreenshot(args []string) {
	fs := flag.NewFlagSet("screenshot", flag.ExitOnError)
	bf := addBrowserFlags(fs)
	output := fs.String("output", "", "Output file path (default: temp file)")
	fs.Usage = func() { printScreenshotUsage() }
	fs.Parse(args)

	if fs.NArg() < 1 {
		printScreenshotUsage()
		os.Exit(exitWorkflowError)
	}

	url := fs.Arg(0)
	exec, useJSON := startBrowser(bf)
	defer exec.Close()

	start := time.Now()
	if err := exec.NavigateTo(url); err != nil {
		outputError(useJSON, exitActionFailed, err.Error())
		return
	}

	data, err := exec.Page().Screenshot(true, nil)
	if err != nil {
		outputError(useJSON, exitActionFailed, fmt.Sprintf("screenshot: %v", err))
		return
	}

	// Determine output path
	filePath := *output
	if filePath == "" {
		f, err := os.CreateTemp("", "brz-screenshot-*.png")
		if err != nil {
			outputError(useJSON, exitActionFailed, fmt.Sprintf("create temp file: %v", err))
			return
		}
		filePath = f.Name()
		f.Close()
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		outputError(useJSON, exitActionFailed, fmt.Sprintf("write screenshot: %v", err))
		return
	}

	duration := time.Since(start).Milliseconds()

	if useJSON {
		out := map[string]interface{}{
			"ok":          true,
			"url":         url,
			"file":        filePath,
			"size":        len(data),
			"duration_ms": duration,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		enc.Encode(out)
	} else {
		fmt.Printf("OK  %s  %s (%s)  %dms\n", url, filePath, humanSize(int64(len(data))), duration)
	}
}

// ---------------------------------------------------------------------------
// brz eval
// ---------------------------------------------------------------------------

func cmdEval(args []string) {
	fs := flag.NewFlagSet("eval", flag.ExitOnError)
	bf := addBrowserFlags(fs)
	fs.Usage = func() { printEvalUsage() }
	fs.Parse(args)

	if fs.NArg() < 2 {
		printEvalUsage()
		os.Exit(exitWorkflowError)
	}

	url := fs.Arg(0)
	jsExpr := fs.Arg(1)
	exec, useJSON := startBrowser(bf)
	defer exec.Close()

	start := time.Now()
	if err := exec.NavigateTo(url); err != nil {
		outputError(useJSON, exitActionFailed, err.Error())
		return
	}

	// Wrap expression so it returns a value
	js := fmt.Sprintf(`() => { return %s; }`, jsExpr)
	res, err := exec.Page().Eval(js)
	if err != nil {
		outputError(useJSON, exitActionFailed, fmt.Sprintf("eval: %v", err))
		return
	}

	duration := time.Since(start).Milliseconds()

	if useJSON {
		out := map[string]interface{}{
			"ok":          true,
			"url":         url,
			"result":      res.Value.Val(),
			"duration_ms": duration,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		enc.Encode(out)
	} else {
		fmt.Printf("OK  %s  %dms\n  %v\n", url, duration, res.Value.Val())
	}
}

// ---------------------------------------------------------------------------
// brz validate
// ---------------------------------------------------------------------------

func cmdValidate(args []string) {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "Force JSON output")
	fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: brz validate <workflow.yaml>")
		os.Exit(exitWorkflowError)
	}

	useJSON := *jsonOut || !term.IsTerminal(int(os.Stdout.Fd()))

	w, err := workflow.Load(fs.Arg(0))
	if err != nil {
		outputError(useJSON, exitWorkflowError, err.Error())
		return
	}

	totalSteps := 0
	for _, a := range w.Actions {
		totalSteps += len(a.Steps)
	}

	if useJSON {
		out := map[string]interface{}{
			"valid":   true,
			"name":    w.Name,
			"actions": len(w.Actions),
			"steps":   totalSteps,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		enc.Encode(out)
	} else {
		fmt.Printf("OK  %s  %d actions  %d steps\n", w.Name, len(w.Actions), totalSteps)
	}
}

// ---------------------------------------------------------------------------
// brz actions
// ---------------------------------------------------------------------------

func cmdActions(args []string) {
	fs := flag.NewFlagSet("actions", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "Force JSON output")
	fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: brz actions <workflow.yaml>")
		os.Exit(exitWorkflowError)
	}

	useJSON := *jsonOut || !term.IsTerminal(int(os.Stdout.Fd()))

	w, err := workflow.Load(fs.Arg(0))
	if err != nil {
		outputError(useJSON, exitWorkflowError, err.Error())
		return
	}

	// Collect and sort action names for deterministic output
	names := make([]string, 0, len(w.Actions))
	for name := range w.Actions {
		names = append(names, name)
	}
	sort.Strings(names)

	if useJSON {
		type actionInfo struct {
			Name  string `json:"name"`
			URL   string `json:"url,omitempty"`
			Steps int    `json:"steps"`
		}
		var actions []actionInfo
		for _, name := range names {
			a := w.Actions[name]
			actions = append(actions, actionInfo{Name: name, URL: a.URL, Steps: len(a.Steps)})
		}
		out := map[string]interface{}{
			"workflow": w.Name,
			"actions":  actions,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		enc.Encode(out)
	} else {
		fmt.Printf("%s\n\n", w.Name)
		for _, name := range names {
			a := w.Actions[name]
			if a.URL != "" {
				fmt.Printf("  %-20s  %d steps  %s\n", name, len(a.Steps), a.URL)
			} else {
				fmt.Printf("  %-20s  %d steps\n", name, len(a.Steps))
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Output helpers
// ---------------------------------------------------------------------------

func outputError(useJSON bool, code int, msg string) {
	if useJSON {
		out := map[string]interface{}{
			"ok":    false,
			"error": msg,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		enc.Encode(out)
	} else {
		fmt.Fprintf(os.Stderr, "ERROR: %s\n", msg)
	}
	os.Exit(code)
}

func printHumanResult(r *workflow.ActionResult) {
	if r.OK {
		line := fmt.Sprintf("OK  %s  %d steps  %dms", r.Action, r.Steps, r.DurationMs)
		if r.Download != "" {
			line += fmt.Sprintf("  %s", r.Download)
			if r.DownloadSize > 0 {
				line += fmt.Sprintf(" (%s)", humanSize(r.DownloadSize))
			}
		}
		fmt.Println(line)
	} else {
		line := fmt.Sprintf("FAIL  %s", r.Action)
		if r.FailedStep > 0 {
			line += fmt.Sprintf("  step %d (%s)", r.FailedStep, r.StepType)
		}
		fmt.Fprintln(os.Stderr, line)
		fmt.Fprintf(os.Stderr, "  %s\n", r.Error)
		if r.Screenshot != "" {
			fmt.Fprintf(os.Stderr, "  screenshot: %s\n", r.Screenshot)
		}
	}
}

func humanSize(b int64) string {
	switch {
	case b >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1fKB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%dB", b)
	}
}

// ---------------------------------------------------------------------------
// Usage / help text
// ---------------------------------------------------------------------------

func printUsage() {
	fmt.Print(`Brainstorm (brz) — browser automation for humans and machines

Usage: brz <command> [flags] [args]

Commands:
  run        Execute a single workflow action and return structured results
  inspect    List interactive elements on a page (inputs, buttons, links, forms)
  screenshot Save a page screenshot and return the file path
  eval       Execute JavaScript on a page and return the result
  validate   Parse a workflow file and report errors or summary stats
  actions    List all actions defined in a workflow with step counts
  prompt     Print the LLM agent prompt (teaches an AI how to use brz)
  version    Print the brz version string
  help       Show this help text

Run "brz <command> --help" for detailed command usage, flags, and output schemas.

Quick start:
  brz inspect https://example.com/login          Discover page elements
  brz screenshot https://example.com              Capture a page screenshot
  brz eval https://example.com "document.title"   Run JS on a page
  brz validate my-workflow.yaml                   Check workflow syntax
  brz run my-workflow.yaml login --headed          Execute a workflow action

Output modes:
  Interactive terminal (TTY):  human-readable one-line summaries
  Piped or --json flag:        single-line JSON objects (for agents, scripts, jq)

Exit codes:
  0  Success — action completed, result is in stdout
  1  Action step failed — a browser step timed out, element not found, etc.
  2  Workflow error — invalid YAML, missing action name, bad file path
  3  Browser error — Chrome not found, failed to launch, connection refused

Environment variables:
  BRZ_HEADED=1       Show browser window (equivalent to --headed flag)
  BRZ_DEBUG=1        Enable verbose logging + failure screenshots (equivalent to --debug)
  BRZ_PROFILE_DIR    Chrome profile directory (default: ~/.config/brz/chrome-profile)

Config files (loaded in order, earlier values take precedence):
  ~/.config/brz/agent.env    Stable per-user config
  .env                       Local directory overrides

Session persistence:
  By default, brz reuses a Chrome profile at ~/.config/brz/chrome-profile/.
  Cookies and login sessions survive between invocations. Use --ephemeral
  to get a clean session, or --profile DIR to use a custom location.

Workflow env vars:
  YAML values can reference ${VAR_NAME}. Resolution order:
  1. Workflow-level "env:" map in the YAML file
  2. --env KEY=VAL flags passed on the command line
  3. OS environment variables (from .env files or shell)

Workflow YAML structure:
  name: my-workflow
  env:                              # optional default env vars
    BASE_URL: https://example.com
  actions:
    action_name:
      url: ${BASE_URL}/page         # navigate here first (optional)
      steps:
        - fill: { selector: 'input[name="q"]', value: '${QUERY}' }
        - click: { selector: '#submit' }
        - download: { timeout: '60s' }

Available step types:
  navigate     Go to a URL
  click        Click element (supports: selector, text, nth, timeout)
  fill         Type into input (supports: selector, value, clear)
  select       Set dropdown value (supports: selector, value, text, timeout)
  upload       Set file on <input type="file"> (source: path or "result")
  download     Wait for file download (supports: timeout)
  wait_visible Wait for element to appear (supports: selector, timeout)
  wait_text    Wait for text on page (supports: text, timeout)
  wait_url     Wait for URL to match pattern (supports: match, timeout)
  screenshot   Save a screenshot to a file
  sleep        Pause execution (supports: duration, e.g. "5s")
  eval         Execute JavaScript in the page context
`)
}

func printRunUsage() {
	fmt.Print(`Brainstorm (brz) run — execute a single workflow action

Usage: brz run <workflow.yaml> <action> [flags]

Arguments:
  workflow.yaml   Path to a YAML workflow file
  action          Name of the action to execute (use "brz actions" to list them)

Flags:
  --json          Force JSON output (auto-enabled when stdout is piped)
  --headed        Show the browser window (needed for CAPTCHAs, useful for debugging)
  --debug         Log each step as it executes + save screenshots on failure
  --profile DIR   Chrome profile directory for session/cookie persistence
  --ephemeral     Use a fresh temp profile (no cookies, no session reuse)
  --env KEY=VAL   Set a workflow env var (repeatable, e.g. --env USER=x --env PASS=y)

JSON output schema (success):
  {
    "ok": true,             // always true on success
    "action": "export",     // action name that was executed
    "steps": 3,             // number of steps completed
    "duration_ms": 2100,    // wall-clock time in milliseconds
    "download": "/path",    // file path if a download step ran (omitted otherwise)
    "download_size": 51200  // file size in bytes (omitted if no download)
  }

JSON output schema (failure):
  {
    "ok": false,                  // always false on failure
    "action": "login",            // action that failed
    "steps": 1,                   // steps completed before failure
    "duration_ms": 5030,          // time elapsed before failure
    "error": "find element ...",  // human-readable error message
    "failed_step": 2,             // 1-indexed step number that failed
    "step_type": "click",         // type of the failed step
    "screenshot": "/tmp/...png"   // debug screenshot path (if --debug or BRZ_DEBUG=1)
  }

LLM agent usage patterns:

  # 1. Run an action and check success
  brz run site.yaml login --env EMAIL=me@co.com --env PASSWORD=secret
  # exit code 0 = success, 1 = step failed

  # 2. Get the downloaded file path
  brz run site.yaml export | jq -r .download

  # 3. Chain actions (login then export)
  brz run site.yaml login --env EMAIL=$E --env PASSWORD=$P && \
  brz run site.yaml export | jq -r .download

  # 4. Conditional retry on failure
  result=$(brz run site.yaml export)
  if echo "$result" | jq -e '.ok' > /dev/null; then
    file=$(echo "$result" | jq -r '.download')
  fi

  # 5. Discover available actions first
  brz actions site.yaml | jq -r '.actions[].name'
`)
}

func printInspectUsage() {
	fmt.Print(`Brainstorm (brz) inspect — discover interactive elements on a page

Usage: brz inspect <url> [flags]

Arguments:
  url   The page URL to inspect

Flags:
  --full        Return all visible elements (default: actionable elements only)
  --json        Force JSON output (auto-enabled when stdout is piped)
  --headed      Show the browser window
  --debug       Verbose logging
  --profile DIR Chrome profile directory
  --ephemeral   Use a fresh temp profile

Default mode returns only actionable elements:
  inputs, textareas, selects, buttons, links, [role="button"],
  file inputs, contenteditable elements. Hidden form elements are
  included (they reveal CSRF tokens, hidden IDs, form structure).

--full mode returns all visible elements, capped at 500.

JSON output schema:
  {
    "ok": true,
    "url": "https://example.com/login",
    "title": "Login",
    "total": 5,
    "elements": [
      {"selector": "input#email", "tag": "input", "type": "email", "name": "email", "placeholder": "Email"},
      {"selector": "input#password", "tag": "input", "type": "password", "name": "password"},
      {"selector": "input[type=\"hidden\"]", "tag": "input", "type": "hidden", "name": "csrf_token", "value": "abc123", "hidden": true},
      {"selector": "button.submit", "tag": "button", "text": "Sign In"},
      {"selector": "a.forgot", "tag": "a", "text": "Forgot password?", "href": "/reset"}
    ],
    "duration_ms": 1200
  }

Element fields:
  selector      CSS selector (usable directly in workflow YAML click/fill steps)
  tag           HTML tag name
  type          Input type (text, password, email, hidden, file, submit, etc.)
  name          Form field name attribute
  placeholder   Placeholder text
  text          Visible text content (buttons, links — truncated to 80 chars)
  href          Link URL
  value         Value (only for hidden inputs — reveals CSRF tokens, IDs)
  role          ARIA role
  hidden        true if element is not visible (display:none, zero-size)

LLM agent workflow:
  # 1. Discover what's on a login page
  brz inspect https://app.example.com/login | jq '.elements[] | {selector, tag, name, type}'

  # 2. Use the selectors to write a workflow YAML
  # 3. Validate and run it
  brz validate login-workflow.yaml
  brz run login-workflow.yaml login --env EMAIL=me@co.com
`)
}

func printScreenshotUsage() {
	fmt.Print(`Brainstorm (brz) screenshot — capture a page screenshot

Usage: brz screenshot <url> [flags]

Arguments:
  url   The page URL to screenshot

Flags:
  --output FILE   Save to a specific path (default: temp file)
  --json          Force JSON output (auto-enabled when stdout is piped)
  --headed        Show the browser window
  --profile DIR   Chrome profile directory
  --ephemeral     Use a fresh temp profile

JSON output schema:
  {
    "ok": true,
    "url": "https://example.com",
    "file": "/tmp/brz-screenshot-123456.png",
    "size": 234567,
    "duration_ms": 1200
  }

Examples:
  brz screenshot https://example.com
  brz screenshot https://example.com --output page.png
  brz screenshot https://example.com | jq -r .file
`)
}

func printEvalUsage() {
	fmt.Print(`Brainstorm (brz) eval — execute JavaScript on a page

Usage: brz eval <url> <js-expression> [flags]

Arguments:
  url             The page URL to navigate to
  js-expression   JavaScript expression to evaluate (result is returned)

Flags:
  --json          Force JSON output (auto-enabled when stdout is piped)
  --headed        Show the browser window
  --profile DIR   Chrome profile directory
  --ephemeral     Use a fresh temp profile

The expression is wrapped in a function and its return value is captured.
If the expression returns a Promise, it is automatically awaited.

JSON output schema:
  {
    "ok": true,
    "url": "https://example.com",
    "result": "Example Domain",
    "duration_ms": 800
  }

Examples:
  brz eval https://example.com "document.title"
  brz eval https://example.com "document.querySelectorAll('a').length"
  brz eval https://example.com "document.querySelector('#price').textContent"
  brz eval https://app.com/api "await fetch('/api/status').then(r => r.json())"
`)
}
