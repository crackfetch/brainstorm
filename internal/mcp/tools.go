package mcp

import (
	"encoding/json"
	"fmt"
)

// toolHandler executes a tool given its parsed arguments and returns the
// content blocks to wrap in a callToolResult.
type toolHandler func(b *Browser, args json.RawMessage) ([]contentBlock, error)

// registeredTool pairs metadata with its handler.
type registeredTool struct {
	def     toolDefinition
	handler toolHandler
}

// allTools enumerates every MCP tool the server exposes. Order is preserved
// in tools/list responses for deterministic introspection.
func allTools() []registeredTool {
	return []registeredTool{
		gotoTool(),
		clickTool(),
		typeTool(),
		extractTool(),
		screenshotTool(),
		evalTool(),
		waitForTool(),
		getURLTool(),
		sessionInfoTool(),
	}
}

// stringSchema is the most common JSON-schema fragment we emit.
func stringSchema(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}
func boolSchema(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}
func intSchema(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

func gotoTool() registeredTool {
	return registeredTool{
		def: toolDefinition{
			Name:        "browser_goto",
			Description: "Navigate the active page to a URL. Lazy-launches the browser on first call.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url":           stringSchema("Absolute URL or bare host (https:// added automatically)."),
					"wait_for_load": boolSchema("Block until the load event fires. Defaults to true."),
				},
				"required": []string{"url"},
			},
		},
		handler: func(b *Browser, raw json.RawMessage) ([]contentBlock, error) {
			var p struct {
				URL         string `json:"url"`
				WaitForLoad *bool  `json:"wait_for_load"`
			}
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, fmt.Errorf("parse args: %w", err)
			}
			wait := true
			if p.WaitForLoad != nil {
				wait = *p.WaitForLoad
			}
			out, err := b.Goto(p.URL, wait)
			if err != nil {
				return nil, err
			}
			return []contentBlock{jsonBlock(out)}, nil
		},
	}
}

func clickTool() registeredTool {
	return registeredTool{
		def: toolDefinition{
			Name:        "browser_click",
			Description: "Click the nth element matching a CSS selector.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"selector":   stringSchema("CSS selector."),
					"nth":        intSchema("0-indexed match to click. Defaults to 0."),
					"timeout_ms": intSchema("Selector wait timeout. Defaults to 5000."),
				},
				"required": []string{"selector"},
			},
		},
		handler: func(b *Browser, raw json.RawMessage) ([]contentBlock, error) {
			var p struct {
				Selector  string `json:"selector"`
				Nth       int    `json:"nth"`
				TimeoutMs int    `json:"timeout_ms"`
			}
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, fmt.Errorf("parse args: %w", err)
			}
			out, err := b.Click(p.Selector, p.Nth, p.TimeoutMs)
			if err != nil {
				return nil, err
			}
			return []contentBlock{jsonBlock(out)}, nil
		},
	}
}

func typeTool() registeredTool {
	return registeredTool{
		def: toolDefinition{
			Name:        "browser_type",
			Description: "Focus a selector and type text. Optionally clear existing input or press Enter after.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"selector":    stringSchema("CSS selector for an editable element."),
					"text":        stringSchema("Text to type."),
					"clear_first": boolSchema("Clear existing value before typing. Defaults to false."),
					"submit":      boolSchema("Press Enter after typing. Defaults to false."),
				},
				"required": []string{"selector", "text"},
			},
		},
		handler: func(b *Browser, raw json.RawMessage) ([]contentBlock, error) {
			var p struct {
				Selector   string `json:"selector"`
				Text       string `json:"text"`
				ClearFirst bool   `json:"clear_first"`
				Submit     bool   `json:"submit"`
			}
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, fmt.Errorf("parse args: %w", err)
			}
			out, err := b.Type(p.Selector, p.Text, p.ClearFirst, p.Submit)
			if err != nil {
				return nil, err
			}
			return []contentBlock{jsonBlock(out)}, nil
		},
	}
}

