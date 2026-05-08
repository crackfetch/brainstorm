package main

// brz status — diagnostic snapshot of brz's local state.
//
// Answers questions like:
//   - is there a browser still running from a previous brz run?
//   - which profile dir is brz currently using?
//   - is there a stale SingletonLock blocking a fresh launch?
//   - did the last run leave failure artifacts in /tmp?
//
// Always exits 0; status is informational. Pipe-friendly via --json.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/crackfetch/brainstorm/internal/config"
	"golang.org/x/term"
)

// isStdoutTTY mirrors the helper used elsewhere in cmd/brz/main.go. Local
// alias so status.go doesn't have to thread state through main.go.
func isStdoutTTY() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// StatusReport is the full payload returned by `brz status --json`.
// Field names use snake_case in JSON so the shape is friendly for jq pipelines.
type StatusReport struct {
	BrzVersion        string                 `json:"brz_version"`
	OS                string                 `json:"os"`
	ProfileDir        ProfileDirInfo         `json:"profile_dir"`
	BrzChromium       []ChromiumProcess      `json:"brz_chromium"`
	Downloads         DownloadsDirInfo       `json:"downloads"`
	FailureArtifacts  FailureArtifactsInfo   `json:"failure_artifacts"`
	ProcessScanError  string                 `json:"process_scan_error,omitempty"`
}

type ProfileDirInfo struct {
	Path              string `json:"path"`
	Exists            bool   `json:"exists"`
	SingletonLockHeld bool   `json:"singleton_lock_held"`
}

type ChromiumProcess struct {
	PID         int    `json:"pid"`
	UserDataDir string `json:"user_data_dir,omitempty"`
	ExePath     string `json:"exe_path,omitempty"`
}

type DownloadsDirInfo struct {
	Path       string    `json:"path"`
	Exists     bool      `json:"exists"`
	FileCount  int       `json:"file_count"`
	TotalBytes int64     `json:"total_bytes"`
	NewestMod  time.Time `json:"newest_modified,omitempty"`
}

type FailureArtifactsInfo struct {
	TmpDir          string    `json:"tmp_dir"`
	FailedPNGCount  int       `json:"failed_screenshots"`
	BeforeJPGCount  int       `json:"before_screenshots"`
	NewestArtifact  time.Time `json:"newest_artifact,omitempty"`
}

func cmdStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	jsonFlag := fs.Bool("json", false, "Force JSON output (auto-enabled when stdout is piped)")
	fs.Usage = func() { printStatusUsage() }
	fs.Parse(args)

	useJSON := *jsonFlag || !isStdoutTTY()

	cfg := config.Load()
	report := buildStatusReport(cfg.ProfileDir)

	if useJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(report)
		return
	}
	printStatusHuman(report)
}

// buildStatusReport collects the full snapshot. Pure function — easy to test.
// Each sub-check tolerates errors silently (returns zero values) because
// status is diagnostic: a missing /tmp shouldn't fail the whole report.
func buildStatusReport(profileDir string) StatusReport {
	r := StatusReport{
		BrzVersion: Version,
		OS:         runtime.GOOS + "/" + runtime.GOARCH,
		ProfileDir: inspectProfileDir(profileDir),
		Downloads:  inspectDownloadsDir(filepath.Join(os.TempDir(), "brz-downloads")),
	}
	r.FailureArtifacts = inspectFailureArtifacts(os.TempDir())

	procs, err := scanBrzChromiumProcesses(profileDir)
	if procs == nil {
		// Marshal as [] not null so jq pipelines can rely on a list shape.
		procs = []ChromiumProcess{}
	}
	r.BrzChromium = procs
	if err != nil {
		r.ProcessScanError = err.Error()
	}
	return r
}

// inspectProfileDir reports whether the profile exists and whether a
// SingletonLock is held. The lock is created by Chrome when a profile
// is in use; a leftover lock from a crashed launch blocks fresh starts.
// brz removes stale locks during launch, but seeing the lock here lets
// users diagnose "why won't my next brz run start" without launching.
func inspectProfileDir(path string) ProfileDirInfo {
	info := ProfileDirInfo{Path: path}
	if path == "" {
		return info
	}
	if st, err := os.Stat(path); err == nil && st.IsDir() {
		info.Exists = true
	}
	if _, err := os.Stat(filepath.Join(path, "SingletonLock")); err == nil {
		info.SingletonLockHeld = true
	}
	return info
}

