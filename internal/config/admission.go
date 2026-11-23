package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dikshaWagh/meridian/internal/balancer"
)

// Footprint is the memory a single local replica needs: its model file size.
type Footprint struct {
	Name  string
	Bytes int64
}

// ReplicaFootprints lists one footprint per replica, in --llm order. Each
// instance loads its own copy of the model, so every instance counts.
func ReplicaFootprints(o balancer.Options) ([]Footprint, error) {
	dir, err := ExpandHome(o.ModelsDir)
	if err != nil {
		return nil, err
	}
	var out []Footprint
	var problems []string
	for _, l := range o.LLMConfigs {
		file := strings.TrimSpace(l.Model)
		path := file
		if !filepath.IsAbs(file) {
			path = filepath.Join(dir, file)
		}
		fi, err := os.Stat(path)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%q: %v", path, err))
			continue
		}
		name := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
		for i := 1; i <= max(l.Workers, 1); i++ {
			out = append(out, Footprint{Name: fmt.Sprintf("%s-%d", name, i), Bytes: fi.Size()})
		}
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("model files missing: %s", strings.Join(problems, "; "))
	}
	return out, nil
}

// AdmitReplicas checks that the replicas fit in availableBytes after headroomMB
// is kept free. Replicas are admitted in order. If the total does not fit, the
// error lists each replica that would exceed the budget.
func AdmitReplicas(fps []Footprint, availableBytes int64, headroomMB int) error {
	budget := availableBytes - int64(headroomMB)<<20
	var cum int64
	var over []string
	for _, f := range fps {
		cum += f.Bytes
		if cum > budget {
			over = append(over, fmt.Sprintf("%s (%d MB)", f.Name, f.Bytes>>20))
		}
	}
	if len(over) == 0 {
		return nil
	}
	return fmt.Errorf(
		"insufficient RAM on this node: MemAvailable is %d MB and %d MB headroom is required, leaving %d MB for %d replicas; these replicas do not fit: %s; reduce instance counts or models",
		availableBytes>>20, headroomMB, max(budget, 0)>>20, len(fps), strings.Join(over, ", "))
}

// ReadMemAvailable returns MemAvailable from /proc/meminfo in bytes. Each node
// reads its own value; the leader's value says nothing about a worker.
func ReadMemAvailable() (int64, error) {
	return readMeminfo("MemAvailable:")
}

// ReadMemTotal returns MemTotal from /proc/meminfo in bytes.
func ReadMemTotal() (int64, error) {
	return readMeminfo("MemTotal:")
}

func readMeminfo(key string) (int64, error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == key {
			kb, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil {
				return 0, err
			}
			return kb << 10, nil
		}
	}
	return 0, fmt.Errorf("%s not found in /proc/meminfo", strings.TrimSuffix(key, ":"))
}

// CheckLocalMemory runs the admission check for this node's replicas. It is
// called once at startup in cluster modes.
func CheckLocalMemory(o balancer.Options, avail int64) error {
	fps, err := ReplicaFootprints(o)
	if err != nil {
		return err
	}
	return AdmitReplicas(fps, avail, o.RAMHeadroomMB)
}
