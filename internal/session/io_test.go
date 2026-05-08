package session

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func sampleBundle() *Bundle {
	return &Bundle{
		Version:    FormatVersion,
		ExportedAt: time.Date(2026, 5, 7, 12, 34, 56, 0, time.UTC),
		BrzVersion: "0.13.0-test",
		Domains:    []string{"example.com"},
		Cookies: []Cookie{
			{
				Name: "sid", Value: "abc123",
				Domain: "example.com", Path: "/",
				Secure: true, HTTPOnly: true,
				SameSite: "Lax", Expires: 1893456000,
			},
		},
		LocalStorage: map[string]map[string]string{
			"https://example.com": {"k": "v"},
		},
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	in := sampleBundle()
	var buf bytes.Buffer
	if err := Encode(&buf, in); err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, err := Decode(&buf)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(in.Cookies, out.Cookies) {
		t.Errorf("cookies mismatch:\n in=%+v\nout=%+v", in.Cookies, out.Cookies)
	}
	if !reflect.DeepEqual(in.LocalStorage, out.LocalStorage) {
		t.Errorf("localStorage mismatch:\n in=%+v\nout=%+v", in.LocalStorage, out.LocalStorage)
	}
	if out.Version != in.Version {
		t.Errorf("version: got %d, want %d", out.Version, in.Version)
	}
}

func TestDecodeRejectsMissingVersion(t *testing.T) {
	r := bytes.NewBufferString(`{"cookies":[]}`)
	if _, err := Decode(r); err == nil {
		t.Fatal("expected error on missing version")
	}
}

func TestDecodeRejectsFutureVersion(t *testing.T) {
	r := bytes.NewBufferString(`{"version":99999,"cookies":[]}`)
	if _, err := Decode(r); err == nil {
		t.Fatal("expected error on future version")
	}
}

func TestWriteFileMode0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission semantics don't apply on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "bundle.json")
	if err := WriteFile(path, sampleBundle()); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	mode := st.Mode().Perm()
	if mode != 0600 {
		t.Errorf("got mode %#o, want 0600 (a session bundle is sensitive)", mode)
	}
}

func TestWriteFileChmodsExistingLoosePerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission semantics don't apply on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "bundle.json")
	// Create a pre-existing file with world-readable perms — the
	// scenario codex flagged: O_CREATE is a no-op when the file
	// already exists, so without an explicit Chmod the bundle
	// inherits 0644 and leaks secrets to other users on the box.
	if err := os.WriteFile(path, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, sampleBundle()); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	st, _ := os.Stat(path)
	if got := st.Mode().Perm(); got != 0600 {
		t.Errorf("WriteFile did not tighten perms on existing file: got %#o, want 0600", got)
	}
}

func TestWriteFileTruncatesOldContents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bundle.json")
	// Pre-populate with a long string.
	if err := os.WriteFile(path, bytes.Repeat([]byte("X"), 4096), 0600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, sampleBundle()); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, _ := os.ReadFile(path)
	if bytes.Contains(got, []byte("XXXX")) {
		t.Error("WriteFile did not truncate old contents — bundle may leak prior secret data")
	}
}
