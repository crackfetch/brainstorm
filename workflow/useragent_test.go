package workflow

import (
	"strings"
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

func TestBuildUserAgentMetadata(t *testing.T) {
	tests := []struct {
		name    string
		product string
	}{
		{"headless product", "HeadlessChrome/131.0.6778.86"},
		{"headed product", "Chrome/131.0.6778.86"},
		{"empty product", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			meta := buildUserAgentMetadata(tc.product)

			if meta == nil {
				t.Fatal("buildUserAgentMetadata returned nil")
			}

			// No brand should contain HeadlessChrome.
			for _, b := range meta.Brands {
				if strings.Contains(b.Brand, "HeadlessChrome") {
					t.Errorf("Brands contains HeadlessChrome: %q", b.Brand)
				}
			}
			for _, b := range meta.FullVersionList {
				if strings.Contains(b.Brand, "HeadlessChrome") {
					t.Errorf("FullVersionList contains HeadlessChrome: %q", b.Brand)
				}
			}

			// Must include a real browser brand.
			hasBrand := false
			for _, b := range meta.Brands {
				if b.Brand == "Chromium" || b.Brand == "Google Chrome" {
					hasBrand = true
					break
				}
			}
			if !hasBrand {
				t.Error("Brands missing Chromium or Google Chrome")
			}

			// Platform must not be empty.
			if meta.Platform == "" {
				t.Error("Platform is empty")
			}
		})
	}
}

func TestRefreshUserAgent_UpdatesOnProductChange(t *testing.T) {
	w := &Workflow{Name: "test"}
	e := NewExecutor(w)

	// Simulate initial state from Start(): product "HeadlessChrome/131.0.6778.86"
	e.cachedProduct = "HeadlessChrome/131.0.6778.86"
	e.userAgent = resolveUserAgent("Mozilla/5.0 HeadlessChrome/131.0.6778.86 Safari/537.36")
	e.userAgentMeta = buildUserAgentMetadata("HeadlessChrome/131.0.6778.86")

	originalUA := e.userAgent
	originalMeta := e.userAgentMeta

	// Simulate a reconnect where the browser now reports a different version.
	newProduct := "HeadlessChrome/132.0.6834.15"
	newUA := "Mozilla/5.0 HeadlessChrome/132.0.6834.15 Safari/537.36"
	e.refreshUserAgent(newUA, newProduct)

	// The UA should have been updated (HeadlessChrome stripped).
	if e.userAgent == originalUA {
		t.Error("refreshUserAgent did not update userAgent when product changed")
	}
	if !strings.Contains(e.userAgent, "Chrome/132") {
		t.Errorf("expected updated UA to contain Chrome/132, got %q", e.userAgent)
	}

	// Metadata should have been updated too.
	if e.userAgentMeta == originalMeta {
		t.Error("refreshUserAgent did not update userAgentMeta when product changed")
	}

	// cachedProduct should be updated.
	if e.cachedProduct != newProduct {
		t.Errorf("expected cachedProduct %q, got %q", newProduct, e.cachedProduct)
	}
}

func TestRefreshUserAgent_NoOpWhenProductUnchanged(t *testing.T) {
	w := &Workflow{Name: "test"}
	e := NewExecutor(w)

	product := "HeadlessChrome/131.0.6778.86"
	e.cachedProduct = product
	e.userAgent = resolveUserAgent("Mozilla/5.0 HeadlessChrome/131.0.6778.86 Safari/537.36")
	e.userAgentMeta = buildUserAgentMetadata(product)

	originalUA := e.userAgent
	originalMeta := e.userAgentMeta

	// Same product — should be a no-op.
	e.refreshUserAgent("Mozilla/5.0 HeadlessChrome/131.0.6778.86 Safari/537.36", product)

	if e.userAgent != originalUA {
		t.Errorf("refreshUserAgent changed UA when product unchanged: %q -> %q", originalUA, e.userAgent)
	}
	if e.userAgentMeta != originalMeta {
		t.Error("refreshUserAgent changed metadata when product unchanged")
	}
}

func TestBuildUserAgentMetadata_VersionParsing(t *testing.T) {
	meta := buildUserAgentMetadata("HeadlessChrome/131.0.6778.86")

	// Major version should be extracted into brands.
	foundMajor := false
	for _, b := range meta.Brands {
		if b.Brand == "Chromium" && b.Version == "131" {
			foundMajor = true
		}
	}
	if !foundMajor {
		t.Error("expected Chromium brand with major version 131")
	}

	// Full version should include the minor parts.
	if !strings.HasPrefix(meta.FullVersion, "131.") {
		t.Errorf("expected FullVersion starting with 131., got %q", meta.FullVersion)
	}
}
