//go:build darwin

package server

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"sync"
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

// macOS has no /sys or sysctl key for die temperature; it lives in the SMC,
// reachable only through the private IOKit interface (needs CGO, which this build
// avoids) or `powermetrics` (needs root). The owner grants a NOPASSWD sudoers
// entry for powermetrics at install time, which is the door this uses. Absolute
// paths for launchd's minimal PATH, like the rest of this file.
//
// It is cached: cpuTemp is read by both the stats endpoint and the guardian loop,
// each every ~15s, and a fresh sudo+powermetrics spawn per call would be wasteful
// and noisy. One reading every tempTTL is plenty for a thermal guard.
//
// UNVERIFIED on real hardware from the Linux dev box. On Apple Silicon the SMC
// sampler's coverage is patchy; if the parse finds nothing the guard degrades to
// its wall-clock cap, which is the safe direction.
var (
	tempMu   sync.Mutex
	tempVal  float64
	tempWhen time.Time
)

const (
	tempTTL     = 10 * time.Second // refresh a real reading this often
	tempFailTTL = 2 * time.Minute  // back off hard after a failed/empty read
)

// cpuTemp reads the cached temperature, refreshing at most every tempTTL. A
// failed read (no sudoers grant, powermetrics missing, unparseable) backs off for
// tempFailTTL instead: on a Mac that never opted into the privileged temperature
// feature this is every stats poll and every guardian tick, and a failing sudo
// every 10s forever would spam the auth log. The long backoff still notices the
// grant being added within a couple of minutes, and a restart re-probes at once.
func cpuTemp() float64 {
	tempMu.Lock()
	defer tempMu.Unlock()
	ttl := tempTTL
	if tempVal <= 0 {
		ttl = tempFailTTL
	}
	if !tempWhen.IsZero() && time.Since(tempWhen) < ttl {
		return tempVal
	}
	tempVal = readPowermetricsTemp()
	tempWhen = time.Now()
	return tempVal
}

func readPowermetricsTemp() float64 {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	// -n 1: one sample then exit. -i 200: sample over 200ms. sudo -n: fail rather
	// than prompt if the NOPASSWD entry is missing.
	out, err := exec.CommandContext(ctx, "/usr/bin/sudo", "-n",
		"/usr/bin/powermetrics", "--samplers", "smc", "-i", "200", "-n", "1").Output()
	if err != nil {
		return 0
	}
	// The parse lives in a platform-neutral file so it can be unit-tested on the
	// Linux dev box against captured powermetrics output.
	return parsePowermetricsTemp(string(out))
}
