package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"regexp"

	"github.com/crackfetch/brainstorm/internal/config"
	"github.com/crackfetch/brainstorm/workflow"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"golang.org/x/term"
)

// saveNameRE restricts :save names to a YAML-safe identifier shape.
var saveNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

// ---------------------------------------------------------------------------
// brz probe — interactive selector REPL
// ---------------------------------------------------------------------------
//
// Launches a browser (HEADED by default — a probe with no window is useless),
// navigates to the URL, and drops the user into a stdin REPL. Each line is
// either:
//
//   - a CSS selector (default)        e.g. `.product-card`
//   - an XPath selector               e.g. `xpath://button[@id="go"]`
//   - a reserved command beginning with `:`
//
// Reserved commands:
//
//   :q | :quit | :exit          quit the REPL
//   :reload                     reload the current page
//   :url <url>                  navigate to a new URL
//   :eval <js>                  run a JS expression and print result
//   :mode css | xpath           switch default selector engine
//   :save <name>                append last selector to ./selectors.yaml
//   :help                       print help
//
// Highlighting: matched elements get an `outline: 3px solid red` via a single
// injected <style> tag. Highlights from the previous query are cleared on each
// new query.
//
// REPL is resilient: a malformed selector is caught and reported, never panics.

func cmdProbe(args []string) {
	fs := flag.NewFlagSet("probe", flag.ExitOnError)
	bf := addBrowserFlags(fs)
	headless := fs.Bool("headless", false, "Run without a window (no highlight; useful for CI checks)")
	xpathMode := fs.Bool("xpath", false, "Default selector engine is XPath instead of CSS")
	fs.Usage = func() { printProbeUsage() }
	fs.Parse(args)

	if fs.NArg() < 1 {
		printProbeUsage()
		os.Exit(exitWorkflowError)
	}
	url := fs.Arg(0)

	// probe is headed-by-default; --headless overrides.
	// Build the browser directly so --headless reliably wins over
	// BRZ_HEADED=1 / config.Headed = "1" in the user's env.
	useHeaded := !*headless
	cfg := config.Load()
	opts := []workflow.Option{
		workflow.WithHeaded(useHeaded),
		workflow.WithDebug(bf.debug || cfg.Debug),
	}
	if bf.ephemeral {
		dir, err := os.MkdirTemp("", "brz-ephemeral-*")
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: tempdir: %v\n", err)
			os.Exit(exitBrowserError)
		}
		defer os.RemoveAll(dir)
		opts = append(opts, workflow.WithProfileDir(dir))
	} else if bf.profile != "" {
		opts = append(opts, workflow.WithProfileDir(bf.profile))
	} else {
		opts = append(opts, workflow.WithProfileDir(cfg.ProfileDir))
	}
	exec := workflow.NewExecutor(nil, opts...)
	if err := exec.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %s\n", err.Error())
		os.Exit(exitBrowserError)
	}
	defer exec.Close()

	if err := exec.NavigateTo(url); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: navigate: %v\n", err)
		os.Exit(exitActionFailed)
	}

	mode := "css"
	if *xpathMode {
		mode = "xpath"
	}

	rp := &repl{
		exec:        exec,
		mode:        mode,
		highlight:   !*headless,
		out:         os.Stdout,
		err:         os.Stderr,
		isTTY:       term.IsTerminal(int(os.Stdout.Fd())),
		lastSelType: "",
		lastSel:     "",
	}
	rp.printBanner(url)
	rp.run(os.Stdin)
}

// ---------------------------------------------------------------------------
// REPL
// ---------------------------------------------------------------------------

type repl struct {
	exec      *workflow.Executor
	mode      string // "css" | "xpath"
	highlight bool
	out       io.Writer
	err       io.Writer
	isTTY     bool

	lastSelType string // "css" | "xpath"
	lastSel     string
}

// run reads lines from r and dispatches them. Returns on EOF or :q.
func (rp *repl) run(r io.Reader) {
	sc := bufio.NewScanner(r)
	// Allow long pasted lines.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for {
		if rp.isTTY {
			fmt.Fprint(rp.out, "> ")
		}
		if !sc.Scan() {
			if err := sc.Err(); err != nil {
				fmt.Fprintf(rp.err, "input: %v\n", err)
			}
			return
		}
		line := strings.TrimRight(sc.Text(), "\r\n")
		stripped := strings.TrimSpace(line)
		if stripped == "" {
			continue
		}
		if cont := rp.dispatch(stripped); !cont {
			return
		}
	}
}

