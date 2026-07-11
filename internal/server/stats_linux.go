//go:build linux

package server

import (
	"os"
	"strconv"
	"strings"
)

// hostUptimeLoad reads /proc for uptime and 1-minute load average.
func hostUptimeLoad() (int64, float64) {
	var uptime int64
	var load float64
	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			if v, err := strconv.ParseFloat(f[0], 64); err == nil {
				uptime = int64(v)
			}
		}
	}
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			load, _ = strconv.ParseFloat(f[0], 64)
		}
	}
	return uptime, load
}

// memInfo reads /proc/meminfo for total and available memory (bytes).
func memInfo() (total, avail uint64) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		kb, err := strconv.ParseUint(f[1], 10, 64)
		if err != nil {
			continue
		}
		switch f[0] {
		case "MemTotal:":
			total = kb * 1024
		case "MemAvailable:":
			avail = kb * 1024
		}
	}
	return total, avail
}
