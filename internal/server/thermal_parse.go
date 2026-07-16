package server

import (
	"strconv"
	"strings"
)

// parsePowermetricsTemp pulls a CPU die temperature (Celsius) out of the SMC
// sampler's output. Platform-neutral on purpose: the reader that runs it is
// macOS-only and unrunnable on the Linux dev box, but the text parsing is where
// the risk is, and this can be tested against captured samples.
//
// Sample lines look like:
//
//	CPU die temperature: 51.23 C
//	GPU die temperature: 44.00 C
//
// Prefer the CPU line; fall back to the hottest other "die temperature" line so a
// renamed or missing CPU key still yields a real signal rather than zero.
func parsePowermetricsTemp(out string) float64 {
	var fallback float64
	for _, line := range strings.Split(out, "\n") {
		low := strings.ToLower(line)
		if !strings.Contains(low, "die temperature") {
			continue
		}
		c := parseTempLine(line)
		if c <= 0 {
			continue
		}
		if strings.Contains(low, "cpu") {
			return c
		}
		if c > fallback {
			fallback = c
		}
	}
	return fallback
}

// parseTempLine pulls the Celsius value out of a "... : 51.23 C" line.
func parseTempLine(line string) float64 {
	i := strings.LastIndex(line, ":")
	if i < 0 {
		return 0
	}
	fields := strings.Fields(line[i+1:])
	if len(fields) == 0 {
		return 0
	}
	c, err := strconv.ParseFloat(strings.TrimSuffix(fields[0], "C"), 64)
	if err != nil {
		return 0
	}
	return c
}
