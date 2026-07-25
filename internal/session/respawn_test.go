package session

import "testing"

func fullSpec() spawnSpec {
	return spawnSpec{
		model:         "opus",
		effort:        "high",
		mode:          "acceptEdits",
		cliName:       "claude-work",
		cliBin:        "claude",
		cliEnv:        map[string]string{"CLAUDE_CONFIG_DIR": "/accounts/work"},
		appendPrompt:  "You are working in a git worktree",
		contextTokens: 42_000,
		overhead:      36_000,
	}
}

// An effort change is the restart that has historically dropped things: it used
// to send a work session back to the default account. Everything not asked for
// must survive it untouched.
func TestEffortChangeKeepsEverythingElse(t *testing.T) {
	got := fullSpec().withOverrides("low", nil)

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
	got := fullSpec().withOverrides("", &acctOverride{
		name: "Claude", bin: "claude", env: map[string]string{"CLAUDE_CONFIG_DIR": "/accounts/personal"},
	})

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
	kept := fullSpec().withOverrides("", &acctOverride{name: "B", bin: "claude"})
	if kept.model != "opus" {
		t.Errorf("model = %q, want it carried over", kept.model)
	}

	reset := fullSpec().withOverrides("", &acctOverride{name: "B", bin: "claude", model: "sonnet"})
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
	got := spec.withOverrides("", &acctOverride{
		name: "Codex", bin: "claude",
		env: map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:9"},
	})
	if got.mode != ProviderPermissionMode {
		t.Errorf("mode = %q, want %q", got.mode, ProviderPermissionMode)
	}

	// And a Claude account is left alone.
	claude := fullSpec().withOverrides("", nil)
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
