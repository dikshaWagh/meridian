package ml

import (
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"testing"
	"time"
)

func fakeProbe(ramAvail, ramTotal, vramAvail, vramTotal int64, vramKnown bool) *FakeProbe {
	return &FakeProbe{Snap: Snapshot{
		RAMAvailableMB:  ramAvail,
		RAMTotalMB:      ramTotal,
		RAMKnown:        true,
		VRAMAvailableMB: vramAvail,
		VRAMTotalMB:     vramTotal,
		VRAMKnown:       vramKnown,
	}}
}

func TestReserveSuccessAndRelease(t *testing.T) {
	m := NewManager(ResourcesConfig{RAMHeadroomMB: 100, VRAMHeadroomMB: 0, ProtectedLLMVRAMMB: 0}, fakeProbe(2048, 4096, 0, 0, false))
	r, ok, reason := m.Reserve("m1", KindOCR, 512, 0, 0)
	if !ok {
		t.Fatalf("reservation should succeed: %s", reason)
	}
	if got := m.Status().RAMReservedMB; got != 512 {
		t.Fatalf("RAMReservedMB = %d, want 512", got)
	}
	m.Release(r)
	if got := m.Status().RAMReservedMB; got != 0 {
		t.Fatalf("RAMReservedMB after release = %d, want 0", got)
	}
}

func TestReserveFailsInsufficientRAM(t *testing.T) {
	m := NewManager(ResourcesConfig{RAMHeadroomMB: 1024}, fakeProbe(1500, 4096, 0, 0, false))
	_, ok, reason := m.Reserve("m1", KindOCR, 1024, 0, 0)
	if ok {
		t.Fatal("reservation should fail: 1500 - 1024 headroom < 1024")
	}
	if reason[:len("insufficient_resources")] != "insufficient_resources" {
		t.Fatalf("unexpected reason: %s", reason)
	}
}

func TestUnknownVRAMIsNotUnlimited(t *testing.T) {
	m := NewManager(ResourcesConfig{}, fakeProbe(4096, 8192, 0, 0, false))
	_, ok, reason := m.Reserve("img", KindImageGeneration, 0, 6144, 1024)
	if ok {
		t.Fatal("GPU-backed reservation must fail when VRAM is unknown")
	}
	if reason[:len("resource_probe_unavailable")] != "resource_probe_unavailable" {
		t.Fatalf("unexpected reason: %s", reason)
	}
}

func TestUnknownRAMBlocksNonZeroReservation(t *testing.T) {
	p := &FakeProbe{Snap: Snapshot{RAMKnown: false}}
	m := NewManager(ResourcesConfig{}, p)
	_, ok, _ := m.Reserve("m1", KindOCR, 512, 0, 0)
	if ok {
		t.Fatal("reservation must fail when RAM is unknown")
	}
}

func TestConcurrentReservationsDoNotOversubscribe(t *testing.T) {
	m := NewManager(ResourcesConfig{RAMHeadroomMB: 0}, fakeProbe(1000, 2000, 0, 0, false))
	var wg sync.WaitGroup
	var mu sync.Mutex
	granted := 0
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok, _ := m.Reserve("m1", KindOCR, 100, 0, 0); ok {
				mu.Lock()
				granted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if granted != 10 {
		t.Fatalf("granted %d reservations, want 10 (1000 MB / 100 MB)", granted)
	}
	if got := m.Status().RAMReservedMB; got != 1000 {
		t.Fatalf("RAMReservedMB = %d, want 1000", got)
	}
}

// TestProtectLLMPolicyNeverSignalsLLM documents the acceptance invariant: the
// resource manager can reject ML admission but has no eviction capability, so a
// running LLM process stays alive no matter how many reservations fail.
func TestProtectLLMPolicyNeverSignalsLLM(t *testing.T) {
	proc := exec.Command("sleep", "30")
	if err := proc.Start(); err != nil {
		t.Skipf("cannot start helper process: %v", err)
	}
	defer func() {
		_ = proc.Process.Kill()
		_ = proc.Wait()
	}()

	m := NewManager(ResourcesConfig{RAMHeadroomMB: 1024}, fakeProbe(512, 1024, 0, 0, false))
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, ok, _ := m.Reserve("img", KindImageGeneration, 8192, 8192, 0); ok {
			t.Fatal("reservation should have been rejected")
		}
	}
	if err := proc.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("LLM process was signalled/killed during ML admission: %v", err)
	}
}

func TestReservationReasonFormat(t *testing.T) {
	m := NewManager(ResourcesConfig{}, fakeProbe(100, 100, 0, 0, false))
	_, ok, reason := m.Reserve("m1", KindOCR, 200, 0, 0)
	if ok {
		t.Fatal("expected failure")
	}
	if want := fmt.Sprintf("insufficient_resources: requested %d MB RAM, available %d MB", 200, 100); reason != want {
		t.Fatalf("reason = %q, want %q", reason, want)
	}
}
