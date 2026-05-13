//go:build windows

package mobile

import "syscall"

// On Windows syscall.SetNonblock takes syscall.Handle, and is in fact a
// no-op there. The mobile package is only consumed via gomobile on
// iOS/Android, so this branch exists only so `go build ./...` succeeds
// when cross-compiled to Windows.
func setNonblock(fd int) error {
	return syscall.SetNonblock(syscall.Handle(uintptr(fd)), true)
}
