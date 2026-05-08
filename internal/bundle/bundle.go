// Package bundle writes a self-contained forensics bundle when a brz run
// fails. The bundle is a directory + a sibling .tar.gz, suitable for
// attaching to a bug report. See docs/failure-bundles.md.
package bundle

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Failure is the structured failure descriptor written to failure.json.
// Field names are stable; downstream tooling parses this.
type Failure struct {
	WorkflowPath     string   `json:"workflow_path"`
	StepName         string   `json:"step_name,omitempty"`
	StepType         string   `json:"step_type,omitempty"`
	FailedStep       int      `json:"failed_step,omitempty"`
	Action           string   `json:"action,omitempty"`
	Target           string   `json:"target,omitempty"`
	Error            string   `json:"error"`
	ErrorChain       []string `json:"error_chain,omitempty"`
	RetriesAttempted int      `json:"retries_attempted"`
	Timestamp        string   `json:"ts"`
	BrzVersion       string   `json:"brz_version"`
	GoVersion        string   `json:"go_version"`
	OS               string   `json:"os"`
	BrowserVersion   string   `json:"browser_version,omitempty"`
}

// Inputs bundles everything the writer needs from the executor.
// All fields are best-effort — empty/nil values produce ".error" sidecars
// or are simply skipped. failure.json is always written.
type Inputs struct {
	Failure         Failure
	WorkflowSource  []byte // raw bytes of the workflow file (file copy, not re-serialized)
	Screenshot      []byte
	ScreenshotErr   error
	DOMHTML         string
	DOMErr          error
	ConsoleLog      string // newline-joined captured console messages
	ConsoleErr      error
	EventsJSONL     []byte
	StderrLog       []byte
	EnvAllowlist    map[string]string // already-redacted snapshot
}

// Options configures where bundles are written.
type Options struct {
	Dir         string // root directory; default: ~/.brz/failures
	WorkflowKey string // human-readable workflow name (for path)
}

// DefaultDir returns the default bundle root, honoring BRZ_BUNDLE_DIR.
func DefaultDir() string {
	if d := os.Getenv("BRZ_BUNDLE_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "brz-failures")
	}
	return filepath.Join(home, ".brz", "failures")
}

// EnvAllowlistKeys is the only set of env vars permitted in env.txt.
// Anything else risks leaking secrets and is excluded.
var EnvAllowlistKeys = []string{
	"DISPLAY",
	"WAYLAND_DISPLAY",
	"OS",
}

// secretSubstrings are case-insensitive substrings that mark a key as
// likely-sensitive. Any matching env var is REDACTED to "<redacted>" in
// env.txt, even if its prefix would otherwise be allowed (BRZ_*, CHROME_*).
// Keep this list conservative — false positives are cheap, false negatives
// can leak credentials into bug reports.
var secretSubstrings = []string{
	"TOKEN", "PASS", "PASSWORD", "SECRET", "KEY", "AUTH", "COOKIE",
	"CREDENTIAL", "PRIVATE", "BEARER", "SESSION", "API_KEY",
}

// looksSensitive returns true if k contains any of secretSubstrings (case-insensitive).
func looksSensitive(k string) bool {
	upper := strings.ToUpper(k)
	for _, s := range secretSubstrings {
		if strings.Contains(upper, s) {
			return true
		}
	}
	return false
}

// CaptureEnv builds a redacted env snapshot. Only allowlisted keys plus
// any var matching the BRZ_* or CHROME_* prefix are included. PATH is
// reduced to its first entry. Variables whose name looks sensitive
// (TOKEN/PASS/SECRET/KEY/AUTH/COOKIE/...) are reduced to "<redacted>" so a
// well-intentioned BRZ_* or CHROME_* prefix can't smuggle a credential out.
func CaptureEnv() map[string]string {
	out := map[string]string{}
	add := func(k, v string) {
		if v == "" {
			return
		}
		if looksSensitive(k) {
			out[k] = "<redacted>"
			return
		}
		out[k] = v
	}
	for _, k := range EnvAllowlistKeys {
		add(k, os.Getenv(k))
	}
	for _, kv := range os.Environ() {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		k := kv[:eq]
		if strings.HasPrefix(k, "BRZ_") || strings.HasPrefix(k, "CHROME_") {
			add(k, kv[eq+1:])
		}
	}
	if p := os.Getenv("PATH"); p != "" {
		first := p
		if i := strings.IndexAny(p, ":;"); i > 0 {
			first = p[:i]
		}
		out["PATH_FIRST"] = first
	}
	return out
}

