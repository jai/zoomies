package machine

import "testing"

type fakeCounters struct {
	total, idle uint64
	cpus        int
	ticksOK     bool
	totalMB     int64
	availableMB int64
	memOK       bool
	load        float64
	loadOK      bool
}

func (f *fakeCounters) CPUTicks() (uint64, uint64, int, bool) {
	return f.total, f.idle, f.cpus, f.ticksOK
}
func (f *fakeCounters) Memory() (int64, int64, bool) { return f.totalMB, f.availableMB, f.memOK }
func (f *fakeCounters) LoadAverage() (float64, bool) { return f.load, f.loadOK }

func TestNativeSampleTakesCPUFromTheDeltaBetweenSamples(t *testing.T) {
	c := &fakeCounters{total: 1000, idle: 800, cpus: 8, ticksOK: true, totalMB: 16384, availableMB: 4096, memOK: true, load: 3.5, loadOK: true}
	s := UsageSampler{native: c}
	first := s.Sample(8, 16384)
	if first.CPUPercent != nil {
		t.Fatalf("first sample has no interval, got CPU %v", *first.CPUPercent)
	}
	if first.MemoryAvailableMB == nil || *first.MemoryAvailableMB != 4096 || first.LoadAverage1 == nil || *first.LoadAverage1 != 3.5 {
		t.Fatalf("first sample memory and load: %+v", first)
	}
	c.total, c.idle = 1200, 850 // 200 ticks, 50 idle
	second := s.Sample(8, 16384)
	if second.CPUPercent == nil || *second.CPUPercent != 75 {
		t.Fatalf("CPU = %v, want 75", second.CPUPercent)
	}
	if second.IOWaitPercent != nil || second.StealPercent != nil {
		t.Fatal("macOS reports no I/O wait or steal; they must stay unmeasured")
	}
}

func TestNativeSampleLeavesOutAMachineThatIsNotTheCallers(t *testing.T) {
	c := &fakeCounters{total: 1000, idle: 800, cpus: 8, ticksOK: true, totalMB: 16384, availableMB: 4096, memOK: true, load: 2, loadOK: true}
	s := UsageSampler{native: c}
	s.Sample(4, 8192)
	c.total, c.idle = 1200, 850
	got := s.Sample(4, 8192)
	if got.CPUPercent != nil || got.MemoryAvailableMB != nil || got.LoadAverage1 != nil {
		t.Fatalf("a different CPU count and memory size must leave every figure out: %+v", got)
	}
}

func TestNativeSampleDropsACounterThatWentBackwards(t *testing.T) {
	c := &fakeCounters{total: 5000, idle: 4000, cpus: 2, ticksOK: true}
	s := UsageSampler{native: c}
	s.Sample(2, 0)
	c.total, c.idle = 100, 50 // 32-bit ticks wrapped
	if got := s.Sample(2, 0); got.CPUPercent != nil {
		t.Fatalf("a wrapped counter gave CPU %v", *got.CPUPercent)
	}
	c.total, c.idle = 300, 100 // 200 ticks since the wrap, 50 idle
	if got := s.Sample(2, 0); got.CPUPercent == nil || *got.CPUPercent != 75 {
		t.Fatalf("after a wrap the next interval is measured again: CPU = %v, want 75", got.CPUPercent)
	}
}

func TestNativeSampleReportsNothingItCouldNotRead(t *testing.T) {
	s := UsageSampler{native: &fakeCounters{}}
	s.Sample(4, 1024)
	if got := s.Sample(4, 1024); got.CPUPercent != nil || got.MemoryAvailableMB != nil || got.LoadAverage1 != nil {
		t.Fatalf("unreadable counters must be unmeasured, not zero: %+v", got)
	}
}
