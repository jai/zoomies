package machine

// hostCounters is a platform's own way to read the machine, for a platform with no
// procfs. Each reading is the whole machine, like procfs is, and answers false
// when it cannot be taken: false is unmeasured, never zero.
type hostCounters interface {
	// CPUTicks is the machine's CPU time since boot, all CPUs together: the
	// total and the idle part of it, and how many CPUs it counts.
	CPUTicks() (total, idle uint64, cpus int, ok bool)
	// Memory is the machine's memory and the part a new job could have.
	Memory() (totalMB, availableMB int64, ok bool)
	// LoadAverage is the one-minute load average.
	LoadAverage() (float64, bool)
}

// platformCounters is set by a platform that has a native source (macOS); it is
// nil where procfs is the source.
var platformCounters hostCounters

func (s *UsageSampler) counters() hostCounters {
	if s.native != nil {
		return s.native
	}
	if s.root == "" {
		return platformCounters
	}
	return nil
}

// sampleNative is Sample for a platform with no procfs. It applies the same
// checks: the CPU figure is a delta between two samples of the same machine, and
// a reading that is not the machine the caller believes it is on is left out.
// The platform reports no I/O wait or steal, so both stay unmeasured.
func (s *UsageSampler) sampleNative(c hostCounters, cpus int, memoryMB int64) Usage {
	var out Usage
	total, idle, count, ok := c.CPUTicks()
	if ok && count == cpus && count == s.cpus && total > s.total && idle >= s.idle && idle-s.idle <= total-s.total {
		percent := 100 * (1 - float64(idle-s.idle)/float64(total-s.total))
		out.CPUPercent = &percent
	}
	if ok && count == cpus {
		s.total, s.idle, s.cpus = total, idle, count
	} else {
		s.total, s.idle, s.cpus = 0, 0, 0
	}
	if totalMB, available, ok := c.Memory(); ok && totalMB == memoryMB {
		out.MemoryAvailableMB = &available
	}
	if s.cpus == cpus && cpus > 0 {
		if v, ok := c.LoadAverage(); ok {
			out.LoadAverage1 = &v
		}
	}
	return out
}