// inspectDownloadsDir reports on the brz-downloads tmpdir where the
// download: step lands files when no save_as/save_to is set.
func inspectDownloadsDir(path string) DownloadsDirInfo {
	info := DownloadsDirInfo{Path: path}
	entries, err := os.ReadDir(path)
	if err != nil {
		return info // leaves Exists=false
	}
	info.Exists = true
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		info.FileCount++
		info.TotalBytes += fi.Size()
		if fi.ModTime().After(info.NewestMod) {
			info.NewestMod = fi.ModTime()
		}
	}
	return info
}

// inspectFailureArtifacts counts the failure-screenshot PNGs and
// before-screenshot JPGs that the executor writes to TempDir on a
// failed step. A non-zero count is a hint that a previous run errored
// and those artifacts are still around for `brz logs` (future bead) to
// surface.
func inspectFailureArtifacts(tmpDir string) FailureArtifactsInfo {
	info := FailureArtifactsInfo{TmpDir: tmpDir}
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return info
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		isFailed := strings.Contains(name, "_failed_") && strings.HasSuffix(name, ".png")
		isBefore := strings.Contains(name, "_before_") && strings.HasSuffix(name, ".jpg")
		if !isFailed && !isBefore {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		if isFailed {
			info.FailedPNGCount++
		}
		if isBefore {
			info.BeforeJPGCount++
		}
		if fi.ModTime().After(info.NewestArtifact) {
			info.NewestArtifact = fi.ModTime()
		}
	}
	return info
}

// scanBrzChromiumProcesses returns Chromium processes whose --user-data-dir
// argument matches brz's profile dir (or any /tmp/brz-ephemeral-* path —
// those are also brz-owned).
//
// On Windows we skip the scan and return an empty slice with no error;
// `ps` doesn't exist there and the equivalent (`tasklist /v`) doesn't
// expose command-line args reliably enough to be worth shipping in this
// pass. Windows users still see the rest of the report.
func scanBrzChromiumProcesses(profileDir string) ([]ChromiumProcess, error) {
	if runtime.GOOS == "windows" {
		return nil, nil
	}
	// -ww disables column truncation. Both BSD ps (macOS) and GNU ps (Linux)
	// honor this; without it, long Chromium command lines can lose the
	// --user-data-dir tail and the parser would silently miss matches.
	out, err := exec.Command("ps", "-ww", "-eo", "pid=,args=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps failed: %w", err)
	}
	return parsePsOutputForBrz(string(out), profileDir), nil
}

// parsePsOutputForBrz extracts brz-owned Chromium processes from `ps -eo
// pid=,args=` output. A process is "brz-owned" when its --user-data-dir
// points at the configured profile dir or at /<tmpdir>/brz-ephemeral-*.
//
// Pure function for unit testing — feed it a fixed string, assert the slice.
func parsePsOutputForBrz(psOut string, profileDir string) []ChromiumProcess {
	var procs []ChromiumProcess
	for _, line := range strings.Split(psOut, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// "pid args" — split off the leading PID token.
		spaceIdx := strings.IndexByte(line, ' ')
		if spaceIdx <= 0 {
			continue
		}
		pidStr := line[:spaceIdx]
		argsStr := strings.TrimSpace(line[spaceIdx+1:])
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			continue
		}
		userDataDir := extractFlagValue(argsStr, "--user-data-dir")
		if userDataDir == "" {
			continue
		}
		if !isBrzOwnedProfile(userDataDir, profileDir) {
			continue
		}
		exePath := firstSpaceSeparatedToken(argsStr)
		procs = append(procs, ChromiumProcess{
			PID:         pid,
			UserDataDir: userDataDir,
			ExePath:     exePath,
		})
	}
	sort.Slice(procs, func(i, j int) bool { return procs[i].PID < procs[j].PID })
	return procs
}

// extractFlagValue scans a command-line for "--key=value" or "--key value"
// and returns the matched value (or ""). The value runs until we hit the
// next argument that looks like a flag (starts with "-") or end-of-string.
//
// This handles paths with spaces — common on macOS where Chrome profiles
// often live under "~/Library/Application Support/..." — because ps prints
// argv with single spaces between args but does NOT quote spaces inside an
// arg. Without this multi-token logic, status would silently miss stuck
// Chrome processes whose --user-data-dir contained a space, defeating the
// whole point of the command.
func extractFlagValue(args, flag string) string {
	idx := strings.Index(args, flag)
	if idx < 0 {
		return ""
	}
	rest := args[idx+len(flag):]
	if rest == "" {
		return ""
	}
	switch rest[0] {
	case '=':
		rest = rest[1:]
	case ' ':
		rest = strings.TrimLeft(rest, " ")
	default:
		// Flag matched as a substring of a longer flag (e.g. "--user-data-dir-extra").
		return ""
	}
	rest = strings.TrimRight(rest, " ")
	if rest == "" {
		return ""
	}
	// Greedy: accumulate space-separated tokens until we hit one that looks
	// like a new flag (leading "-") or end-of-string. We Split (not Fields)
	// so internal single spaces inside the value are preserved verbatim,
	// but the leading TrimRight prevents an empty trailing token from
	// turning "/p" into "/p ".
	tokens := strings.Split(rest, " ")
	var valTokens []string
	for _, tok := range tokens {
		if strings.HasPrefix(tok, "-") {
			break
		}
		valTokens = append(valTokens, tok)
	}
	return strings.Join(valTokens, " ")
}

