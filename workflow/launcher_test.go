package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestBuildLauncher_HeadlessModeFlag asserts that buildLauncher emits the
// "--headless=new" flag in headless mode and does NOT emit it in headed mode.
// This locks in two things at once:
//
//  1. rod's launcher.Set("headless", "new") still produces a single
//     "--headless=new" arg (and not, say, two competing --headless flags or
//     none at all if rod's flag normalization changes upstream).
//  2. The headed and headless code paths inside buildLauncher cannot drift
//     silently — if a future change forgets the else branch, this test fails.
//
// It does not launch Chrome, so it runs everywhere with no Chrome dependency.
func TestBuildLauncher_HeadlessModeFlag(t *testing.T) {
	tests := []struct {
		name         string
		headed       bool
		wantContains []string
		wantAbsent   []string
	}{
		{
			name:         "headless mode emits --headless=new",
			headed:       false,
			wantContains: []string{"--headless=new"},
			wantAbsent:   []string{"--window-position"},
		},
		{
			name:         "headed mode does NOT emit --headless=new",
			headed:       true,
			wantContains: []string{"--window-position=100,100"},
			wantAbsent:   []string{"--headless=new"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := &Executor{
				workflow: &Workflow{Name: "test"},
				headed:   tc.headed,
			}
			args := e.buildLauncher().FormatArgs()
			joined := strings.Join(args, " ")

			for _, want := range tc.wantContains {
				if !containsArg(args, want) {
					t.Errorf("expected arg %q in launcher args, got: %s", want, joined)
				}
			}
			for _, absent := range tc.wantAbsent {
				if containsArg(args, absent) {
					t.Errorf("did NOT expect arg %q in launcher args, got: %s", absent, joined)
				}
			}

			// Defensive: ensure exactly one --headless flag exists in headless
			// mode. Two would mean rod's Set() changed semantics (e.g. append
			// rather than replace) and Chrome would see ambiguous flags.
			if !tc.headed {
				headlessCount := 0
				for _, a := range args {
					if strings.HasPrefix(a, "--headless") {
						headlessCount++
					}
				}
				if headlessCount != 1 {
					t.Errorf("expected exactly one --headless arg, got %d in: %s", headlessCount, joined)
				}
			}
		})
	}
}

// TestBuildLauncher_AlwaysSetsStealthFlags asserts that the stealth-related
// flags every brz session needs (AutomationControlled disable, viewport)
// are present regardless of headed mode.
func TestBuildLauncher_AlwaysSetsStealthFlags(t *testing.T) {
	for _, headed := range []bool{false, true} {
		e := &Executor{
			workflow: &Workflow{Name: "test"},
			headed:   headed,
		}
		args := e.buildLauncher().FormatArgs()

		if !containsArg(args, "--disable-blink-features=AutomationControlled") {
			t.Errorf("headed=%v: missing AutomationControlled disable flag in: %s", headed, strings.Join(args, " "))
		}
		if containsArg(args, "--enable-automation") {
			t.Errorf("headed=%v: enable-automation flag must be deleted but was present in: %s", headed, strings.Join(args, " "))
		}
		// DefaultViewport is 1280x900.
		if !containsArg(args, "--window-size=1280,900") {
			t.Errorf("headed=%v: missing window-size flag in: %s", headed, strings.Join(args, " "))
		}
	}
}

func TestBuildLauncher_DisablesLeaklessHelper(t *testing.T) {
	e := &Executor{workflow: &Workflow{Name: "test"}}
	args := e.buildLauncher().FormatArgs()
	if containsArg(args, "--rod-leakless") {
		t.Fatalf("launcher must not use leakless helper; args: %s", strings.Join(args, " "))
	}
}

// TestParseChromeVersion verifies the version-string parser that gates
// --headless=new vs bare --headless.
func TestParseChromeVersion(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		want    int
	}{
		{"chrome 131", "Google Chrome 131.0.6778.86", 131},
		{"chrome 109", "Google Chrome 109.0.5414.119", 109},
		{"chrome 108", "Google Chrome 108.0.5359.124", 108},
		{"chromium 90", "Chromium 90.0.4430.212 built on Debian", 90},
		{"chrome 200", "Google Chrome 200.1.2.3", 200},
		{"empty string", "", 0},
		{"garbage", "not a chrome version", 0},
		{"partial match", "Chrome", 0},
		{"just number", "131", 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseChromeVersion(tc.output)
			if got != tc.want {
				t.Errorf("parseChromeVersion(%q) = %d, want %d", tc.output, got, tc.want)
			}
		})
	}
}

