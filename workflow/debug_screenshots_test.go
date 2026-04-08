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
	// Default should be true (nil pointer means enabled)
	if w.DebugScreenshots != nil && !*w.DebugScreenshots {
		t.Error("debug_screenshots should default to enabled")
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

func TestDebugScreenshotsParsing_Enabled(t *testing.T) {
	yaml := `
name: test-enabled
debug_screenshots: true
actions:
  test:
    steps:
      - eval: "1+1"
`
	w, err := LoadFromBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if w.DebugScreenshots == nil || !*w.DebugScreenshots {
		t.Error("debug_screenshots should be true")
	}
}

func TestDebugScreenshotsEnabled(t *testing.T) {
	// nil = enabled (default)
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

func TestActionResultHasBeforeScreenshot(t *testing.T) {
	r := &ActionResult{
		OK:               false,
		ScreenshotBefore: "/tmp/before.jpg",
		Screenshot:       "/tmp/after.png",
	}
	if r.ScreenshotBefore == "" {
		t.Error("expected before screenshot path")
	}
}
