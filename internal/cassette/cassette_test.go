package cassette

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.cassette.json")

	orig := &Cassette{
		Version:    FormatVersion,
		RecordedAt: time.Now().UTC().Truncate(time.Second),
		BrzVersion: "test",
		Workflow:   "demo.yaml",
		Entries: []*Entry{
			{
				Request: Request{
					Method:  "GET",
					URL:     "https://example.com/api?b=2&a=1",
					Headers: map[string]string{"X-Test": "1"},
				},
				Response: Response{
					Status:  200,
					Headers: map[string]string{"Content-Type": "application/json"},
					BodyB64: EncodeBody([]byte(`{"ok":true}`)),
				},
			},
			{
				Request: Request{
					Method:  "POST",
					URL:     "https://example.com/api/post",
					BodyB64: EncodeBody([]byte(`{"q":1}`)),
				},
				Response: Response{Status: 201, BodyB64: EncodeBody([]byte("created"))},
			},
		},
	}

	if err := Save(orig, path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Version != orig.Version || loaded.Workflow != orig.Workflow {
		t.Fatalf("metadata mismatch: %+v vs %+v", loaded, orig)
	}
	if len(loaded.Entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(loaded.Entries))
	}
	body, err := DecodeBody(loaded.Entries[0].Response.BodyB64)
	if err != nil {
		t.Fatalf("DecodeBody: %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("body roundtrip mismatch: %q", body)
	}
}

func TestRejectsFutureVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "future.json")
	c := &Cassette{Version: FormatVersion + 99, Entries: []*Entry{}}
	if err := Save(c, path); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for future version")
	}
}

func TestRejectsMissingVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nover.json")
	if err := os.WriteFile(path, []byte(`{"entries":[]}`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for missing version")
	}
}

func TestCanonicalizeURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://Example.com/path?b=2&a=1#frag", "https://example.com/path?a=1&b=2"},
		{"http://x/y", "http://x/y"},
		{"https://api.example.com/v1?x=1&x=2", "https://api.example.com/v1?x=1&x=2"},
	}
	for _, tc := range cases {
		got := canonicalizeURL(tc.in)
		if got != tc.want {
			t.Errorf("canonicalizeURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestMakeKeyMatchesAcrossWhitespaceAndCase(t *testing.T) {
	a := MakeKey("get", "https://X.com/y?b=2&a=1", nil)
	b := MakeKey("GET", "https://x.com/y?a=1&b=2", []byte{})
	if a != b {
		t.Fatalf("keys should match: %+v vs %+v", a, b)
	}
}

func TestMakeKeyBodySensitive(t *testing.T) {
	a := MakeKey("POST", "https://x/y", []byte("a"))
	b := MakeKey("POST", "https://x/y", []byte("b"))
	if a == b {
		t.Fatal("body-different keys should differ")
	}
}

func TestIndexLookupRoundRobin(t *testing.T) {
	c := &Cassette{
		Version: FormatVersion,
		Entries: []*Entry{
			{Request: Request{Method: "GET", URL: "https://x/a"}, Response: Response{Status: 200, BodyB64: EncodeBody([]byte("first"))}},
			{Request: Request{Method: "GET", URL: "https://x/a"}, Response: Response{Status: 200, BodyB64: EncodeBody([]byte("second"))}},
		},
	}
	idx := NewIndex(c)
	k := MakeKey("GET", "https://x/a", nil)

	e1, ok := idx.Lookup(k)
	if !ok || string(mustDecode(e1.Response.BodyB64)) != "first" {
		t.Fatalf("first lookup wrong: %+v ok=%v", e1, ok)
	}
	e2, ok := idx.Lookup(k)
	if !ok || string(mustDecode(e2.Response.BodyB64)) != "second" {
		t.Fatalf("second lookup wrong: %+v ok=%v", e2, ok)
	}
	// Beyond the recorded count, repeat the last one.
	e3, ok := idx.Lookup(k)
	if !ok || string(mustDecode(e3.Response.BodyB64)) != "second" {
		t.Fatalf("third lookup should repeat last: %+v ok=%v", e3, ok)
	}
}

func TestIndexLookupMiss(t *testing.T) {
	c := &Cassette{Version: FormatVersion, Entries: []*Entry{}}
	idx := NewIndex(c)
	if _, ok := idx.Lookup(MakeKey("GET", "https://nope/", nil)); ok {
		t.Fatal("expected miss")
	}
}

func mustDecode(s string) []byte {
	b, err := DecodeBody(s)
	if err != nil {
		panic(err)
	}
	return b
}
