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
		port, err := discoverDebugPort(dir, time.Second)
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
		port, err := discoverDebugPort(dir, 2*time.Second)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if port != "12345" {
			t.Errorf("got port %q, want %q", port, "12345")
		}
	})

	t.Run("times out when file never appears", func(t *testing.T) {
		dir := t.TempDir()
		_, err := discoverDebugPort(dir, 150*time.Millisecond)
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
		port, err := discoverDebugPort(dir, time.Second)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if port != "9876" {
			t.Errorf("got port %q, want %q", port, "9876")
		}
	})
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
