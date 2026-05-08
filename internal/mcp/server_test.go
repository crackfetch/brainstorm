package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"strings"
	"sync"
	"testing"
	"time"
)

// runServerEcho starts a Server with a closed Browser (no real Chrome) and
// returns a client interface for sending requests + reading responses.
// Tools that need a browser will return an error message — that's fine for
// tools/list, initialize, and similar protocol-level tests.
func runServerEcho(t *testing.T) (sendFn func(string), readLine func() string, shutdown func()) {
	t.Helper()

	// A pre-closed Browser means any tool call needing the browser fails
	// fast with "browser is closed". Good for protocol tests.
	br := NewBrowser(BrowserOptions{})
	br.Close()

	srv := NewServer(Config{
		Browser:     br,
		IdleTimeout: 0,
		Logger:      log.New(io.Discard, "", 0),
	})

	pr, pw := io.Pipe()
	out := &threadSafeBuf{}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = srv.Run(ctx, pr, out)
		close(done)
	}()

	sendFn = func(line string) {
		if !strings.HasSuffix(line, "\n") {
			line += "\n"
		}
		_, _ = pw.Write([]byte(line))
	}
	readLine = func() string {
		// Poll the buffer until a full line shows up. Tests run with a
		// short deadline so this can't hang forever.
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if line, ok := out.popLine(); ok {
				return line
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("timeout waiting for response line")
		return ""
	}
	shutdown = func() {
		_ = pw.Close()
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("server did not exit within 2s of EOF")
		}
	}
	return
}

func TestInitializeReturnsProtocolVersion(t *testing.T) {
	send, read, shutdown := runServerEcho(t)
	defer shutdown()

	send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	line := read()

	var resp struct {
		ID     int              `json:"id"`
		Result initializeResult `json:"result"`
	}
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("parse: %v: %s", err, line)
	}
	if resp.Result.ProtocolVersion != protocolVersion {
		t.Errorf("got protocolVersion %q want %q", resp.Result.ProtocolVersion, protocolVersion)
	}
	if resp.Result.ServerInfo.Name != "brz-mcp" {
		t.Errorf("got serverInfo.Name %q want brz-mcp", resp.Result.ServerInfo.Name)
	}
	if _, ok := resp.Result.Capabilities["tools"]; !ok {
		t.Errorf("expected tools capability, got %v", resp.Result.Capabilities)
	}
}

func TestToolsListIncludesAllNineTools(t *testing.T) {
	send, read, shutdown := runServerEcho(t)
	defer shutdown()

	send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	line := read()

	var resp struct {
		Result toolsListResult `json:"result"`
	}
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("parse: %v: %s", err, line)
	}

	want := []string{
		"browser_goto", "browser_click", "browser_type", "browser_extract",
		"browser_screenshot", "browser_eval", "browser_wait_for",
		"browser_get_url", "browser_session_info",
	}
	got := map[string]bool{}
	for _, tl := range resp.Result.Tools {
		got[tl.Name] = true
		// every tool must have an inputSchema with type=object so MCP
		// clients can render forms.
		if tl.InputSchema["type"] != "object" {
			t.Errorf("tool %s: inputSchema.type %v, want object", tl.Name, tl.InputSchema["type"])
		}
	}
	if len(resp.Result.Tools) != len(want) {
		t.Errorf("got %d tools, want %d", len(resp.Result.Tools), len(want))
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("missing tool: %s", name)
		}
	}
}

func TestUnknownMethodReturnsMethodNotFound(t *testing.T) {
	send, read, shutdown := runServerEcho(t)
	defer shutdown()

	send(`{"jsonrpc":"2.0","id":3,"method":"does/not/exist"}`)
	line := read()

	var resp struct {
		Error *rpcError `json:"error"`
	}
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("parse: %v: %s", err, line)
	}
	if resp.Error == nil || resp.Error.Code != errMethodNotFound {
		t.Errorf("got %+v want method-not-found error", resp.Error)
	}
}

func TestParseErrorOnInvalidJSON(t *testing.T) {
	send, read, shutdown := runServerEcho(t)
	defer shutdown()

	send(`not json at all`)
	line := read()
	var resp struct {
		Error *rpcError `json:"error"`
	}
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("parse: %v: %s", err, line)
	}
	if resp.Error == nil || resp.Error.Code != errParseError {
		t.Errorf("got %+v want parse-error", resp.Error)
	}
}

