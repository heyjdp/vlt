//go:build windows

package store

import (
	"os/exec"
	"syscall"
)

// createNoWindow stops a console window flashing up when the GUI starts ssh.
const createNoWindow = 0x08000000

// hideConsole starts cmd without a console window.
func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
