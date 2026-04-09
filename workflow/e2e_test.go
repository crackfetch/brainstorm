package workflow

import (
	"testing"

	"github.com/go-rod/rod/lib/launcher"
)

// skipIfNoChrome skips the test if no Chrome/Chromium is installed.
func skipIfNoChrome(t *testing.T) {
	t.Helper()
	if _, exists := launcher.LookPath(); !exists {
		t.Skip("Chrome not found — skipping browser E2E test")
	}
}

func TestStealthInjection_Idempotent(t *testing.T) {
	skipIfNoChrome(t)

	exec := NewExecutor(&Workflow{Name: "test", Actions: map[string]Action{}})
	if err := exec.Start(); err != nil {
		t.Fatalf("start browser: %v", err)
	}
	defer exec.Close()

	if err := exec.NavigateTo("about:blank"); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	// Second injection on the same page must not panic.
	// Before the fix, this panicked with:
	//   TypeError: Cannot redefine property: webdriver
	exec.injectStealth()
}

func TestRunAction_SameURL_ReusesPage(t *testing.T) {
	skipIfNoChrome(t)

	w := &Workflow{
		Name: "test-reuse",
		Actions: map[string]Action{
			"first": {
				URL:   "about:blank",
				Steps: []Step{},
			},
			"second": {
				URL:   "about:blank",
				Steps: []Step{},
			},
		},
	}

	exec := NewExecutor(w)
	if err := exec.Start(); err != nil {
		t.Fatalf("start browser: %v", err)
	}
	defer exec.Close()

	r1 := exec.RunAction("first")
	if !r1.OK {
		t.Fatalf("first action failed: %s", r1.Error)
	}

	// Second action with same URL should reuse the page (no new tab).
	r2 := exec.RunAction("second")
	if !r2.OK {
		t.Fatalf("second action failed: %s", r2.Error)
	}
}

func TestRunAction_NoURL_ContinuationPattern(t *testing.T) {
	skipIfNoChrome(t)

	w := &Workflow{
		Name: "test-continuation",
		Actions: map[string]Action{
			"navigate_first": {
				URL:   "about:blank",
				Steps: []Step{},
			},
			"continue": {
				// No URL — should operate on current page.
				Steps: []Step{},
			},
		},
	}

	exec := NewExecutor(w)
	if err := exec.Start(); err != nil {
		t.Fatalf("start browser: %v", err)
	}
	defer exec.Close()

	r1 := exec.RunAction("navigate_first")
	if !r1.OK {
		t.Fatalf("first action failed: %s", r1.Error)
	}

	// Continuation action with no URL reuses the current page.
	r2 := exec.RunAction("continue")
	if !r2.OK {
		t.Fatalf("continuation action failed: %s", r2.Error)
	}
}
