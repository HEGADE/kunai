package worktree

import (
	"strings"
	"testing"
)

func sampleInfo() Info {
	return Info{
		Path:    "/data/worktrees/kunai/fix-auth",
		Repo:    "/home/me/kunai",
		Branch:  "kunai/fix-auth",
		Base:    "origin/main",
		BaseSHA: "abc1234def5678",
	}
}

func TestBriefStatesWhereTheAgentIs(t *testing.T) {
	got := Brief(sampleInfo(), SetupResult{State: SetupOK, Command: "npm ci"}, nil)

	for _, want := range []string{
		"/data/worktrees/kunai/fix-auth", // the worktree
		"/home/me/kunai",                 // the main checkout
		"kunai/fix-auth",                 // the branch
		"origin/main",                    // the base
		"abc1234",                        // the base commit, abbreviated
	} {
		if !strings.Contains(got, want) {
			t.Errorf("brief does not mention %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "Do not edit files under the main checkout") {
		t.Error("brief does not warn against editing the main checkout")
	}
}

// The shared-path warning is the one line in the brief that prevents real
// damage: writing through a symlink into the main checkout destroys the original.
func TestBriefWarnsAboutSharedPaths(t *testing.T) {
	got := Brief(sampleInfo(), SetupResult{State: SetupOK, Command: "npm ci"}, []string{".env", "infra/relay/.env"})

	if !strings.Contains(got, "SHARED") {
		t.Error("shared paths are not marked as shared")
	}
	for _, p := range []string{".env", "infra/relay/.env"} {
		if !strings.Contains(got, p) {
			t.Errorf("shared path %q missing from the brief", p)
		}
	}
	if !strings.Contains(got, "Do not delete") {
		t.Error("the brief does not say not to delete a shared path")
	}
}

func TestBriefWithNoSharedPathsSaysNothingAboutThem(t *testing.T) {
	got := Brief(sampleInfo(), SetupResult{State: SetupOK, Command: "npm ci"}, nil)
	if strings.Contains(got, "SHARED") {
		t.Errorf("brief invented a shared-path section:\n%s", got)
	}
}

// A failed setup has to reach the agent, or it spends the session debugging
// missing dependencies as though they were bugs in the code.
func TestBriefCarriesASetupFailure(t *testing.T) {
	got := Brief(sampleInfo(), SetupResult{
		State:    SetupFailed,
		Command:  "npm ci",
		ExitCode: 1,
		Output:   "npm ERR! network timeout",
	}, nil)

	if !strings.Contains(got, "did not succeed") {
		t.Error("the failure is not stated")
	}
	if !strings.Contains(got, "exit 1") {
		t.Error("the exit code is missing")
	}
	if !strings.Contains(got, "npm ERR! network timeout") {
		t.Error("the output that explains the failure is missing")
	}
	if !strings.Contains(got, "before assuming the code is at fault") {
		t.Error("the brief does not tell the agent how to read a resulting build failure")
	}
}

func TestBriefSaysWhenNothingWasPrepared(t *testing.T) {
	got := Brief(sampleInfo(), SetupResult{State: SetupNone}, nil)
	if !strings.Contains(got, "No setup command ran") {
		t.Errorf("an unprepared worktree is not called out:\n%s", got)
	}
}

func TestTailKeepsWholeLines(t *testing.T) {
	in := "first line\nsecond line\nthird line\n"
	got := tail(in, 15)
	if strings.HasPrefix(got, "cond") {
		t.Errorf("tail cut mid-line: %q", got)
	}
	if !strings.Contains(got, "third line") {
		t.Errorf("tail lost the end: %q", got)
	}
	if tail("short", 100) != "short" {
		t.Error("tail trimmed something already short enough")
	}
}
