//go:build darwin

package machine

import (
	"encoding/binary"
	"runtime"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestDarwinCountersReadThisMac(t *testing.T) {
	c := &darwinCounters{}
	total, idle, cpus, ok := c.CPUTicks()
	if !ok || cpus != runtime.NumCPU() || total == 0 || idle > total {
		t.Fatalf("CPU ticks: total %d idle %d cpus %d ok %v (NumCPU %d)", total, idle, cpus, ok, runtime.NumCPU())
	}
	size, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		t.Fatal(err)
	}
	totalMB, available, ok := c.Memory()
	if !ok || totalMB != int64(size>>20) || available <= 0 || available > totalMB {
		t.Fatalf("memory: total %d available %d ok %v (hw.memsize %d MB)", totalMB, available, ok, size>>20)
	}
	if load, ok := c.LoadAverage(); !ok || load < 0 {
		t.Fatalf("load average %v ok %v", load, ok)
	}
}

func TestDarwinSampleMeasuresAnInterval(t *testing.T) {
	size, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		t.Fatal(err)
	}
	var s UsageSampler // root "": the platform's own counters
	cpus, memoryMB := runtime.NumCPU(), int64(size>>20)
	s.Sample(cpus, memoryMB)
	time.Sleep(200 * time.Millisecond) // about 100 ticks a second for each CPU
	got := s.Sample(cpus, memoryMB)
	if got.CPUPercent == nil || *got.CPUPercent < 0 || *got.CPUPercent > 100 {
		t.Fatalf("CPU = %v, want a share between 0 and 100", got.CPUPercent)
	}
	if got.MemoryAvailableMB == nil || got.LoadAverage1 == nil {
		t.Fatalf("memory and load must be measured on macOS: %+v", got)
	}
}

func TestParseLoadavg(t *testing.T) {
	raw := make([]byte, 24)
	binary.NativeEndian.PutUint32(raw[0:4], 2048*3/2) // 1.5 at fscale 2048
	binary.NativeEndian.PutUint64(raw[16:24], 2048)
	if v, ok := parseLoadavg(raw); !ok || v != 1.5 {
		t.Fatalf("load = %v ok %v, want 1.5", v, ok)
	}
	if _, ok := parseLoadavg(raw[:20]); ok {
		t.Fatal("a short struct must not parse")
	}
	binary.NativeEndian.PutUint64(raw[16:24], 0)
	if _, ok := parseLoadavg(raw); ok {
		t.Fatal("a zero scale must not parse")
	}
}
