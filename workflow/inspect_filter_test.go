package workflow

import "testing"

func TestFilterByTag(t *testing.T) {
	elements := []ElementInfo{
		{Selector: "input#email", Tag: "input", Type: "email", Name: "email"},
		{Selector: "button.submit", Tag: "button", Text: "Sign In"},
		{Selector: "a.forgot", Tag: "a", Text: "Forgot?", Href: "/reset"},
		{Selector: "select#country", Tag: "select", Name: "country"},
	}

	tests := []struct {
		name     string
		tags     []string
		expected int
	}{
		{"single tag", []string{"input"}, 1},
		{"multiple tags", []string{"input", "button"}, 2},
		{"no match", []string{"textarea"}, 0},
		{"empty filter", []string{}, 4},
		{"all tags", []string{"input", "button", "a", "select"}, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FilterByTag(elements, tt.tags)
			if len(result) != tt.expected {
				t.Errorf("FilterByTag(%v) returned %d elements, want %d", tt.tags, len(result), tt.expected)
			}
		})
	}
}

func TestFilterByName(t *testing.T) {
	elements := []ElementInfo{
		{Selector: "input#email", Tag: "input", Name: "email"},
		{Selector: "input#pw", Tag: "input", Name: "password"},
		{Selector: "button.submit", Tag: "button", Text: "Sign In"},
		{Selector: "input#phone", Tag: "input", Name: "phone"},
	}

	tests := []struct {
		name     string
		names    []string
		expected int
	}{
		{"single name", []string{"email"}, 1},
		{"multiple names", []string{"email", "password"}, 2},
		{"no match", []string{"address"}, 0},
		{"empty filter", []string{}, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FilterByName(elements, tt.names)
			if len(result) != tt.expected {
				t.Errorf("FilterByName(%v) returned %d elements, want %d", tt.names, len(result), tt.expected)
			}
		})
	}
}

func TestFilterComposition(t *testing.T) {
	elements := []ElementInfo{
		{Selector: "input#email", Tag: "input", Name: "email"},
		{Selector: "input#pw", Tag: "input", Name: "password"},
		{Selector: "button.submit", Tag: "button", Text: "Sign In"},
		{Selector: "select#role", Tag: "select", Name: "role"},
	}

	// Filter by tag=input AND name=email -> should get 1
	filtered := FilterByTag(elements, []string{"input"})
	filtered = FilterByName(filtered, []string{"email"})
	if len(filtered) != 1 {
		t.Errorf("composed filter returned %d elements, want 1", len(filtered))
	}
	if filtered[0].Selector != "input#email" {
		t.Errorf("expected input#email, got %s", filtered[0].Selector)
	}
}

func TestFilterPreservesFields(t *testing.T) {
	elements := []ElementInfo{
		{Selector: "input#email", Tag: "input", Type: "email", Name: "email", Placeholder: "Enter email", Hidden: false},
	}
	result := FilterByTag(elements, []string{"input"})
	if len(result) != 1 {
		t.Fatal("expected 1 element")
	}
	el := result[0]
	if el.Type != "email" || el.Name != "email" || el.Placeholder != "Enter email" {
		t.Error("filter stripped fields that should be preserved")
	}
}
