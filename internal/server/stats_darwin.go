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

// cpuTemp is always 0 on macOS. Die temperature has no unprivileged, CGO-free
// reading here: the SMC lives behind the private IOKit interface, and the `smc`
// powermetrics sampler does not even exist on Apple Silicon ("unrecognized
// sampler: smc", confirmed on a real Mac16,12). The signal Apple does expose is
// thermal PRESSURE, which the guard uses instead; see thermalPressure.
func cpuTemp() float64 { return 0 }

// The pressure reading is cached: it is read by both the stats endpoint and the
// guardian loop, each roughly every 15s, and every read is a fresh sudo +
// powermetrics spawn, which is heavy and logs. One reading per TTL is plenty. A
// failed read (no sudoers grant yet, powermetrics missing) backs off hard so a Mac
// that never opted in does not spawn a failing sudo every 10s forever and spam the
// auth log; the long backoff still notices the grant within a couple of minutes,
// and a restart re-probes at once.
var (
	pressMu   sync.Mutex
	pressVal  string
	pressWhen time.Time
)

const (
	pressTTL     = 12 * time.Second // refresh a good reading this often
	pressFailTTL = 2 * time.Minute  // back off after an empty/failed read
)

// thermalPressure returns the host's current thermal pressure level, lowercased
// ("nominal"/"fair"/"serious"/"critical"), or "" when it cannot be read.
func thermalPressure() string {
	pressMu.Lock()
	defer pressMu.Unlock()
	ttl := pressTTL
	if pressVal == "" {
		ttl = pressFailTTL
	}
	if !pressWhen.IsZero() && time.Since(pressWhen) < ttl {
		return pressVal
	}
	pressVal = readThermalPressure()
	pressWhen = time.Now()
	return pressVal
}

// thermalPrivileged reports whether the admin grant is in place. The installer
// grants pmset, powermetrics, and shutdown in one sudoers file, all or nothing,
// so a successful pressure read (powermetrics ran) proves the same file also
// authorizes the lid hold and the poweroff. This reuses the cached read, so it
// costs no extra sudo spawn.
func thermalPrivileged() bool { return thermalPressure() != "" }

func readThermalPressure() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// --samplers thermal: the only temperature-adjacent sampler Apple Silicon has.
	// -n 1 / -i 300: one sample over 300ms then exit. sudo -n: fail rather than
	// prompt when the NOPASSWD entry is missing. Absolute paths for launchd's PATH.
	out, err := exec.CommandContext(ctx, "/usr/bin/sudo", "-n",
		"/usr/bin/powermetrics", "--samplers", "thermal", "-i", "300", "-n", "1").Output()
	if err != nil {
		return ""
	}
	return parseThermalPressure(string(out))
}
