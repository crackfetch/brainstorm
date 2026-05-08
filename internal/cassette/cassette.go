// Package cassette implements recording and replaying of HTTP traffic
// captured via CDP Fetch interception, for deterministic browser-automation runs.
//
// A cassette is a versioned JSON file holding (request, response) pairs.
// The match key for serving a request from a cassette is the tuple
// (method, url, body-hash). Querystring is part of the URL and matters.
// Headers are NOT part of the match key (they vary too much for replay
// to be useful). Bodies are stored base64 — they may be binary.
//
// Forward-compat: Version is bumped on any breaking format change. Readers
// should reject cassettes with a higher Version than they understand.
package cassette

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// filepathDir wraps filepath.Dir for documentation clarity. Kept as a
// thin wrapper so the import stays in scope for one call site.
func filepathDir(p string) string { return filepath.Dir(p) }

// FormatVersion is the cassette format version. Bump on breaking changes.
const FormatVersion = 1

// DefaultBodyCapBytes is the per-response body cap when not opted out.
// Responses larger than this are truncated; a stderr warning is emitted by
// the recording layer.
const DefaultBodyCapBytes = 5 * 1024 * 1024

// Cassette is the on-disk format. Entries is intentionally a slice (not map)
// to preserve recording order — useful for human inspection and diffs.
type Cassette struct {
	Version    int       `json:"version"`
	RecordedAt time.Time `json:"recorded_at"`
	BrzVersion string    `json:"brz_version,omitempty"`
	Workflow   string    `json:"workflow,omitempty"`
	Entries    []*Entry  `json:"entries"`
}

// Entry is a single (request, response) pair plus bookkeeping.
type Entry struct {
	Request       Request  `json:"request"`
	Response      Response `json:"response"`
	MatchedCount  int      `json:"matched_count"`
	BodyTruncated bool     `json:"body_truncated,omitempty"`
}

// Request captures the request side of an interception.
type Request struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	BodyB64 string            `json:"body_b64,omitempty"`
}

// Response captures the response side of an interception.
type Response struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	BodyB64 string            `json:"body_b64,omitempty"`
}

// Key is the match-key tuple used to look up entries in replay mode.
// URL and method are normalized; bodyHash is sha256 hex of the raw body bytes.
type Key struct {
	Method   string
	URL      string
	BodyHash string
}

// String renders the key for logging/error messages.
func (k Key) String() string {
	return fmt.Sprintf("%s %s [body=%s]", k.Method, k.URL, k.BodyHash[:min(8, len(k.BodyHash))])
}

// MakeKey builds a Key from method, URL, and raw body bytes.
// Method is uppercased. URL is canonicalized (host lowercased, query keys
// sorted, fragment stripped) so cosmetically-different URLs match.
func MakeKey(method, rawURL string, body []byte) Key {
	return Key{
		Method:   strings.ToUpper(strings.TrimSpace(method)),
		URL:      canonicalizeURL(rawURL),
		BodyHash: hashBody(body),
	}
}

func hashBody(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// canonicalizeURL normalizes URLs for stable matching. We lowercase the host,
// strip the fragment, and sort query parameters. We deliberately keep the
// path and querystring values as-is — sites embed meaningful tokens there.
func canonicalizeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Fragment = ""
	if u.Host != "" {
		u.Host = strings.ToLower(u.Host)
	}
	if u.RawQuery != "" {
		q := u.Query()
		keys := make([]string, 0, len(q))
		for k := range q {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var sb strings.Builder
		for i, k := range keys {
			vs := q[k]
			sort.Strings(vs)
			for j, v := range vs {
				if i > 0 || j > 0 {
					sb.WriteByte('&')
				}
				sb.WriteString(url.QueryEscape(k))
				sb.WriteByte('=')
				sb.WriteString(url.QueryEscape(v))
			}
		}
		u.RawQuery = sb.String()
	}
	return u.String()
}

// EncodeBody base64-encodes a body for on-disk storage.
func EncodeBody(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return base64.StdEncoding.EncodeToString(b)
}

// DecodeBody base64-decodes a stored body. Returns nil for empty input.
func DecodeBody(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	return base64.StdEncoding.DecodeString(s)
}

// Save writes the cassette to path with pretty-printed JSON. The parent
// directory is created if it does not exist (so callers can supply a path
// like "fixtures/foo.cassette.json" without a separate mkdir).
func Save(c *Cassette, path string) error {
	if dir := filepathDir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(c); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// Load reads and parses a cassette file. Rejects unknown future versions.
func Load(path string) (*Cassette, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	var c Cassette
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse cassette %s: %w", path, err)
	}
	if c.Version == 0 {
		return nil, fmt.Errorf("cassette %s missing version field", path)
	}
	if c.Version > FormatVersion {
		return nil, fmt.Errorf("cassette %s has version %d, this brz only understands up to %d (upgrade brz)", path, c.Version, FormatVersion)
	}
	return &c, nil
}

// Index is an in-memory match-key → entries map built from a Cassette.
// On hash collision (same key, multiple entries), entries are returned
// in recording order — a round-robin across calls.
type Index struct {
	mu       sync.Mutex
	byKey    map[Key][]*Entry
	cursor   map[Key]int
	cassette *Cassette
}

// NewIndex builds an Index from a Cassette by hashing each entry's request.
func NewIndex(c *Cassette) *Index {
	idx := &Index{
		byKey:    make(map[Key][]*Entry, len(c.Entries)),
		cursor:   make(map[Key]int),
		cassette: c,
	}
	for _, e := range c.Entries {
		body, _ := DecodeBody(e.Request.BodyB64)
		k := MakeKey(e.Request.Method, e.Request.URL, body)
		idx.byKey[k] = append(idx.byKey[k], e)
	}
	return idx
}

// Lookup returns the next entry matching k, advancing a per-key cursor so
// repeated identical requests hit successive recorded responses (when the
// cassette has multiple entries for the same key). When the cursor exceeds
// the entry count, we wrap and continue serving the last entry — a request
// the page makes a 4th time gets the same recording as the 3rd.
func (i *Index) Lookup(k Key) (*Entry, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	entries, ok := i.byKey[k]
	if !ok || len(entries) == 0 {
		return nil, false
	}
	c := i.cursor[k]
	var e *Entry
	if c < len(entries) {
		e = entries[c]
		i.cursor[k] = c + 1
	} else {
		e = entries[len(entries)-1]
	}
	e.MatchedCount++
	return e, true
}

// Cassette returns the underlying cassette (read-only callers).
func (i *Index) Cassette() *Cassette { return i.cassette }

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
