package server

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

// Stats is the home-screen snapshot of the host and the kunai process. Fields
// the platform can't provide are zero and hidden by the client.
type Stats struct {
	Hostname      string  `json:"hostname"`
	OS            string  `json:"os"`
	Arch          string  `json:"arch"`
	Sessions      int     `json:"sessions"`
	UptimeSec     int64   `json:"uptime_sec"`     // host uptime
	Load1         float64 `json:"load1"`          // 1-minute load average
	MemTotal      uint64  `json:"mem_total"`      // bytes
	MemAvailable  uint64  `json:"mem_available"`  // bytes
	DiskTotal     uint64  `json:"disk_total"`     // bytes (data dir filesystem)
	DiskFree      uint64  `json:"disk_free"`      // bytes
	Cores         int     `json:"cores"`          // logical CPUs
	ClaudeVersion string  `json:"claude_version"` //
	KunaiVersion  string  `json:"kunai_version"`  // build revision
	KunaiUptime   int64   `json:"kunai_uptime_sec"`
	KeepAwake     bool    `json:"keep_awake"`           // idle-sleep hold currently held
	KeepAwakeSupp bool    `json:"keep_awake_supported"` // platform can hold it
	// CPUTempC is the hottest CPU sensor in degrees Celsius, 0 on macOS (which has
	// no unprivileged die temperature; it reports ThermalPressure instead, and the
	// client shows whichever the host has). ThermalTrip is set while the guardian
	// is holding everything stopped after a trip.
	CPUTempC    float64 `json:"cpu_temp_c"`
	ThermalTrip bool    `json:"thermal_trip"`
	// ThermalPressure is the macOS thermal pressure level ("nominal".."critical"),
	// empty on hosts that report real degrees instead (Linux). Apple Silicon has no
	// unprivileged die temperature, so this is what its guard runs on.
	ThermalPressure string `json:"thermal_pressure"`
	// The guard's live policy, so the Settings fan-out can render it without a
	// second fetch (it is tailnet-only and holds no secret).
	ThermalGuard    bool    `json:"thermal_guard"`
	ThermalSoftC    float64 `json:"thermal_soft_c"`
	ThermalMaxHours float64 `json:"thermal_max_hours"`
	// ThermalPrivileged is true when the admin grant (sudoers/polkit) that lets the
	// guard power the host off and hold the lid is actually in place, so the UI can
	// say "ready" instead of "needs setup".
	ThermalPrivileged bool    `json:"thermal_privileged"`
	ThermalHardC      float64 `json:"thermal_hard_c"`
	ThermalAction     string  `json:"thermal_action"` // "sleep" | "poweroff"
	KeepLid           bool    `json:"keep_lid"`       // lid-closed hold currently held
	KeepLidSupp       bool    `json:"keep_lid_supported"`
	// RateResets maps a usage window ("five_hour"/"seven_day") to the unix time
	// it resets, as last reported by the CLI. Drives scheduler previews.
	RateResets map[string]int64 `json:"rate_resets,omitempty"`
}

var (
	serverStart     = time.Now()
	claudeVerOnce   sync.Once
	claudeVerCached string
	kunaiVerOnce    sync.Once
	kunaiVerCached  string
)

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	st := Stats{
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		Sessions:      len(s.mgr.List()),
		Cores:         runtime.NumCPU(),
		KunaiVersion:  kunaiVersion(),
		KunaiUptime:   int64(time.Since(serverStart).Seconds()),
		KeepAwake:     s.awake.Enabled(),
		KeepAwakeSupp: s.awake.Supported(),
		RateResets:    s.sched.Resets(),
	}
	st.Hostname, _ = os.Hostname()
	st.UptimeSec, st.Load1 = hostUptimeLoad()
	st.MemTotal, st.MemAvailable = memInfo()
	st.DiskTotal, st.DiskFree = diskInfo(s.cfg.DataDir)
	st.ClaudeVersion = claudeVersion()
	st.CPUTempC = cpuTemp()
	st.ThermalPressure = thermalPressure()
	st.ThermalTrip = s.guardian.tripped()
	gc := s.guardian.config()
	st.ThermalGuard, st.ThermalSoftC, st.ThermalMaxHours = gc.Enabled, gc.SoftC, gc.MaxHours
	st.ThermalHardC, st.ThermalAction = gc.HardC, gc.Action
	st.KeepLid, st.KeepLidSupp = s.lid.Enabled(), s.lid.Supported()
	st.ThermalPrivileged = thermalPrivileged()
	writeJSON(w, http.StatusOK, st)
}

// buildVersion is injected at build time via -ldflags "-X …server.buildVersion=".
// The Makefile sets it from `git describe`; direct `go build` leaves it empty
// and we fall back to the VCS revision the toolchain stamps.
var buildVersion = ""

// kunaiVersion reports the injected build version, else the VCS revision (short)
// stamped by the Go toolchain, else "dev".
func kunaiVersion() string {
	kunaiVerOnce.Do(func() {
		if buildVersion != "" {
			kunaiVerCached = buildVersion
			return
		}
		kunaiVerCached = "dev"
		info, ok := debug.ReadBuildInfo()
		if !ok {
			return
		}
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" && s.Value != "" {
				if len(s.Value) > 7 {
					kunaiVerCached = s.Value[:7]
				} else {
					kunaiVerCached = s.Value
				}
			}
		}
	})
	return kunaiVerCached
}

// claudeVersion shells out once and caches (the binary is the source of truth).
func claudeVersion() string {
	claudeVerOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "claude", "--version").Output()
		if err == nil {
			claudeVerCached = strings.TrimSpace(strings.Fields(string(out))[0])
		}
	})
	return claudeVerCached
}

// hostUptimeLoad, memInfo, and diskInfo are platform-specific (stats_unix.go for
// darwin/linux, stats_windows.go for Windows); they return zero for values the
// platform can't provide.
