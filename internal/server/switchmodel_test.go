package server

import "testing"

func TestSwitchModelFor(t *testing.T) {
	s := &Server{} // cfg.DefaultModel empty -> s.model() == "opus"
	claude := CLIProfile{Name: "Claude", Bin: "claude"}
	provider := CLIProfile{Name: "Grok", Bin: "claude", Env: map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:9"}}

	// provider model -> Claude account: must reset to the default Claude tier.
	if got := s.switchModelFor(claude, "grok-4.5"); got != "opus" {
		t.Errorf("provider->Claude should reset to opus, got %q", got)
	}
	// Claude tier -> Claude account: keep it (no reset).
	if got := s.switchModelFor(claude, "claude-opus-4-8"); got != "" {
		t.Errorf("Claude tier should carry over (no reset), got %q", got)
	}
	if got := s.switchModelFor(claude, "sonnet"); got != "" {
		t.Errorf("sonnet should carry over, got %q", got)
	}
	// switching to a provider: no reset (env-driven slot).
	if got := s.switchModelFor(provider, "opus"); got != "" {
		t.Errorf("provider target should not reset, got %q", got)
	}
}

func TestIsClaudeTierModel(t *testing.T) {
	for _, m := range []string{"opus", "sonnet", "haiku", "fable", "claude-opus-4-8"} {
		if !isClaudeTierModel(m) {
			t.Errorf("%q should be a Claude tier", m)
		}
	}
	for _, m := range []string{"grok-4.5", "gpt-5.5", "", "codex"} {
		if isClaudeTierModel(m) {
			t.Errorf("%q should NOT be a Claude tier", m)
		}
	}
}
