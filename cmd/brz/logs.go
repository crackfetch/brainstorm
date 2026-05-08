package main

// brz logs — surface recent failure artifacts brz wrote to TempDir.
//
// When a workflow step fails, the executor saves a "failed" screenshot
// (PNG of the page after failure) and a "before" screenshot (JPEG of the
// page just before the failed step ran). These land in $TMPDIR with names
// like "<action>_failed_<timestamp>_<stepIdx>.png" and
// "<action>_before_<stepIdx>.jpg". `brz logs` lists them newest-first so
// you can quickly find the artifacts from the last failed run without
// grep'ing /tmp by hand.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"
)

// Artifact describes one failure-screenshot file in TempDir.
type Artifact struct {
	Path     string    `json:"path"`
	Kind     string    `json:"kind"`            // "failed" | "before"
	Action   string    `json:"action"`          // action name parsed from filename
	StepIdx  int       `json:"step_index"`      // 0-based step index from filename
	Stamp    string    `json:"timestamp,omitempty"` // only for "failed"; empty for "before"
	Modified time.Time `json:"modified"`
	Size     int64     `json:"size"`
}

// Filename patterns (must match workflow/executor.go's screenshotPath /
// beforePath construction):
//   <action>_failed_<YYYYMMDD-HHMMSS>_<idx>.png
//   <action>_before_<idx>.jpg
var (
	failedPattern = regexp.MustCompile(`^(.+)_failed_(\d{8}-\d{6})_(\d+)\.png$`)
	beforePattern = regexp.MustCompile(`^(.+)_before_(\d+)\.jpg$`)
)

func cmdLogs(args []string) {
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	jsonFlag := fs.Bool("json", false, "Force JSON output (auto when piped)")
	follow := fs.Bool("follow", false, "Watch TempDir and print new artifacts as they appear (Ctrl+C to stop)")
	limit := fs.Int("limit", 20, "Max number of artifacts to show (newest first)")
	fs.Usage = func() { printLogsUsage() }
	fs.Parse(args)

	useJSON := *jsonFlag || !isStdoutTTY()

	if *follow {
		runLogsFollow(os.TempDir(), *limit, useJSON)
		return
	}

	arts := collectArtifacts(os.TempDir(), *limit)
	if useJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if arts == nil {
			arts = []Artifact{}
		}
		_ = enc.Encode(arts)
		return
	}
	printLogsHuman(arts)
}

// collectArtifacts scans dir for failure / before screenshots and returns
// up to limit entries sorted newest first.
func collectArtifacts(dir string, limit int) []Artifact {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var arts []Artifact
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		a, ok := parseArtifactFilename(e.Name())
		if !ok {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		a.Path = filepath.Join(dir, e.Name())
		a.Modified = fi.ModTime()
		a.Size = fi.Size()
		arts = append(arts, a)
	}
	sort.Slice(arts, func(i, j int) bool {
		return arts[i].Modified.After(arts[j].Modified)
	})
	if limit > 0 && len(arts) > limit {
		arts = arts[:limit]
	}
	return arts
}

// parseArtifactFilename extracts the action name, kind, step index, and
// (for failed) timestamp from a brz failure-screenshot filename. Returns
// the partially-populated Artifact and true on match; zero value + false
// otherwise.
//
// Pure function — feed any string, get a deterministic parse result. No
// stat / ReadDir interaction. Easy to unit-test against many filenames.
func parseArtifactFilename(name string) (Artifact, bool) {
	if m := failedPattern.FindStringSubmatch(name); m != nil {
		idx, _ := strconv.Atoi(m[3])
		return Artifact{
			Kind:    "failed",
			Action:  m[1],
			Stamp:   m[2],
			StepIdx: idx,
		}, true
	}
	if m := beforePattern.FindStringSubmatch(name); m != nil {
		idx, _ := strconv.Atoi(m[2])
		return Artifact{
			Kind:    "before",
			Action:  m[1],
			StepIdx: idx,
		}, true
	}
	return Artifact{}, false
}

