// Package project describes a directory well enough for a model to know what it
// is looking at, without reading the code. It reads only what is cheap and
// stable — the layout, the language mix, the git head, the files that name the
// project — so a session can be handed a second (or third) codebase for context
// without anything having to crawl or summarise it first.
package project

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Lang is a file count for one language.
type Lang struct {
	Name  string `json:"name"`
	Files int    `json:"files"`
}

// Info is a light description of a project directory.
type Info struct {
	Name   string   `json:"name"`
	Path   string   `json:"path"`
	Branch string   `json:"branch,omitempty"`
	Remote string   `json:"remote,omitempty"`
	Langs  []Lang   `json:"langs,omitempty"`
	Dirs   []string `json:"dirs,omitempty"` // top-level directories
	Docs   []string `json:"docs,omitempty"` // README, CLAUDE.md, …
	Build  []string `json:"build,omitempty"`
	Files  int      `json:"files,omitempty"`
}

// walkLimit bounds the scan. A project can be enormous and this runs while
// someone waits, so stop counting once the shape is obvious.
const walkLimit = 20000

// skipDirs never say anything about the project itself.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "dist": true,
	"build": true, "target": true, ".next": true, ".venv": true,
	"__pycache__": true, ".cache": true,
}

// langByExt maps a file extension to the language it implies. Anything absent is
// not counted: the point is to characterise a codebase, not inventory it.
var langByExt = map[string]string{
	".go": "Go", ".ts": "TypeScript", ".tsx": "TypeScript", ".js": "JavaScript",
	".jsx": "JavaScript", ".svelte": "Svelte", ".vue": "Vue", ".py": "Python",
	".rs": "Rust", ".java": "Java", ".kt": "Kotlin", ".rb": "Ruby", ".php": "PHP",
	".c": "C", ".h": "C", ".cpp": "C++", ".cc": "C++", ".hpp": "C++",
	".cs": "C#", ".swift": "Swift", ".m": "Objective-C", ".sh": "Shell",
	".sql": "SQL", ".css": "CSS", ".scss": "SCSS", ".html": "HTML",
}

// buildFiles name a project and its dependencies.
var buildFiles = map[string]bool{
	"go.mod": true, "package.json": true, "Cargo.toml": true, "pyproject.toml": true,
	"requirements.txt": true, "Gemfile": true, "pom.xml": true, "build.gradle": true,
	"Makefile": true, "Dockerfile": true, "composer.json": true,
}

// isDoc reports whether a top-level file is the kind that explains the project.
func isDoc(name string) bool {
	u := strings.ToUpper(name)
	return u == "CLAUDE.MD" || u == "AGENTS.MD" || strings.HasPrefix(u, "README")
}

// Scan reads a directory into an Info. It fails only if the path is not a
// readable directory; everything else is best-effort, since a missing git dir or
// an unreadable subtree still leaves a useful description.
func Scan(path string) (Info, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Info{}, err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return Info{}, err
	}
	if !fi.IsDir() {
		return Info{}, &os.PathError{Op: "scan", Path: abs, Err: os.ErrInvalid}
	}

	info := Info{Name: filepath.Base(abs), Path: abs}
	info.Branch, info.Remote = gitHead(abs)
	readTop(&info, abs)
	countTree(&info, abs)
	return info, nil
}

// readTop records the top-level directories, docs and build files.
func readTop(info *Info, abs string) {
	ents, err := os.ReadDir(abs)
	if err != nil {
		return
	}
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() {
			if !skipDirs[name] && !strings.HasPrefix(name, ".") {
				info.Dirs = append(info.Dirs, name)
			}
			continue
		}
		if isDoc(name) {
			info.Docs = append(info.Docs, name)
		} else if buildFiles[name] {
			info.Build = append(info.Build, name)
		}
	}
	sort.Strings(info.Dirs)
	sort.Strings(info.Docs)
	sort.Strings(info.Build)
}

// countTree tallies files per language, skipping the directories that describe
// tooling rather than the project.
func countTree(info *Info, abs string) {
	counts := map[string]int{}
	n := 0
	_ = filepath.WalkDir(abs, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtree: keep going
		}
		if d.IsDir() {
			if p != abs && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		n++
		if n > walkLimit {
			return filepath.SkipAll
		}
		if lang, ok := langByExt[strings.ToLower(filepath.Ext(p))]; ok {
			counts[lang]++
		}
		return nil
	})
	info.Files = n
	for name, c := range counts {
		info.Langs = append(info.Langs, Lang{Name: name, Files: c})
	}
	// Most-used first; ties by name so the output is stable.
	sort.Slice(info.Langs, func(i, j int) bool {
		if info.Langs[i].Files != info.Langs[j].Files {
			return info.Langs[i].Files > info.Langs[j].Files
		}
		return info.Langs[i].Name < info.Langs[j].Name
	})
	if len(info.Langs) > 6 {
		info.Langs = info.Langs[:6]
	}
}

// gitHead reads the checked-out branch and origin URL straight from .git, which
// is cheaper and safer than shelling out to git for two strings.
func gitHead(abs string) (branch, remote string) {
	if b, err := os.ReadFile(filepath.Join(abs, ".git", "HEAD")); err == nil {
		s := strings.TrimSpace(string(b))
		if ref, ok := strings.CutPrefix(s, "ref: refs/heads/"); ok {
			branch = ref
		}
	}
	f, err := os.Open(filepath.Join(abs, ".git", "config"))
	if err != nil {
		return branch, ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	inOrigin := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			inOrigin = line == `[remote "origin"]`
			continue
		}
		if inOrigin {
			if u, ok := strings.CutPrefix(line, "url = "); ok {
				return branch, strings.TrimSpace(u)
			}
		}
	}
	return branch, ""
}
