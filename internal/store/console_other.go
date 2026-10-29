//go:build !windows

package store

import "os/exec"

// hideConsole does nothing outside Windows: ssh has no window to hide.
func hideConsole(*exec.Cmd) {}
