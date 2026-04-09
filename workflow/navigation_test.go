package workflow

import (
	"strings"
	"testing"
)

func TestURLsMatchForSkip(t *testing.T) {
	tests := []struct {
		name    string
		current string
		target  string
		want    bool
	}{
		{"exact match", "https://example.com/admin/pricing", "https://example.com/admin/pricing", true},
		{"different path", "https://example.com/admin/pricing", "https://example.com/admin/orders", false},
		{"different scheme", "http://example.com/page", "https://example.com/page", false},
		{"different host", "https://a.example.com/page", "https://b.example.com/page", false},
		{"with query match", "https://example.com/page?a=1", "https://example.com/page?a=1", true},
		{"with query differ", "https://example.com/page?a=1", "https://example.com/page?a=2", false},
		{"target has query", "https://example.com/page", "https://example.com/page?a=1", false},
		{"current has query", "https://example.com/page?a=1", "https://example.com/page", false},
		{"with fragment", "https://example.com/page#top", "https://example.com/page#top", true},
		{"fragment differ", "https://example.com/page#top", "https://example.com/page#bottom", false},
		{"trailing slash match", "https://example.com/admin/", "https://example.com/admin/", true},
		{"trailing slash differ", "https://example.com/admin", "https://example.com/admin/", false},
		{"about:blank vs url", "about:blank", "https://example.com/page", false},
		{"empty current", "", "https://example.com/page", false},
		{"empty target", "https://example.com/page", "", false},
		{"both empty", "", "", true},
		{"invalid url current", "not a url %%%", "https://example.com", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := urlsMatchForSkip(tt.current, tt.target)
			if got != tt.want {
				t.Errorf("urlsMatchForSkip(%q, %q) = %v, want %v", tt.current, tt.target, got, tt.want)
			}
		})
	}
}

func TestForceNavigateParsing(t *testing.T) {
	yaml := `
name: test-force-nav
actions:
  normal:
    url: https://example.com/page
    steps:
      - click: { selector: '#btn' }
  forced:
    url: https://example.com/page
    force_navigate: true
    steps:
      - click: { selector: '#btn' }
`
	w, err := LoadFromBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if w.Actions["normal"].ForceNavigate {
		t.Error("normal action should not have force_navigate")
	}
	if !w.Actions["forced"].ForceNavigate {
		t.Error("forced action should have force_navigate=true")
	}
}

func TestRunAction_NoURL_NoPage_ReturnsError(t *testing.T) {
	// First action in a workflow with no URL and no prior page should fail
	// with a clear error, not silently run against about:blank.
	w := &Workflow{
		Name:    "test",
		Actions: map[string]Action{"continue": {Steps: []Step{{Eval: "1+1"}}}},
	}
	exec := &Executor{workflow: w} // no browser, no page

	result := exec.RunAction("continue")
	if result.OK {
		t.Fatal("expected failure when no URL and no prior page")
	}
	if !strings.Contains(result.Error, "no URL") {
		t.Errorf("expected error about no URL, got: %s", result.Error)
	}
}

func TestNoURLActionParsing(t *testing.T) {
	yaml := `
name: test-no-url
actions:
  continue_export:
    steps:
      - click: { selector: '#export-btn' }
`
	w, err := LoadFromBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	action := w.Actions["continue_export"]
	if action.URL != "" {
		t.Errorf("expected empty URL for continuation action, got %q", action.URL)
	}
	if len(action.Steps) != 1 {
		t.Errorf("expected 1 step, got %d", len(action.Steps))
	}
}
