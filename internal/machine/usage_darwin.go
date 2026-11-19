//go:build darwin

package machine

import (
	"encoding/binary"
	"math"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"golang.org/x/sys/unix"
)

func init() { platformCounters = &darwinCounters{} }

// darwinCounters reads macOS the way Activity Monitor does, without cgo: the CPU
// ticks from the Mach host_statistics call (through purego, since the binaries
// are built with CGO_ENABLED=0), memory and load from sysctl.
type darwinCounters struct {
	once           sync.Once
	host           uint32
	hostStatistics func(host uint32, flavor int32, info unsafe.Pointer, count *uint32) int32
}

const (
	// hostCPULoadInfo is HOST_CPU_LOAD_INFO in <mach/host_info.h>: the CPU ticks
	// of all CPUs together, in the order user, system, idle, nice.
	hostCPULoadInfo = 3
	cpuStates       = 4
	cpuStateIdle    = 2
)

func (d *darwinCounters) open() bool {
	d.once.Do(func() {
		lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		self, err := purego.Dlsym(lib, "mach_host_self")
		if err != nil {
			return
		}
		stats, err := purego.Dlsym(lib, "host_statistics")
		if err != nil {
			return
		}
		var machHostSelf func() uint32
		purego.RegisterFunc(&machHostSelf, self)
		purego.RegisterFunc(&d.hostStatistics, stats)
		// One send right for the life of the process: each mach_host_self call
		// adds a reference, so it is taken once rather than per sample.
		d.host = machHostSelf()
	})
	return d.host != 0 && d.hostStatistics != nil
}

func (d *darwinCounters) CPUTicks() (total, idle uint64, cpus int, ok bool) {
	if !d.open() {
		return 0, 0, 0, false
	}
	var ticks [cpuStates]uint32
	count := uint32(cpuStates)
	if d.hostStatistics(d.host, hostCPULoadInfo, unsafe.Pointer(&ticks[0]), &count) != 0 || count != cpuStates {
		return 0, 0, 0, false
	}
	n, err := unix.SysctlUint32("hw.ncpu")
	if err != nil || n == 0 {
		return 0, 0, 0, false
	}
	for _, t := range ticks {
		total += uint64(t)
	}
	return total, uint64(ticks[cpuStateIdle]), int(n), true
}

// Memory counts as available what macOS gives back without swapping: free pages,
// purgeable pages and the file cache (pageable external pages).
func (d *darwinCounters) Memory() (totalMB, availableMB int64, ok bool) {
	size, err := unix.SysctlUint64("hw.memsize")
	if err != nil || size == 0 {
		return 0, 0, false
	}
	page, ok := sysctlCount("vm.pagesize")
	if !ok || page == 0 {
		return 0, 0, false
	}
	var pages uint64
	for _, name := range []string{"vm.page_free_count", "vm.page_purgeable_count", "vm.page_pageable_external_count"} {
		n, ok := sysctlCount(name)
		if !ok {
			return 0, 0, false
		}
		pages += n
	}
	totalMB = int64(size >> 20)
	availableMB = int64(pages * page >> 20)
	if availableMB > totalMB {
		availableMB = totalMB
	}
	return totalMB, availableMB, true
}

// sysctlCount reads an unsigned count that macOS stores in 4 bytes for some names
// and 8 for others (vm.page_purgeable_count is 8, vm.page_free_count is 4).
func sysctlCount(name string) (uint64, bool) {
	raw, err := unix.SysctlRaw(name)
	if err != nil {
		return 0, false
	}
	switch len(raw) {
	case 4:
		return uint64(binary.NativeEndian.Uint32(raw)), true
	case 8:
		return binary.NativeEndian.Uint64(raw), true
	}
	return 0, false
}

func (d *darwinCounters) LoadAverage() (float64, bool) {
	raw, err := unix.SysctlRaw("vm.loadavg")
	if err != nil {
		return 0, false
	}
	return parseLoadavg(raw)
}

// parseLoadavg reads struct loadavg from <sys/resource.h>: three 32-bit fixed-point
// averages, then the long that scales them, aligned to eight bytes.
func parseLoadavg(raw []byte) (float64, bool) {
	if len(raw) < 24 {
		return 0, false
	}
	scale := int64(binary.NativeEndian.Uint64(raw[16:24]))
	if scale <= 0 {
		return 0, false
	}
	v := float64(binary.NativeEndian.Uint32(raw[0:4])) / float64(scale)
	if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}
