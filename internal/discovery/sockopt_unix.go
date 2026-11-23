//go:build !windows

package discovery

import "syscall"

// setBroadcast enables SO_BROADCAST on the socket file descriptor.
func setBroadcast(fd uintptr) error {
	return syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
}
