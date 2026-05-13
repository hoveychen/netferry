//go:build !windows

package mobile

import "syscall"

func setNonblock(fd int) error {
	return syscall.SetNonblock(fd, true)
}
