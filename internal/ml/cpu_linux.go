//go:build linux

package ml

import (
	"os"
	"strconv"
	"strings"
)

// readCPUTimes reads host CPU time counters from the aggregate line of
// /proc/stat. idle includes iowait.
func readCPUTimes() (idle, total uint64, ok bool) {
	raw, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)[1:]
		if len(fields) < 5 {
			return 0, 0, false
		}
		for i, f := range fields {
			v, err := strconv.ParseUint(f, 10, 64)
			if err != nil {
				return 0, 0, false
			}
			total += v
			if i == 3 || i == 4 { // idle, iowait
				idle += v
			}
		}
		return idle, total, true
	}
	return 0, 0, false
}
