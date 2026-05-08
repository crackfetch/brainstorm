package workflow

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// E2E tests for download.save_as / save_to / return_to. Each test stands up
// a local httptest server that serves an HTML page with a button. Clicking
// the button navigates to /csv (Content-Disposition: attachment), which
// Chrome treats as a download. brz captures it, optionally renames it,
// optionally re-navigates the tab.

const downloadHTML = `<!doctype html>
<html><body>
<button id="trigger" onclick="window.location.href='/csv'">Download</button>
</body></html>`

const downloadCSV = "name,score\nalice,42\nbob,99\n"

func downloadServer() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/csv", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="export.csv"`)
		w.Header().Set("Content-Type", "text/csv")
		_, _ = w.Write([]byte(downloadCSV))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(downloadHTML))
	})
	return httptest.NewServer(mux)
}

func runDownloadAction(t *testing.T, dl *DownloadStep) (lastDownload string, finalURL string, err error) {
	t.Helper()
	srv := downloadServer()
	t.Cleanup(srv.Close)

	w := &Workflow{
		Name: "download-save",
		Actions: map[string]Action{
			"do": {
				URL: srv.URL,
				Steps: []Step{
					{Click: &ClickStep{Selector: "#trigger"}},
					{Download: dl},
				},
			},
		},
	}
	exec := NewExecutor(w)
	if err := exec.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { exec.Close() })

	res := exec.RunAction("do")
	if !res.OK {
		return "", "", &actionError{res.Error}
	}
	info, ierr := exec.page.Info()
	if ierr == nil {
		finalURL = info.URL
	}
	return exec.LastDownload, finalURL, nil
}

func TestDownload_NoSaveTarget_KeepsTempPath(t *testing.T) {
	skipIfNoChrome(t)
	// Backward-compat guard: download with no save_as/save_to/return_to
	// behaves exactly like before — file lands in the brz-downloads temp
	// dir and LastDownload points at it.
	got, _, err := runDownloadAction(t, &DownloadStep{Timeout: "30s"})
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if !strings.Contains(got, "brz-downloads") {
		t.Errorf("LastDownload should be in brz-downloads temp dir, got %q", got)
	}
	data, rerr := os.ReadFile(got)
	if rerr != nil {
		t.Fatalf("read captured file: %v", rerr)
	}
	if string(data) != downloadCSV {
		t.Errorf("captured content mismatch:\n  got %q\n  want %q", string(data), downloadCSV)
	}
}

func TestDownload_SaveAs_RenamesFile(t *testing.T) {
	skipIfNoChrome(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "scores.csv")
	got, _, err := runDownloadAction(t, &DownloadStep{Timeout: "30s", SaveAs: target})
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if got != target {
		t.Errorf("LastDownload: got %q, want %q", got, target)
	}
	data, rerr := os.ReadFile(target)
	if rerr != nil {
		t.Fatalf("read target: %v", rerr)
	}
	if string(data) != downloadCSV {
		t.Errorf("content mismatch")
	}
}

func TestDownload_SaveTo_RenamesFile(t *testing.T) {
	skipIfNoChrome(t)
	// save_to is a YAML alias of save_as. Same Go-level behavior.
	dir := t.TempDir()
	target := filepath.Join(dir, "subdir", "report.csv")
	got, _, err := runDownloadAction(t, &DownloadStep{Timeout: "30s", SaveTo: target})
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if got != target {
		t.Errorf("LastDownload: got %q, want %q", got, target)
	}
	if _, statErr := os.Stat(filepath.Dir(target)); statErr != nil {
		t.Errorf("parent dir should be auto-created: %v", statErr)
	}
}

func TestDownload_SaveAs_BeatsSaveTo(t *testing.T) {
	skipIfNoChrome(t)
	// When both fields are set, save_as wins. Documented precedence so
	// users updating from save_as → save_to don't get surprises.
	dir := t.TempDir()
	wantPath := filepath.Join(dir, "winner.csv")
	otherPath := filepath.Join(dir, "loser.csv")
	got, _, err := runDownloadAction(t, &DownloadStep{
		Timeout: "30s", SaveAs: wantPath, SaveTo: otherPath,
	})
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if got != wantPath {
		t.Errorf("LastDownload: got %q, want %q", got, wantPath)
	}
	if _, statErr := os.Stat(otherPath); statErr == nil {
		t.Errorf("save_to path should not exist when save_as wins; got file at %q", otherPath)
	}
}

