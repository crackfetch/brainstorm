package workflow

import (
	"strings"
	"testing"
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
		// DefaultViewport is 1280x900.
		if !containsArg(args, "--window-size=1280,900") {
			t.Errorf("headed=%v: missing window-size flag in: %s", headed, strings.Join(args, " "))
		}
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
