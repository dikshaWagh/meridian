//go:build !windows && !linux

package ml

// readCPUTimes has no portable implementation on this platform. CPU is then
// reported as unavailable and scored as 0 rather than invented.
func readCPUTimes() (idle, total uint64, ok bool) { return 0, 0, false }