func TestNotificationsInitializedReturnsNoResponse(t *testing.T) {
	send, _, shutdown := runServerEcho(t)
	defer shutdown()

	// No id => notification. Server must not respond.
	send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)

	// Drain briefly — there must be nothing to read.
	time.Sleep(50 * time.Millisecond)
}

// JSON-RPC 2.0 forbids responses to notifications. We treat any request
// without an `id` field as a notification, including standard methods.
func TestNotificationOfStandardMethodReturnsNoResponse(t *testing.T) {
	send, read, shutdown := runServerEcho(t)
	defer shutdown()

	// tools/list as a notification (no id) — must NOT respond.
	send(`{"jsonrpc":"2.0","method":"tools/list"}`)
	time.Sleep(50 * time.Millisecond)

	// Now send a real request and confirm we get exactly one response,
	// proving the prior notification produced none.
	send(`{"jsonrpc":"2.0","id":99,"method":"ping"}`)
	line := read()
	var resp struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("parse: %v: %s", err, line)
	}
	if resp.ID != 99 {
		t.Errorf("got id=%d want 99 (notification leaked through?)", resp.ID)
	}
}

func TestParseErrorIncludesIdNull(t *testing.T) {
	send, read, shutdown := runServerEcho(t)
	defer shutdown()

	send(`not json`)
	line := read()

	// JSON-RPC requires id present (null is fine) on error responses.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		t.Fatalf("parse: %v: %s", err, line)
	}
	id, ok := raw["id"]
	if !ok {
		t.Fatalf("response missing id field: %s", line)
	}
	if string(id) != "null" {
		t.Errorf("got id=%s want null", string(id))
	}
}

func TestBatchRequestsRejectedExplicitly(t *testing.T) {
	send, read, shutdown := runServerEcho(t)
	defer shutdown()

	send(`[{"jsonrpc":"2.0","id":1,"method":"ping"}]`)
	line := read()
	var resp struct {
		Error *rpcError `json:"error"`
	}
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("parse: %v: %s", err, line)
	}
	if resp.Error == nil || resp.Error.Code != errInvalidRequest {
		t.Errorf("got %+v want invalid-request for batch", resp.Error)
	}
}

func TestToolsCallUnknownToolReturnsInvalidParams(t *testing.T) {
	send, read, shutdown := runServerEcho(t)
	defer shutdown()

	send(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"nonexistent","arguments":{}}}`)
	line := read()
	var resp struct {
		Error *rpcError `json:"error"`
	}
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("parse: %v: %s", err, line)
	}
	if resp.Error == nil || resp.Error.Code != errInvalidParams {
		t.Errorf("got %+v want invalid-params", resp.Error)
	}
}

func TestToolErrorReturnsIsErrorTrue(t *testing.T) {
	// Browser is pre-closed in runServerEcho, so any browser-touching
	// tool will error. We expect a callToolResult shape with IsError=true,
	// not a JSON-RPC error envelope.
	send, read, shutdown := runServerEcho(t)
	defer shutdown()

	send(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"browser_goto","arguments":{"url":"https://example.com"}}}`)
	line := read()
	var resp struct {
		Result *callToolResult `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("parse: %v: %s", err, line)
	}
	if resp.Error != nil {
		t.Fatalf("expected tool-level error, got protocol error: %+v", resp.Error)
	}
	if resp.Result == nil || !resp.Result.IsError {
		t.Errorf("expected IsError=true, got %+v", resp.Result)
	}
}

func TestEOFTriggersCleanShutdown(t *testing.T) {
	br := NewBrowser(BrowserOptions{})
	defer br.Close()

	srv := NewServer(Config{Browser: br, Logger: log.New(io.Discard, "", 0)})

	pr, pw := io.Pipe()
	out := &threadSafeBuf{}

	done := make(chan error, 1)
	go func() {
		done <- srv.Run(context.Background(), pr, out)
	}()

	// Close stdin immediately. Server must return within 2s.
	_ = pw.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned error on EOF: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not return within 2s of EOF")
	}
}

// threadSafeBuf serializes writes; popLine pulls one newline-terminated line.
type threadSafeBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *threadSafeBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *threadSafeBuf) popLine() (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	data := b.buf.Bytes()
	idx := bytes.IndexByte(data, '\n')
	if idx < 0 {
		return "", false
	}
	line := string(data[:idx])
	rest := append([]byte(nil), data[idx+1:]...)
	b.buf.Reset()
	b.buf.Write(rest)
	return line, true
}
