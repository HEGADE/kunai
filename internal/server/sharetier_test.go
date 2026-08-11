package server

import (
	"testing"

	"github.com/hegade/kunai/internal/share"
)

// The reconciler gives back the toolset of a session whose share has ended. What
// it must never do is decide a session was shared BECAUSE its tools are withheld.
//
// That inference held only while sharing was the one thing that withheld tools.
// The pull request reviewer withholds Bash too (so an unattended review cannot
// park on a permission ask), so every review looked like an expired share: about
// a minute after one started, the reconciler respawned its session, which ended
// the turn that was running. From the outside the review simply stopped, having
// read the diff and some source, with no error anywhere. The log line "outlived
// its share" against a session that was never shared is what gave it away.
func TestOnlyASharesOwnRestrictionIsLifted(t *testing.T) {
	cases := []struct {
		name      string
		denied    []string
		owner     string
		shareLive bool
		want      bool
	}{
		{
			name:   "a share that has ended is restored",
			denied: []string{"Bash", "Task"}, owner: share.ToolsOwner, shareLive: false,
			want: true,
		},
		{
			name:   "a live share is left restricted",
			denied: []string{"Bash", "Task"}, owner: share.ToolsOwner, shareLive: true,
			want: false,
		},
		{
			// The regression. Another feature restricted the session for its whole
			// life and has no share at all, so every condition but the owner said
			// "restore me" and the reconciler handed the tools back.
			name:   "another feature's restriction is not ours to restore",
			denied: []string{"Bash", "Write", "Edit"}, owner: "some-other-feature", shareLive: false,
			want: false,
		},
		{
			// Belt and braces for the next feature that withholds tools and forgets
			// to claim them: an unclaimed restriction is not assumed to be a share's.
			name:   "an unclaimed restriction is left alone",
			denied: []string{"Bash"}, owner: "", shareLive: false,
			want: false,
		},
		{
			name:   "an unrestricted session needs no restoring",
			denied: nil, owner: "", shareLive: false,
			want: false,
		},
	}

	for _, c := range cases {
		if got := shareShouldRestore(c.denied, c.owner, c.shareLive); got != c.want {
			t.Errorf("%s: restore = %v, want %v", c.name, got, c.want)
		}
	}
}
