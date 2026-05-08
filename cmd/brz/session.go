package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/crackfetch/brainstorm/internal/config"
	"github.com/crackfetch/brainstorm/internal/session"
	"github.com/crackfetch/brainstorm/workflow"
	"golang.org/x/term"
)

// cmdSession dispatches `brz session <subcommand>`.
//
// Subcommands:
//
//	export   Read cookies + storage from a profile, emit a JSON bundle
//	import   Read a JSON bundle from a file or stdin, write it into a profile
//
// Both subcommands use the same profile resolution as `brz run`: an
// explicit --profile flag, the BRZ_PROFILE_DIR env, or the default
// ~/.config/brz/chrome-profile.
func cmdSession(args []string) {
	if len(args) == 0 {
		printSessionUsage()
		os.Exit(exitWorkflowError)
	}
	switch args[0] {
	case "export":
		cmdSessionExport(args[1:])
	case "import":
		cmdSessionImport(args[1:])
	case "help", "--help", "-h":
		printSessionUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown session subcommand: %s\n\n", args[0])
		printSessionUsage()
		os.Exit(exitWorkflowError)
	}
}

// stringSliceFlag collects repeatable string flags (e.g. --for-domain).
type stringSliceFlag []string

func (s *stringSliceFlag) String() string { return strings.Join(*s, ",") }
func (s *stringSliceFlag) Set(v string) error {
	for _, p := range strings.Split(v, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			*s = append(*s, p)
		}
	}
	return nil
}

func cmdSessionExport(args []string) {
	fs := flag.NewFlagSet("session export", flag.ExitOnError)
	profile := fs.String("profile", "", "Chrome profile directory to read from")
	output := fs.String("output", "", "Write to file (default: stdout). Files are created 0600.")
	include := fs.String("include", "cookies,localstorage,sessionstorage", "Comma-separated scopes: cookies,localstorage,sessionstorage,indexeddb")
	var domains stringSliceFlag
	fs.Var(&domains, "for-domain", "Glob filter, repeatable. Example: --for-domain '*.tcgplayer.com'")
	fs.Usage = printSessionExportUsage
	fs.Parse(args)

	cfg := config.Load()
	profileDir := *profile
	if profileDir == "" {
		profileDir = cfg.ProfileDir
	}
	if _, err := os.Stat(profileDir); err != nil {
		fmt.Fprintf(os.Stderr, "session export: profile dir not readable: %v\n", err)
		os.Exit(exitBrowserError)
	}

	// Run a transient headless browser against the requested profile.
	// We do NOT mutate the profile during export — we only call
	// Network.getAllCookies and read window.localStorage on each origin.
	exec := workflow.NewExecutor(nil,
		workflow.WithHeaded(false),
		workflow.WithProfileDir(profileDir),
	)
	if err := exec.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "session export: launch browser: %v\n", err)
		os.Exit(exitBrowserError)
	}
	defer exec.Close()

	// The executor lazily creates pages on workflow steps; we need a
	// page so we can reach the underlying *rod.Browser. about:blank
	// is cheap and won't touch any origin.
	if err := exec.NavigateTo("about:blank"); err != nil {
		fmt.Fprintf(os.Stderr, "session export: open scratch page: %v\n", err)
		os.Exit(exitBrowserError)
	}
	page := exec.Page()
	if page == nil {
		fmt.Fprintln(os.Stderr, "session export: no page available after browser start")
		os.Exit(exitBrowserError)
	}

	bundle, err := session.Export(page.Browser(), session.ExportOptions{
		Include:        splitCSV(*include),
		DomainPatterns: domains,
		BrzVersion:     Version,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "session export: %v\n", err)
		os.Exit(exitActionFailed)
	}

	if *output != "" {
		if err := session.WriteFile(*output, bundle); err != nil {
			fmt.Fprintf(os.Stderr, "session export: write file: %v\n", err)
			os.Exit(exitActionFailed)
		}
		fmt.Fprintf(os.Stderr, "wrote %d cookies, %d localStorage origins to %s (mode 0600)\n",
			len(bundle.Cookies), len(bundle.LocalStorage), *output)
		return
	}

	// Stdout path: warn about secrets if a TTY is attached. The
	// warning goes to stderr so it never contaminates the JSON
	// stream that a caller is piping somewhere.
	if term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Fprintln(os.Stderr, "WARNING: session export contains auth secrets. Do not paste into chat.")
	}
	if err := session.Encode(os.Stdout, bundle); err != nil {
		fmt.Fprintf(os.Stderr, "session export: encode: %v\n", err)
		os.Exit(exitActionFailed)
	}
}

