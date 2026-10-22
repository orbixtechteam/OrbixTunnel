//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// hideConsole stops sing-box from opening its own console window.
func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
