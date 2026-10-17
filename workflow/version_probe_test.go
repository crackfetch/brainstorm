package workflow

import (
	"fmt"
	"os"
	"runtime"
	"testing"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println("Google Chrome 123.0.0.0")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestDetectChromeVersion(t *testing.T) {
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	want := 123
	if runtime.GOOS == "windows" {
		want = 0
	}
	if got := detectChromeVersion(bin); got != want {
		t.Fatalf("browser version probe = %d, want %d", got, want)
	}
}
