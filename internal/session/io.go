package session

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// Encode writes a bundle to w as pretty-printed JSON with a trailing
// newline. Pretty output is intentional: bundles are sometimes inspected
// by humans before being piped into `brz session import`.
func Encode(w io.Writer, b *Bundle) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(b)
}

// Decode reads a bundle from r and validates the version field.
func Decode(r io.Reader) (*Bundle, error) {
	var b Bundle
	dec := json.NewDecoder(r)
	if err := dec.Decode(&b); err != nil {
		return nil, fmt.Errorf("decode session bundle: %w", err)
	}
	if b.Version == 0 {
		return nil, fmt.Errorf("session bundle missing 'version' field")
	}
	if b.Version > FormatVersion {
		return nil, fmt.Errorf("session bundle version %d is newer than this brz supports (max %d)", b.Version, FormatVersion)
	}
	return &b, nil
}

// WriteFile writes a bundle to path with mode 0600. The file is
// created with O_TRUNC so re-exporting over an existing file does
// not leak old contents through a partial overwrite.
//
// 0600 is non-negotiable: a session bundle contains live auth
// cookies. Anyone who can read it can hijack the session.
//
// We call Chmod after open because os.OpenFile only applies the
// mode argument when creating a new file. If the path already
// existed with looser permissions (e.g. 0644 from a prior tool),
// O_CREATE is a no-op and the bundle would inherit those perms.
func WriteFile(path string, b *Bundle) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Chmod(0600); err != nil {
		return err
	}
	return Encode(f, b)
}

// ReadFile reads a bundle from path.
func ReadFile(path string) (*Bundle, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Decode(f)
}
