package ghapp

import (
	"errors"
	"testing"
)

// Every spelling below is in daily use, and none is more correct than the others.
// Getting one wrong means review is silently unavailable on somebody's checkout
// for a reason nothing on screen explains.
func TestParseRemoteAcceptsEverySpelling(t *testing.T) {
	want := Repo{Owner: "lyzr", Name: "kunai"}
	for _, remote := range []string{
		"git@github.com:lyzr/kunai.git",
		"git@github.com:lyzr/kunai",
		"https://github.com/lyzr/kunai.git",
		"https://github.com/lyzr/kunai",
		"ssh://git@github.com/lyzr/kunai.git",
		"https://someone@github.com/lyzr/kunai.git",
		"  git@github.com:lyzr/kunai.git  ",
		"git@GitHub.com:lyzr/kunai.git",
	} {
		got, err := ParseRemote(remote)
		if err != nil {
			t.Errorf("ParseRemote(%q) failed: %v", remote, err)
			continue
		}
		if got != want {
			t.Errorf("ParseRemote(%q) = %+v, want %+v", remote, got, want)
		}
	}
}

// A repository somewhere other than GitHub is an ordinary state, not a failure:
// the UI answers it by not offering review, so it has to be distinguishable.
func TestParseRemoteRejectsOtherHosts(t *testing.T) {
	for _, remote := range []string{
		"git@gitlab.com:lyzr/kunai.git",
		"https://bitbucket.org/lyzr/kunai.git",
		"ssh://git@github.enterprise.example.com/lyzr/kunai.git",
	} {
		if _, err := ParseRemote(remote); !errors.Is(err, ErrNotGitHub) {
			t.Errorf("ParseRemote(%q) = %v, want ErrNotGitHub", remote, err)
		}
	}
}

// A malformed remote is a different error again, so "this is somewhere else" and
// "this makes no sense" do not read the same to whoever has to fix it.
func TestParseRemoteRejectsMalformed(t *testing.T) {
	for _, remote := range []string{
		"",
		"   ",
		"github.com",
		"git@github.com:",
		"git@github.com:lyzr",
		"https://github.com/",
		"https://github.com/lyzr",
		"https://github.com/lyzr/kunai/extra",
	} {
		got, err := ParseRemote(remote)
		if err == nil {
			t.Errorf("ParseRemote(%q) = %+v, want an error", remote, got)
			continue
		}
		if errors.Is(err, ErrNotGitHub) {
			t.Errorf("ParseRemote(%q) reported the wrong host; it is malformed", remote)
		}
	}
}