// TestBuildLauncher_LegacyHeadlessFallback verifies that when Chrome version
// is <109, buildLauncher emits bare "--headless" instead of "--headless=new".
func TestBuildLauncher_LegacyHeadlessFallback(t *testing.T) {
	tests := []struct {
		name         string
		chromeVer    int
		wantContains string
		wantAbsent   string
	}{
		{"chrome 108 gets bare --headless", 108, "--headless", "--headless=new"},
		{"chrome 90 gets bare --headless", 90, "--headless", "--headless=new"},
		{"chrome 109 gets --headless=new", 109, "--headless=new", ""},
		{"chrome 131 gets --headless=new", 131, "--headless=new", ""},
		{"chrome 0 (unknown) gets --headless=new", 0, "--headless=new", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := &Executor{
				workflow:       &Workflow{Name: "test"},
				headed:         false,
				chromeVersion:  tc.chromeVer,
			}
			args := e.buildLauncher().FormatArgs()
			joined := strings.Join(args, " ")

			if !containsArg(args, tc.wantContains) {
				t.Errorf("expected arg %q in launcher args, got: %s", tc.wantContains, joined)
			}
			if tc.wantAbsent != "" && containsArg(args, tc.wantAbsent) {
				t.Errorf("did NOT expect arg %q in launcher args, got: %s", tc.wantAbsent, joined)
			}

			// Ensure exactly one --headless flag.
			headlessCount := 0
			for _, a := range args {
				if strings.HasPrefix(a, "--headless") {
					headlessCount++
				}
			}
			if headlessCount != 1 {
				t.Errorf("expected exactly one --headless arg, got %d in: %s", headlessCount, joined)
			}
		})
	}
}

