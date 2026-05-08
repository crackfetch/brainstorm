package workflow

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Tests for announceHeadedLaunch.
//
// Two layers:
//   - Pure unit tests (no browser launch) that pin the message format and
//     handling of empty/zero fields. Run on every machine.
//   - E2E tests behind skipIfNoChrome that exercise the actual launch paths
//     and verify headed announces / headless stays silent. These pin the
//     contract that motivated the bead — without them a refactor could
//     silently disable the announcement and our format tests would still
//     pass.

func TestAnnounceHeadedLaunch_FormatsLineWithPidAndExe(t *testing.T) {
	var buf bytes.Buffer
	e := &Executor{
		headed:     true,
		profileDir: "/tmp/test-profile",
	}
	e.SetAnnounceWriterForTest(&buf)
	e.announceHeadedLaunch(12345, "/Applications/Chromium.app/Contents/MacOS/Chromium")

	out := buf.String()
	for _, want := range []string{
		"[brz]",
		"Headed Chromium launched",
		"pid=12345",
		"exe=/Applications/Chromium.app/Contents/MacOS/Chromium",
		"profile=/tmp/test-profile",
		"Cmd+Tab",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("announce output missing %q\n  got: %s", want, out)
		}
	}
	if !strings.HasSuffix(strings.TrimRight(out, "\n"), "if not visible") {
		t.Errorf("announce should end with 'if not visible' guidance; got %q", out)
	}
}

func TestAnnounceHeadedLaunch_DefaultsForMissingFields(t *testing.T) {
	// Empty profile dir → "(default)" so the line still parses cleanly
	// for users running brz without WithProfileDir. Empty exe → "(unknown)".
	// Zero pid → omitted from the line entirely.
	var buf bytes.Buffer
	e := &Executor{headed: true}
	e.SetAnnounceWriterForTest(&buf)
	e.announceHeadedLaunch(0, "")

	out := buf.String()
	if !strings.Contains(out, "exe=(unknown)") {
		t.Errorf("empty exe should render as (unknown); got %q", out)
	}
	if !strings.Contains(out, "profile=(default)") {
		t.Errorf("empty profile should render as (default); got %q", out)
	}
	if strings.Contains(out, "pid=") {
		t.Errorf("zero pid should be omitted from line; got %q", out)
	}
}

func TestAnnounceHeadedLaunch_IsSingleLine(t *testing.T) {
	// A multi-line announcement would clutter consumers parsing brz logs
	// or grep'ing stderr. Pin to one line + trailing newline.
	var buf bytes.Buffer
	e := &Executor{headed: true}
	e.SetAnnounceWriterForTest(&buf)
	e.announceHeadedLaunch(99, "/path/to/chrome")

	lines := strings.Count(buf.String(), "\n")
	if lines != 1 {
		t.Errorf("expected exactly 1 newline (one line + trailing \\n), got %d in %q", lines, buf.String())
	}
}

// E2E: actually start the browser and verify the announcement gates on
// headed correctly. These test the contract the bead is paying for —
// previous tests only exercise the formatter, not the call sites.

func TestStart_Headless_NoAnnouncement(t *testing.T) {
	skipIfNoChrome(t)
	// A standard headless executor must NOT print the announcement —
	// that line is meaningful only when there's a visible window to look
	// for. False signals would train users to ignore the line.
	var buf bytes.Buffer
	exec := NewExecutor(&Workflow{Name: "t", Actions: map[string]Action{}})
	exec.SetAnnounceWriterForTest(&buf)
	if err := exec.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer exec.Close()

	if buf.Len() != 0 {
		t.Errorf("headless Start should be silent; got announcement: %q", buf.String())
	}
}

func TestStart_Headed_AnnouncesOnce(t *testing.T) {
	skipIfNoChrome(t)
	// Headed Start must emit exactly one announcement. Two would mean a
	// duplicate call site (e.g. both the launch and a stealth-injection
	// path firing the announce). Zero would mean a refactor disabled it.
	var buf bytes.Buffer
	exec := NewExecutor(&Workflow{Name: "t", Actions: map[string]Action{}}, WithHeaded(true))
	exec.SetAnnounceWriterForTest(&buf)
	if err := exec.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer exec.Close()

	out := buf.String()
	if !strings.Contains(out, "Headed Chromium launched") {
		t.Fatalf("headed Start did not announce; stderr buffer: %q", out)
	}
	count := strings.Count(out, "Headed Chromium launched")
	if count != 1 {
		t.Errorf("expected exactly 1 announcement, got %d:\n%s", count, out)
	}
	// Quick sanity: the actual launch info reached the formatter.
	if !strings.Contains(out, "pid=") {
		t.Errorf("real launch should include pid=...; got %q", out)
	}
}

func TestStart_Headed_NavigatesAfterAnnouncement(t *testing.T) {
	skipIfNoChrome(t)
	// After the announce, the browser must still be usable — make sure we
	// didn't accidentally consume some launch-time error path. Standing up
	// a tiny httptest server and navigating is the cheapest end-to-end
	// signal that "headed mode + announcement" doesn't break the executor.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><h1 id="ok">ok</h1></body></html>`))
	}))
	defer srv.Close()

	var buf bytes.Buffer
	exec := NewExecutor(&Workflow{Name: "t", Actions: map[string]Action{}}, WithHeaded(true))
	exec.SetAnnounceWriterForTest(&buf)
	if err := exec.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer exec.Close()
	if err := exec.NavigateTo(srv.URL); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	got := exec.page.MustEval(`() => document.querySelector('#ok').textContent`).String()
	if got != "ok" {
		t.Errorf("post-announcement navigation broke; got page text %q", got)
	}
}
