package session

// A session is respawned, not reconfigured, whenever something spawn-time
// changes: reasoning effort and the account are CLI flags and process
// environment, so the only way to change them is to close the process and start
// another one with --resume.
//
// That makes "what carries over" a real decision, and getting it wrong is a
// class of bug this project has already shipped twice: an effort change used to
// drop a work session back onto the default account, and a provider session used
// to lose its permission mode. Each field below is one that must survive, so
// they are gathered in one place with the rule stated, rather than being read off
// the old session at four scattered points in restart().

// spawnSpec is everything fixed at spawn that a respawn has to reproduce.
type spawnSpec struct {
	model  string
	effort string
	mode   string

	// The account: which binary runs, under which environment.
	cliName string
	cliBin  string
	cliEnv  map[string]string

	// appendPrompt is the worktree brief. It is spawn-time only, so without an
	// explicit carry an effort change would leave the agent believing it is in
	// the main checkout.
	appendPrompt string

	// disallowedTools is the toolset withheld from the session, and toolsOwner is
	// what withheld it. The most important field here to carry: the others going
	// missing is a nuisance, this one going missing hands a guest Bash on somebody
	// else's machine. Changing effort mid-share must not be a way to get it back.
	// The owner travels with it so a respawn cannot leave a restriction nobody
	// claims, which would make it look like a stale share to the share reconciler.
	disallowedTools []string
	toolsOwner      string

	// The context meter's state, so a respawn does not reset it to zero and
	// mislead until the next turn corrects it.
	contextTokens int64
	overhead      int64
}

// specOf reads the spawn-time state off a live session. The account fields are
// set once at create and never mutated, and the rest are read under the lock the
// session uses for them.
func specOf(s *Session) spawnSpec {
	meta := s.Meta()
	s.mu.Lock()
	defer s.mu.Unlock()
	return spawnSpec{
		model:           meta.Model,
		effort:          meta.Effort,
		mode:            s.mode,
		cliName:         s.cliName,
		cliBin:          s.cliBin,
		cliEnv:          s.cliEnv,
		appendPrompt:    s.appendPrompt,
		disallowedTools: s.disallowedTools,
		toolsOwner:      s.toolsOwner,
		contextTokens:   s.contextTokens,
		overhead:        s.overhead,
	}
}

// withOverrides applies a restart's requested changes and nothing else. Every
// zero field means "keep what the session already has".
func (sp spawnSpec) withOverrides(ov restartOverride) spawnSpec {
	if ov.effort != "" {
		sp.effort = ov.effort
	}
	if ov.tools != nil {
		// Non-nil and empty is a real instruction: it is how a share ending gives
		// the session its full toolset back. The owner is set from the same
		// instruction, so lifting a restriction also drops the claim on it.
		sp.disallowedTools = *ov.tools
		sp.toolsOwner = ov.toolsOwner
	}
	if ov.mode != "" {
		sp.mode = ov.mode
	}
	if acct := ov.acct; acct != nil {
		sp.cliName, sp.cliBin, sp.cliEnv = acct.name, acct.bin, acct.env
		if acct.model != "" {
			// Reset the model when the new account makes the old one meaningless,
			// e.g. provider -> Claude, where a carried-over "grok-4.5" is not a
			// Claude tier and leaves the picker blank.
			sp.model = acct.model
		}
	}
	// A proxy-backed (provider) account keeps the provider default across every
	// respawn. Keyed on the env the same way the server's isProxyProfile is, so
	// the two can never disagree.
	//
	// An explicitly requested mode wins, though, and that exception is the whole
	// reason a respawn can carry a mode at all: entering Yolo IS a respawn, since
	// the CLI refuses to switch into it on a running process. Without this, asking
	// a Codex or Grok session for Yolo would respawn it straight back into auto
	// and report success, which is the same silent lie this respawn exists to fix.
	if sp.cliEnv["ANTHROPIC_BASE_URL"] != "" && ov.mode == "" {
		sp.mode = ProviderPermissionMode
	}
	return sp
}

// configDir is where the resumed process reads its transcript from, which for a
// named account is that account's own config directory.
func (sp spawnSpec) configDir() string { return sp.cliEnv["CLAUDE_CONFIG_DIR"] }

// apply writes the spec into the options a respawn is built from.
func (sp spawnSpec) apply(opts *CreateOptions) {
	opts.Model = sp.model
	opts.Effort = sp.effort
	opts.Mode = sp.mode
	opts.CLIName = sp.cliName
	opts.Bin = sp.cliBin
	opts.Env = sp.cliEnv
	opts.AppendSystemPrompt = sp.appendPrompt
	opts.DisallowedTools = sp.disallowedTools
	opts.ToolsOwner = sp.toolsOwner
	opts.ContextTokens = sp.contextTokens
	opts.Overhead = sp.overhead
}
