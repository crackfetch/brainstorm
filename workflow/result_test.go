package workflow

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestActionResult_PageElementsField(t *testing.T) {
	r := ActionResult{
		OK:     false,
		Action: "login",
		Error:  "find element \"button.submit\"",
		PageElements: []ElementInfo{
			{Selector: "button.btn-submit", Tag: "button", Text: "Submit"},
			{Selector: "button.cancel", Tag: "button", Text: "Cancel"},
		},
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, `"page_elements"`) {
		t.Errorf("expected page_elements in JSON, got: %s", s)
	}
	if !strings.Contains(s, `"button.btn-submit"`) {
		t.Errorf("expected selector in page_elements, got: %s", s)
	}
}

func TestActionResult_PageElementsOmitEmpty(t *testing.T) {
	r := ActionResult{OK: true, Action: "login"}
	data, _ := json.Marshal(r)
	s := string(data)
	if strings.Contains(s, "page_elements") {
		t.Errorf("nil page_elements should be omitted, got: %s", s)
	}
}
