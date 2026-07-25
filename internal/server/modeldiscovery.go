package server

// Model discovery: read the newest version per Claude family (opus/sonnet/haiku/
// fable) straight from the claude binary, so the app's model picker always matches
// the CLI's own /model list -- a new model (Opus 5) shows up the moment the CLI
// knows it, with no hardcoded list, no release, and no model call. The binary
// bakes in every model id it can serve (e.g. "claude-opus-5"); we take the highest
// version per family. Cached per binary (path+size+mtime) because the executable
// is large (~260MB), and the scan streams it rather than loading it whole.

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
)

// claudeModelIDRe matches a baked-in model id and captures family + major + minor.
var claudeModelIDRe = regexp.MustCompile(`claude-(opus|sonnet|haiku|fable)-([0-9]+)(?:-([0-9]+))?`)

type modelVersionCache struct {
	mu   sync.Mutex
	key  string
	vers map[string]string
}

// discoverModelVersions returns family -> newest version ("opus"->"5",
// "haiku"->"4.5") from the default account's claude binary, or nil if it can't be
// read (a wrapper script, a missing binary): the client then falls back to the
// version learned from real sessions, then to its seed.
func (s *Server) discoverModelVersions() map[string]string {
	bin := s.resolveCLI("").Bin
	if bin == "" {
		bin = "claude"
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return nil
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil
	}
	key := path + "|" + strconv.FormatInt(fi.Size(), 10) + "|" + strconv.FormatInt(fi.ModTime().UnixNano(), 10)

	s.modelVers.mu.Lock()
	defer s.modelVers.mu.Unlock()
	if s.modelVers.key == key && s.modelVers.vers != nil {
		return s.modelVers.vers
	}
	vers := scanClaudeModelVersions(path)
	s.modelVers.key, s.modelVers.vers = key, vers
	return vers
}

// scanClaudeModelVersions streams the binary and returns the highest version per
// family. Streaming with an overlap keeps a model id from being split across a
// read boundary without holding the whole 260MB file in memory.
func scanClaudeModelVersions(path string) map[string]string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	best := map[string]int{} // family -> major*100+minor, for comparison
	out := map[string]string{}
	consider := func(b []byte) {
		for _, m := range claudeModelIDRe.FindAllSubmatch(b, -1) {
			fam := string(m[1])
			major, _ := strconv.Atoi(string(m[2]))
			minor := 0
			// A 1-2 digit second segment is a minor version (opus-4-8 -> 4.8); a
			// longer one is a date (opus-4-20250514), not a minor, so ignore it.
			if len(m[3]) > 0 && len(m[3]) <= 2 {
				minor, _ = strconv.Atoi(string(m[3]))
			}
			if score := major*100 + minor; score > best[fam] {
				best[fam] = score
				if minor > 0 {
					out[fam] = strconv.Itoa(major) + "." + strconv.Itoa(minor)
				} else {
					out[fam] = strconv.Itoa(major)
				}
			}
		}
	}

	const chunk = 4 << 20
	const overlap = 64 // longer than any model id, so one is never split in two
	buf := make([]byte, chunk)
	var tail []byte
	for {
		n, err := f.Read(buf)
		if n > 0 {
			window := append(tail, buf[:n]...)
			consider(window)
			if len(window) > overlap {
				tail = append(tail[:0], window[len(window)-overlap:]...)
			} else {
				tail = append(tail[:0], window...)
			}
		}
		if err != nil {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
