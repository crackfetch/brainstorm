package workflow

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCompactElementInfo(t *testing.T) {
	el := ElementInfo{
		Selector:    "input#email",
		Tag:         "input",
		Type:        "email",
		Name:        "email",
		Placeholder: "Enter email",
		Href:        "https://example.com",
		Value:       "hidden-val",
		Role:        "textbox",
		Hidden:      true,
	}

	compact := CompactElement(el)

	// Should keep: selector, tag, type, name, text
	if compact.Selector != "input#email" {
		t.Error("compact should preserve selector")
	}
	if compact.Tag != "input" {
		t.Error("compact should preserve tag")
	}
	if compact.Type != "email" {
		t.Error("compact should preserve type")
	}
	if compact.Name != "email" {
		t.Error("compact should preserve name")
	}

	// Should strip: placeholder, href, value, role, hidden
	if compact.Placeholder != "" {
		t.Error("compact should strip placeholder")
	}
	if compact.Href != "" {
		t.Error("compact should strip href")
	}
	if compact.Value != "" {
		t.Error("compact should strip value")
	}
	if compact.Role != "" {
		t.Error("compact should strip role")
	}
	if compact.Hidden {
		t.Error("compact should strip hidden")
	}
}

func TestCompactPreservesText(t *testing.T) {
	el := ElementInfo{
		Selector: "button.submit",
		Tag:      "button",
		Text:     "Sign In",
	}
	compact := CompactElement(el)
	if compact.Text != "Sign In" {
		t.Error("compact should preserve text")
	}
}

func TestCompactElements(t *testing.T) {
	elements := []ElementInfo{
		{Selector: "input#a", Tag: "input", Type: "text", Placeholder: "Name", Role: "textbox"},
		{Selector: "button#b", Tag: "button", Text: "OK", Href: "/submit"},
	}
	result := CompactElements(elements)
	if len(result) != 2 {
		t.Fatalf("expected 2 elements, got %d", len(result))
	}
	// Verify stripped fields don't appear in JSON
	data, _ := json.Marshal(result[0])
	s := string(data)
	if strings.Contains(s, `"placeholder"`) || strings.Contains(s, `"role"`) {
		t.Errorf("compact JSON should not contain stripped fields: %s", s)
	}
}
