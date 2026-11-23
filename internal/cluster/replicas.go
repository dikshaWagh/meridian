package cluster

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dikshaWagh/meridian/internal/api"
	"github.com/dikshaWagh/meridian/internal/balancer"
	"github.com/dikshaWagh/meridian/internal/config"
	"github.com/dikshaWagh/meridian/internal/control"
	"github.com/dikshaWagh/meridian/internal/registry"
)

// replicaView pairs one local replica's identity with its live status. Leader
// and worker both build their view of local replicas from this one function,
// so IDs are the same wherever they are reported.
type replicaView struct {
	ID       string
	Model    string
	Instance int
	Port     int
	Status   api.WorkerStatus
	Worker   *balancer.Worker
}

// replicaViews lists the pool's replicas in pool order. ID is
// <model>-<node-id[:6]>-<i>, with i counting instances of each model.
func replicaViews(nodeID string, pool *balancer.Pool) []replicaView {
	short := nodeID
	if len(short) > 6 {
		short = short[:6]
	}
	instances := map[string]int{}
	snaps := pool.Snapshot()
	workers := pool.Workers()
	out := make([]replicaView, 0, len(snaps))
	for i, s := range snaps {
		instances[s.Model]++
		n := instances[s.Model]
		out = append(out, replicaView{
			ID:       fmt.Sprintf("%s-%s-%d", s.Model, short, n),
			Model:    s.Model,
			Instance: n,
			Port:     s.Port,
			Status:   s,
			Worker:   workers[i],
		})
	}
	return out
}

// registryState maps a pool state to the registry's replica state.
func registryState(s string) registry.ReplicaState {
	switch s {
	case "healthy":
		return registry.ReplicaReady
	case "unhealthy":
		return registry.ReplicaUnhealthy
	default:
		return registry.ReplicaLoading
	}
}

// wireState maps a pool state to the control protocol's replica state.
func wireState(s string) string {
	return string(registryState(s))
}

// localReplicas turns the leader's pool into registry records.
func localReplicas(nodeID string, pool *balancer.Pool, o balancer.Options, now time.Time) []registry.ReplicaRecord {
	views := replicaViews(nodeID, pool)
	out := make([]registry.ReplicaRecord, 0, len(views))
	for _, v := range views {
		s := v.Status
		out = append(out, registry.ReplicaRecord{
			ReplicaID:         v.ID,
			NodeID:            nodeID,
			Model:             v.Model,
			Instance:          v.Instance,
			Kind:              "llm",
			Runtime:           "llama.cpp",
			Endpoint:          fmt.Sprintf("http://127.0.0.1:%d", v.Port),
			ContextWindow:     o.Context,
			MaxConcurrent:     o.Parallel,
			State:             registryState(s.State),
			PID:               s.PID,
			Healthy:           s.State == "healthy",
			InFlight:          s.InFlight,
			Served:            s.Served,
			EstTokensInFlight: s.EstTokensInFlight,
			TokPerSec:         s.TokPerSec,
			LastCheck:         parseTime(s.LastCheck, now),
			LastError:         s.LastError,
		})
	}
	return out
}

// workerSnapshot builds the worker's REGISTER and HEARTBEAT contents.
func workerSnapshot(nodeID, advertise string, httpPort int, pool *balancer.Pool, o balancer.Options, cpu *cpuSampler) control.WorkerSnapshot {
	views := replicaViews(nodeID, pool)
	snap := control.WorkerSnapshot{
		Hardware: hardwareInfo(),
		Replicas: make([]control.ReplicaInfo, 0, len(views)),
		Statuses: make([]control.ReplicaStatus, 0, len(views)),
	}
	for _, v := range views {
		st := v.Status
		snap.Replicas = append(snap.Replicas, control.ReplicaInfo{
			ID:            v.ID,
			Model:         v.Model,
			Instance:      v.Instance,
			Kind:          "llm",
			Runtime:       "llama.cpp",
			Endpoint:      fmt.Sprintf("http://%s:%d/replicas/%s", advertise, httpPort, v.ID),
			ContextWindow: o.Context,
			MaxConcurrent: o.Parallel,
			State:         wireState(st.State),
			PID:           st.PID,
		})
		snap.Statuses = append(snap.Statuses, control.ReplicaStatus{
			ID:                v.ID,
			State:             wireState(st.State),
			PID:               st.PID,
			InFlight:          st.InFlight,
			Served:            st.Served,
			EstTokensInFlight: st.EstTokensInFlight,
			TokPerSec:         st.TokPerSec,
			LastError:         st.LastError,
		})
	}
	snap.CPUPercent = cpu.sample()
	if total, err := config.ReadMemTotal(); err == nil {
		if avail, err := config.ReadMemAvailable(); err == nil {
			snap.RAMUsedMB = (total - avail) >> 20
		}
	}
	return snap
}

func hardwareInfo() control.HardwareInfo {
	h := control.HardwareInfo{CPUCores: runtime.NumCPU()}
	if total, err := config.ReadMemTotal(); err == nil {
		h.RAMTotalMB = total >> 20
	}
	return h
}

func parseTime(s string, fallback time.Time) time.Time {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return fallback
}

// cpuSampler turns successive /proc/stat readings into a busy percentage.
// Each call to sample uses the time since the previous call.
type cpuSampler struct {
	mu                  sync.Mutex
	prevIdle, prevTotal uint64
	primed              bool
}

func (c *cpuSampler) sample() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	idle, total, err := readCPUTimes()
	if err != nil {
		return 0
	}
	defer func() { c.prevIdle, c.prevTotal, c.primed = idle, total, true }()
	if !c.primed || total <= c.prevTotal {
		return 0
	}
	dTotal := float64(total - c.prevTotal)
	dIdle := float64(idle - c.prevIdle)
	return 100 * (1 - dIdle/dTotal)
}

func readCPUTimes() (idle, total uint64, err error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 5 || fields[0] != "cpu" {
			continue
		}
		for i, v := range fields[1:] {
			n, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				return 0, 0, err
			}
			total += n
			if i == 3 || i == 4 { // idle and iowait
				idle += n
			}
		}
		return idle, total, nil
	}
	return 0, 0, fmt.Errorf("cpu line not found in /proc/stat")
}
