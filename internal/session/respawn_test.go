package session

import "testing"

func fullSpec() spawnSpec {
	return spawnSpec{
		model:           "opus",
		effort:          "high",
		mode:            "acceptEdits",
		cliName:         "claude-work",
		cliBin:          "claude",
		cliEnv:          map[string]string{"CLAUDE_CONFIG_DIR": "/accounts/work"},
		appendPrompt:    "You are working in a git worktree",
		disallowedTools: []string{"Bash", "Task", "NotebookEdit"},
		contextTokens:   42_000,
		overhead:        36_000,
	}
}

// The field with the sharpest edge. A session shared with somebody else runs
// without Bash, and that is a spawn-time flag, so every respawn has to reproduce
// it. Losing it in an effort change would hand a guest a shell on the owner's
// machine, silently, at a moment nobody is thinking about the share.
func TestToolRestrictionsSurviveEveryRespawn(t *testing.T) {
	for _, c := range []struct {
		name string
		ov   restartOverride
	}{
		{"effort change", restartOverride{effort: "low"}},
		{"account switch", restartOverride{acct: &acctOverride{name: "B", bin: "claude"}}},
		{"nothing in particular", restartOverride{}},
	} {
		got := fullSpec().withOverrides(c.ov)
		if len(got.disallowedTools) != 3 || got.disallowedTools[0] != "Bash" {
			t.Errorf("%s dropped the tool restrictions: %v", c.name, got.disallowedTools)
		}
	}

	// And apply has to write it, or carrying it through the spec achieves nothing.
	var opts CreateOptions
	fullSpec().apply(&opts)
	if len(opts.DisallowedTools) != 3 {
		t.Errorf("apply did not write the tool restrictions: %v", opts.DisallowedTools)
	}
}

// Ending a share has to be able to give the tools BACK, so "no restrictions" is a
// real instruction and not the same as "leave them alone".
func TestToolRestrictionsCanBeCleared(t *testing.T) {
	none := []string{}
	got := fullSpec().withOverrides(restartOverride{tools: &none})
	if len(got.disallowedTools) != 0 {
		t.Errorf("clearing the restrictions left %v", got.disallowedTools)
	}
	// A nil override means keep, which is what every other restart relies on.
	kept := fullSpec().withOverrides(restartOverride{tools: nil})
	if len(kept.disallowedTools) != 3 {
		t.Errorf("a nil tools override changed them to %v", kept.disallowedTools)
	}
}

// An effort change is the restart that has historically dropped things: it used
// to send a work session back to the default account. Everything not asked for
// must survive it untouched.
func TestEffortChangeKeepsEverythingElse(t *testing.T) {
	got := fullSpec().withOverrides(restartOverride{effort: "low"})

	if got.effort != "low" {
		t.Errorf("effort = %q, want low", got.effort)
	}
	want := fullSpec()
	want.effort = "low"
	if specComparable(got) != specComparable(want) {
		t.Errorf("spec changed beyond the effort:\n got %+v\nwant %+v", got, want)
	}
	if got.appendPrompt != want.appendPrompt {
		t.Error("the worktree brief was dropped by an effort change")
	}
	if got.cliName != "claude-work" || got.cliEnv["CLAUDE_CONFIG_DIR"] != "/accounts/work" {
		t.Error("the account was dropped by an effort change")
	}
	if got.contextTokens != want.contextTokens || got.overhead != want.overhead {
		t.Error("the context meter was reset by an effort change")
	}
}

func TestAccountChangeKeepsTheWorktreeBrief(t *testing.T) {
	got := fullSpec().withOverrides(restartOverride{acct: &acctOverride{
		name: "Claude", bin: "claude", env: map[string]string{"CLAUDE_CONFIG_DIR": "/accounts/personal"},
	}})

	if got.cliName != "Claude" {
		t.Errorf("account did not switch: %q", got.cliName)
	}
	if got.appendPrompt != fullSpec().appendPrompt {
		t.Error("switching account dropped the worktree brief; the agent would think it is in the main checkout")
	}
	if got.effort != "high" {
		t.Errorf("effort = %q, want the original high", got.effort)
	}
}