func cmdSessionImport(args []string) {
	fs := flag.NewFlagSet("session import", flag.ExitOnError)
	profile := fs.String("profile", "", "Chrome profile directory to write to")
	input := fs.String("input", "", "Read bundle from file (default: stdin)")
	merge := fs.Bool("merge", true, "Keep existing cookies; overlapping name/domain/path tuples are overwritten (default)")
	replace := fs.Bool("replace", false, "Clear existing cookies first, then apply the bundle")
	fs.Usage = printSessionImportUsage
	fs.Parse(args)

	if *replace {
		// --replace overrides the default --merge.
		*merge = false
	}

	cfg := config.Load()
	profileDir := *profile
	if profileDir == "" {
		profileDir = cfg.ProfileDir
	}
	// Ensure the profile dir exists; rod also does this but we want
	// a clean error message before launching Chrome.
	if err := os.MkdirAll(profileDir, 0700); err != nil {
		fmt.Fprintf(os.Stderr, "session import: cannot create profile dir: %v\n", err)
		os.Exit(exitBrowserError)
	}

	var (
		bundle *session.Bundle
		err    error
	)
	if *input != "" {
		bundle, err = session.ReadFile(*input)
	} else {
		bundle, err = session.Decode(os.Stdin)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "session import: %v\n", err)
		os.Exit(exitWorkflowError)
	}

	exec := workflow.NewExecutor(nil,
		workflow.WithHeaded(false),
		workflow.WithProfileDir(profileDir),
	)
	if err := exec.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "session import: launch browser: %v\n", err)
		os.Exit(exitBrowserError)
	}
	// Closing the executor (which closes the browser) is what causes
	// rod's launcher to flush profile state to disk. We must not
	// short-circuit defer here.
	defer exec.Close()

	// Warm the network context by navigating to one of the bundle's
	// origins (if any) before issuing Storage.setCookies. Chrome's
	// cookie persistence is more reliable when the per-host network
	// context has been initialized at least once.
	warmURL := "about:blank"
	if len(bundle.Domains) > 0 {
		warmURL = "https://" + bundle.Domains[0]
	}
	if err := exec.NavigateTo(warmURL); err != nil {
		// Fall back to about:blank if the warm origin is unreachable.
		_ = exec.NavigateTo("about:blank")
	}
	page := exec.Page()
	if page == nil {
		fmt.Fprintln(os.Stderr, "session import: no page available after browser start")
		os.Exit(exitBrowserError)
	}

	if err := session.Import(page.Browser(), bundle, session.ImportOptions{
		Replace:      *replace,
		PageForFlush: page,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "session import: %v\n", err)
		os.Exit(exitActionFailed)
	}

	// Give Chrome time to flush its cookie store before we close.
	// Chrome's cookie persistence is async: cookies set via CDP land
	// in the in-memory cookie store and only reach disk on the next
	// periodic flush, on shutdown, or when a real navigation triggers
	// a persistence pass. KNOWN LIMITATION: some Chrome builds drop
	// CDP-set cookies entirely on close, so the on-disk Cookies
	// SQLite may be empty even after a successful import. The
	// in-process round-trip (using the same brz invocation that
	// performed the import) is reliable; cross-process persistence
	// is not. See CHANGELOG + PR description for the recommended
	// workaround.
	time.Sleep(500 * time.Millisecond)

	fmt.Fprintf(os.Stderr, "imported %d cookies, %d localStorage origins into %s\n",
		len(bundle.Cookies), len(bundle.LocalStorage), profileDir)
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// --- usage --------------------------------------------------------

func printSessionUsage() {
	io.WriteString(os.Stderr, `Usage: brz session <subcommand> [flags]

Subcommands:
  export   Read cookies + storage from a profile, emit a JSON bundle
  import   Read a JSON bundle, write it into a profile

Run "brz session <subcommand> --help" for details.

Round-trip:
  brz session export --for-domain '*.example.com' > bundle.json
  brz session import --input bundle.json --profile /tmp/fresh

Privacy:
  Bundle files are written 0600 and contain auth secrets.
  Do not paste them into chat or commit them to git.
`)
}

func printSessionExportUsage() {
	io.WriteString(os.Stderr, `Usage: brz session export [flags]

Read cookies + per-origin storage from the configured Chrome profile
and emit a portable JSON bundle.

Flags:
  --profile DIR        Chrome profile directory (default: $BRZ_PROFILE_DIR or ~/.config/brz/chrome-profile)
  --output FILE        Write to FILE (default: stdout). Files are created with mode 0600.
  --include LIST       Comma-separated scopes (default: cookies,localstorage,sessionstorage)
                       Valid: cookies, localstorage, sessionstorage, indexeddb
  --for-domain GLOB    Filter by cookie domain. Repeatable. Example: --for-domain '*.tcgplayer.com'

Examples:
  brz session export > all-state.json
  brz session export --for-domain '*.tcgplayer.com' --output tcg.json
  brz session export --include cookies --for-domain '*.example.com' | ssh server brz session import
`)
}

func printSessionImportUsage() {
	io.WriteString(os.Stderr, `Usage: brz session import [flags]

Read a JSON bundle and write it into a Chrome profile. The browser
is launched headless against the target profile dir, then cookies +
storage are applied via CDP.

KNOWN LIMITATION: cross-process cookie persistence depends on
Chrome flushing its in-memory cookie store to disk on close. Some
Chrome builds drop CDP-set cookies entirely on close, so the
on-disk Cookies SQLite may be empty after import even though the
import call succeeded. The reliable usage today is to perform the
import + the dependent workflow inside a single brz invocation
that keeps the browser alive.

Flags:
  --profile DIR    Target profile directory (default: $BRZ_PROFILE_DIR or ~/.config/brz/chrome-profile)
  --input FILE     Read bundle from FILE (default: stdin)
  --merge          Additive: keep existing cookies; overlapping name/domain/path tuples are overwritten (default)
  --replace        Clear existing cookies first, then apply the bundle

Examples:
  brz session import < bundle.json
  brz session import --input tcg.json --profile /tmp/fresh-profile
  brz session import --replace < bundle.json
`)
}