// dispatch handles a single REPL input line. Returns false to terminate.
func (rp *repl) dispatch(line string) bool {
	if strings.HasPrefix(line, ":") {
		return rp.runCommand(line)
	}
	rp.runSelector(line)
	return true
}

// runCommand handles ":..." reserved commands.
func (rp *repl) runCommand(line string) bool {
	// Split into name + rest (preserving rest verbatim for :eval, :url).
	name := line
	rest := ""
	if i := strings.IndexAny(line, " \t"); i > 0 {
		name = line[:i]
		rest = strings.TrimSpace(line[i+1:])
	}

	switch name {
	case ":q", ":quit", ":exit":
		return false
	case ":help", ":h", ":?":
		rp.printHelp()
	case ":reload":
		page := rp.exec.Page()
		// Register the navigation wait BEFORE triggering the reload so we
		// don't race the new doc loading.
		wait := page.WaitNavigation(proto.PageLifecycleEventNameLoad)
		if err := page.Reload(); err != nil {
			fmt.Fprintf(rp.err, "reload: %v\n", err)
			return true
		}
		wait()
		fmt.Fprintln(rp.out, "[reloaded]")
	case ":url":
		if rest == "" {
			fmt.Fprintln(rp.err, ":url <url>")
			return true
		}
		if err := rp.exec.NavigateTo(rest); err != nil {
			fmt.Fprintf(rp.err, "navigate: %v\n", err)
			return true
		}
		fmt.Fprintf(rp.out, "[navigated to %s]\n", rest)
	case ":eval":
		if rest == "" {
			fmt.Fprintln(rp.err, ":eval <js-expression>")
			return true
		}
		// Wrap in `Boolean()`-safe form: just return whatever the expr produces.
		js := fmt.Sprintf(`() => { return %s; }`, rest)
		page := rp.exec.Page()
		res, err := page.Eval(js)
		if err != nil {
			fmt.Fprintf(rp.err, "eval: %v\n", err)
			return true
		}
		fmt.Fprintf(rp.out, "%v\n", res.Value.Val())
	case ":mode":
		switch rest {
		case "css", "xpath":
			rp.mode = rest
			fmt.Fprintf(rp.out, "[default mode: %s]\n", rp.mode)
		default:
			fmt.Fprintln(rp.err, ":mode css | xpath")
		}
	case ":save":
		if rest == "" {
			fmt.Fprintln(rp.err, ":save <name>")
			return true
		}
		if !saveNameRE.MatchString(rest) {
			fmt.Fprintln(rp.err, "invalid name: must match [A-Za-z_][A-Za-z0-9_-]*")
			return true
		}
		if rp.lastSel == "" {
			fmt.Fprintln(rp.err, "no last selector to save")
			return true
		}
		if err := saveSelector("selectors.yaml", rest, rp.lastSelType, rp.lastSel); err != nil {
			fmt.Fprintf(rp.err, "save: %v\n", err)
			return true
		}
		fmt.Fprintf(rp.out, "[saved %s -> selectors.yaml]\n", rest)
	default:
		fmt.Fprintf(rp.err, "unknown command: %s (try :help)\n", name)
	}
	return true
}

// runSelector evaluates a single selector and prints the report.
func (rp *repl) runSelector(line string) {
	// Parse: explicit prefix wins, otherwise default mode.
	selType, sel := parseSelector(line, rp.mode)

	// Quick syntax pre-check: empty selector.
	if sel == "" {
		fmt.Fprintln(rp.err, "invalid selector: empty")
		return
	}

	page := rp.exec.Page()

	// Clear any previous highlight before the new query.
	if rp.highlight {
		clearHighlight(page)
	}

	// Catch malformed selector errors from rod / CDP.
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(rp.err, "invalid selector: %v\n", r)
		}
	}()

	var els rod.Elements
	var err error
	switch selType {
	case "xpath":
		els, err = page.ElementsX(sel)
	default:
		els, err = page.Elements(sel)
	}
	if err != nil {
		fmt.Fprintf(rp.err, "invalid selector: %v\n", err)
		return
	}

	rp.lastSel = sel
	rp.lastSelType = selType

	out := formatMatches(els, 5)
	fmt.Fprint(rp.out, out)

	if rp.highlight && len(els) > 0 {
		applyHighlight(page, els)
		fmt.Fprintln(rp.out, "[highlighted in browser]")
	}
}

