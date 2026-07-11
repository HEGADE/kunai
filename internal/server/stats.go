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
	"syscall"
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
		OS:           runtime.GOOS,
		Arch:         runtime.GOARCH,
		Sessions:     len(s.mgr.List()),
		Cores:        runtime.NumCPU(),
		KunaiVersion: kunaiVersion(),
		KunaiUptime:  int64(time.Since(serverStart).Seconds()),
	}
	st.Hostname, _ = os.Hostname()
	st.UptimeSec, st.Load1 = hostUptimeLoad()
	st.MemTotal, st.MemAvailable = memInfo()
	st.DiskTotal, st.DiskFree = diskInfo(s.cfg.DataDir)
	st.ClaudeVersion = claudeVersion()
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

// hostUptimeLoad and memInfo are platform-specific (stats_linux.go /
// stats_darwin.go); they return zero for values the platform can't provide.

func diskInfo(dir string) (total, free uint64) {
	if dir == "" {
		dir = "/"
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, 0
	}
	bs := uint64(st.Bsize)
	return st.Blocks * bs, st.Bavail * bs
}
