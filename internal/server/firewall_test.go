package server

import (
	"os"
	"path/filepath"
	"testing"
)

// The case that cost an afternoon: ufw enabled with a DROP default, so the
// listener binds, logs its address, reports itself enabled, answers perfectly
// from this machine, and drops every packet that actually arrives.
func TestUFWIsNoticedOnlyWhenItWouldActuallyBlock(t *testing.T) {
	for _, c := range []struct {
		name     string
		conf     string
		defaults string
		want     bool
	}{
		{
			name: "enabled and dropping: the real case",
			conf: "ENABLED=yes\nLOGLEVEL=low\n",
			defaults: `# /etc/default/ufw
DEFAULT_INPUT_POLICY="DROP"
DEFAULT_OUTPUT_POLICY="ACCEPT"`,
			want: true,
		},
		{
			// Installed but switched off is not the problem, and warning about it
			// would train people to ignore the warning.
			name: "installed but disabled",
			conf: "ENABLED=no\n", defaults: `DEFAULT_INPUT_POLICY="DROP"`,
			want: false,
		},
		{
			name: "enabled but accepting inbound",
			conf: "ENABLED=yes\n", defaults: `DEFAULT_INPUT_POLICY="ACCEPT"`,
			want: false,
		},
		{
			// A commented-out setting is not a setting. Reading one would produce a
			// warning nobody can act on, about a firewall that is not running.
			name: "the enabling line is commented out",
			conf: "#ENABLED=yes\n", defaults: `DEFAULT_INPUT_POLICY="DROP"`,
			want: false,
		},
		{name: "no ufw installed", conf: "", defaults: "", want: false},
	} {
		dir := t.TempDir()
		confPath := filepath.Join(dir, "ufw.conf")
		defPath := filepath.Join(dir, "ufw")
		if c.conf != "" {
			mustWrite(t, confPath, c.conf)
		}
		if c.defaults != "" {
			mustWrite(t, defPath, c.defaults)
		}
		if got := ufwDrops(confPath, defPath); got != c.want {
			t.Errorf("%s: ufwDrops = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestConfValueReadsShellStyleSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conf")
	mustWrite(t, path, "# a comment\nQUOTED=\"DROP\"\nSINGLE='yes'\nBARE=plain\n  SPACED = yes \n")
	for _, c := range []struct{ key, want string }{
		{"QUOTED", "DROP"},
		{"SINGLE", "yes"},
		{"BARE", "plain"},
		{"SPACED", "yes"},
		{"MISSING", ""},
	} {
		if got := confValue(path, c.key); got != c.want {
			t.Errorf("confValue(%q) = %q, want %q", c.key, got, c.want)
		}
	}
	if got := confValue(filepath.Join(t.TempDir(), "nope"), "ENABLED"); got != "" {
		t.Errorf("a missing file read as %q", got)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