// parseSelector splits a user input line into (type, selector).
// `xpath:...` and `css:...` prefixes are explicit; otherwise default applies.
func parseSelector(line, defaultMode string) (string, string) {
	low := strings.ToLower(line)
	switch {
	case strings.HasPrefix(low, "xpath:"):
		return "xpath", strings.TrimSpace(line[len("xpath:"):])
	case strings.HasPrefix(low, "css:"):
		return "css", strings.TrimSpace(line[len("css:"):])
	}
	return defaultMode, line
}

// formatMatches returns a human-readable preview of element matches.
// Each match shows its tag, key attrs (id, class, name, type), and a short
// trimmed text. Caps preview at maxPreview entries (rest summarised as "...").
func formatMatches(els rod.Elements, maxPreview int) string {
	var b strings.Builder
	if len(els) == 0 {
		b.WriteString("0 matches\n")
		return b.String()
	}

	fmt.Fprintf(&b, "%d match", len(els))
	if len(els) != 1 {
		b.WriteString("es")
	}
	b.WriteString("\n")

	limit := len(els)
	if limit > maxPreview {
		limit = maxPreview
	}
	for i := 0; i < limit; i++ {
		el := els[i]
		desc := describeElement(el)
		fmt.Fprintf(&b, "[%d] %s\n", i+1, desc)
	}
	if len(els) > limit {
		fmt.Fprintf(&b, "... %d more\n", len(els)-limit)
	}
	return b.String()
}

// describeElement returns "<tag class=...> \"text...\"" for one element.
// Soft-fails on any rod error: returns a partial description.
func describeElement(el *rod.Element) string {
	tag := safeEval(el, `() => this.tagName ? this.tagName.toLowerCase() : ""`)
	if tag == "" {
		tag = "?"
	}

	var attrs []string
	for _, name := range []string{"id", "class", "name", "type", "href"} {
		v := safeEval(el, fmt.Sprintf(`() => this.getAttribute(%q) || ""`, name))
		if v == "" {
			continue
		}
		// Truncate excessive class lists.
		if len(v) > 60 {
			v = v[:57] + "..."
		}
		attrs = append(attrs, fmt.Sprintf("%s=%q", name, v))
	}

	openTag := fmt.Sprintf("<%s", tag)
	if len(attrs) > 0 {
		openTag += " " + strings.Join(attrs, " ")
	}
	openTag += ">"

	text := safeEval(el, `() => (this.innerText || this.textContent || "").trim()`)
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.ReplaceAll(text, "\r", " ")
	for strings.Contains(text, "  ") {
		text = strings.ReplaceAll(text, "  ", " ")
	}
	if len(text) > 80 {
		text = text[:77] + "..."
	}

	if text == "" {
		return openTag
	}
	return fmt.Sprintf("%s %q", openTag, text)
}

// safeEval evaluates a JS expression on an element and returns the string
// result, or empty string on any error. Never panics.
func safeEval(el *rod.Element, js string) string {
	defer func() { _ = recover() }()
	res, err := el.Eval(js)
	if err != nil || res == nil {
		return ""
	}
	return res.Value.String()
}

// ---------------------------------------------------------------------------
// Highlight injection
// ---------------------------------------------------------------------------

const probeStyleID = "__brz_probe_style__"
const probeAttr = "data-brz-probe"

// clearHighlight removes the brz probe style + attributes from the page.
func clearHighlight(page *rod.Page) {
	defer func() { _ = recover() }()
	js := fmt.Sprintf(`() => {
		try {
			var s = document.getElementById(%q);
			if (s) s.remove();
			document.querySelectorAll('['+%q+']').forEach(function(e){ e.removeAttribute(%q); });
		} catch (e) { /* page may be transitioning */ }
	}`, probeStyleID, probeAttr, probeAttr)
	_, _ = page.Eval(js)
}

