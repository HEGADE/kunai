package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hegade/kunai/internal/share"
)

func guardOver(t *testing.T, mode string) (*shareGuard, string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "id_rsa"), []byte("KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	return newShareGuard(&share.Share{Roots: []string{root}, Mode: mode}), root
}

// The guard's whole job: a guest-prompted call that names a file outside the
// share's folders never runs, and never reaches the owner to be approved by
// reflex either.
func TestGuardDeniesPathsOutsideTheRoots(t *testing.T) {
	g, root := guardOver(t, "")
	outside := filepath.Join(filepath.Dir(root), "id_rsa")

	for _, c := range []struct{ tool, input string }{
		{"Read", `{"file_path":"` + outside + `"}`},
		{"Read", `{"file_path":"/etc/passwd"}`},
		{"Write", `{"file_path":"` + outside + `"}`},
		{"Edit", `{"file_path":"/root/.ssh/authorized_keys"}`},
		{"Grep", `{"pattern":"x","path":"/"}`},
		{"MultiEdit", `{"file_path":"` + root + `/ok.go","edits":[{"file_path":"/etc/hosts"}]}`},
	} {
		if reason := g.Check(c.tool, json.RawMessage(c.input)); reason == "" {
			t.Errorf("%s %s was allowed out of the share's folders", c.tool, c.input)
		}
	}
}

func TestGuardAllowsWorkInsideTheRoots(t *testing.T) {
	g, root := guardOver(t, "")
	for _, c := range []struct{ tool, input string }{
		{"Read", `{"file_path":"` + root + `/main.go"}`},
		{"Write", `{"file_path":"` + root + `/new/file.go"}`},
		{"Grep", `{"pattern":"TODO","path":"` + root + `"}`},
		{"WebSearch", `{"query":"how to sort a slice"}`},
		{"TodoWrite", `{"todos":[]}`},
	} {
		if reason := g.Check(c.tool, json.RawMessage(c.input)); reason != "" {
			t.Errorf("%s was refused inside the share's own folder: %s", c.tool, reason)
		}
	}
}

// An unrecognised tool is refused, not allowed. "We found no paths in this" and
// "this has no paths" look identical from here and only one of them is safe, so
// a tool added to the CLI later arrives denied and somebody has to look at it.
func TestGuardRefusesToolsItDoesNotUnderstand(t *testing.T) {
	g, _ := guardOver(t, "")
	for _, tool := range []string{"Bash", "Task", "SomeNewTool", ""} {
		if reason := g.Check(tool, json.RawMessage(`{}`)); reason == "" {
			t.Errorf("the guard allowed %q, a tool it cannot inspect", tool)
		}
	}
}

// With no roots there is nothing to be inside of, so everything is refused
// rather than everything being allowed.
func TestGuardWithNoRootsRefusesEverything(t *testing.T) {
	g := newShareGuard(&share.Share{})
	if reason := g.Check("Read", json.RawMessage(`{"file_path":"/tmp/x"}`)); reason == "" {
		t.Fatal("a guard with no roots allowed a read")
	}
}

// The standing mode only ever applies to calls the guard already cleared, and
// only when the owner asked for one.
func TestGuardAutoModeIsOptIn(t *testing.T) {
	for _, c := range []struct{ mode, want string }{
		{"", ""},
		{"acceptEdits", "acceptEdits"},
		// auto is NOT a standing yes. Everywhere else in kunai it means the session
		// still stops for anything risky, and accepting it here made it a synonym
		// for acceptEdits: the owner picked the cautious-sounding option on the
		// share dialog and got the permissive one.
		{"auto", ""},
		{"default", ""},  // "ask me" means ask me
		{"plan", ""},     // not a standing yes
		{"nonsense", ""}, // never guessed at
	} {
		g, _ := guardOver(t, c.mode)
		if got := g.AutoMode(); got != c.want {
			t.Errorf("mode %q gave AutoMode %q, want %q", c.mode, got, c.want)
		}
	}
}

