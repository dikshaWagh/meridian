//go:build !windows

package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// systemMemory reads MemTotal and MemAvailable from /proc/meminfo. The second
// return value is the memory a new process can use without swapping.
func systemMemory() (total, available int64, err error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			total = kb << 10
		case "MemAvailable:":
			available = kb << 10
		}
	}
	if total == 0 {
		return 0, 0, fmt.Errorf("MemTotal not found in /proc/meminfo")
	}
	if available == 0 {
		return 0, 0, fmt.Errorf("MemAvailable not found in /proc/meminfo")
	}
	return total, available, nil
}
