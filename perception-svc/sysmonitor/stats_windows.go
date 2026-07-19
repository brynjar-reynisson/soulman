//go:build windows

package sysmonitor

import (
	"fmt"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// New builds a Watcher backed by real Windows system calls
// (golang.org/x/sys/windows — already an indirect dependency of this
// module via oauth2/nats.go, promoted to direct for this package) for
// disk, memory, and CPU statistics, and a real TCP/HTTP client for
// service_health checks.
func New(checks []CheckConfig, publisher Publisher, interval time.Duration) *Watcher {
	return newWatcher(&winStats{}, httpTCPHealthChecker{}, checks, publisher, interval)
}

// winStats implements statsProvider. CPU usage needs the previous poll's
// cumulative idle/total time to compute a delta, so it carries that state
// internally (mutex-guarded since Watcher's poll loop and any future
// concurrent caller could both invoke it).
type winStats struct {
	mu          sync.Mutex
	haveCPUPrev bool
	prevIdle    uint64
	prevTotal   uint64
}

func (s *winStats) DiskUsagePercent(path string) (float64, error) {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, fmt.Errorf("sysmonitor: invalid disk path %q: %w", path, err)
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(ptr, &free, &total, &totalFree); err != nil {
		return 0, fmt.Errorf("sysmonitor: GetDiskFreeSpaceEx(%q): %w", path, err)
	}
	if total == 0 {
		return 0, fmt.Errorf("sysmonitor: GetDiskFreeSpaceEx(%q) reported zero total bytes", path)
	}
	return 100 * (1 - float64(free)/float64(total)), nil
}

// memoryStatusEx mirrors the Win32 MEMORYSTATUSEX struct. golang.org/x/sys/windows
// does not wrap GlobalMemoryStatusEx (verified against the resolved v0.47.0 module
// source — no MemoryStatusEx type or GlobalMemoryStatusEx func exists there, unlike
// GetDiskFreeSpaceEx which the package does provide directly), so this calls
// kernel32.dll!GlobalMemoryStatusEx directly via a lazy proc, matching the layout
// from the Win32 API docs.
type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

var (
	modkernel32              = windows.NewLazySystemDLL("kernel32.dll")
	procGlobalMemoryStatusEx = modkernel32.NewProc("GlobalMemoryStatusEx")
	procGetSystemTimes       = modkernel32.NewProc("GetSystemTimes")
)

func (s *winStats) MemoryUsagePercent() (float64, error) {
	mem := memoryStatusEx{Length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	r1, _, err := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&mem)))
	runtime.KeepAlive(&mem)
	if r1 == 0 {
		return 0, fmt.Errorf("sysmonitor: GlobalMemoryStatusEx: %w", err)
	}
	return float64(mem.MemoryLoad), nil
}

// GetSystemTimes is likewise not wrapped by golang.org/x/sys/windows (only
// GetSystemTimeAsFileTime, the wall-clock variant, is present there) so it is
// called directly via a lazy proc too. windows.Filetime is reused as the output
// struct shape since that type IS provided by the package.
func (s *winStats) CPUUsagePercent() (float64, error) {
	var idle, kernel, user windows.Filetime
	r1, _, err := procGetSystemTimes.Call(
		uintptr(unsafe.Pointer(&idle)),
		uintptr(unsafe.Pointer(&kernel)),
		uintptr(unsafe.Pointer(&user)),
	)
	runtime.KeepAlive(&idle)
	runtime.KeepAlive(&kernel)
	runtime.KeepAlive(&user)
	if r1 == 0 {
		return 0, fmt.Errorf("sysmonitor: GetSystemTimes: %w", err)
	}

	idleNow := filetimeToUint64(idle)
	// kernel time from GetSystemTimes already includes idle time, so total
	// elapsed time is (kernel + user); non-idle work is (total - idle).
	totalNow := filetimeToUint64(kernel) + filetimeToUint64(user)

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.haveCPUPrev {
		s.prevIdle = idleNow
		s.prevTotal = totalNow
		s.haveCPUPrev = true
		return 0, errNoCPUBaseline
	}

	idleDelta := idleNow - s.prevIdle
	totalDelta := totalNow - s.prevTotal
	s.prevIdle = idleNow
	s.prevTotal = totalNow

	if totalDelta == 0 {
		return 0, errNoCPUBaseline
	}

	return 100 * (1 - float64(idleDelta)/float64(totalDelta)), nil
}

func filetimeToUint64(ft windows.Filetime) uint64 {
	return uint64(ft.HighDateTime)<<32 | uint64(ft.LowDateTime)
}
