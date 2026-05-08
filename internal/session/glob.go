package session

import "strings"

// MatchDomain returns true if domain matches any of the supplied
// glob-style patterns. An empty pattern list matches everything
// (the "no filter" case).
//
// Supported wildcards:
//   - "*" matches any run of characters within a single segment-or-more.
//     We deliberately accept "*.example.com" matching "example.com" too,
//     because cookies often have a leading-dot Domain ("." stripped here)
//     and users typically want "*.example.com" to mean "the example.com
//     site, including bare apex." This is friendlier than strict glob
//     semantics for the session-portability use case.
//
// Matching is case-insensitive on the domain (DNS is case-insensitive).
func MatchDomain(domain string, patterns []string) bool {
	if len(patterns) == 0 {
		return true
	}
	d := strings.ToLower(strings.TrimPrefix(domain, "."))
	for _, p := range patterns {
		p = strings.ToLower(p)
		if matchOne(d, p) {
			return true
		}
		// Friendly apex case: "*.example.com" should also match "example.com".
		if strings.HasPrefix(p, "*.") && d == p[2:] {
			return true
		}
	}
	return false
}

// matchOne implements minimal glob: '*' matches any substring, no other
// metacharacters. We avoid path/filepath.Match because it has
// path-separator semantics that don't apply to domains.
func matchOne(s, pattern string) bool {
	// Iterative algorithm: walk segments split by '*'.
	if !strings.Contains(pattern, "*") {
		return s == pattern
	}
	parts := strings.Split(pattern, "*")
	// Anchor the first segment if pattern doesn't start with '*'.
	if parts[0] != "" {
		if !strings.HasPrefix(s, parts[0]) {
			return false
		}
		s = s[len(parts[0]):]
	}
	last := len(parts) - 1
	for i := 1; i < last; i++ {
		idx := strings.Index(s, parts[i])
		if idx < 0 {
			return false
		}
		s = s[idx+len(parts[i]):]
	}
	// Anchor the last segment if pattern doesn't end with '*'.
	if parts[last] != "" {
		return strings.HasSuffix(s, parts[last])
	}
	return true
}
