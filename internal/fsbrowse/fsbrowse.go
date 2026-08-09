// Package fsbrowse provides a minimal, read-only directory listing so the phone
// can pick any project path on the host without blind-typing. There is no path
// allowlist by design: the tailnet ACL is the entire auth perimeter, and the
// user is choosing a directory to run their own Claude Code in.
package fsbrowse

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Entry is one item in a directory listing.
type Entry struct {
	Name string `json:"name"`
	Dir  bool   `json:"dir"`
	Path string `json:"path"`
}

// Listing is a browsable directory: its absolute path, its parent (empty at
// root), and its immediate children (directories first).
type Listing struct {
	Path    string  `json:"path"`
	Parent  string  `json:"parent"`
	Entries []Entry `json:"entries"`
}

// List returns the contents of dir. If dir is empty it defaults to the user's
// home directory. Only directories and regular files are reported; hidden
// entries are included (developers keep projects in dotfolders too).
func List(dir string) (*Listing, error) {
	if dir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			dir = home
		} else {
			dir = "/"
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}

	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		full := filepath.Join(abs, e.Name())
		typ := e.Type()

		// Only a symlink is followed, and that restraint is a macOS fix rather than
		// a micro-optimisation.
		//
		// ReadDir already knows whether each entry is a directory, so stat'ing every
		// one of them asks the OS for something it has just been told. On macOS that
		// question is not free: Downloads, Documents and Desktop are TCC-protected,
		// and touching them makes the system put up "kunai would like to access
		// files in your Downloads folder" -- for a folder picker that was only
		// listing $HOME and had no intention of reading anything in there. The
		// prompt is indistinguishable from an app being nosy, which is the last
		// impression a tool that runs shell commands should give.
		//
		// A symlink still has to be resolved (Stat, not Lstat) so that /tmp on macOS
		// and a symlinked checkout stay navigable rather than being dropped as
		// "not a directory". Those are rare, and none of them is a protected folder.
		dir := e.IsDir()
		if typ&os.ModeSymlink != 0 {
			info, err := os.Stat(full)
			if err != nil {
				continue // broken symlink, permission denied, …
			}
			mode := info.Mode()
			if !mode.IsDir() && !mode.IsRegular() {
				continue
			}
			dir = mode.IsDir()
		} else if !dir && !typ.IsRegular() {
			continue // sockets, devices, …
		}
		out = append(out, Entry{
			Name: e.Name(),
			Dir:  dir,
			Path: full,
		})
	}
	// Order: directories first, then real (non-hidden) entries before dotfolders
	// so the picker opens on actual projects instead of a wall of ~/.config-style
	// hidden dirs. Hidden entries are still listed (some projects live in
	// dotfolders), just after the rest.
	hidden := func(name string) bool { return strings.HasPrefix(name, ".") }
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir // directories first
		}
		if hi, hj := hidden(out[i].Name), hidden(out[j].Name); hi != hj {
			return !hi // non-hidden before hidden
		}
		return out[i].Name < out[j].Name
	})

	parent := filepath.Dir(abs)
	if parent == abs {
		parent = "" // at filesystem root
	}
	return &Listing{Path: abs, Parent: parent, Entries: out}, nil
}