func firstSpaceSeparatedToken(s string) string {
	if i := strings.IndexByte(s, ' '); i > 0 {
		return s[:i]
	}
	return s
}

// isBrzOwnedProfile decides whether a Chromium --user-data-dir was
// brz-launched. Two forms accepted:
//   - exact match against the configured profile dir
//   - basename starts with "brz-ephemeral-" (the --ephemeral CLI flag
//     creates these as $TMPDIR/brz-ephemeral-*)
//
// We deliberately do NOT match on substring "brz-downloads": that path
// is a download scratch dir, never a profile, and a user with an unrelated
// profile path containing the substring would otherwise false-positive.
func isBrzOwnedProfile(userDataDir, profileDir string) bool {
	if userDataDir == "" {
		return false
	}
	if profileDir != "" && pathsEqual(userDataDir, profileDir) {
		return true
	}
	base := filepath.Base(userDataDir)
	return strings.HasPrefix(base, "brz-ephemeral-")
}

// pathsEqual compares paths after cleaning. Avoids classifying
// "/foo/bar/" and "/foo/bar" as different profiles.
func pathsEqual(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

func printStatusHuman(r StatusReport) {
	fmt.Printf("brz %s on %s\n", r.BrzVersion, r.OS)
	fmt.Println()

	fmt.Println("Profile:")
	fmt.Printf("  path:               %s\n", r.ProfileDir.Path)
	fmt.Printf("  exists:             %v\n", r.ProfileDir.Exists)
	if r.ProfileDir.SingletonLockHeld {
		fmt.Println("  singleton_lock:     HELD (a previous brz run did not clean up — next launch will retry once and remove)")
	} else {
		fmt.Println("  singleton_lock:     none")
	}
	fmt.Println()

	fmt.Println("Running brz Chromium processes:")
	if r.ProcessScanError != "" {
		fmt.Printf("  scan error:         %s\n", r.ProcessScanError)
	} else if len(r.BrzChromium) == 0 {
		fmt.Println("  none")
	} else {
		for _, p := range r.BrzChromium {
			fmt.Printf("  pid=%d  exe=%s  user-data-dir=%s\n", p.PID, p.ExePath, p.UserDataDir)
		}
	}
	fmt.Println()

	fmt.Println("Downloads tmpdir:")
	fmt.Printf("  path:               %s\n", r.Downloads.Path)
	if !r.Downloads.Exists {
		fmt.Println("  status:             does not exist (no downloads captured yet)")
	} else {
		fmt.Printf("  files:              %d (%d bytes)\n", r.Downloads.FileCount, r.Downloads.TotalBytes)
		if !r.Downloads.NewestMod.IsZero() {
			fmt.Printf("  newest:             %s\n", r.Downloads.NewestMod.Format(time.RFC3339))
		}
	}
	fmt.Println()

	fmt.Println("Failure artifacts in tmpdir:")
	fmt.Printf("  path:               %s\n", r.FailureArtifacts.TmpDir)
	fmt.Printf("  failed screenshots: %d\n", r.FailureArtifacts.FailedPNGCount)
	fmt.Printf("  before screenshots: %d\n", r.FailureArtifacts.BeforeJPGCount)
	if !r.FailureArtifacts.NewestArtifact.IsZero() {
		fmt.Printf("  newest:             %s\n", r.FailureArtifacts.NewestArtifact.Format(time.RFC3339))
	}
}

func printStatusUsage() {
	fmt.Print(`brz status — print a diagnostic snapshot of brz's local state

Usage: brz status [--json]

Reports:
  - resolved Chrome profile directory + SingletonLock status
  - any running Chromium processes brz launched (PID, exe, user-data-dir)
  - downloads tmpdir occupancy (file count + total size + newest)
  - failure-screenshot count from past failed runs

Exit code is always 0; status is informational and never errors.

Use --json (or pipe stdout) for machine-readable output suitable for jq.
`)
}