func printLogsHuman(arts []Artifact) {
	if len(arts) == 0 {
		fmt.Println("No brz failure artifacts in TempDir.")
		fmt.Printf("Looking in: %s\n", os.TempDir())
		fmt.Println("Run a workflow that fails to populate this list (or this is a healthy host).")
		return
	}
	fmt.Printf("%-22s  %-7s  %-30s  %5s  %-8s  %s\n",
		"MODIFIED", "KIND", "ACTION", "STEP", "SIZE", "PATH")
	for _, a := range arts {
		fmt.Printf("%-22s  %-7s  %-30s  %5d  %-8s  %s\n",
			a.Modified.Format("2006-01-02 15:04:05"),
			a.Kind,
			truncate(a.Action, 30),
			a.StepIdx,
			humanSize(a.Size),
			a.Path,
		)
	}
}

// runLogsFollow polls TempDir at 1s intervals, printing each NEW artifact
// (one whose mtime is more recent than the last poll's high-water mark).
// Ctrl+C exits cleanly. Useful when running brz in another terminal and
// watching for failures here.
func runLogsFollow(dir string, limit int, useJSON bool) {
	// Print the initial batch first, then watermark to "now".
	initial := collectArtifacts(dir, limit)
	if useJSON {
		// In follow mode, JSON outputs one object per line as artifacts
		// appear (NDJSON). Emit the initial batch the same way.
		enc := json.NewEncoder(os.Stdout)
		for _, a := range initial {
			_ = enc.Encode(a)
		}
	} else {
		printLogsHuman(initial)
		fmt.Println()
		fmt.Println("Following — Ctrl+C to stop.")
	}

	mark := time.Now()
	for {
		time.Sleep(1 * time.Second)
		batch := collectArtifacts(dir, 0) // no limit — let the mtime filter prune
		var fresh []Artifact
		for _, a := range batch {
			if a.Modified.After(mark) {
				fresh = append(fresh, a)
			}
		}
		// Re-sort fresh entries oldest→newest so they print in the order
		// they happened.
		sort.Slice(fresh, func(i, j int) bool {
			return fresh[i].Modified.Before(fresh[j].Modified)
		})
		if useJSON {
			enc := json.NewEncoder(os.Stdout)
			for _, a := range fresh {
				_ = enc.Encode(a)
			}
		} else {
			for _, a := range fresh {
				fmt.Printf("%s  %-7s  %-30s  step=%d  %s  %s\n",
					a.Modified.Format("2006-01-02 15:04:05"),
					a.Kind,
					truncate(a.Action, 30),
					a.StepIdx,
					humanSize(a.Size),
					a.Path,
				)
			}
		}
		if len(fresh) > 0 {
			mark = fresh[len(fresh)-1].Modified
		}
	}
}

func truncate(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-3]) + "..."
}


func printLogsUsage() {
	fmt.Print(`brz logs — list recent failure artifacts (failed + before screenshots)

Usage: brz logs [--follow] [--limit N] [--json]

When a workflow step fails, brz writes two artifacts to $TMPDIR:
  <action>_failed_<timestamp>_<step>.png   — page state after failure
  <action>_before_<step>.jpg                — page state just before the failed step

This command lists those artifacts newest-first so you can find them
without grep'ing /tmp by hand. Default limit is 20.

Flags:
  --follow      Watch TempDir and print each new artifact as it appears.
                Useful when running brz in another shell.
  --limit N     Max entries to show (default 20). Use a large number or
                0 to see everything.
  --json        Force JSON output (auto-enabled when stdout is piped).
                In --follow mode JSON is NDJSON (one object per line).

Use ` + "`brz status`" + ` to see counts of these artifacts at a glance.

Note: ` + "`screenshot: \"name.png\"`" + ` steps in workflow YAML write to user-supplied
filenames and are NOT listed here — only the auto-generated failure
artifacts are.
`)
}
