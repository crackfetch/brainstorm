// Package baseline implements site-drift detection for brz workflows.
//
// A baseline is a JSON snapshot of selector hits captured during a
// successful workflow run. On a later run, brz compares live observations
// against the baseline and surfaces drift — selectors whose match counts
// changed, selectors that no longer match, or matches whose first-element
// text fingerprint changed.
//
// The format is intentionally tolerant: unknown fields in a baseline file
// are ignored so a newer baseline written by a future brz can still be
// read (with the unknown signals quietly skipped) by an older binary.
package baseline

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SchemaVersion is the current baseline file schema version. Bumped when
// a backwards-incompatible change is made. Readers tolerate older versions
// (missing fields default to zero) and newer versions (unknown fields are
// ignored — encoding/json drops them by default).
const SchemaVersion = 1

// StepObservation captures the per-step signals brz records for drift
// detection. Only steps with a selector record matched_count / sample
// hashes; non-selector steps (navigate, sleep, eval, screenshot) record
// just name + action + ok so the baseline keeps step-order context.
type StepObservation struct {
	// Name is the step's label if set, otherwise "step N".
	Name string `json:"name"`
	// Action is the step type as reported by stepType() (click, fill, ...).
	Action string `json:"action"`
	// Target is the navigate URL for navigate steps. Empty otherwise.
	Target string `json:"target,omitempty"`
	// Selector is the CSS selector probed for this step (if any).
	Selector string `json:"selector,omitempty"`
	// MatchedCount is the number of elements the selector resolved to.
	// Captured from rod's Elements() — zero is meaningful (the selector
	// matched nothing), so we use a pointer to distinguish "not measured"
	// from "measured zero" in the JSON encoding.
	MatchedCount *int `json:"matched_count,omitempty"`
	// SampleTextHash is sha256(strings.ToLower(strings.TrimSpace(firstMatch.Text())))
	// prefixed "sha256:". Only set when a selector matched at least one
	// element. Used as a cheap "page contents look completely different"
	// fingerprint.
	SampleTextHash string `json:"sample_text_hash,omitempty"`
	// OK is true if the step succeeded.
	OK bool `json:"ok"`
}

// Baseline is the on-disk snapshot of one successful run.
type Baseline struct {
	Version    int               `json:"version"`
	Workflow   string            `json:"workflow"`
	Action     string            `json:"action,omitempty"`
	CapturedAt string            `json:"captured_at"`
	BrzVersion string            `json:"brz_version"`
	Steps      []StepObservation `json:"steps"`
}

// New constructs a Baseline ready to be written. capturedAt is normally
// time.Now().UTC().
func New(workflow, action, brzVersion string, capturedAt time.Time, steps []StepObservation) *Baseline {
	return &Baseline{
		Version:    SchemaVersion,
		Workflow:   workflow,
		Action:     action,
		CapturedAt: capturedAt.UTC().Format(time.RFC3339),
		BrzVersion: brzVersion,
		Steps:      append([]StepObservation(nil), steps...),
	}
}

// Write serializes the baseline as pretty JSON to w.
func (b *Baseline) Write(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(b)
}

// WriteFile writes the baseline to path, creating any missing parent
// directories.
func (b *Baseline) WriteFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create baseline dir: %w", err)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create baseline file: %w", err)
	}
	defer f.Close()
	return b.Write(f)
}

// Read parses a baseline from r. Unknown fields are ignored so files
// written by newer brz versions stay readable.
func Read(r io.Reader) (*Baseline, error) {
	var b Baseline
	dec := json.NewDecoder(r)
	if err := dec.Decode(&b); err != nil {
		return nil, fmt.Errorf("decode baseline: %w", err)
	}
	return &b, nil
}

// ReadFile parses a baseline from a file path.
func ReadFile(path string) (*Baseline, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open baseline: %w", err)
	}
	defer f.Close()
	return Read(f)
}

// ResolvePath returns the baseline file path for a workflow. If userArg
// is "auto", the path is `<workflow-dir>/.brz-baselines/<basename>.baseline.json`
// (relative to the workflow file). Otherwise userArg is returned unchanged.
func ResolvePath(workflowPath, userArg string) string {
	if userArg != "auto" {
		return userArg
	}
	dir := filepath.Dir(workflowPath)
	base := filepath.Base(workflowPath)
	// Strip extension(s) — fetch-orders.yaml -> fetch-orders.
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	return filepath.Join(dir, ".brz-baselines", stem+".baseline.json")
}

// HashText returns the canonical sample-text hash used in baselines.
// Trim, lowercase, sha256, hex, with a "sha256:" prefix. Empty input
// returns "" (so an empty Text() doesn't yield a misleading fingerprint).
func HashText(s string) string {
	trimmed := strings.ToLower(strings.TrimSpace(s))
	if trimmed == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(trimmed))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// IntPtr is a small constructor helper for the *int matched_count field.
func IntPtr(n int) *int { return &n }
