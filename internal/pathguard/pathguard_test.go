package pathguard

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// repo builds a folder with a file in it, plus a secret OUTSIDE it and a symlink
// from inside pointing at that secret. The symlink is the case string comparison
// gets wrong.
func repo(t *testing.T) (root, secret string) {
	t.Helper()
	base := t.TempDir()
	root = filepath.Join(base, "repo")
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	secret = filepath.Join(base, "id_rsa")
	if err := os.WriteFile(secret, []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(secret), filepath.Join(root, "up")); err != nil {
		t.Fatal(err)
	}
	return root, secret
}

func TestResolveAcceptsWhatIsInside(t *testing.T) {
	root, _ := repo(t)
	for _, rel := range []string{"src/main.go", "./src/main.go", "src", ".", "newfile.txt", "src/new/deep.txt"} {
		if _, err := Resolve(root, rel); err != nil {
			t.Errorf("Resolve(%q) refused a path inside the folder: %v", rel, err)
		}
	}
	// An absolute path inside the folder is fine too; the agent uses those.
	if _, err := Resolve(root, filepath.Join(root, "src", "main.go")); err != nil {
		t.Errorf("an absolute in-folder path was refused: %v", err)
	}
}

func TestResolveRefusesTraversal(t *testing.T) {
	root, secret := repo(t)
	for _, rel := range []string{
		"../id_rsa",
		"../../etc/passwd",
		"src/../../id_rsa",
		secret,
		"/etc/passwd",
		filepath.Join(filepath.Dir(root), "id_rsa"),
	} {
		if got, err := Resolve(root, rel); err == nil {
			t.Errorf("Resolve(%q) allowed an escape to %q", rel, got)
		}
	}
}

// The reason this is not a string comparison. A symlink inside the folder can
// point anywhere, and the resolution has to happen before the check, or reading
// "repo/escape" reads the private key while looking perfectly contained.
func TestResolveSeesThroughSymlinks(t *testing.T) {
	root, _ := repo(t)
	if got, err := Resolve(root, "escape"); err == nil {
		t.Errorf("a symlink out of the folder was followed to %q", got)
	}
	// And through a symlinked DIRECTORY, which is how a write escapes: the leaf
	// does not exist yet, so only the parent can give it away.
	if got, err := Resolve(root, "up/newfile"); err == nil {
		t.Errorf("a write through a symlinked parent was allowed to %q", got)
	}
	if got, err := Resolve(root, "up/id_rsa"); err == nil {
		t.Errorf("a read through a symlinked parent reached %q", got)
	}
}

// Containment is separator-aware, or a sibling folder whose name merely starts
// the same way would be inside.
func TestSiblingWithASharedPrefixIsOutside(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repo")
	sneaky := filepath.Join(base, "repo-secrets")
	for _, d := range []string{root, sneaky} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Resolve(root, sneaky); err == nil {
		t.Error("repo-secrets was treated as inside repo")
	}
}

// No root means refuse, never "anything goes".
func TestNoRootIsRefused(t *testing.T) {
	if _, err := Resolve("", "anything"); !errors.Is(err, ErrNoRoot) {
		t.Errorf("empty root gave %v, want ErrNoRoot", err)
	}
	if _, err := ResolveAny(nil, "anything"); !errors.Is(err, ErrNoRoot) {
		t.Errorf("no roots gave %v, want ErrNoRoot", err)
	}
	if Inside(nil, "/tmp") {
		t.Error("Inside said yes with no roots to be inside of")
	}
}

// A session can be given more than one codebase, and a path in any of them is in
// the session.
func TestResolveAnyAcceptsASecondRoot(t *testing.T) {
	a, _ := repo(t)
	b, _ := repo(t)
	if !Inside([]string{a, b}, filepath.Join(b, "src", "main.go")) {
		t.Error("a path in the second root was refused")
	}
	if Inside([]string{a, b}, "/etc/passwd") {
		t.Error("a path in neither root was accepted")
	}
}

func TestToolPathsReadsEveryArgumentThatNamesAFile(t *testing.T) {
	cases := []struct {
		tool  string
		input string
		want  []string
		ok    bool
	}{
		{"Read", `{"file_path":"/repo/a.go"}`, []string{"/repo/a.go"}, true},
		{"NotebookEdit", `{"notebook_path":"/repo/n.ipynb"}`, []string{"/repo/n.ipynb"}, true},

		// Glob's pattern IS a path expression, and the tool accepts an absolute one
		// or one that climbs out. Reading only `path` left it able to enumerate the
		// whole filesystem, since a pattern-only call named nothing to check.
		{"Glob", `{"pattern":"**/*.go","path":"/repo/src"}`, []string{"**/*.go", "/repo/src"}, true},
		{"Glob", `{"pattern":"/etc/**"}`, []string{"/etc/**"}, true},
		{"Glob", `{"pattern":"../../*.pem"}`, []string{"../../*.pem"}, true},

		// Grep's pattern is a REGULAR EXPRESSION and must never be resolved as a
		// path, or searching for "../" would be denied. Its path is optional and
		// defaults to the working directory, so a pattern-only call is complete.
		{"Grep", `{"pattern":"../../etc"}`, nil, true},
		{"Grep", `{"pattern":"TODO","path":"/repo"}`, []string{"/repo"}, true},

		// Tools that name no file at all: nothing found, and nothing missing.
		{"WebSearch", `{"query":"how to sort a slice"}`, nil, true},
		{"TodoWrite", `{"todos":[]}`, nil, true},

		// ok=false is "could not verify". A tool that always names a file, in a
		// shape we cannot read, must not look the same as one with nothing to check.
		{"Read", `{"file_path":123}`, nil, false},
		{"Read", `{"paff":"/repo/a.go"}`, nil, false},
		{"Read", `"hello"`, nil, false},
		{"Read", ``, nil, false},
		{"Write", `{}`, nil, false},
		{"SomeNewTool", `{"file_path":"/repo/a.go"}`, nil, false},
	}
	for _, c := range cases {
		got, ok := ToolPaths(c.tool, json.RawMessage(c.input))
		if ok != c.ok {
			t.Errorf("%s %s: ok=%v, want %v", c.tool, c.input, ok, c.ok)
			continue
		}
		if len(got) != len(c.want) {
			t.Errorf("%s %s: got %v, want %v", c.tool, c.input, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s %s: got %v, want %v", c.tool, c.input, got, c.want)
				break
			}
		}
	}
}

// MultiEdit carries a list, each entry with its own path. Missing this would let
// one call touch a dozen files with only the first checked.
func TestToolPathsReadsEveryEditInAMultiEdit(t *testing.T) {
	input := `{"file_path":"/repo/a.go","edits":[
		{"file_path":"/repo/b.go"},
		{"file_path":"/etc/passwd"}
	]}`
	got, ok := ToolPaths("MultiEdit", json.RawMessage(input))
	if !ok {
		t.Fatal("a well-formed MultiEdit was reported as an unreadable shape")
	}
	if len(got) != 3 {
		t.Fatalf("got %v, want all three paths", got)
	}
	found := false
	for _, p := range got {
		if p == "/etc/passwd" {
			found = true
		}
	}
	if !found {
		t.Error("a path buried in the edits list was not extracted, so it would go unchecked")
	}
}
