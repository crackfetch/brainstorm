package workflow

import (
	"runtime"
	"testing"
	"time"
)

// Tests for the macOS window-surface helper. Two layers:
//   - extractMacOSBundleName: pure string scan, runs on every platform.
//   - surfaceMacOSWindow: actual osascript invocation, gated on Darwin.

func TestExtractMacOSBundleName_CommonPaths(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "system Chrome from /Applications",
			in:   "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			want: "Google Chrome",
		},
		{
			name: "Chrome for Testing from Playwright cache",
			in:   "/Users/x/Library/Caches/ms-playwright/chromium-1208/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing",
			want: "Google Chrome for Testing",
		},
		{
			name: "Brave",
			in:   "/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
			want: "Brave Browser",
		},
		{
			name: "non-bundle Linux path returns empty",
			in:   "/usr/bin/chromium",
			want: "",
		},
		{
			name: "empty input returns empty",
			in:   "",
			want: "",
		},
		{
			name: "deeply-nested .app picks the first one from the right",
			in:   "/Applications/Outer.app/Contents/Frameworks/Inner.app/Contents/MacOS/Inner",
			want: "Inner",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractMacOSBundleName(tc.in)
			if got != tc.want {
				t.Errorf("extractMacOSBundleName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSurfaceMacOSWindow_NonDarwinIsNoop(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("non-Darwin guard test")
	}
	// On Linux/Windows the helper must return immediately without calling
	// osascript (which doesn't exist there). We can't observe directly, so
	// we time-bound the call and assert it returned fast.
	exec := &Executor{}
	start := time.Now()
	exec.surfaceMacOSWindow("/Applications/Anything.app/Contents/MacOS/Anything")
	if d := time.Since(start); d > 50*time.Millisecond {
		t.Errorf("non-Darwin surfaceMacOSWindow should be a no-op; took %s", d)
	}
}

func TestSurfaceMacOSWindow_NoBundleIsNoop(t *testing.T) {
	// On Darwin or otherwise: an exe path with no .app component must
	// return without invoking osascript. Bound the call to catch any
	// regression where we'd accidentally pass an empty bundle name.
	exec := &Executor{}
	start := time.Now()
	exec.surfaceMacOSWindow("/usr/bin/chromium")
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Errorf("no-bundle exe path should be a no-op; took %s", d)
	}
	exec.surfaceMacOSWindow("")
	exec.surfaceMacOSWindow("relative/path")
}

func TestSurfaceMacOSWindow_DarwinHonorsTimeout(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Darwin-only behavior test")
	}
	// The osascript call is bounded to 2s. Even when the bundle doesn't
	// exist (osascript errors out fast), the function must return within
	// the timeout window. We verify with a plausible-but-fictional bundle
	// name so osascript will fail-fast rather than actually activate
	// anything on the developer's machine.
	exec := &Executor{}
	start := time.Now()
	exec.surfaceMacOSWindow("/Applications/Surely-Does-Not-Exist-1234567.app/Contents/MacOS/Surely-Does-Not-Exist-1234567")
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("Darwin surfaceMacOSWindow exceeded its 2s timeout; took %s", d)
	}
}
