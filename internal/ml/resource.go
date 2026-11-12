package ml

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Snapshot is one estimate of host memory/GPU availability.
type Snapshot struct {
	RAMAvailableMB  int64
	RAMTotalMB      int64
	RAMKnown        bool
	VRAMAvailableMB int64
	VRAMTotalMB     int64
	VRAMKnown       bool
	Error           string
}

// Probe reports host resource availability. Implementations are deliberately
// small so they can be replaced with fakes in tests.
type Probe interface {
	Snapshot() Snapshot
}

// FakeProbe returns a fixed snapshot. Used by tests.
type FakeProbe struct {
	Snap Snapshot
}

func (f *FakeProbe) Snapshot() Snapshot { return f.Snap }

// hostProbe reads Linux /proc/meminfo and, when enabled and available, nvidia-smi.
// Values are cached briefly because nvidia-smi is relatively expensive.
type hostProbe struct {
	gpu       string // auto | off
	mu        sync.Mutex
	last      Snapshot
	lastAt    time.Time
	cacheLife time.Duration
}

func newHostProbe(gpuProbe string) *hostProbe {
	return &hostProbe{gpu: gpuProbe, cacheLife: 2 * time.Second}
}

func (p *hostProbe) Snapshot() Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.lastAt.IsZero() && time.Since(p.lastAt) < p.cacheLife {
		return p.last
	}

	var snap Snapshot
	var errs []string

	if f, err := os.Open("/proc/meminfo"); err == nil {
		scanner := bufio.NewScanner(f)
		var availKB, totalKB int64
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 2 {
				continue
			}
			switch fields[0] {
			case "MemAvailable:":
				availKB, _ = strconv.ParseInt(fields[1], 10, 64)
			case "MemTotal:":
				totalKB, _ = strconv.ParseInt(fields[1], 10, 64)
			}
		}
		f.Close()
		if totalKB > 0 {
			snap.RAMKnown = true
			snap.RAMAvailableMB = availKB / 1024
			snap.RAMTotalMB = totalKB / 1024
		}
	} else {
		errs = append(errs, "ram probe: "+err.Error())
	}
	if !snap.RAMKnown && len(errs) == 0 {
		errs = append(errs, "ram probe: MemTotal not found")
	}

	if p.gpu != "off" {
		if vram, total, err := probeNvidia(); err == nil {
			snap.VRAMKnown = true
			snap.VRAMAvailableMB = vram
			snap.VRAMTotalMB = total
		} else {
			errs = append(errs, "vram probe: "+err.Error())
		}
	} else {
		errs = append(errs, "vram probe: disabled by configuration")
	}

	snap.Error = strings.Join(errs, "; ")
	p.last = snap
	p.lastAt = time.Now()
	return snap
}

func probeNvidia() (freeMB, totalMB int64, err error) {
	out, err := exec.Command("nvidia-smi",
		"--query-gpu=memory.total,memory.free",
		"--format=csv,noheader,nounits").Output()
	if err != nil {
		return 0, 0, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return 0, 0, fmt.Errorf("nvidia-smi returned no devices")
	}
	for _, line := range lines {
		parts := strings.Split(line, ",")
		if len(parts) != 2 {
			continue
		}
		total, e1 := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
		free, e2 := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		if e1 != nil || e2 != nil {
			continue
		}
		totalMB += total
		freeMB += free
	}
	if totalMB == 0 {
		return 0, 0, fmt.Errorf("could not parse nvidia-smi output")
	}
	return freeMB, totalMB, nil
}

// Reservation is an admission-control reservation for one ML worker.
type Reservation struct {
	ID       int64
	ModelID  string
	Kind     Kind
	RAMMB    int64
	VRAMMB   int64
	Overhead int64
}

// Manager admits ML workers against host capacity. It has no ability to stop
// or evict LLM workers; the protect-llm policy is enforced structurally.
type Manager struct {
	mu       sync.Mutex
	probe    Probe
	cfg      ResourcesConfig
	ramUsed  int64
	vramUsed int64
	nextID   int64
	reserved map[int64]Reservation
}

