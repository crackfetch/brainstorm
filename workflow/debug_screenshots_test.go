package workflow

import (
	"testing"
)

func TestDebugScreenshotsParsing_Default(t *testing.T) {
	yaml := `
name: test-default
actions:
  test:
    steps:
      - eval: "1+1"
`
	w, err := LoadFromBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if w.DebugScreenshots != nil && !*w.DebugScreenshots {
		t.Error("debug_screenshots should default to enabled (nil)")
	}
}

func TestDebugScreenshotsParsing_Disabled(t *testing.T) {
	yaml := `
name: test-disabled
debug_screenshots: false
actions:
  test:
    steps:
      - eval: "1+1"
`
	w, err := LoadFromBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if w.DebugScreenshots == nil {
		t.Fatal("expected debug_screenshots to be set")
	}
	if *w.DebugScreenshots {
		t.Error("debug_screenshots should be false")
	}
}

func TestDebugScreenshotsEnabled(t *testing.T) {
	// nil = enabled (default behavior)
	if !debugScreenshotsEnabled(nil) {
		t.Error("nil should mean enabled")
	}

	tr := true
	if !debugScreenshotsEnabled(&tr) {
		t.Error("true should mean enabled")
	}

	fa := false
	if debugScreenshotsEnabled(&fa) {
		t.Error("false should mean disabled")
	}
}

func TestCaptureJPEG_NoPage(t *testing.T) {
	// captureJPEG should return nil when no browser page exists,
	// not panic. This happens when the browser crashed or never launched.
	exec := &Executor{}
	data := exec.captureJPEG()
	if data != nil {
		t.Error("expected nil from captureJPEG with no page")
	}
}