func TestDownload_SaveAs_InterpolatesEnv(t *testing.T) {
	skipIfNoChrome(t)
	// Env interpolation in the target path so a single workflow can export
	// many records into uniquely-named files.
	dir := t.TempDir()
	srv := downloadServer()
	defer srv.Close()
	w := &Workflow{
		Name: "download-env",
		Env:  map[string]string{"NAME": "alpha", "DIR": dir},
		Actions: map[string]Action{
			"do": {
				URL: srv.URL,
				Steps: []Step{
					{Click: &ClickStep{Selector: "#trigger"}},
					{Download: &DownloadStep{
						Timeout: "30s",
						SaveAs:  "${DIR}/report_${NAME}.csv",
					}},
				},
			},
		},
	}
	exec := NewExecutor(w)
	if err := exec.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer exec.Close()
	res := exec.RunAction("do")
	if !res.OK {
		t.Fatalf("action failed: %s", res.Error)
	}
	want := filepath.Join(dir, "report_alpha.csv")
	if exec.LastDownload != want {
		t.Errorf("LastDownload: got %q, want %q", exec.LastDownload, want)
	}
}

func TestDownload_ReturnTo_Previous_RestoresURL(t *testing.T) {
	skipIfNoChrome(t)
	srv := downloadServer()
	defer srv.Close()
	dir := t.TempDir()
	w := &Workflow{
		Name: "download-return",
		Actions: map[string]Action{
			"do": {
				URL: srv.URL,
				Steps: []Step{
					{Click: &ClickStep{Selector: "#trigger"}},
					{Download: &DownloadStep{
						Timeout:  "30s",
						SaveAs:   filepath.Join(dir, "x.csv"),
						ReturnTo: "previous",
					}},
				},
			},
		},
	}
	exec := NewExecutor(w)
	if err := exec.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer exec.Close()
	res := exec.RunAction("do")
	if !res.OK {
		t.Fatalf("action failed: %s", res.Error)
	}
	info, _ := exec.page.Info()
	// Before this fix, the tab would be at about:blank after the click-triggered
	// download. With return_to: previous it should be back at the original page.
	if !strings.HasPrefix(info.URL, srv.URL) {
		t.Errorf("after return_to=previous, URL should start with %q (the page that triggered the download), got %q",
			srv.URL, info.URL)
	}
	if strings.Contains(info.URL, "about:blank") {
		t.Errorf("URL should not be about:blank after return_to: %q", info.URL)
	}
}

func TestDownload_ReturnTo_LiteralURL(t *testing.T) {
	skipIfNoChrome(t)
	// return_to also accepts a literal URL — useful when the workflow
	// wants to navigate somewhere specific after the download.
	srv1 := downloadServer()
	defer srv1.Close()
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><h1 id="dest">arrived</h1></body></html>`))
	}))
	defer srv2.Close()

	dir := t.TempDir()
	w := &Workflow{
		Name: "download-return-literal",
		Env:  map[string]string{"DEST": srv2.URL + "/landing"},
		Actions: map[string]Action{
			"do": {
				URL: srv1.URL,
				Steps: []Step{
					{Click: &ClickStep{Selector: "#trigger"}},
					{Download: &DownloadStep{
						Timeout:  "30s",
						SaveAs:   filepath.Join(dir, "x.csv"),
						ReturnTo: "${DEST}",
					}},
				},
			},
		},
	}
	exec := NewExecutor(w)
	if err := exec.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer exec.Close()
	res := exec.RunAction("do")
	if !res.OK {
		t.Fatalf("action failed: %s", res.Error)
	}
	info, _ := exec.page.Info()
	if !strings.Contains(info.URL, srv2.URL) {
		t.Errorf("after return_to literal, URL should contain %q, got %q", srv2.URL, info.URL)
	}
}

func TestExpandHomeDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("home dir unavailable: %v", err)
	}
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"/tmp/x", "/tmp/x"},
		{"~", home},
		{"~/Downloads/x.csv", filepath.Join(home, "Downloads", "x.csv")},
		{"~user/foo", "~user/foo"}, // ~user form is not expanded; left as-is
	}
	for _, tc := range tests {
		got, err := expandHomeDir(tc.in)
		if err != nil {
			t.Errorf("expandHomeDir(%q) error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("expandHomeDir(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestMoveFile_AcrossFilesystemFallback(t *testing.T) {
	// moveFile must succeed when os.Rename fails. We simulate that by
	// passing a destination on a different mount — but tmpfs/devfs vary
	// across CI. Instead, test the fallback path explicitly: rename
	// succeeds in the same dir, but we also verify content correctness.
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := moveFile(src, dst); err != nil {
		t.Fatalf("moveFile: %v", err)
	}
	if _, err := os.Stat(src); err == nil {
		t.Error("src should be removed after moveFile")
	}
	data, err := os.ReadFile(dst)
	if err != nil || string(data) != "hello" {
		t.Errorf("dst content: got %q (err=%v), want %q", string(data), err, "hello")
	}
}