func NewManager(cfg ResourcesConfig, probe Probe) *Manager {
	return &Manager{probe: probe, cfg: cfg, reserved: map[int64]Reservation{}}
}

// Reserve atomically checks capacity and records a reservation.
// The returned reason is stable for logs/status: prefix is either
// "insufficient_resources" or "resource_probe_unavailable".
func (m *Manager) Reserve(modelID string, kind Kind, ramMB, vramMB, overheadVRAMMB int64) (Reservation, bool, string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	snap := m.probe.Snapshot()

	if ramMB > 0 {
		if !snap.RAMKnown {
			return Reservation{}, false, "resource_probe_unavailable: RAM availability is unknown"
		}
		avail := snap.RAMAvailableMB - m.cfg.RAMHeadroomMB - m.ramUsed
		if avail < 0 {
			avail = 0
		}
		if ramMB > avail {
			return Reservation{}, false, fmt.Sprintf("insufficient_resources: requested %d MB RAM, available %d MB", ramMB, avail)
		}
	}

	needVRAM := vramMB + overheadVRAMMB
	if needVRAM > 0 {
		if !snap.VRAMKnown {
			return Reservation{}, false, "resource_probe_unavailable: GPU memory availability is unknown"
		}
		avail := snap.VRAMAvailableMB - m.cfg.VRAMHeadroomMB - m.cfg.ProtectedLLMVRAMMB - m.vramUsed
		if avail < 0 {
			avail = 0
		}
		if needVRAM > avail {
			return Reservation{}, false, fmt.Sprintf("insufficient_resources: requested %d MB VRAM, available %d MB", needVRAM, avail)
		}
	}

	m.nextID++
	r := Reservation{ID: m.nextID, ModelID: modelID, Kind: kind, RAMMB: ramMB, VRAMMB: vramMB, Overhead: overheadVRAMMB}
	m.reserved[r.ID] = r
	m.ramUsed += ramMB
	m.vramUsed += needVRAM
	return r, true, ""
}

// Release frees a reservation. Unknown reservations are ignored.
func (m *Manager) Release(r Reservation) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.reserved[r.ID]
	if !ok {
		return
	}
	delete(m.reserved, r.ID)
	m.ramUsed -= current.RAMMB
	m.vramUsed -= current.VRAMMB + current.Overhead
	if m.ramUsed < 0 {
		m.ramUsed = 0
	}
	if m.vramUsed < 0 {
		m.vramUsed = 0
	}
}

// Status returns a point-in-time view for /ml/status.
func (m *Manager) Status() ResourceStatus {
	snap := m.probe.Snapshot()
	m.mu.Lock()
	defer m.mu.Unlock()

	out := ResourceStatus{
		Policy:             "protect-llm",
		RAMKnown:           snap.RAMKnown,
		RAMAvailableMB:     snap.RAMAvailableMB,
		RAMTotalMB:         snap.RAMTotalMB,
		RAMReservedMB:      m.ramUsed,
		RAMHeadroomMB:      m.cfg.RAMHeadroomMB,
		VRAMKnown:          snap.VRAMKnown,
		VRAMAvailableMB:    snap.VRAMAvailableMB,
		VRAMTotalMB:        snap.VRAMTotalMB,
		VRAMReservedMB:     m.vramUsed,
		VRAMHeadroomMB:     m.cfg.VRAMHeadroomMB,
		ProtectedLLMVRAMMB: m.cfg.ProtectedLLMVRAMMB,
		GPUProbe:           m.cfg.GPUProbe,
		ProbeError:         snap.Error,
		Reservations:       make([]ReservationStatus, 0, len(m.reserved)),
	}
	for _, r := range m.reserved {
		out.Reservations = append(out.Reservations, ReservationStatus{
			ModelID:  r.ModelID,
			Kind:     r.Kind,
			RAMMB:    r.RAMMB,
			VRAMMB:   r.VRAMMB,
			Overhead: r.Overhead,
		})
	}
	return out
}
