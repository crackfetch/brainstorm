//go:build !windows

package workflow

import (
	"os"
	"syscall"
)

// isProcessAlive checks whether a process with the given PID is still running.
// On Unix, signal 0 probes the process without killing it.
func isProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// killProcessTree kills a process. On Unix, child processes typically die with
// the parent in most setups (same process group), so a simple Kill suffices.
func killProcessTree(proc *os.Process) {
	proc.Kill()
}