// The denial is written for the model, which reads it and tries again, so it has
// to be actionable. It must NOT list the roots: the model repeats what it is
// told, so a denial naming them handed the guest the owner's absolute directory
// layout, one refusal at a time.
func TestDenialDoesNotHandTheGuestTheOwnersLayout(t *testing.T) {
	g, root := guardOver(t, "")
	reason := g.Check("Read", json.RawMessage(`{"file_path":"/etc/passwd"}`))
	if reason == "" {
		t.Fatal("the read was allowed")
	}
	if strings.Contains(reason, root) {
		t.Errorf("the denial names the owner's folder, which the model will repeat: %q", reason)
	}
	if !strings.Contains(reason, "/etc/passwd") {
		t.Errorf("the denial does not say what was refused, so the model cannot correct itself: %q", reason)
	}
}

// The roots are copied out of the share, so nothing that happens to the share
// afterwards can widen a guard already installed on a running session.
func TestGuardDoesNotFollowTheShareItCameFrom(t *testing.T) {
	base := t.TempDir()
	sh := &share.Share{Roots: []string{filepath.Join(base, "repo")}}
	g := newShareGuard(sh)
	sh.Roots[0] = "/"
	if reason := g.Check("Read", json.RawMessage(`{"file_path":"/etc/passwd"}`)); reason == "" {
		t.Fatal("widening the share's roots widened a guard already in force")
	}
}

// Glob's pattern is a path expression, and it was going unchecked.
//
// pathFields was one shared list across every tool, so `pattern` could not be
// included without also resolving Grep's regular expression as a path. It was
// left out, which meant a Glob naming only a pattern produced no paths, the
// containment loop had nothing to iterate, and the call was allowed. A guest
// could enumerate the entire filesystem by name.
func TestGuardDeniesAGlobThatClimbsOutOfTheRoots(t *testing.T) {
	g, root := guardOver(t, "")
	for _, input := range []string{
		`{"pattern":"/etc/**"}`,
		`{"pattern":"../../**/*.pem"}`,
		`{"pattern":"**/*.key","path":"/"}`,
	} {
		if reason := g.Check("Glob", json.RawMessage(input)); reason == "" {
			t.Errorf("Glob %s enumerated outside the share's folders", input)
		}
	}
	// And an ordinary relative pattern still works, or the tool would be useless.
	for _, input := range []string{
		`{"pattern":"**/*.go"}`,
		`{"pattern":"*.md","path":"` + root + `"}`,
	} {
		if reason := g.Check("Glob", json.RawMessage(input)); reason != "" {
			t.Errorf("Glob %s was refused inside the share's own folder: %s", input, reason)
		}
	}
}

// Grep's pattern is a regex, not a path, so searching FOR "../" must still work.
func TestGuardDoesNotTreatAGrepPatternAsAPath(t *testing.T) {
	g, _ := guardOver(t, "")
	if reason := g.Check("Grep", json.RawMessage(`{"pattern":"\\.\\./\\.\\./etc"}`)); reason != "" {
		t.Errorf("a search for a string that looks like a path was refused: %s", reason)
	}
}

// "We found no paths" must not read as "there are none".
//
// A tool that always names a file, called in a shape this build cannot read (a
// non-string value, a field the CLI renamed in a later version), used to pass the
// guard by having nothing to check. That is the direction the whole package warns
// about, so it is refused instead.
func TestGuardRefusesACallItCannotRead(t *testing.T) {
	g, _ := guardOver(t, "")
	for _, c := range []struct{ tool, input string }{
		{"Read", `{"file_path":123}`},
		{"Read", `{"renamed_in_a_later_cli":"/etc/passwd"}`},
		{"Write", `{}`},
		{"Edit", `not json at all`},
		{"MultiEdit", `{"edits":[]}`},
	} {
		if reason := g.Check(c.tool, json.RawMessage(c.input)); reason == "" {
			t.Errorf("%s %s was allowed although its paths could not be read", c.tool, c.input)
		}
	}
}
