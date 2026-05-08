package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Tests for `brz logs` parser + collector. Filesystem-aware tests use
// t.TempDir() so they're hermetic; no actual brz launches.

func TestParseArtifactFilename(t *testing.T) {
	tests := []struct {
		name      string
		want      bool
		wantKind  string
		wantAct   string
		wantStamp string
		wantStep  int
	}{
		{
			// Real example from a failed manual_login action.
			name:      "manual_login_failed_20260507-180413_4.png",
			want:      true,
			wantKind:  "failed",
			wantAct:   "manual_login",
			wantStamp: "20260507-180413",
			wantStep:  4,
		},
		{
			name:     "manual_login_before_4.jpg",
			want:     true,
			wantKind: "before",
			wantAct:  "manual_login",
			wantStep: 4,
		},
		{
			// Underscores in action names must round-trip cleanly.
			name:     "two_word_action_failed_20260101-000000_0.png",
			want:     true,
			wantKind: "failed",
			wantAct:  "two_word_action",
			wantStep: 0,
		},
		// Negative cases — must not match.
		{name: "screenshot.png", want: false},
		{name: "user_supplied_export.csv", want: false},
		{name: "action_failed.png", want: false},  // missing timestamp + step
		{name: "action_before.jpg", want: false},  // missing step
		{name: "_failed_20260507-180413_4.png", want: false}, // empty action prefix
		{name: "", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseArtifactFilename(tc.name)
			if ok != tc.want {
				t.Fatalf("match: got %v, want %v", ok, tc.want)
			}
			if !tc.want {
				return
			}
			if got.Kind != tc.wantKind {
				t.Errorf("Kind: got %q, want %q", got.Kind, tc.wantKind)
			}
			if got.Action != tc.wantAct {
				t.Errorf("Action: got %q, want %q", got.Action, tc.wantAct)
			}
			if got.StepIdx != tc.wantStep {
				t.Errorf("StepIdx: got %d, want %d", got.StepIdx, tc.wantStep)
			}
			if tc.wantStamp != "" && got.Stamp != tc.wantStamp {
				t.Errorf("Stamp: got %q, want %q", got.Stamp, tc.wantStamp)
			}
		})
	}
}

func TestCollectArtifacts_Empty(t *testing.T) {
	dir := t.TempDir()
	got := collectArtifacts(dir, 20)
	if len(got) != 0 {
		t.Errorf("empty dir → got %d artifacts, want 0", len(got))
	}
}

func TestCollectArtifacts_NonExistentDir(t *testing.T) {
	got := collectArtifacts("/this/does/not/exist", 20)
	if got != nil {
		t.Errorf("non-existent dir → got %v, want nil", got)
	}
}

func TestCollectArtifacts_FiltersAndSorts(t *testing.T) {
	dir := t.TempDir()
	// Mix of real artifacts and unrelated noise.
	files := []struct {
		name  string
		mtime time.Time
	}{
		{"login_failed_20260507-100000_3.png", time.Now().Add(-10 * time.Minute)},
		{"login_before_3.jpg", time.Now().Add(-11 * time.Minute)},
		{"export_failed_20260507-120000_2.png", time.Now().Add(-2 * time.Minute)}, // newest
		{"export_before_2.jpg", time.Now().Add(-3 * time.Minute)},
		{"unrelated.txt", time.Now()},                  // noise
		{"user_supplied_screenshot.png", time.Now()},   // noise (no _failed_)
		{"export_archive.csv", time.Now()},             // noise
	}
	for _, f := range files {
		path := filepath.Join(dir, f.name)
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, f.mtime, f.mtime); err != nil {
			t.Fatal(err)
		}
	}
	got := collectArtifacts(dir, 20)
	if len(got) != 4 {
		t.Fatalf("expected 4 artifacts (filtered out 3 noise files), got %d: %+v", len(got), got)
	}
	// Newest first: export_failed at -2m
	if got[0].Action != "export" || got[0].Kind != "failed" {
		t.Errorf("first entry should be newest export_failed; got %+v", got[0])
	}
	// Oldest last: login_before at -11m
	if got[len(got)-1].Action != "login" || got[len(got)-1].Kind != "before" {
		t.Errorf("last entry should be oldest login_before; got %+v", got[len(got)-1])
	}
}

func TestCollectArtifacts_RespectsLimit(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 10; i++ {
		name := "act_failed_20260101-000000_" + string(rune('0'+i)) + ".png"
		os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644)
	}
	got := collectArtifacts(dir, 3)
	if len(got) != 3 {
		t.Errorf("limit=3 → got %d, want 3", len(got))
	}
}

func TestCollectArtifacts_LimitZeroMeansAll(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 5; i++ {
		name := "act_failed_20260101-000000_" + string(rune('0'+i)) + ".png"
		os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644)
	}
	got := collectArtifacts(dir, 0)
	if len(got) != 5 {
		t.Errorf("limit=0 should mean unlimited; got %d, want 5", len(got))
	}
}

func TestArtifactJSONShape(t *testing.T) {
	// Ensure the JSON tag set matches what users will rely on. Snake_case
	// across the board so jq pipelines feel native.
	a := Artifact{
		Path:     "/tmp/x.png",
		Kind:     "failed",
		Action:   "login",
		StepIdx:  3,
		Stamp:    "20260507-180413",
		Modified: time.Now(),
		Size:     1234,
	}
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"path"`, `"kind"`, `"action"`, `"step_index"`, `"timestamp"`, `"modified"`, `"size"`,
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("missing JSON key %s in %s", want, data)
		}
	}
}

func TestArtifactJSONShape_BeforeOmitsTimestamp(t *testing.T) {
	// before screenshots have no embedded timestamp in the filename.
	// JSON should omit the empty stamp field via omitempty.
	a := Artifact{Kind: "before", Action: "login", StepIdx: 3}
	data, _ := json.Marshal(a)
	if strings.Contains(string(data), `"timestamp":""`) {
		t.Errorf("empty timestamp should be omitted; got %s", data)
	}
}
