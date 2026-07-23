package claude

import "testing"

func TestProviderModelArg(t *testing.T) {
	provEnv := []string{"ANTHROPIC_BASE_URL=http://127.0.0.1:9999", "ANTHROPIC_AUTH_TOKEN=x"}
	cases := []struct {
		model string
		env   []string
		want  string
	}{
		{"claude-opus-4-8", provEnv, "opus"}, // switched provider session -> slot
		{"claude-sonnet-4-5", provEnv, "opus"},
		{"gpt-5.5", provEnv, "gpt-5.5"}, // already a provider model -> untouched
		{"grok-4.5", provEnv, "grok-4.5"},
		{"opus", provEnv, "opus"},                   // a slot -> untouched
		{"claude-opus-4-8", nil, "claude-opus-4-8"}, // a real Claude session -> untouched
		{"claude-opus-4-8", []string{"FOO=bar"}, "claude-opus-4-8"},
	}
	for _, c := range cases {
		if got := providerModelArg(c.model, c.env); got != c.want {
			t.Errorf("providerModelArg(%q, %v) = %q, want %q", c.model, c.env, got, c.want)
		}
	}
}
