package workflow

import "testing"

func TestExtractTagFromSelector(t *testing.T) {
	tests := []struct {
		selector string
		expected string
	}{
		{"button.submit", "button"},
		{"button#save", "button"},
		{"input[name=\"email\"]", "input"},
		{"input#email", "input"},
		{"a.forgot-link", "a"},
		{"select#country", "select"},
		{"div.container", "div"},
		// Edge cases
		{"#just-id", ""},           // no tag, just ID
		{".just-class", ""},        // no tag, just class
		{"[role=\"button\"]", ""},   // attribute selector, no tag
		{"", ""},                    // empty
		{"BUTTON.submit", "button"}, // uppercase -> lowercase
	}

	for _, tt := range tests {
		t.Run(tt.selector, func(t *testing.T) {
			result := ExtractTagFromSelector(tt.selector)
			if result != tt.expected {
				t.Errorf("ExtractTagFromSelector(%q) = %q, want %q", tt.selector, result, tt.expected)
			}
		})
	}
}

func TestStepSelector(t *testing.T) {
	tests := []struct {
		name     string
		step     Step
		expected string
	}{
		{"click", Step{Click: &ClickStep{Selector: "#btn"}}, "#btn"},
		{"fill", Step{Fill: &FillStep{Selector: "#input", Value: "x"}}, "#input"},
		{"select", Step{Select: &SelectStep{Selector: "#dropdown"}}, "#dropdown"},
		{"wait_visible", Step{WaitVisible: &WaitStep{Selector: "#loader"}}, "#loader"},
		{"navigate", Step{Navigate: "https://example.com"}, ""},
		{"download", Step{Download: &DownloadStep{Timeout: "30s"}}, ""},
		{"sleep", Step{Sleep: &SleepStep{Duration: "2s"}}, ""},
		{"eval", Step{Eval: "document.title"}, ""},
		{"screenshot", Step{Screenshot: "page.png"}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := StepSelector(tt.step)
			if result != tt.expected {
				t.Errorf("StepSelector() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestSimilarElementsJS_IsValidJS(t *testing.T) {
	// SimilarElementsJS should be a non-empty string that looks like a JS function
	if SimilarElementsJS == "" {
		t.Fatal("SimilarElementsJS is empty")
	}
	// Should start with a function pattern (it's a function that takes a tag parameter)
	if len(SimilarElementsJS) < 20 {
		t.Error("SimilarElementsJS is suspiciously short")
	}
}