// FillRuntime sets runtime fields on the failure that the writer can derive
// (go version, OS, timestamp). Caller is responsible for brz_version.
func (f *Failure) FillRuntime() {
	if f.Timestamp == "" {
		f.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	f.GoVersion = runtime.Version()
	f.OS = runtime.GOOS + "/" + runtime.GOARCH
}

// Write builds the bundle directory and tar.gz archive.
// Returns the absolute path of the .tar.gz on success.
// On any best-effort write failure (screenshot/dom/etc) it writes a
// "<file>.error" sidecar and continues. failure.json is mandatory; if
// that write fails the function returns an error and the caller MUST
// preserve the original failure exit code.
func Write(opts Options, in Inputs) (string, error) {
	root := opts.Dir
	if root == "" {
		root = DefaultDir()
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("create bundle root: %w", err)
	}

	stamp := time.Now().UTC().Format("20060102-150405")
	name := safeName(opts.WorkflowKey)
	if name == "" {
		name = "workflow"
	}
	hash := shortHash(stamp + name + in.Failure.Error)
	dir := filepath.Join(root, fmt.Sprintf("%s-%s-%s", stamp, name, hash))

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create bundle dir: %w", err)
	}

	// Mandatory: failure.json
	in.Failure.FillRuntime()
	failureBytes, err := json.MarshalIndent(in.Failure, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal failure: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "failure.json"), failureBytes, 0o644); err != nil {
		return "", fmt.Errorf("write failure.json: %w", err)
	}

	// Best-effort artifacts. errSide writes "<name>.error" with the reason.
	errSide := func(name string, e error) {
		_ = os.WriteFile(filepath.Join(dir, name+".error"), []byte(e.Error()+"\n"), 0o644)
	}

	if len(in.WorkflowSource) > 0 {
		if e := os.WriteFile(filepath.Join(dir, "workflow.yaml"), in.WorkflowSource, 0o644); e != nil {
			errSide("workflow.yaml", e)
		}
	} else {
		errSide("workflow.yaml", fmt.Errorf("workflow source not available"))
	}

	if in.ScreenshotErr != nil {
		errSide("screenshot.png", in.ScreenshotErr)
	} else if len(in.Screenshot) > 0 {
		if e := os.WriteFile(filepath.Join(dir, "screenshot.png"), in.Screenshot, 0o644); e != nil {
			errSide("screenshot.png", e)
		}
	}

	if in.DOMErr != nil {
		errSide("dom.html", in.DOMErr)
	} else if in.DOMHTML != "" {
		if e := os.WriteFile(filepath.Join(dir, "dom.html"), []byte(in.DOMHTML), 0o644); e != nil {
			errSide("dom.html", e)
		}
	}

	writeBest := func(name string, data []byte) {
		if len(data) == 0 {
			return
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			errSide(name, err)
		}
	}

	if in.ConsoleErr != nil {
		errSide("console.log", in.ConsoleErr)
	} else {
		writeBest("console.log", []byte(in.ConsoleLog))
	}
	writeBest("events.jsonl", in.EventsJSONL)
	writeBest("stderr.log", in.StderrLog)
	if len(in.EnvAllowlist) > 0 {
		writeBest("env.txt", []byte(formatEnv(in.EnvAllowlist)))
	}

	// tar.gz the directory next to it.
	archive := dir + ".tar.gz"
	if err := tarGzDir(dir, archive); err != nil {
		// Archive failed — directory is still on disk, point caller at it.
		return dir, fmt.Errorf("tar.gz: %w", err)
	}
	return archive, nil
}

// safeName strips path separators and dot components from a workflow path
// to make it safe for use in a directory name.
func safeName(s string) string {
	s = filepath.Base(s)
	s = strings.TrimSuffix(s, filepath.Ext(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

func shortHash(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}

func formatEnv(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(m[k])
		b.WriteString("\n")
	}
	return b.String()
}

func tarGzDir(srcDir, dstFile string) error {
	out, err := os.Create(dstFile)
	if err != nil {
		return err
	}
	defer out.Close()
	gw := gzip.NewWriter(out)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	base := filepath.Base(srcDir)
	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(filepath.Join(base, rel))
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, f)
		f.Close()
		return err
	})
}
