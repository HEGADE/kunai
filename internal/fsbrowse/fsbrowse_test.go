package fsbrowse

import (
	"os"
	"path/filepath"
	"testing"
)

// The picker classifies what it lists without following anything it does not
// have to.
//
// The classification is the visible half; the restraint is the half that
// matters on macOS, where stat'ing an entry in $HOME named Downloads (or
// Documents, or Desktop) makes the system prompt for access to a folder the
// picker was never going to read. ReadDir already reports whether an entry is a
// directory, so only a symlink is worth resolving -- and this test is what keeps
// somebody reintroducing a stat-everything loop without noticing the prompt come
// back on a platform they are not developing on.
func TestListClassifiesWithoutFollowingEverything(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "project"))
	mustWrite(t, filepath.Join(root, "notes.md"))
	mustMkdir(t, filepath.Join(root, ".hidden"))

	// A symlink to a directory must stay navigable: /tmp on macOS is one, and so
	// is many a checkout. This is the one case that still costs a Stat.
	target := filepath.Join(root, "project")
	if err := os.Symlink(target, filepath.Join(root, "linkdir")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "gone"), filepath.Join(root, "broken")); err != nil {
		t.Fatal(err)
	}

	got, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	kind := map[string]bool{}
	for _, e := range got.Entries {
		kind[e.Name] = e.Dir
	}

	for name, wantDir := range map[string]bool{
		"project":  true,
		".hidden":  true,
		"linkdir":  true, // followed, so the picker can walk into it
		"notes.md": false,
	} {
		dir, listed := kind[name]
		if !listed {
			t.Errorf("%s was not listed", name)
			continue
		}
		if dir != wantDir {
			t.Errorf("%s: Dir = %v, want %v", name, dir, wantDir)
		}
	}
	// A symlink pointing at nothing is dropped rather than offered as a folder
	// that cannot be opened.
	if _, listed := kind["broken"]; listed {
		t.Error("a broken symlink was listed")
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.Mkdir(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, p string) {
	t.Helper()
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}
