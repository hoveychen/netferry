//go:build windows

package nexttrace

import (
	"os/exec"
	"syscall"
)

// hideWindow keeps nexttrace from flashing a console (CREATE_NO_WINDOW).
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
}
