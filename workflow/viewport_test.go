package workflow

import (
	"testing"
)

func TestViewportDefaults(t *testing.T) {
	vp := DefaultViewport()
	if vp.Width != 1280 {
		t.Errorf("default width: got %d, want 1280", vp.Width)
	}
	if vp.Height != 900 {
		t.Errorf("default height: got %d, want 900", vp.Height)
	}
}

func TestViewportParsing_WorkflowLevel(t *testing.T) {
	yaml := `
name: test-viewport
viewport:
  width: 1440
  height: 1080
actions:
  test:
    steps:
      - eval: "1+1"
`
	w, err := LoadFromBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if w.Viewport == nil {
		t.Fatal("expected viewport, got nil")
	}
	if w.Viewport.Width != 1440 {
		t.Errorf("width: got %d, want 1440", w.Viewport.Width)
	}
	if w.Viewport.Height != 1080 {
		t.Errorf("height: got %d, want 1080", w.Viewport.Height)
	}
}

func TestViewportParsing_ActionLevel(t *testing.T) {
	yaml := `
name: test-viewport-action
actions:
  mobile:
    viewport:
      width: 375
      height: 812
    steps:
      - eval: "1+1"
  desktop:
    steps:
      - eval: "1+1"
`
	w, err := LoadFromBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	mobile := w.Actions["mobile"]
	if mobile.Viewport == nil {
		t.Fatal("expected viewport on mobile action")
	}
	if mobile.Viewport.Width != 375 {
		t.Errorf("mobile width: got %d, want 375", mobile.Viewport.Width)
	}

	desktop := w.Actions["desktop"]
	if desktop.Viewport != nil {
		t.Error("desktop should not have viewport")
	}
}

func TestViewportParsing_NoViewport(t *testing.T) {
	yaml := `
name: test-no-viewport
actions:
  test:
    steps:
      - eval: "1+1"
`
	w, err := LoadFromBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if w.Viewport != nil {
		t.Error("expected nil viewport when not specified")
	}
}

func TestViewportResolve(t *testing.T) {
	wf := &Viewport{Width: 1440, Height: 900}
	action := &Viewport{Width: 375, Height: 812}

	// Action overrides workflow
	got := ResolveViewport(wf, action)
	if got.Width != 375 || got.Height != 812 {
		t.Errorf("action should override workflow: got %dx%d", got.Width, got.Height)
	}

	// Workflow used when action is nil
	got = ResolveViewport(wf, nil)
	if got.Width != 1440 || got.Height != 900 {
		t.Errorf("workflow should be used when action is nil: got %dx%d", got.Width, got.Height)
	}

	// Default used when both nil
	got = ResolveViewport(nil, nil)
	if got.Width != 1280 || got.Height != 900 {
		t.Errorf("default should be used when both nil: got %dx%d", got.Width, got.Height)
	}
}
