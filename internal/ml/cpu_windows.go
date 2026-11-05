//go:build windows

package ml

import (
	"syscall"
	"unsafe"
)

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procGetSystemTimes = kernel32.NewProc("GetSystemTimes")
)

// readCPUTimes reads host CPU time counters via GetSystemTimes. Kernel time
// already includes idle time, so total = kernel + user.
func readCPUTimes() (idle, total uint64, ok bool) {
	var idleT, kernelT, userT syscall.Filetime
	r, _, _ := procGetSystemTimes.Call(
		uintptr(unsafe.Pointer(&idleT)),
		uintptr(unsafe.Pointer(&kernelT)),
		uintptr(unsafe.Pointer(&userT)),
	)
	if r == 0 {
		return 0, 0, false
	}
	ft := func(f syscall.Filetime) uint64 {
		return uint64(f.HighDateTime)<<32 | uint64(f.LowDateTime)
	}
	return ft(idleT), ft(kernelT) + ft(userT), true
}
