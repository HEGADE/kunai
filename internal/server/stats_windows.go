package server

import (
	"syscall"
	"unsafe"
)

// Windows host stats via kernel32 (pure syscall, no cgo, no extra deps).
var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	procGetDiskFreeSpaceExW  = kernel32.NewProc("GetDiskFreeSpaceExW")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
	procGetTickCount64       = kernel32.NewProc("GetTickCount64")
)

func diskInfo(dir string) (total, free uint64) {
	if dir == "" {
		dir = `C:\`
	}
	p, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		return 0, 0
	}
	var freeAvail, totalBytes, totalFree uint64
	r, _, _ := procGetDiskFreeSpaceExW.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&freeAvail)),
		uintptr(unsafe.Pointer(&totalBytes)),
		uintptr(unsafe.Pointer(&totalFree)),
	)
	if r == 0 {
		return 0, 0
	}
	return totalBytes, freeAvail
}

type memoryStatusEx struct {
	dwLength                uint32
	dwMemoryLoad            uint32
	ullTotalPhys            uint64
	ullAvailPhys            uint64
	ullTotalPageFile        uint64
	ullAvailPageFile        uint64
	ullTotalVirtual         uint64
	ullAvailVirtual         uint64
	ullAvailExtendedVirtual uint64
}

func memInfo() (total, avail uint64) {
	var m memoryStatusEx
	m.dwLength = uint32(unsafe.Sizeof(m))
	r, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m)))
	if r == 0 {
		return 0, 0
	}
	return m.ullTotalPhys, m.ullAvailPhys
}

// hostUptimeLoad returns host uptime; Windows has no load average, so load is 0.
func hostUptimeLoad() (uptimeSec int64, load1 float64) {
	r, _, _ := procGetTickCount64.Call()
	return int64(uint64(r) / 1000), 0
}

// cpuTemp is not implemented on Windows (no unprivileged, dependency-free path);
// the guardian relies on its wall-clock cap there.
func cpuTemp() float64 { return 0 }

// thermalPressure is a macOS concept; empty on Windows.
func thermalPressure() string { return "" }

// thermalPrivileged: no privileged thermal actions on Windows.
func thermalPrivileged() bool { return false }
