package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// Server is the MCP JSON-RPC server. One Server per `brz mcp` process.
type Server struct {
	browser     *Browser
	tools       map[string]registeredTool
	toolList    []toolDefinition
	idleTimeout time.Duration

	// lastActivity is updated on every accepted request AND on response
	// completion; the idle watchdog reads it. Stored as UnixNano via
	// atomic for lock-free updates.
	lastActivity atomic.Int64

	// activeCalls counts in-flight tool/* requests so the idle watchdog
	// can avoid timing out while real work is still happening.
	activeCalls atomic.Int32

	// outMu serializes writes to stdout. JSON-RPC framing requires whole
	// messages on a single line; concurrent goroutines (e.g. idle watchdog)
	// must not interleave bytes.
	outMu sync.Mutex
	out   io.Writer

	logger *log.Logger
}

// Config configures the Server before Run.
type Config struct {
	Browser     *Browser
	IdleTimeout time.Duration // 0 = disabled
	Logger      *log.Logger   // defaults to stderr
}

// NewServer constructs a Server with the registered tools.
func NewServer(cfg Config) *Server {
	tools := allTools()
	m := make(map[string]registeredTool, len(tools))
	defs := make([]toolDefinition, 0, len(tools))
	for _, t := range tools {
		m[t.def.Name] = t
		defs = append(defs, t.def)
	}
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	return &Server{
		browser:     cfg.Browser,
		tools:       m,
		toolList:    defs,
		idleTimeout: cfg.IdleTimeout,
		logger:      logger,
	}
}

// Run reads JSON-RPC messages from in and writes responses to out until
// in returns EOF or ctx is canceled. Browser teardown happens in Run's
// caller (cmd/brz/mcp.go) so signal handlers also work.
func (s *Server) Run(ctx context.Context, in io.Reader, out io.Writer) error {
	s.out = out
	s.touch()

	// Idle watchdog: if configured, signals via idleTimedOut when the gap
	// between activity exceeds idleTimeout. Run translates that into a
	// clean (nil) return so the caller's defers can run.
	loopCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	idleTimedOut := make(chan struct{}, 1)
	if s.idleTimeout > 0 {
		go s.idleWatchdog(loopCtx, idleTimedOut)
	}

	// Wrap stdin in a Scanner with a generous buffer; some tool args can be
	// large (HTML payloads, base64 images on input). bufio.Scanner default
	// is only 64KB, which would silently truncate.
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	type lineMsg struct {
		line []byte
		err  error
	}
	lines := make(chan lineMsg, 1)
	go func() {
		defer close(lines)
		for scanner.Scan() {
			b := append([]byte(nil), scanner.Bytes()...)
			select {
			case lines <- lineMsg{line: b}:
			case <-loopCtx.Done():
				return
			}
		}
		if err := scanner.Err(); err != nil {
			select {
			case lines <- lineMsg{err: err}:
			case <-loopCtx.Done():
			}
		}
	}()

	for {
		select {
		case <-idleTimedOut:
			// Treat idle timeout as a clean exit; caller's defers (browser
			// teardown) must run, so we return nil rather than an error.
			return nil
		case <-loopCtx.Done():
			return loopCtx.Err()
		case msg, ok := <-lines:
			if !ok {
				// stdin EOF — clean shutdown.
				return nil
			}
			if msg.err != nil {
				return msg.err
			}
			if len(msg.line) == 0 {
				continue
			}
			s.touch()
			s.handleLine(msg.line)
			s.touch() // also after response so long calls don't look idle
		}
	}
}

func (s *Server) touch() {
	s.lastActivity.Store(time.Now().UnixNano())
}

// idleWatchdog cancels the read loop after idleTimeout of inactivity.
// activity is touched at request receipt AND at response completion, so
// long-running tool calls are not falsely classified idle while running.
// The poll interval is clamped to a minimum so very short timeouts (e.g.
// in tests) don't panic time.NewTicker with a zero duration.
func (s *Server) idleWatchdog(ctx context.Context, idleTimedOut chan<- struct{}) {
	tick := s.idleTimeout / 4
	if tick < 100*time.Millisecond {
		tick = 100 * time.Millisecond
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			// If a tool call is in flight, count that as activity.
			if s.activeCalls.Load() > 0 {
				s.touch()
				continue
			}
			last := time.Unix(0, s.lastActivity.Load())
			if now.Sub(last) >= s.idleTimeout {
				s.logger.Printf("idle timeout (%s) reached, shutting down", s.idleTimeout)
				select {
				case idleTimedOut <- struct{}{}:
				default:
				}
				return
			}
		}
	}
}

