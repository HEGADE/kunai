package claude

import (
	"slices"
	"testing"
)

// The worktree brief reaches the model as a CLI flag, so a change to args() that
// drops it would silently leave every worktree agent believing it is in the main
// checkout. That failure is invisible until something is overwritten, which is
// why it is asserted here rather than trusted.
func TestArgsCarryTheAppendedSystemPrompt(t *testing.T) {
	brief := "You are working in a git worktree, not the main checkout."
	s := NewSession(Options{Cwd: "/tmp", PermissionMode: "default", AppendSystemPrompt: brief})

	args := s.args()
	i := slices.Index(args, "--append-system-prompt")
	if i < 0 {
		t.Fatalf("--append-system-prompt missing from %v", args)
	}
	if i+1 >= len(args) || args[i+1] != brief {
		t.Errorf("flag value = %q, want the brief", args[i+1:])
	}
}

func TestArgsOmitTheFlagWhenThereIsNoPrompt(t *testing.T) {
	s := NewSession(Options{Cwd: "/tmp", PermissionMode: "default"})
	if slices.Contains(s.args(), "--append-system-prompt") {
		t.Error("an empty prompt should not produce the flag at all")
	}
}
