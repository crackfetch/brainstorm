package bundle

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteCreatesAllFiles(t *testing.T) {
	dir := t.TempDir()
	in := Inputs{
		Failure: Failure{
			WorkflowPath: "/tmp/wf.yaml",
			Action:       "login",
			StepName:     "click submit",
			Target:       ".does-not-exist",
			Error:        "element not found",
			BrzVersion:   "test",
		},
		WorkflowSource: []byte("name: test\nactions: {}\n"),
		Screenshot:     []byte("\x89PNG\r\n\x1a\n fake png"),
		DOMHTML:        "<html><body>oops</body></html>",
		ConsoleLog:     "[error] boom\n",
		EventsJSONL:    []byte(`{"event":"step_start"}` + "\n"),
		StderrLog:      []byte("some stderr\n"),
		EnvAllowlist:   map[string]string{"BRZ_DEBUG": "1"},
	}
	out, err := Write(Options{Dir: dir, WorkflowKey: "/some/path/login.yaml"}, in)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !strings.HasSuffix(out, ".tar.gz") {
		t.Fatalf("expected tar.gz path, got %s", out)
	}
	bundleDir := strings.TrimSuffix(out, ".tar.gz")
	for _, name := range []string{
		"failure.json", "workflow.yaml", "screenshot.png", "dom.html",
		"console.log", "events.jsonl", "stderr.log", "env.txt",
	} {
		p := filepath.Join(bundleDir, name)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	// failure.json should parse back.
	b, _ := os.ReadFile(filepath.Join(bundleDir, "failure.json"))
	var f Failure
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatalf("failure.json invalid: %v", err)
	}
	if f.GoVersion == "" || f.OS == "" || f.Timestamp == "" {
		t.Errorf("runtime fields not filled: %+v", f)
	}
	if f.Action != "login" {
		t.Errorf("action lost: %+v", f)
	}
	// Verify tar.gz is readable and contains the directory entries.
	verifyTarGz(t, out, "failure.json", "workflow.yaml")
}

func TestWriteWritesErrorSidecarsForBestEffortFailures(t *testing.T) {
	dir := t.TempDir()
	in := Inputs{
		Failure:       Failure{Error: "x"},
		ScreenshotErr: errors.New("page closed"),
		DOMErr:        errors.New("dom unreachable"),
	}
	out, err := Write(Options{Dir: dir, WorkflowKey: "wf"}, in)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	bd := strings.TrimSuffix(out, ".tar.gz")
	if _, err := os.Stat(filepath.Join(bd, "screenshot.png.error")); err != nil {
		t.Errorf("expected screenshot.png.error sidecar: %v", err)
	}
	if _, err := os.Stat(filepath.Join(bd, "dom.html.error")); err != nil {
		t.Errorf("expected dom.html.error sidecar: %v", err)
	}
	// failure.json must still be present.
	if _, err := os.Stat(filepath.Join(bd, "failure.json")); err != nil {
		t.Errorf("failure.json missing: %v", err)
	}
}

func TestCaptureEnvRedactsSecrets(t *testing.T) {
	t.Setenv("AWS_SECRET_ACCESS_KEY", "AKIA-leak-me")
	t.Setenv("GITHUB_TOKEN", "ghp_xxx")
	t.Setenv("BRZ_DEBUG", "1")
	t.Setenv("CHROME_PATH", "/opt/chrome")
	t.Setenv("DISPLAY", ":0")
	got := CaptureEnv()
	if _, ok := got["AWS_SECRET_ACCESS_KEY"]; ok {
		t.Errorf("CaptureEnv leaked AWS_SECRET_ACCESS_KEY")
	}
	if _, ok := got["GITHUB_TOKEN"]; ok {
		t.Errorf("CaptureEnv leaked GITHUB_TOKEN")
	}
	if got["BRZ_DEBUG"] != "1" {
		t.Errorf("BRZ_DEBUG missing/wrong: %q", got["BRZ_DEBUG"])
	}
	if got["CHROME_PATH"] != "/opt/chrome" {
		t.Errorf("CHROME_PATH missing/wrong: %q", got["CHROME_PATH"])
	}
	if got["DISPLAY"] != ":0" {
		t.Errorf("DISPLAY missing: %q", got["DISPLAY"])
	}
}

func TestCaptureEnvRedactsSensitiveAllowlistedKeys(t *testing.T) {
	// BRZ_*/CHROME_* prefix matches but name looks sensitive — must be redacted.
	t.Setenv("BRZ_TOKEN", "supersecret")
	t.Setenv("BRZ_API_KEY", "k-leak")
	t.Setenv("CHROME_PROXY_PASSWORD", "pw-leak")
	t.Setenv("BRZ_DEBUG", "1") // benign — should pass through
	got := CaptureEnv()
	for _, k := range []string{"BRZ_TOKEN", "BRZ_API_KEY", "CHROME_PROXY_PASSWORD"} {
		if got[k] != "<redacted>" {
			t.Errorf("%s should be redacted, got %q", k, got[k])
		}
	}
	if got["BRZ_DEBUG"] != "1" {
		t.Errorf("benign BRZ_DEBUG should pass through, got %q", got["BRZ_DEBUG"])
	}
}

func TestCaptureEnvPathFirstOnly(t *testing.T) {
	t.Setenv("PATH", "/first/bin:/second/bin:/third/bin")
	got := CaptureEnv()
	if got["PATH_FIRST"] != "/first/bin" {
		t.Errorf("PATH_FIRST = %q, want /first/bin", got["PATH_FIRST"])
	}
	if _, ok := got["PATH"]; ok {
		t.Errorf("full PATH leaked")
	}
}

func TestSafeName(t *testing.T) {
	cases := map[string]string{
		"/tmp/foo bar.yaml": "foo_bar",
		"login.yml":         "login",
		"":                  "",
		"weird/../$x":       "_x",
	}
	for in, want := range cases {
		if got := safeName(in); got != want {
			t.Errorf("safeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func verifyTarGz(t *testing.T, path string, mustHave ...string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer f.Close()
	gr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	seen := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar: %v", err)
		}
		seen[filepath.Base(hdr.Name)] = true
	}
	for _, name := range mustHave {
		if !seen[name] {
			t.Errorf("archive missing %s", name)
		}
	}
}
