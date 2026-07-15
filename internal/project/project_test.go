package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// write creates a file (and its parents) with some content.
func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanDescribesAProject(t *testing.T) {
	d := t.TempDir()
	write(t, d, "go.mod", "module example.com/x\n")
	write(t, d, "README.md", "hi")
	write(t, d, "CLAUDE.md", "rules")
	write(t, d, "cmd/app/main.go", "package main")
	write(t, d, "internal/a/a.go", "package a")
	write(t, d, "internal/b/b.go", "package b")
	write(t, d, "web/src/App.svelte", "<div/>")
	write(t, d, "web/src/lib/x.ts", "export {}")
	// Noise that must not be counted or listed.
	write(t, d, "node_modules/dep/index.js", "junk")
	write(t, d, ".git/HEAD", "ref: refs/heads/main\n")
	write(t, d, ".git/config", "[remote \"origin\"]\n\turl = git@github.com:me/x.git\n")

	got, err := Scan(d)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != filepath.Base(d) || got.Path != d {
		t.Fatalf("name/path wrong: %+v", got)
	}
	if got.Branch != "main" {
		t.Fatalf("branch want main, got %q", got.Branch)
	}
	if got.Remote != "git@github.com:me/x.git" {
		t.Fatalf("remote want the origin url, got %q", got.Remote)
	}
	if len(got.Langs) == 0 || got.Langs[0].Name != "Go" || got.Langs[0].Files != 3 {
		t.Fatalf("want Go 3 first, got %+v", got.Langs)
	}
	// node_modules and .git must contribute nothing.
	for _, l := range got.Langs {
		if l.Name == "JavaScript" {
			t.Fatalf("node_modules was counted: %+v", got.Langs)
		}
	}
	for _, dir := range got.Dirs {
		if dir == "node_modules" || dir == ".git" {
			t.Fatalf("skipped dir listed: %v", got.Dirs)
		}
	}
	if strings.Join(got.Dirs, ",") != "cmd,internal,web" {
		t.Fatalf("top level want cmd,internal,web; got %v", got.Dirs)
	}
	if strings.Join(got.Docs, ",") != "CLAUDE.md,README.md" {
		t.Fatalf("docs want CLAUDE.md,README.md; got %v", got.Docs)
	}
	if strings.Join(got.Build, ",") != "go.mod" {
		t.Fatalf("build want go.mod, got %v", got.Build)
	}
}

func TestScanRejectsNonDirectories(t *testing.T) {
	d := t.TempDir()
	write(t, d, "f.txt", "x")
	if _, err := Scan(filepath.Join(d, "f.txt")); err == nil {
		t.Fatal("scanning a file should fail")
	}
	if _, err := Scan(filepath.Join(d, "nope")); err == nil {
		t.Fatal("scanning a missing path should fail")
	}
}

// The brief is what the model actually reads, so pin its promises: the path has
// to be there (it is how Claude reaches the files) and it must not claim the
// project was read.
func TestBriefStatesPathAndStaysMetadataOnly(t *testing.T) {
	d := t.TempDir()
	write(t, d, "go.mod", "module x")
	write(t, d, "main.go", "package main")
	got, err := Scan(d)
	if err != nil {
		t.Fatal(err)
	}
	b := got.Brief()
	if !strings.Contains(b, d) {
		t.Fatalf("brief must give the path:\n%s", b)
	}
	if !strings.Contains(b, "metadata only") {
		t.Fatalf("brief must say it is metadata only:\n%s", b)
	}
	if !strings.Contains(b, "Go 1") {
		t.Fatalf("brief should carry the language mix:\n%s", b)
	}
}
