package workflow

import (
	"runtime"
	"strings"
	"testing"
)

// TestBuildLoginArgs_DefaultsProfileDirectory asserts the default argv
// includes --profile-directory=Default. This is the core fix: without it,
// Chrome on Windows shows the multi-profile picker when Local State has
// more than one profile entry, hijacking the --app window.
func TestBuildLoginArgs_DefaultsProfileDirectory(t *testing.T) {
	e := &Executor{loginURL: "https://example.com/login"}
	args := e.buildLoginArgs("/tmp/profile", DefaultViewport())

	if !containsFlag(args, "--profile-directory=Default") {
		t.Fatalf("expected --profile-directory=Default in argv, got: %v", args)
	}
}

// TestBuildLoginArgs_ProfileDirectoryOverridable asserts that a caller can
// override the default via WithChromeFlags. Useful for users running multi-
// profile setups intentionally (e.g. a "Work" profile for the agent).
func TestBuildLoginArgs_ProfileDirectoryOverridable(t *testing.T) {
	e := &Executor{
		loginURL:    "https://example.com/login",
		chromeFlags: map[string]string{"profile-directory": "Work"},
	}
	args := e.buildLoginArgs("/tmp/profile", DefaultViewport())

	if containsFlag(args, "--profile-directory=Default") {
		t.Errorf("default --profile-directory=Default should not appear when caller overrides; got: %v", args)
	}
	if !containsFlag(args, "--profile-directory=Work") {
		t.Errorf("expected caller's --profile-directory=Work in argv, got: %v", args)
	}
}

// TestBuildLoginArgs_ChromeFlagsAppended asserts that arbitrary chromeFlags
// are merged into the argv with --key=value (or --key for empty values),
// in sorted order for stable composition.
func TestBuildLoginArgs_ChromeFlagsAppended(t *testing.T) {
	e := &Executor{
		loginURL: "https://example.com/login",
		chromeFlags: map[string]string{
			"lang":            "en-US",
			"force-dark-mode": "",
		},
	}
	args := e.buildLoginArgs("/tmp/profile", DefaultViewport())

	if !containsFlag(args, "--lang=en-US") {
		t.Errorf("expected --lang=en-US in argv, got: %v", args)
	}
	if !containsFlag(args, "--force-dark-mode") {
		t.Errorf("expected bare --force-dark-mode in argv, got: %v", args)
	}
	// Sorted: force-dark-mode comes before lang.
	if idx(args, "--force-dark-mode") > idx(args, "--lang=en-US") {
		t.Errorf("expected chromeFlags in sorted order, got: %v", args)
	}
}

// TestBuildLoginArgs_AppURLLast asserts --app= is appended last. Process-
// list inspection and crash logs are easier to read when the login URL is
// at the tail of argv.
func TestBuildLoginArgs_AppURLLast(t *testing.T) {
	e := &Executor{loginURL: "https://example.com/login"}
	args := e.buildLoginArgs("/tmp/profile", DefaultViewport())

	last := args[len(args)-1]
	if last != "--app=https://example.com/login" {
		t.Errorf("expected --app=<loginURL> as last arg, got %q", last)
	}
}

// TestBuildLoginArgs_LinuxAddsSandboxFlags asserts the Linux-specific flags
// only appear on linux. Sandbox + dev/shm flags are required for CI/Docker.
func TestBuildLoginArgs_LinuxAddsSandboxFlags(t *testing.T) {
	e := &Executor{loginURL: "https://example.com/login"}
	args := e.buildLoginArgs("/tmp/profile", DefaultViewport())

	hasSandbox := containsFlag(args, "--no-sandbox")
	if runtime.GOOS == "linux" && !hasSandbox {
		t.Errorf("expected --no-sandbox on linux, got: %v", args)
	}
	if runtime.GOOS != "linux" && hasSandbox {
		t.Errorf("did not expect --no-sandbox on %s, got: %v", runtime.GOOS, args)
	}
}

func containsFlag(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func idx(args []string, want string) int {
	for i, a := range args {
		if a == want || strings.HasPrefix(a, want+"=") {
			return i
		}
	}
	return -1
}
