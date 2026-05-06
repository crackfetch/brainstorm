//go:build windows

package workflow

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// PROCESS_QUERY_LIMITED_INFORMATION is the minimum access right needed to query
// whether a process is still running.
const _PROCESS_QUERY_LIMITED_INFORMATION = 0x1000

var (
	modkernel32  = syscall.NewLazyDLL("kernel32.dll")
	procOpenProc = modkernel32.NewProc("OpenProcess")
)

// isProcessAlive checks whether a process with the given PID is still running.
// On Windows, we attempt to open the process with minimal query rights.
func isProcessAlive(pid int) bool {
	handle, _, err := procOpenProc.Call(
		uintptr(_PROCESS_QUERY_LIMITED_INFORMATION),
		0,
		uintptr(pid),
	)
	if handle == 0 {
		// Access denied means the process exists but we lack privileges.
		if err == syscall.ERROR_ACCESS_DENIED {
			return true
		}
		return false
	}
	syscall.CloseHandle(syscall.Handle(handle))
	return true
}

// killProcessTree kills a process and all its children. On Windows,
// Process.Kill() only kills the main process — Chrome spawns renderer, GPU,
// and plugin subprocesses that would be orphaned. taskkill /F /T /PID kills
// the entire process tree.
func killProcessTree(proc *os.Process) {
	// Try taskkill first for the full tree kill.
	if err := exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", proc.Pid)).Run(); err != nil {
		// Fallback: kill just the main process.
		proc.Kill()
	}
}