// handleLine parses one JSON-RPC envelope and dispatches it. Errors during
// parse become JSON-RPC error responses (with id:null); errors during
// dispatch become either tool-level errors (callToolResult.IsError=true)
// or protocol-level errors depending on the method.
//
// Notifications (no `id` field) are fire-and-forget per JSON-RPC 2.0:
// the server MUST NOT respond, even on error. We honor that for every
// method including unknown ones.
func (s *Server) handleLine(line []byte) {
	// Reject batch requests explicitly. The hand-rolled framing here is
	// one JSON object per line; supporting arrays would require a
	// different response strategy. Document this in README.
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		s.writeError(nullID, errInvalidRequest, "batch requests are not supported by brz mcp")
		return
	}

	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		s.writeError(nullID, errParseError, "parse error: "+err.Error())
		return
	}
	if req.JSONRPC != "2.0" {
		// Even invalid-request needs an id field; pass through whatever
		// we received (which may be null/missing).
		s.writeError(idOrNull(req.ID), errInvalidRequest, "jsonrpc must be 2.0")
		return
	}

	isNotification := len(req.ID) == 0
	id := idOrNull(req.ID)

	switch req.Method {
	case "initialize":
		if isNotification {
			return
		}
		s.writeResult(id, initializeResult{
			ProtocolVersion: protocolVersion,
			Capabilities: map[string]any{
				"tools": map[string]any{},
			},
			ServerInfo: serverInfo{
				Name:    "brz-mcp",
				Version: "0.1.0",
			},
		})
	case "notifications/initialized":
		// No response for notifications, ever.
		return
	case "ping":
		if isNotification {
			return
		}
		s.writeResult(id, map[string]any{})
	case "tools/list":
		if isNotification {
			return
		}
		s.writeResult(id, toolsListResult{Tools: s.toolList})
	case "tools/call":
		if isNotification {
			return
		}
		s.handleCallTool(id, req.Params)
	default:
		if isNotification {
			return
		}
		s.writeError(id, errMethodNotFound, "method not found: "+req.Method)
	}
}

// idOrNull returns the request id, defaulting to JSON null when missing.
func idOrNull(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return nullID
	}
	return id
}

func (s *Server) handleCallTool(id json.RawMessage, params json.RawMessage) {
	var p callToolParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.writeError(id, errInvalidParams, "invalid params: "+err.Error())
		return
	}
	tool, ok := s.tools[p.Name]
	if !ok {
		s.writeError(id, errInvalidParams, "unknown tool: "+p.Name)
		return
	}
	args := p.Arguments
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}

	s.activeCalls.Add(1)
	defer s.activeCalls.Add(-1)

	content, err := tool.handler(s.browser, args)
	if err != nil {
		// Distinguish argument-decoding errors (protocol-level: bad input
		// from the client) from execution errors (tool-level: the action
		// failed against the page). Handlers prefix decode errors with
		// "parse args:" so we can route them to -32602.
		if isArgParseError(err) {
			s.writeError(id, errInvalidParams, err.Error())
			return
		}
		// Tool-level execution error: success-shaped envelope, IsError=true.
		s.writeResult(id, callToolResult{
			Content: []contentBlock{textBlock("error: " + err.Error())},
			IsError: true,
		})
		return
	}
	s.writeResult(id, callToolResult{Content: content})
}

// isArgParseError reports whether err came from a tool's argument JSON
// unmarshaling (vs. a real execution failure). Matches the "parse args:"
// prefix every handler uses.
func isArgParseError(err error) bool {
	if err == nil {
		return false
	}
	const prefix = "parse args:"
	msg := err.Error()
	return len(msg) >= len(prefix) && msg[:len(prefix)] == prefix
}

func (s *Server) writeResult(id json.RawMessage, result any) {
	s.write(rpcResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func (s *Server) writeError(id json.RawMessage, code int, msg string) {
	s.write(rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: msg},
	})
}

func (s *Server) write(resp rpcResponse) {
	b, err := json.Marshal(resp)
	if err != nil {
		s.logger.Printf("marshal response: %v", err)
		return
	}
	s.outMu.Lock()
	defer s.outMu.Unlock()
	if _, err := s.out.Write(append(b, '\n')); err != nil {
		s.logger.Printf("write response: %v", err)
	}
}

// ErrIdleTimeout is reported when the idle watchdog fires.
var ErrIdleTimeout = errors.New("mcp: idle timeout")
