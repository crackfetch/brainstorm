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

func TestResolveUserAgentStripsHeadless(t *testing.T) {
	// Chrome reports "HeadlessChrome/X" via Browser.getVersion when running
	// headless. resolveUserAgent must rewrite that to plain "Chrome/X" so
	// downstream pages don't advertise headless mode.
	//
	// CRITICAL: only the canonical "HeadlessChrome/<digit>" form is stripped.
	// Free-floating substrings (custom builds, parenthetical annotations,
	// hypothetical token variants) MUST be left intact — silent rewriting of
	// unknown tokens is data corruption that can be exploited if those tokens
	// surface elsewhere.
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "canonical HeadlessChrome/version token is stripped",
			in:   "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) HeadlessChrome/146.0.0.0 Safari/537.36",
			want: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36",
		},
		{
			name: "plain Chrome UA is left untouched (regression guard against over-broad matching)",
			in:   "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36",
			want: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36",
		},
		{
			name: "non-canonical HeadlessChrome substring (no /version) is preserved",
			in:   "Mozilla/5.0 (HeadlessChrome internal) Chrome/146.0.0.0",
			want: "Mozilla/5.0 (HeadlessChrome internal) Chrome/146.0.0.0",
		},
		{
			name: "hypothetical HeadlessChromium variant is not corrupted",
			in:   "Mozilla/5.0 HeadlessChromium/146.0.0.0",
			want: "Mozilla/5.0 HeadlessChromium/146.0.0.0",
		},
		{
			name: "case-sensitive: lowercase headlesschrome is NOT stripped",
			in:   "Mozilla/5.0 headlesschrome/146.0.0.0",
			want: "Mozilla/5.0 headlesschrome/146.0.0.0",
		},
		{
			name: "empty input falls back to fallbackUserAgent",
			in:   "",
			want: fallbackUserAgent,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveUserAgent(tc.in)
			if got != tc.want {
				t.Errorf("resolveUserAgent(%q):\n  want: %q\n  got:  %q", tc.in, tc.want, got)
			}
		})
	}
}
