//go:build !windows

package nexttrace

import "os/exec"

func hideWindow(*exec.Cmd) {}
