package main

import (
	"strings"
	"testing"

	"github.com/go-rod/rod/lib/proto"
)

func TestConvertCookies_BasicShape(t *testing.T) {
	in := []*proto.NetworkCookie{
		{
			Name: "session", Value: "abc",
			Domain: "example.com", Path: "/",
			Expires:  1893456000, // 2030-01-01
			Secure:   true,
			HTTPOnly: true,
			SameSite: proto.NetworkCookieSameSiteLax,
		},
	}
	out := convertCookies(in)
	if len(out) != 1 {
		t.Fatalf("want 1 cookie, got %d", len(out))
	}
	c := out[0]
	if c.Name != "session" || c.Value != "abc" || c.Domain != "example.com" {
		t.Errorf("name/value/domain: %+v", c)
	}
	if !c.Secure || !c.HTTPOnly {
		t.Errorf("secure/httpOnly flags lost: %+v", c)
	}
	if c.SameSite != "Lax" {
		t.Errorf("SameSite want Lax, got %q", c.SameSite)
	}
	if c.Expires == "" || !strings.HasPrefix(c.Expires, "2030-") {
		t.Errorf("expires not RFC3339 in 2030: %q", c.Expires)
	}
}

func TestConvertCookies_SessionCookieHasNoExpires(t *testing.T) {
	in := []*proto.NetworkCookie{
		{Name: "tmp", Value: "x", Domain: "a.com", Path: "/", Expires: 0},
		{Name: "tmp2", Value: "x", Domain: "a.com", Path: "/", Expires: -1},
	}
	out := convertCookies(in)
	for _, c := range out {
		if c.Expires != "" {
			t.Errorf("session cookie %s should have empty Expires, got %q", c.Name, c.Expires)
		}
	}
}

func TestConvertCookies_SortedByDomainThenName(t *testing.T) {
	in := []*proto.NetworkCookie{
		{Name: "z", Domain: "b.com"},
		{Name: "a", Domain: "b.com"},
		{Name: "m", Domain: "a.com"},
	}
	out := convertCookies(in)
	if out[0].Domain != "a.com" || out[0].Name != "m" {
		t.Errorf("first should be a.com/m, got %+v", out[0])
	}
	if out[1].Name != "a" || out[2].Name != "z" {
		t.Errorf("b.com cookies not sorted by name: %+v / %+v", out[1], out[2])
	}
}

func TestMatchHostGlob(t *testing.T) {
	cases := []struct {
		host, glob string
		want       bool
	}{
		{"example.com", "example.com", true},
		{"a.example.com", "*.example.com", true},
		{"example.com", "*.example.com", true},  // bare apex matches *.foo
		{"other.com", "*.example.com", false},
		{"example.com", "*.com", true},
		{"foo.bar.example.com", "*.example.com", true},
		{"newassets.hcaptcha.com", "*.hcaptcha.com", true},
		{"hcaptcha.com", "hcaptcha.com", true},
		{"evil-example.com", "example.com", false},
		{"EXAMPLE.com", "example.com", true}, // case-insensitive
	}
	for _, tc := range cases {
		got := matchHostGlob(tc.host, tc.glob)
		if got != tc.want {
			t.Errorf("matchHostGlob(%q, %q) = %v, want %v", tc.host, tc.glob, got, tc.want)
		}
	}
}

func TestFilterCookies_GlobCSV(t *testing.T) {
	in := []CookieRecord{
		{Name: "tcg-a", Domain: ".tcgplayer.com"},
		{Name: "tcg-b", Domain: "store.tcgplayer.com"},
		{Name: "hc", Domain: ".hcaptcha.com"},
		{Name: "other", Domain: "example.com"},
	}
	out := filterCookies(in, "*.tcgplayer.com,hcaptcha.com")
	got := []string{}
	for _, c := range out {
		got = append(got, c.Name)
	}
	want := []string{"tcg-a", "tcg-b", "hc"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("filter: got %v, want %v", got, want)
	}
}

func TestFilterCookies_EmptyGlob_KeepsAll(t *testing.T) {
	in := []CookieRecord{
		{Name: "a", Domain: "x.com"},
		{Name: "b", Domain: "y.com"},
	}
	out := filterCookies(in, "")
	if len(out) != 2 {
		t.Errorf("empty glob should keep all, got %d", len(out))
	}
	out = filterCookies(in, "   ")
	if len(out) != 2 {
		t.Errorf("whitespace glob should keep all, got %d", len(out))
	}
}

func TestUniqueDomains(t *testing.T) {
	in := []CookieRecord{
		{Domain: ".tcgplayer.com"},
		{Domain: "tcgplayer.com"}, // same after dot-strip
		{Domain: "store.tcgplayer.com"},
		{Domain: "hcaptcha.com"},
	}
	out := uniqueDomains(in)
	want := []string{"hcaptcha.com", "store.tcgplayer.com", "tcgplayer.com"}
	if strings.Join(out, ",") != strings.Join(want, ",") {
		t.Errorf("uniqueDomains: got %v, want %v", out, want)
	}
}

func TestToNetscape_DomainCookie_PreservesIncludeSubdomains(t *testing.T) {
	bundle := SessionBundle{
		Cookies: []CookieRecord{
			{
				Name:    "session", Value: "abc",
				Domain: ".example.com", Path: "/admin",
				Secure: true, Expires: "2030-01-01T00:00:00Z",
			},
		},
	}
	got := toNetscape(bundle)
	// Domain cookie (leading dot) → includeSubdomains TRUE, domain kept verbatim.
	want := ".example.com\tTRUE\t/admin\tTRUE\t1893456000\tsession\tabc"
	if !strings.Contains(got, want) {
		t.Errorf("want line %q in:\n%s", want, got)
	}
}

func TestToNetscape_HostOnlyCookie_StaysHostOnly(t *testing.T) {
	// Host-only cookies (no leading dot from CDP) must NOT be promoted to
	// include-subdomains in the Netscape file — that would leak auth
	// cookies to sibling subdomains under the same registrable domain.
	bundle := SessionBundle{
		Cookies: []CookieRecord{
			{Name: "x", Value: "y", Domain: "example.com", Path: "/", Secure: false},
		},
	}
	got := toNetscape(bundle)
	want := "example.com\tFALSE\t/\tFALSE\t0\tx\ty"
	if !strings.Contains(got, want) {
		t.Errorf("host-only scope lost. want %q in:\n%s", want, got)
	}
	if strings.Contains(got, ".example.com\tTRUE") {
		t.Errorf("must not promote bare domain to includeSubdomains TRUE:\n%s", got)
	}
}
