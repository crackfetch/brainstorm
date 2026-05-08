package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for `brz status` helpers. Pure-Go where possible: parser tests use
// fixed ps-output strings; filesystem-aware inspectors use t.TempDir().
// No actual Chromium launches.

func TestExtractFlagValue(t *testing.T) {
	tests := []struct {
		name string
		args string
		flag string
		want string
	}{
		{"equals form", "/Applications/Chrome.app/Contents/MacOS/Chrome --user-data-dir=/tmp/profile --headless", "--user-data-dir", "/tmp/profile"},
		{"space form", "/usr/bin/chromium --user-data-dir /tmp/profile --headless", "--user-data-dir", "/tmp/profile"},
		{"flag absent returns empty", "/usr/bin/chromium --headless", "--user-data-dir", ""},
		{"empty args returns empty", "", "--user-data-dir", ""},
		{"value at end of args", "/usr/bin/chromium --user-data-dir=/tmp/profile", "--user-data-dir", "/tmp/profile"},
		{
			// Real-world: macOS "Application Support" Chrome profile.
			// Without space-aware parsing, this would have been truncated to
			// "/Users/x/Library/Application" and the brz-status detector
			// would have missed the matching Chrome process entirely —
			// exactly the bug the diagnostic command is supposed to surface.
			name: "value with spaces — equals form",
			args: "/Applications/Chrome.app/Contents/MacOS/Chrome --user-data-dir=/Users/x/Library/Application Support/brz/profile --headless",
			flag: "--user-data-dir",
			want: "/Users/x/Library/Application Support/brz/profile",
		},
		{
			name: "value with spaces — space form",
			args: "/usr/bin/chromium --user-data-dir /Users/x/Library/Application Support/brz/profile --headless",
			flag: "--user-data-dir",
			want: "/Users/x/Library/Application Support/brz/profile",
		},
		{
			// Defensive: matches like "--user-data-dir-extra=/x" should NOT
			// satisfy a search for "--user-data-dir" (the rest after the flag
			// starts with "-" not "=" or " ").
			name: "longer flag with same prefix is not matched",
			args: "/bin/chrome --user-data-dir-extra=/foo --headless",
			flag: "--user-data-dir",
			want: "",
		},
		{
			// Value at end with trailing space (some ps outputs add one)
			name: "trailing space after value — equals form",
			args: "/bin/chrome --user-data-dir=/p ",
			flag: "--user-data-dir",
			want: "/p",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractFlagValue(tc.args, tc.flag)
			if got != tc.want {
				t.Errorf("extractFlagValue(%q,%q)=%q, want %q", tc.args, tc.flag, got, tc.want)
			}
		})
	}
}

func TestIsBrzOwnedProfile(t *testing.T) {
	configured := "/Users/x/.config/brz/chrome-profile"
	tests := []struct {
		name string
		dir  string
		want bool
	}{
		{"exact configured path", configured, true},
		{"trailing slash equivalent", configured + "/", true},
		{"ephemeral tmp profile", "/var/folders/xr/T/brz-ephemeral-12345", true},
		{"unrelated profile", "/Users/x/Library/Application Support/Google/Chrome", false},
		{"empty user-data-dir", "", false},
		{"user's own Chrome with our profile name as substring is rejected", "/Users/x/Library/.config/brz/chrome-profile-but-different", false},
		{
			// Defensive: a path that happens to contain "brz-downloads"
			// (e.g. someone named their profile that) should NOT be
			// detected as brz-owned. The previous substring fallback
			// false-positived here.
			name: "path containing brz-downloads substring is rejected",
			dir:  "/some/random/path-with-brz-downloads-inside",
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isBrzOwnedProfile(tc.dir, configured)
			if got != tc.want {
				t.Errorf("isBrzOwnedProfile(%q, %q) = %v, want %v", tc.dir, configured, got, tc.want)
			}
		})
	}
}

func TestParsePsOutputForBrz_HandlesSpaceInProfilePath(t *testing.T) {
	// macOS-style profile path with a space. Without the space-aware
	// extractor this fails — the very case the bug is most user-visible.
	configured := "/Users/x/Library/Application Support/brz/profile"
	psOut := "  555 /Applications/Google Chrome.app/Contents/MacOS/Google Chrome --user-data-dir=/Users/x/Library/Application Support/brz/profile --headless --remote-debugging-port=0\n"

	got := parsePsOutputForBrz(psOut, configured)
	if len(got) != 1 {
		t.Fatalf("expected 1 match for spaced profile, got %d: %+v", len(got), got)
	}
	if got[0].UserDataDir != configured {
		t.Errorf("UserDataDir: got %q, want %q", got[0].UserDataDir, configured)
	}
}

func TestParsePsOutputForBrz_BasicMatch(t *testing.T) {
	configured := "/Users/x/.config/brz/chrome-profile"
	psOut := strings.Join([]string{
		"  123 /Applications/Google Chrome.app/Contents/MacOS/Google Chrome --user-data-dir=" + configured + " --headless",
		"  456 /usr/bin/firefox",
		"  789 /usr/bin/chromium --user-data-dir /var/folders/T/brz-ephemeral-7777 --remote-debugging-port=0",
		"  101 /usr/bin/chromium --user-data-dir=/Users/x/Library/Application Support/Google/Chrome",
	}, "\n")

	got := parsePsOutputForBrz(psOut, configured)
	if len(got) != 2 {
		t.Fatalf("expected 2 brz-owned procs, got %d: %+v", len(got), got)
	}
	if got[0].PID != 123 {
		t.Errorf("first proc PID: got %d, want 123", got[0].PID)
	}
	if got[1].PID != 789 {
		t.Errorf("second proc PID (sorted by PID): got %d, want 789", got[1].PID)
	}
	if got[1].UserDataDir != "/var/folders/T/brz-ephemeral-7777" {
		t.Errorf("ephemeral user-data-dir mismatch: %q", got[1].UserDataDir)
	}
}

