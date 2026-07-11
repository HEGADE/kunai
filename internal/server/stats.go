package server

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
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
	ClaudeVersion string  `json:"claude_version"` //
	KunaiUptime   int64   `json:"kunai_uptime_sec"`
}

var (
	serverStart     = time.Now()
	claudeVerOnce   sync.Once
	claudeVerCached string
)

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	st := Stats{
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		Sessions:    len(s.mgr.List()),
		KunaiUptime: int64(time.Since(serverStart).Seconds()),
	}
	st.Hostname, _ = os.Hostname()
	st.UptimeSec, st.Load1 = hostUptimeLoad()
	st.MemTotal, st.MemAvailable = memInfo()
	st.DiskTotal, st.DiskFree = diskInfo(s.cfg.DataDir)
	st.ClaudeVersion = claudeVersion()
	writeJSON(w, http.StatusOK, st)
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

// hostUptimeLoad reads /proc on Linux; zero elsewhere.
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

// memInfo reads /proc/meminfo on Linux; zero elsewhere.
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
