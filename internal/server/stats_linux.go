//go:build linux

package server

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// cpuTempDrivers are the hwmon `name` values that mean "this is the CPU package
// sensor". k10temp is AMD, coretemp is Intel, cpu_thermal is common on ARM SoCs.
var cpuTempDrivers = map[string]bool{
	"k10temp":     true,
	"coretemp":    true,
	"cpu_thermal": true,
	"zenpower":    true,
}

// cpuTemp returns the CPU temperature in degrees Celsius, or 0 when it cannot be
// read. It prefers a real CPU driver under /sys/class/hwmon (the hwmonN index is
// not stable across boots, so it matches by driver name, never by number) and
// takes the hottest sensor that driver exposes, because for a safety guard the
// worst reading is the one that matters. It falls back to thermal_zone0, which
// on many machines is a motherboard/ACPI sensor rather than the die.
func cpuTemp() float64 {
	if t := hwmonCPUTemp(); t > 0 {
		return t
	}
	if b, err := os.ReadFile("/sys/class/thermal/thermal_zone0/temp"); err == nil {
		if milli, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64); err == nil {
			return milli / 1000
		}
	}
	return 0
}

func hwmonCPUTemp() float64 {
	dirs, err := filepath.Glob("/sys/class/hwmon/hwmon*")
	if err != nil {
		return 0
	}
	var hottest float64
	for _, dir := range dirs {
		name, err := os.ReadFile(filepath.Join(dir, "name"))
		if err != nil || !cpuTempDrivers[strings.TrimSpace(string(name))] {
			continue
		}
		inputs, _ := filepath.Glob(filepath.Join(dir, "temp*_input"))
		for _, in := range inputs {
			b, err := os.ReadFile(in)
			if err != nil {
				continue
			}
			milli, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
			if err != nil {
				continue
			}
			if c := milli / 1000; c > hottest {
				hottest = c
			}
		}
	}
	return hottest
}

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

// thermalPressure has no macOS-style pressure level on Linux; the guard uses the
// real cpuTemp() degrees here, so this is always empty.
func thermalPressure() string { return "" }