// applyHighlight tags each matched element with a data attribute and injects
// a single <style> rule that outlines them in red. Doing it via a stylesheet
// keeps the page's own style.cssText untouched.
func applyHighlight(page *rod.Page, els rod.Elements) {
	defer func() { _ = recover() }()
	for _, el := range els {
		_, _ = el.Eval(fmt.Sprintf(`() => this.setAttribute(%q, "1")`, probeAttr))
	}
	js := fmt.Sprintf(`() => {
		try {
			var existing = document.getElementById(%q);
			if (existing) existing.remove();
			var s = document.createElement("style");
			s.id = %q;
			s.textContent = '['+%q+'] { outline: 3px solid red !important; outline-offset: 2px !important; }';
			document.head.appendChild(s);
		} catch (e) { /* head may not exist on about:blank */ }
	}`, probeStyleID, probeStyleID, probeAttr)
	_, _ = page.Eval(js)
}

// ---------------------------------------------------------------------------
// :save
// ---------------------------------------------------------------------------

// saveSelector appends a single key/value entry to a YAML file. Hand-written
// (not yaml.Marshal) so we don't rewrite/round-trip user comments. Format:
//
//   name: <selector>          (css)
//   name: "xpath://..."       (xpath)
//
// Creates the file with a `selectors:` header if it does not exist.
func saveSelector(path, name, selType, sel string) error {
	// Quote xpath / anything containing a colon to keep YAML safe.
	value := sel
	if selType == "xpath" {
		value = fmt.Sprintf("xpath:%s", sel)
	}
	if strings.ContainsAny(value, ":#&*!|>'\"%@`") {
		value = fmt.Sprintf("%q", value)
	}

	// Atomically create-with-header if absent. O_EXCL means only one of
	// two racing writers writes the header; the loser falls through to
	// the append path.
	if f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644); err == nil {
		_, werr := f.WriteString("# brz probe — saved selectors\nselectors:\n")
		cerr := f.Close()
		if werr != nil {
			return werr
		}
		if cerr != nil {
			return cerr
		}
	} else if !os.IsExist(err) {
		return err
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "  %s: %s\n", name, value)
	return err
}

// ---------------------------------------------------------------------------
// Banner / help
// ---------------------------------------------------------------------------

func (rp *repl) printBanner(url string) {
	if !rp.isTTY {
		return
	}
	fmt.Fprintf(rp.out, "brz probe — connected to %s (mode: %s)\n", url, rp.mode)
	fmt.Fprintln(rp.out, "Type a CSS selector, `xpath:...`, or `:help`. `:q` to quit.")
}

func (rp *repl) printHelp() {
	fmt.Fprint(rp.out, `Selectors:
  .product-card                   CSS selector (default)
  xpath://button[@id="go"]        XPath selector
  css:div.thing                   explicit CSS

Commands:
  :q | :quit | :exit              quit
  :help                           this help
  :reload                         reload the current page
  :url <url>                      navigate to a new URL
  :eval <js>                      run a JS expression and print result
  :mode css | xpath               change default selector engine
  :save <name>                    append last selector to ./selectors.yaml
`)
}

// printProbeUsage is shown for `brz probe --help` / no-arg.
func printProbeUsage() {
	fmt.Print(`Brainstorm (brz) probe — interactive selector REPL

Usage: brz probe <url> [flags]

Arguments:
  url   The page URL to open

Flags:
  --headless         Run without a window (no highlight). Useful for CI smoke-tests.
  --xpath            Default selector engine is XPath instead of CSS.
  --headed           Show the browser window (default for probe; this is the no-op kept for parity)
  --debug            Verbose logging
  --profile DIR      Chrome profile directory
  --ephemeral        Use a fresh temp profile

Example session:
  $ brz probe https://example.com
  brz probe — connected to https://example.com (mode: css)
  Type a CSS selector, `+"`xpath:...`"+`, or `+"`:help`"+`. `+"`:q`"+` to quit.
  > h1
  1 match
  [1] <h1> "Example Domain"
  [highlighted in browser]
  > .does-not-exist
  0 matches
  > xpath://a
  1 match
  [1] <a href="https://www.iana.org/domains/example"> "More information..."
  [highlighted in browser]
  > :q
`)
}
