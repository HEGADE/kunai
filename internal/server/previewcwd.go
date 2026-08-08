package server

// Reading a process's working directory, which is how a dev server is still
// attributed to its session after the kernel has reparented it.
//
// Two ways round, because there is no one way that works on both platforms and
// the cheap one is not portable:
//
//   - Linux has /proc/<pid>/cwd, a symlink readable for your own processes. No
//     subprocess at all, so this costs nothing on the platform kunai mostly runs
//     on.
//   - macOS has no /proc, so it asks lsof. Kept as the fallback rather than the
//     primary because this machine's lsof declined to report cwd at all (it
//     answered nothing for `-d cwd` on a process whose /proc link read fine),
//     and a scan that depends on one tool's build options is a scan that stops
//     working for somebody.

import (
	"os"
	"strconv"
	"strings"

	"github.com/hegade/kunai/internal/preview"
)

// cwdsFor returns the working directory of each pid it can read one for.
//
// A pid it cannot read is simply absent: attribution then falls back to ancestry
// for that process, and a missing entry must never be mistaken for a match.
func cwdsFor(pids []int) map[int]string {
	out := make(map[int]string, len(pids))
	var missing []int
	for _, pid := range pids {
		if dir, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd"); err == nil && dir != "" {
			out[pid] = dir
			continue
		}
		missing = append(missing, pid)
	}
	if len(missing) == 0 {
		return out
	}

	// macOS, or a Linux process whose link could not be read.
	args := []string{"-a", "-d", "cwd", "-F", "pn", "-p", joinPIDs(missing)}
	if raw, err := previewScan("lsof", args...); err == nil {
		for pid, dir := range preview.ParseCwds(raw) {
			out[pid] = dir
		}
	}
	return out
}

func joinPIDs(pids []int) string {
	parts := make([]string, 0, len(pids))
	for _, p := range pids {
		parts = append(parts, strconv.Itoa(p))
	}
	return strings.Join(parts, ",")
}
