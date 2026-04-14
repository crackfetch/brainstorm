package workflow

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInspectResult_ScreenshotField(t *testing.T) {
	r := InspectResult{
		OK:         true,
		URL:        "https://example.com",
		Screenshot: "/tmp/screenshot.png",
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, `"screenshot":"/tmp/screenshot.png"`) {
		t.Errorf("expected screenshot in JSON, got: %s", s)
	}
}

func TestInspectResult_ScreenshotOmitEmpty(t *testing.T) {
	r := InspectResult{OK: true, URL: "https://example.com"}
	data, _ := json.Marshal(r)
	s := string(data)
	if strings.Contains(s, "screenshot") {
		t.Errorf("empty screenshot should be omitted, got: %s", s)
	}
}

func TestInspectResult_EvalResultField(t *testing.T) {
	r := InspectResult{
		OK:         true,
		URL:        "https://example.com",
		EvalResult: "Example Domain",
	}
	data, _ := json.Marshal(r)
	s := string(data)
	if !strings.Contains(s, `"eval_result":"Example Domain"`) {
		t.Errorf("expected eval_result in JSON, got: %s", s)
	}
}

func TestInspectResult_EvalResultOmitEmpty(t *testing.T) {
	r := InspectResult{OK: true, URL: "https://example.com"}
	data, _ := json.Marshal(r)
	s := string(data)
	if strings.Contains(s, "eval_result") {
		t.Errorf("nil eval_result should be omitted, got: %s", s)
	}
}
