package server

import "strings"

// parseThermalPressure pulls the level out of `powermetrics --samplers thermal`.
// Apple Silicon has no unprivileged die-temperature reading (the `smc` sampler
// does not even exist there), so thermal pressure is the signal it does expose,
// and the one Apple designed for "should I back off". Real output:
//
//	**** Thermal pressure ****
//
//	Current pressure level: Nominal
//
// Returns the level lowercased ("nominal"/"fair"/"serious"/"critical"), or "" when
// no line is found. Platform-neutral so it is testable off a Mac against captured
// output, which is the only way this can be verified from Linux.
func parseThermalPressure(out string) string {
	for _, line := range strings.Split(out, "\n") {
		i := strings.Index(strings.ToLower(line), "pressure level:")
		if i < 0 {
			continue
		}
		level := strings.TrimSpace(line[i+len("pressure level:"):])
		return strings.ToLower(level)
	}
	return ""
}

// pressureTooHot reports whether a level means "stop and cool": Serious is active
// throttling, Critical is worse. Fair and Nominal are fine.
func pressureTooHot(level string) bool {
	return level == "serious" || level == "critical"
}
