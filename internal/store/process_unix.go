//go:build !windows

package store

import (
	"errors"
	"syscall"
)

// processAlive reports whether a process with this ID is running. EPERM
// means it exists but belongs to another user.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
