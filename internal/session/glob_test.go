package session

import "testing"

func TestMatchDomain(t *testing.T) {
	cases := []struct {
		domain   string
		patterns []string
		want     bool
	}{
		{"tcgplayer.com", nil, true},                       // empty pattern = match all
		{"tcgplayer.com", []string{}, true},                // empty slice = match all
		{"store.tcgplayer.com", []string{"*.tcgplayer.com"}, true},
		{"tcgplayer.com", []string{"*.tcgplayer.com"}, true},        // friendly apex
		{"evil.com", []string{"*.tcgplayer.com"}, false},
		{".tcgplayer.com", []string{"*.tcgplayer.com"}, true},       // leading-dot stripped
		{"TCGplayer.com", []string{"*.tcgplayer.com"}, true},        // case-insensitive
		{"a.b.example.com", []string{"*.example.com"}, true},
		{"foo.com", []string{"foo.com"}, true},
		{"foo.com", []string{"bar.com", "foo.com"}, true},
		{"foo.com", []string{"bar.com"}, false},
		{"foo.com", []string{"*"}, true},
		{"x.foo.com", []string{"x.*.com"}, true},
	}
	for _, tc := range cases {
		got := MatchDomain(tc.domain, tc.patterns)
		if got != tc.want {
			t.Errorf("MatchDomain(%q, %v) = %v, want %v", tc.domain, tc.patterns, got, tc.want)
		}
	}
}
