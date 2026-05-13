// executor_attach_test.go covers Executor.HasLiveChrome and
// Executor.ConnectToExisting — the cross-process CDP attach path that lets a
// new process inherit a Chrome instance left behind by a prior, cleanly-exited
// process. Tests must not spawn a real Chrome; httptest stands in for the
// DevTools HTTP endpoint and a package-level connect hook stubs out the
// final rod attach.
package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-rod/rod"
)

// writePortFile creates <dir>/DevToolsActivePort with the canonical two-line
// payload Chrome writes ("<port>\n<ws-path>"). Returns the directory.
func writePortFile(t *testing.T, dir, port string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir profile dir: %v", err)
	}
	content := port + "\n/devtools/browser/abc\n"
	if err := os.WriteFile(filepath.Join(dir, "DevToolsActivePort"), []byte(content), 0o644); err != nil {
		t.Fatalf("write port file: %v", err)
	}
}

// freePort grabs an ephemeral TCP port and immediately releases it. Useful
// when we need a port number that is *probably* not listening.
func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().(*net.TCPAddr)
	_ = l.Close()
	return fmt.Sprintf("%d", addr.Port)
}

// newDevToolsStub returns an httptest server whose /json/version endpoint
// mimics the shape returned by Chrome's DevTools HTTP API. The returned
// host:port pair is what we write into DevToolsActivePort.
func newDevToolsStub(t *testing.T) (host, port string, srv *httptest.Server) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
		// rod's launcher.ResolveURL pulls webSocketDebuggerUrl from this
		// response and rewrites the host:port to the one it dialed.
		_ = json.NewEncoder(w).Encode(map[string]string{
			"Browser":              "Chrome/131.0.0.0",
			"Protocol-Version":     "1.3",
			"User-Agent":           "Mozilla/5.0",
			"webSocketDebuggerUrl": "ws://placeholder/devtools/browser/abc",
		})
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	u := strings.TrimPrefix(srv.URL, "http://")
	parts := strings.SplitN(u, ":", 2)
	if len(parts) != 2 {
		t.Fatalf("unexpected httptest url: %q", srv.URL)
	}
	return parts[0], parts[1], srv
}

// --- HasLiveChrome -----------------------------------------------------------

func TestHasLiveChrome_NoPortFile(t *testing.T) {
	dir := t.TempDir()
	e := &Executor{}
	if e.HasLiveChrome(dir) {
		t.Fatal("HasLiveChrome should be false when DevToolsActivePort is missing")
	}
}

func TestHasLiveChrome_EmptyProfileDir(t *testing.T) {
	e := &Executor{}
	if e.HasLiveChrome("") {
		t.Fatal("HasLiveChrome should be false for empty profile dir")
	}
}

func TestHasLiveChrome_PortFileButPortNotListening(t *testing.T) {
	dir := t.TempDir()
	writePortFile(t, dir, freePort(t))
	e := &Executor{}
	if e.HasLiveChrome(dir) {
		t.Fatal("HasLiveChrome should be false when the named port is not listening")
	}
}

func TestHasLiveChrome_PortFileMalformed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "DevToolsActivePort"), []byte("not-a-port"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	e := &Executor{}
	if e.HasLiveChrome(dir) {
		t.Fatal("HasLiveChrome should be false when port file content is unparseable")
	}
}

func TestHasLiveChrome_PortRespondsTo_jsonVersion(t *testing.T) {
	dir := t.TempDir()
	_, port, _ := newDevToolsStub(t)
	writePortFile(t, dir, port)

	e := &Executor{}
	if !e.HasLiveChrome(dir) {
		t.Fatal("HasLiveChrome should be true when /json/version responds")
	}
}

// --- ConnectToExisting -------------------------------------------------------

func TestConnectToExisting_NoPortFile(t *testing.T) {
	dir := t.TempDir()
	e := &Executor{}
	err := e.ConnectToExisting(dir)
	if err == nil {
		t.Fatal("expected error when DevToolsActivePort is missing")
	}
	if !strings.Contains(err.Error(), "DevToolsActivePort") {
		t.Fatalf("error should mention the missing port file, got: %v", err)
	}
}

func TestConnectToExisting_EmptyProfileDir(t *testing.T) {
	e := &Executor{}
	err := e.ConnectToExisting("")
	if err == nil {
		t.Fatal("expected error for empty profile dir")
	}
}

func TestConnectToExisting_PortUnresponsive(t *testing.T) {
	dir := t.TempDir()
	writePortFile(t, dir, freePort(t))

	e := &Executor{}
	err := e.ConnectToExisting(dir)
	if err == nil {
		t.Fatal("expected error when port is not listening")
	}
}

func TestConnectToExisting_HappyPath(t *testing.T) {
	dir := t.TempDir()
	_, port, _ := newDevToolsStub(t)
	writePortFile(t, dir, port)

	// Stub out the final rod attach so we don't need a real CDP-over-WS
	// implementation. We still exercise the real port-file read and the real
	// launcher.ResolveURL HTTP round-trip against the httptest server.
	prev := connectControlURLFunc
	t.Cleanup(func() { connectControlURLFunc = prev })

	var capturedURL string
	stubBrowser := &rod.Browser{}
	connectControlURLFunc = func(e *Executor, controlURL string) error {
		capturedURL = controlURL
		e.browser = stubBrowser
		return nil
	}

	e := &Executor{}
	if err := e.ConnectToExisting(dir); err != nil {
		t.Fatalf("ConnectToExisting: %v", err)
	}

	if e.browser != stubBrowser {
		t.Fatal("Executor.browser was not set by the connect hook")
	}
	if capturedURL == "" {
		t.Fatal("connect hook did not receive a control URL")
	}
	if !strings.HasPrefix(capturedURL, "ws://") {
		t.Fatalf("expected ws:// control URL, got %q", capturedURL)
	}
	if !strings.Contains(capturedURL, ":"+port) {
		t.Fatalf("control URL %q should point at the discovered port %s", capturedURL, port)
	}
}

func TestConnectToExisting_RodConnectError(t *testing.T) {
	dir := t.TempDir()
	_, port, _ := newDevToolsStub(t)
	writePortFile(t, dir, port)

	prev := connectControlURLFunc
	t.Cleanup(func() { connectControlURLFunc = prev })

	sentinel := errors.New("rod boom")
	connectControlURLFunc = func(e *Executor, controlURL string) error {
		return sentinel
	}

	e := &Executor{}
	err := e.ConnectToExisting(dir)
	if err == nil {
		t.Fatal("expected error from stubbed rod connect")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("error should wrap the rod connect error, got: %v", err)
	}
}