func extractTool() registeredTool {
	return registeredTool{
		def: toolDefinition{
			Name:        "browser_extract",
			Description: "Return text/html/attribute for elements matching a selector.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"selector":  stringSchema("CSS selector."),
					"attribute": stringSchema("Optional attribute name to capture. If empty, returns text+html."),
					"all":       boolSchema("Return all matches. Defaults to false (first match only)."),
				},
				"required": []string{"selector"},
			},
		},
		handler: func(b *Browser, raw json.RawMessage) ([]contentBlock, error) {
			var p struct {
				Selector  string `json:"selector"`
				Attribute string `json:"attribute"`
				All       bool   `json:"all"`
			}
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, fmt.Errorf("parse args: %w", err)
			}
			out, err := b.Extract(p.Selector, p.Attribute, p.All)
			if err != nil {
				return nil, err
			}
			return []contentBlock{jsonBlock(out)}, nil
		},
	}
}

func screenshotTool() registeredTool {
	return registeredTool{
		def: toolDefinition{
			Name:        "browser_screenshot",
			Description: "Capture a PNG screenshot. Returns an MCP image content block (base64).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"selector":  stringSchema("Optional CSS selector. If set, captures just that element."),
					"full_page": boolSchema("Capture full scrollable page (ignored when selector is set)."),
				},
			},
		},
		handler: func(b *Browser, raw json.RawMessage) ([]contentBlock, error) {
			var p struct {
				Selector string `json:"selector"`
				FullPage bool   `json:"full_page"`
			}
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &p); err != nil {
					return nil, fmt.Errorf("parse args: %w", err)
				}
			}
			b64, err := b.Screenshot(p.Selector, p.FullPage)
			if err != nil {
				return nil, err
			}
			return []contentBlock{imageBlock(b64)}, nil
		},
	}
}

func evalTool() registeredTool {
	return registeredTool{
		def: toolDefinition{
			Name:        "browser_eval",
			Description: "Evaluate a JavaScript expression in the page and return its JSON value. Wrap in Boolean(...) yourself if you need strict-bool semantics.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"expression": stringSchema("JS expression. Wrapped in an IIFE; use a `return` only if you write your own function body."),
				},
				"required": []string{"expression"},
			},
		},
		handler: func(b *Browser, raw json.RawMessage) ([]contentBlock, error) {
			var p struct {
				Expression string `json:"expression"`
			}
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, fmt.Errorf("parse args: %w", err)
			}
			out, err := b.Eval(p.Expression)
			if err != nil {
				return nil, err
			}
			return []contentBlock{jsonBlock(out)}, nil
		},
	}
}

func waitForTool() registeredTool {
	return registeredTool{
		def: toolDefinition{
			Name:        "browser_wait_for",
			Description: "Wait for a selector to be visible OR a JS expression to return truthy.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"selector":   stringSchema("CSS selector to wait for visibility on."),
					"expression": stringSchema("JS expression evaluated repeatedly until truthy. Wrapped in Boolean(...)."),
					"timeout_ms": intSchema("Total wait timeout. Defaults to 10000."),
				},
			},
		},
		handler: func(b *Browser, raw json.RawMessage) ([]contentBlock, error) {
			var p struct {
				Selector   string `json:"selector"`
				Expression string `json:"expression"`
				TimeoutMs  int    `json:"timeout_ms"`
			}
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, fmt.Errorf("parse args: %w", err)
			}
			out, err := b.WaitFor(p.Selector, p.Expression, p.TimeoutMs)
			if err != nil {
				return nil, err
			}
			return []contentBlock{jsonBlock(out)}, nil
		},
	}
}

func getURLTool() registeredTool {
	return registeredTool{
		def: toolDefinition{
			Name:        "browser_get_url",
			Description: "Return the current page URL and title.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		handler: func(b *Browser, _ json.RawMessage) ([]contentBlock, error) {
			out, err := b.GetURL()
			if err != nil {
				return nil, err
			}
			return []contentBlock{jsonBlock(out)}, nil
		},
	}
}

func sessionInfoTool() registeredTool {
	return registeredTool{
		def: toolDefinition{
			Name:        "browser_session_info",
			Description: "Return cookies count, current URL, viewport, and user agent.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		handler: func(b *Browser, _ json.RawMessage) ([]contentBlock, error) {
			out, err := b.SessionInfo()
			if err != nil {
				return nil, err
			}
			return []contentBlock{jsonBlock(out)}, nil
		},
	}
}
