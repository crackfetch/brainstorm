// Package mcp implements a minimal Model Context Protocol server over stdio.
//
// The implementation hand-rolls JSON-RPC 2.0 framing (newline-delimited JSON
// objects on stdin/stdout) and the small subset of MCP methods we need:
//
//   - initialize
//   - tools/list
//   - tools/call
//   - notifications/initialized (accepted, ignored)
//   - ping
//
// All log/diagnostic output goes to stderr so the JSON-RPC stream on stdout
// stays clean. Concurrent tool calls are serialized through a single mutex
// around the underlying browser handle.
package mcp

import "encoding/json"

// JSON-RPC 2.0 envelope types.

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	// ID is intentionally NOT omitempty: JSON-RPC 2.0 requires the id field
	// to be present on every response. For parse/invalid-request errors
	// where we cannot recover the client's id, this carries an explicit
	// `null` (we marshal nullID for that case).
	ID     json.RawMessage `json:"id"`
	Result any             `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

// nullID is the literal JSON null, used as the response id when we cannot
// recover the client's request id (e.g. on parse errors).
var nullID = json.RawMessage("null")

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Standard JSON-RPC + MCP error codes.
const (
	errParseError     = -32700
	errInvalidRequest = -32600
	errMethodNotFound = -32601
	errInvalidParams  = -32602
	errInternal       = -32603
)

// MCP protocol version we advertise. Clients negotiate but most accept this.
const protocolVersion = "2024-11-05"

// initializeResult is the response to the initialize handshake.
type initializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ServerInfo      serverInfo     `json:"serverInfo"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// toolDefinition is what we return from tools/list.
type toolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type toolsListResult struct {
	Tools []toolDefinition `json:"tools"`
}

// callToolParams is the input to tools/call.
type callToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// callToolResult is the response to tools/call.
//
// Per MCP spec, content is an array of typed content blocks (text, image, ...).
// IsError signals a tool-level (not protocol-level) failure; the LLM client
// presents the content as the failure detail.
type callToolResult struct {
	Content []contentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

// contentBlock is a tagged-union content element. Only the field matching
// Type should be populated.
type contentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`     // base64 for image
	MimeType string `json:"mimeType,omitempty"` // for image
}

func textBlock(s string) contentBlock {
	return contentBlock{Type: "text", Text: s}
}

func imageBlock(b64Png string) contentBlock {
	return contentBlock{Type: "image", Data: b64Png, MimeType: "image/png"}
}

func jsonBlock(v any) contentBlock {
	b, err := json.Marshal(v)
	if err != nil {
		return textBlock("{\"error\":\"marshal failed\"}")
	}
	return contentBlock{Type: "text", Text: string(b)}
}
