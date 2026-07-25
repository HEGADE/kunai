package worktree

import (
	"os"
	"path/filepath"
	"strings"
)

// sharedScanDepth is how many directories deep the walk descends. Setup commands
// link things at the root or a couple of levels in (t3code's own links
// infra/relay/.env), and past that the odds of finding one stop justifying the
// walk.
const sharedScanDepth = 2

// heavyDirs are skipped by name because they hold thousands of entries and never
// hold a link back to the main checkout. Note this only skips real directories:
// if a setup command symlinks node_modules itself, that symlink is caught as a
// symlink before any of this applies, which is exactly the case the warning in
// the brief exists for.
var heavyDirs = map[string]bool{
	".git": true, "node_modules": true, ".venv": true, "venv": true,
	"target": true, "vendor": true, "dist": true, "build": true,
	".next": true, "__pycache__": true, ".pytest_cache": true,
}

// SharedPaths lists entries in the worktree that are symlinks back into the main
// checkout, discovered by looking rather than by being told.
//
// This matters because of what it is used for: the agent is warned not to delete
// or reinstall these, since doing so through a symlink would destroy the real
// file in the main checkout. A declared list would go stale the moment someone
// edited their setup command; reading what the setup actually left behind cannot.
func SharedPaths(info Info) []string {
	var out []string
	root := filepath.Clean(info.Path)
	repo := filepath.Clean(info.Repo)

	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || path == root {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		if d.IsDir() {
			// depth counts the directory's own segments, so at sharedScanDepth=2
			// "infra" and "infra/relay" are both entered and files inside them are
			// seen, while a third level is not.
			depth := strings.Count(rel, string(filepath.Separator)) + 1
			if heavyDirs[d.Name()] || depth > sharedScanDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink == 0 {
			return nil
		}
		target, readErr := os.Readlink(path)
		if readErr != nil {
			return nil
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		if within(filepath.Clean(target), repo) {
			out = append(out, rel)
		}
		return nil
	})
	return out
}

// within reports whether path is dir or sits underneath it. The separator check
// is what stops /repo-backup matching /repo.
func within(path, dir string) bool {
	if path == dir {
		return true
	}
	return strings.HasPrefix(path, dir+string(filepath.Separator))
}
