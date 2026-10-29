//go:build windows

package store

import "os"

// processAlive reports whether a process with this ID is running. On
// Windows, FindProcess opens the process and fails if it does not exist; it
// also fails (access denied) for another user's elevated process, which is
// then taken for gone. The lock is advisory, so that only skips a warning.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = p.Release()
	return true
}