func TestParsePsOutputForBrz_MalformedLinesIgnored(t *testing.T) {
	// Defensive: a stray header row, an empty line, or a line that looks
	// like a non-Chromium process must not produce false positives.
	configured := "/p"
	psOut := strings.Join([]string{
		"   PID ARGS",          // ps header (no real numeric pid)
		"",                     // blank
		" abc not-a-pid",       // non-numeric pid
		" 1234 /bin/bash",      // no --user-data-dir
		" 5678 /bin/Chrome --user-data-dir=/p", // valid match
	}, "\n")
	got := parsePsOutputForBrz(psOut, configured)
	if len(got) != 1 || got[0].PID != 5678 {
		t.Errorf("expected only PID 5678, got %+v", got)
	}
}

func TestInspectProfileDir(t *testing.T) {
	dir := t.TempDir()
	got := inspectProfileDir(dir)
	if !got.Exists {
		t.Errorf("dir created via TempDir should exist; got %+v", got)
	}
	if got.SingletonLockHeld {
		t.Errorf("fresh tmp dir should have no SingletonLock; got %+v", got)
	}

	// Drop a SingletonLock and re-check
	if err := os.WriteFile(filepath.Join(dir, "SingletonLock"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	got = inspectProfileDir(dir)
	if !got.SingletonLockHeld {
		t.Errorf("SingletonLock present but not detected; got %+v", got)
	}
}

func TestInspectProfileDir_NonExistent(t *testing.T) {
	got := inspectProfileDir("/this/path/should/never/exist")
	if got.Exists {
		t.Errorf("non-existent path reported as exists")
	}
	if got.SingletonLockHeld {
		t.Errorf("non-existent path reported lock held")
	}
}

func TestInspectDownloadsDir(t *testing.T) {
	dir := t.TempDir()
	// Two files of known sizes
	if err := os.WriteFile(filepath.Join(dir, "a.csv"), []byte("aaaa"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.csv"), []byte("bb"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := inspectDownloadsDir(dir)
	if !got.Exists {
		t.Errorf("dir reported as not existing")
	}
	if got.FileCount != 2 {
		t.Errorf("FileCount: got %d, want 2 (subdirs ignored)", got.FileCount)
	}
	if got.TotalBytes != 6 {
		t.Errorf("TotalBytes: got %d, want 6", got.TotalBytes)
	}
	if got.NewestMod.IsZero() {
		t.Errorf("NewestMod should be set when files exist")
	}
}

func TestInspectFailureArtifacts(t *testing.T) {
	dir := t.TempDir()
	files := []struct {
		name string
		want string // "failed" / "before" / ""
	}{
		{"action_failed_20260507-180413_4.png", "failed"},
		{"action_before_4.jpg", "before"},
		{"unrelated.txt", ""},
		{"export_failed_X.jpg", ""}, // failed-but-not-png — ignored
		{"export_before_Y.png", ""}, // before-but-not-jpg — ignored
		{"login_failed_20260507_3.png", "failed"},
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f.name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := inspectFailureArtifacts(dir)
	if got.FailedPNGCount != 2 {
		t.Errorf("FailedPNGCount: got %d, want 2", got.FailedPNGCount)
	}
	if got.BeforeJPGCount != 1 {
		t.Errorf("BeforeJPGCount: got %d, want 1", got.BeforeJPGCount)
	}
	if got.NewestArtifact.IsZero() {
		t.Errorf("NewestArtifact should be set when matching files exist")
	}
}

func TestBuildStatusReport_JSONShape(t *testing.T) {
	// Smoke: build a report against a real-but-empty profile dir, marshal
	// to JSON, confirm the snake_case shape is intact. Catches accidental
	// renames that would break jq pipelines.
	dir := t.TempDir()
	r := buildStatusReport(dir)
	if r.OS == "" {
		t.Error("OS should be populated")
	}
	if r.ProfileDir.Path != dir {
		t.Errorf("ProfileDir.Path: got %q, want %q", r.ProfileDir.Path, dir)
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{
		`"brz_version"`, `"profile_dir"`, `"singleton_lock_held"`,
		`"brz_chromium"`, `"downloads"`, `"failure_artifacts"`,
	} {
		if !strings.Contains(string(data), key) {
			t.Errorf("JSON missing key %s\n%s", key, data)
		}
	}
}

func TestStatusReport_BrzChromiumIsAlwaysList(t *testing.T) {
	// A status against a fresh machine must marshal BrzChromium as []
	// (not null), so jq pipelines can do `.brz_chromium | length` without
	// special-casing the empty state.
	dir := t.TempDir()
	r := buildStatusReport(dir)
	if r.BrzChromium == nil {
		t.Errorf("BrzChromium is nil — would marshal as null. Want empty slice.")
	}
	data, _ := json.Marshal(r)
	if strings.Contains(string(data), `"brz_chromium":null`) {
		t.Errorf("brz_chromium should marshal as [], got null:\n%s", data)
	}
	if !strings.Contains(string(data), `"brz_chromium":[`) {
		t.Errorf("brz_chromium should marshal as JSON array; got:\n%s", data)
	}
}

