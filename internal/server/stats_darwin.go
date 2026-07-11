//go:build darwin

package server

import (
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// macOS has no /proc, so we read the same figures from sysctl and vm_stat.
// Shelling out keeps the build CGO-free and static.

// hostUptimeLoad derives uptime from kern.boottime and load from vm.loadavg.
func hostUptimeLoad() (int64, float64) {
	var uptime int64
	var load float64

	// kern.boottime → "{ sec = 1752000000, usec = 0 } Thu ..."
	if out, err := exec.Command("/usr/sbin/sysctl", "-n", "kern.boottime").Output(); err == nil {
		if i := strings.Index(string(out), "sec ="); i >= 0 {
			rest := string(out)[i+len("sec ="):]
			digits := strings.FieldsFunc(rest, func(r rune) bool { return r < '0' || r > '9' })
			if len(digits) > 0 {
				if boot, err := strconv.ParseInt(digits[0], 10, 64); err == nil && boot > 0 {
					uptime = time.Now().Unix() - boot
				}
			}
		}
	}

	// vm.loadavg → "{ 1.85 1.72 1.60 }"
	if out, err := exec.Command("/usr/sbin/sysctl", "-n", "vm.loadavg").Output(); err == nil {
		f := strings.Fields(strings.Trim(strings.TrimSpace(string(out)), "{} "))
		if len(f) > 0 {
			load, _ = strconv.ParseFloat(f[0], 64)
		}
	}
	return uptime, load
}

// memInfo reads hw.memsize for total and approximates available memory as
// (free + inactive + speculative) pages from vm_stat.
func memInfo() (total, avail uint64) {
	if out, err := exec.Command("/usr/sbin/sysctl", "-n", "hw.memsize").Output(); err == nil {
		total, _ = strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
	}

	pagesize := uint64(4096)
	if out, err := exec.Command("/usr/sbin/sysctl", "-n", "hw.pagesize").Output(); err == nil {
		if p, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64); err == nil && p > 0 {
			pagesize = p
		}
	}

	out, err := exec.Command("/usr/bin/vm_stat").Output()
	if err != nil {
		return total, 0
	}
	var pages uint64
	for _, line := range strings.Split(string(out), "\n") {
		for _, key := range []string{"Pages free", "Pages inactive", "Pages speculative"} {
			if !strings.HasPrefix(line, key+":") {
				continue
			}
			v := strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
			v = strings.TrimSuffix(v, ".")
			if n, err := strconv.ParseUint(v, 10, 64); err == nil {
				pages += n
			}
		}
	}
	return total, pages * pagesize
}
