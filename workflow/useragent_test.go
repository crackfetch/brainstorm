package workflow

import (
	"testing"
)

func TestUserAgentFieldExists(t *testing.T) {
	w := &Workflow{Name: "test"}
	e := NewExecutor(w)

	// Before Start(), UserAgent should return the fallback — never empty.
	ua := e.UserAgent()
	if ua == "" {
		t.Fatal("UserAgent() must never return empty string")
	}
	if ua != fallbackUserAgent {
		t.Errorf("expected fallback UA before Start(), got %q", ua)
	}
}

func TestUserAgentNotHardcoded(t *testing.T) {
	// The fallback constant must exist but the executor should prefer
	// a dynamic value when one is set.
	w := &Workflow{Name: "test"}
	e := NewExecutor(w)

	// Simulate what Start() does after getting the real UA from Chrome.
	e.userAgent = "Mozilla/5.0 RealChrome/136.0.0.0"

	if e.UserAgent() != "Mozilla/5.0 RealChrome/136.0.0.0" {
		t.Errorf("expected dynamic UA, got %q", e.UserAgent())
	}
}

func TestResolveUserAgentFromBrowser(t *testing.T) {
	// resolveUserAgent should return the browser's real UA when available,
	// falling back to fallbackUserAgent on error.
	realUA := "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Chrome/136.0.0.0"

	got := resolveUserAgent(realUA)
	if got != realUA {
		t.Errorf("expected real UA %q, got %q", realUA, got)
	}

	// Empty string should fall back.
	got = resolveUserAgent("")
	if got != fallbackUserAgent {
		t.Errorf("expected fallback UA, got %q", got)
	}
}