// A model reset is only applied when the target account asks for one, because an
// account switch that leaves a provider model on a Claude account blanks the picker.
func TestAccountChangeResetsTheModelOnlyWhenAsked(t *testing.T) {
	kept := fullSpec().withOverrides(restartOverride{acct: &acctOverride{name: "B", bin: "claude"}})
	if kept.model != "opus" {
		t.Errorf("model = %q, want it carried over", kept.model)
	}

	reset := fullSpec().withOverrides(restartOverride{acct: &acctOverride{name: "B", bin: "claude", model: "sonnet"}})
	if reset.model != "sonnet" {
		t.Errorf("model = %q, want the requested reset", reset.model)
	}
}

// A provider session must come back in the provider permission mode on every
// respawn, whatever mode it happened to be in, or a Codex session that borrowed
// acceptEdits comes back in it.
func TestProviderAccountAlwaysGetsTheProviderMode(t *testing.T) {
	spec := fullSpec()
	spec.mode = "default"
	got := spec.withOverrides(restartOverride{acct: &acctOverride{
		name: "Codex", bin: "claude",
		env: map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:9"},
	}})
	if got.mode != ProviderPermissionMode {
		t.Errorf("mode = %q, want %q", got.mode, ProviderPermissionMode)
	}

	// And a Claude account is left alone.
	claude := fullSpec().withOverrides(restartOverride{})
	if claude.mode != "acceptEdits" {
		t.Errorf("a Claude session's mode was rewritten to %q", claude.mode)
	}
}

func TestApplyWritesEverySpawnTimeField(t *testing.T) {
	var opts CreateOptions
	fullSpec().apply(&opts)

	if opts.AppendSystemPrompt != fullSpec().appendPrompt {
		t.Error("apply dropped the appended system prompt")
	}
	if opts.Model != "opus" || opts.Effort != "high" || opts.Mode != "acceptEdits" {
		t.Errorf("apply lost model/effort/mode: %+v", opts)
	}
	if opts.CLIName != "claude-work" || opts.Bin != "claude" {
		t.Errorf("apply lost the account: %+v", opts)
	}
	if opts.ContextTokens != 42_000 || opts.Overhead != 36_000 {
		t.Errorf("apply lost the context meter: %+v", opts)
	}
	if opts.Env["CLAUDE_CONFIG_DIR"] != "/accounts/work" {
		t.Error("apply lost the account environment")
	}
}

func TestConfigDirComesFromTheAccountEnv(t *testing.T) {
	if got := fullSpec().configDir(); got != "/accounts/work" {
		t.Errorf("configDir = %q", got)
	}
	if got := (spawnSpec{}).configDir(); got != "" {
		t.Errorf("a spec with no account should have no config dir, got %q", got)
	}
}

// scalars projects a spec to its comparable fields. spawnSpec holds a map, so it
// cannot be compared directly; the environment is asserted separately by the
// tests that care about it.
type scalars struct {
	model, effort, mode           string
	cliName, cliBin, appendPrompt string
	contextTokens, overhead       int64
}

func specComparable(sp spawnSpec) scalars {
	return scalars{
		model: sp.model, effort: sp.effort, mode: sp.mode,
		cliName: sp.cliName, cliBin: sp.cliBin, appendPrompt: sp.appendPrompt,
		contextTokens: sp.contextTokens, overhead: sp.overhead,
	}
}

// Yolo is a spawn flag, so entering it is a respawn, so the respawn has to be
// able to carry a mode that beats the provider default.
//
// The provider rule above exists so a Codex or Grok session keeps its own
// default across every respawn nobody asked a question about. Applied to an
// explicit request it does the opposite of its job: the user picks Yolo on a
// provider session, the respawn quietly puts it back in auto, and the composer
// reports a mode the process is not in -- which is the exact failure the
// respawn was introduced to fix, reappearing one layer down.
func TestExplicitModeBeatsTheProviderDefault(t *testing.T) {
	provider := fullSpec()
	provider.cliEnv = map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:9"}

	got := provider.withOverrides(restartOverride{mode: BypassPermissionMode})
	if got.mode != BypassPermissionMode {
		t.Errorf("mode = %q on a provider session that asked for %q", got.mode, BypassPermissionMode)
	}

	// With nothing asked for, the provider default still wins, or a provider
	// session would drift to whatever it was last set to.
	untouched := provider.withOverrides(restartOverride{})
	if untouched.mode != ProviderPermissionMode {
		t.Errorf("mode = %q with no override, want the provider default %q", untouched.mode, ProviderPermissionMode)
	}
}