// TestDiscoverDebugPort verifies the DevToolsActivePort file polling logic.
func TestDiscoverDebugPort(t *testing.T) {
	t.Run("reads port from existing file", func(t *testing.T) {
		dir := t.TempDir()
		portFile := filepath.Join(dir, "DevToolsActivePort")
		// Chrome writes port then ws path, separated by newline
		if err := os.WriteFile(portFile, []byte("54321\n/devtools/browser/abc123"), 0644); err != nil {
			t.Fatal(err)
		}
		port, err := discoverDebugPort(dir, time.Second, nil, time.Time{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if port != "54321" {
			t.Errorf("got port %q, want %q", port, "54321")
		}
	})

	t.Run("polls until file appears", func(t *testing.T) {
		dir := t.TempDir()
		portFile := filepath.Join(dir, "DevToolsActivePort")
		// Write the file after a short delay to simulate Chrome startup latency
		go func() {
			time.Sleep(200 * time.Millisecond)
			os.WriteFile(portFile, []byte("12345\n/devtools/browser/xyz"), 0644)
		}()
		port, err := discoverDebugPort(dir, 2*time.Second, nil, time.Time{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if port != "12345" {
			t.Errorf("got port %q, want %q", port, "12345")
		}
	})

	t.Run("times out when file never appears", func(t *testing.T) {
		dir := t.TempDir()
		_, err := discoverDebugPort(dir, 150*time.Millisecond, nil, time.Time{})
		if err == nil {
			t.Fatal("expected timeout error, got nil")
		}
	})

	t.Run("ignores malformed content", func(t *testing.T) {
		dir := t.TempDir()
		portFile := filepath.Join(dir, "DevToolsActivePort")
		// Write garbage first, then valid content (simulates partial write)
		os.WriteFile(portFile, []byte("not-a-port\n"), 0644)
		go func() {
			time.Sleep(100 * time.Millisecond)
			os.WriteFile(portFile, []byte("9876\n/devtools/browser/ok"), 0644)
		}()
		port, err := discoverDebugPort(dir, time.Second, nil, time.Time{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if port != "9876" {
			t.Errorf("got port %q, want %q", port, "9876")
		}
	})

	// Regression for #41: a previous run wrote a port file. A new launch
	// happens; we must NOT return that stale port even though it's
	// well-formed. Once Chrome rewrites the file (after launch), the
	// fresh mtime > staleBefore unblocks the read.
	t.Run("rejects stale file by mtime then accepts fresh write", func(t *testing.T) {
		dir := t.TempDir()
		portFile := filepath.Join(dir, "DevToolsActivePort")
		// Stale file from a previous run (any mtime ≤ staleBefore).
		if err := os.WriteFile(portFile, []byte("11111\n/devtools/browser/old"), 0644); err != nil {
			t.Fatal(err)
		}
		// staleBefore = wall-clock just AFTER the stale file was written.
		// The file's mtime is now ≤ staleBefore, so it must be rejected.
		// Sleep a hair to ensure mtime resolution treats the next write as newer.
		time.Sleep(50 * time.Millisecond)
		staleBefore := time.Now()

		go func() {
			time.Sleep(150 * time.Millisecond)
			// Chrome rebinds and rewrites the file.
			os.WriteFile(portFile, []byte("22222\n/devtools/browser/new"), 0644)
		}()

		port, err := discoverDebugPort(dir, 2*time.Second, nil, staleBefore)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if port != "22222" {
			t.Errorf("got port %q, want %q (stale port leaked through)", port, "22222")
		}
	})

	// When staleBefore is zero (sentinel for "no gating"), an existing
	// well-formed file is accepted immediately — preserves backward-compat
	// for callers that don't supply a gate.
	t.Run("zero staleBefore disables mtime gate", func(t *testing.T) {
		dir := t.TempDir()
		portFile := filepath.Join(dir, "DevToolsActivePort")
		if err := os.WriteFile(portFile, []byte("33333\n/devtools/browser/x"), 0644); err != nil {
			t.Fatal(err)
		}
		port, err := discoverDebugPort(dir, time.Second, nil, time.Time{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if port != "33333" {
			t.Errorf("got port %q, want %q", port, "33333")
		}
	})
}

// TestBuildLauncher_WithChromeFlags asserts that caller-supplied extra flags
// from WithChromeFlags reach the launcher's argv. Covers both the
// bare-flag (empty value) and key=value cases.
func TestBuildLauncher_WithChromeFlags(t *testing.T) {
	e := &Executor{
		workflow: &Workflow{Name: "test"},
		chromeFlags: map[string]string{
			"disable-background-timer-throttling":    "",
			"disable-renderer-backgrounding":         "",
			"disable-backgrounding-occluded-windows": "",
			"force-color-profile":                    "srgb",
		},
	}
	args := e.buildLauncher().FormatArgs()
	joined := strings.Join(args, " ")

	for _, want := range []string{
		"--disable-background-timer-throttling",
		"--disable-renderer-backgrounding",
		"--disable-backgrounding-occluded-windows",
		"--force-color-profile=srgb",
	} {
		if !containsArg(args, want) {
			t.Errorf("missing caller-supplied flag %q in: %s", want, joined)
		}
	}
}

// TestBuildLauncher_ChromeFlagsOverrideDefaults asserts caller flags are
// applied AFTER built-in flags, so passing window-position overrides the
// default 100,100 in headed mode.
func TestBuildLauncher_ChromeFlagsOverrideDefaults(t *testing.T) {
	e := &Executor{
		workflow: &Workflow{Name: "test"},
		headed:   true,
		chromeFlags: map[string]string{
			"window-position": "500,500",
		},
	}
	args := e.buildLauncher().FormatArgs()
	joined := strings.Join(args, " ")

	if !containsArg(args, "--window-position=500,500") {
		t.Errorf("override missing; args: %s", joined)
	}
	for _, a := range args {
		if a == "--window-position=100,100" {
			t.Errorf("default window-position leaked through alongside override; args: %s", joined)
		}
	}
}

// TestBuildLauncher_NilChromeFlagsMatchesBaseline pins backwards compat:
// not passing WithChromeFlags (nil) or passing an empty map must produce
// the exact same argv as prior versions. Pinning a fixed profileDir on
// all three so rod's auto-generated temp --user-data-dir doesn't make
// every run unique.
func TestBuildLauncher_NilChromeFlagsMatchesBaseline(t *testing.T) {
	profileDir := t.TempDir()
	mk := func(flags map[string]string) []string {
		return (&Executor{
			workflow:    &Workflow{Name: "test"},
			profileDir:  profileDir,
			chromeFlags: flags,
		}).buildLauncher().FormatArgs()
	}
	baseline := mk(nil) // no chromeFlags passed at all
	withNil := mk(nil)
	withEmpty := mk(map[string]string{})

	if strings.Join(baseline, " ") != strings.Join(withNil, " ") {
		t.Errorf("nil chromeFlags must equal baseline\nbaseline: %v\n     nil: %v", baseline, withNil)
	}
	if strings.Join(baseline, " ") != strings.Join(withEmpty, " ") {
		t.Errorf("empty chromeFlags must equal baseline\nbaseline: %v\n   empty: %v", baseline, withEmpty)
	}
}

// TestWithChromeFlagsOption verifies the Option function itself: merging,
// no-op on nil/empty, last-write-wins on key collisions.
func TestWithChromeFlagsOption(t *testing.T) {
	e := &Executor{}

	WithChromeFlags(map[string]string{"foo": "bar"})(e)
	if e.chromeFlags["foo"] != "bar" {
		t.Fatalf("first call: foo=%q want bar", e.chromeFlags["foo"])
	}

	WithChromeFlags(map[string]string{"baz": ""})(e)
	if _, ok := e.chromeFlags["foo"]; !ok {
		t.Errorf("foo lost after second call (calls should merge): %v", e.chromeFlags)
	}
	if v, ok := e.chromeFlags["baz"]; !ok || v != "" {
		t.Errorf("baz missing or wrong value after second call: v=%q ok=%v", v, ok)
	}

	WithChromeFlags(map[string]string{"foo": "BAR"})(e)
	if e.chromeFlags["foo"] != "BAR" {
		t.Errorf("collision: want last-write-wins (BAR), got %q", e.chromeFlags["foo"])
	}

	before := len(e.chromeFlags)
	WithChromeFlags(nil)(e)
	WithChromeFlags(map[string]string{})(e)
	if len(e.chromeFlags) != before {
		t.Errorf("nil/empty calls should be no-op; len went %d -> %d", before, len(e.chromeFlags))
	}
}

// containsArg reports whether args contains the given exact arg, OR an arg
// that starts with arg+"=" (so we can match either a prefix like
// "--window-position" or a fully-formed flag like "--headless=new").
func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want || strings.HasPrefix(a, want+"=") || strings.HasPrefix(a, want+" ") {
			return true
		}
		// Allow callers to pass either "--window-position" (prefix match)
		// or "--window-position=100,100" (exact match).
		if strings.Contains(want, "=") && a == want {
			return true
		}
	}
	return false
}
