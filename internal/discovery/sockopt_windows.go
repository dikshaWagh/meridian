//go:build windows

package discovery

import "syscall"

// setBroadcast enables SO_BROADCAST on the socket handle.
func setBroadcast(fd uintptr) error {
	return syscall.SetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
}
