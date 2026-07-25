package worktree

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// git runs a git command in dir and returns its trimmed stdout.
//
// stdout and stderr are captured separately, unlike internal/checkpoint's helper:
// several commands here are parsed (`worktree list --porcelain`, `rev-list
// --count`), and folding a warning from stderr into the parsed text would corrupt
// the result. On failure the error carries stderr, which is the part that says
// why git refused.
func git(dir string, args ...string) (string, error) {
	out, _, err := gitCode(dir, args...)
	return out, err
}

// gitCode is git for commands whose non-zero exit is an answer rather than a
// fault: a ref that does not exist, a merge that hit a conflict. It returns the
// exit code alongside stdout, and only reports an error when git could not be run
// at all or wrote to stderr in a way that mattered.
func gitCode(dir string, args ...string) (string, int, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// A worktree is created and merged on the user's behalf, so git needs an
	// identity for the merge commit even in a repo that has none configured. This
	// mirrors internal/checkpoint: it only ever names commits kunai itself makes.
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=kunai", "GIT_AUTHOR_EMAIL=kunai@localhost",
		"GIT_COMMITTER_NAME=kunai", "GIT_COMMITTER_EMAIL=kunai@localhost",
	)

	err := cmd.Run()
	out := strings.TrimRight(stdout.String(), "\n")
	if err == nil {
		return out, 0, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return out, exit.ExitCode(), fmt.Errorf("git %s: %s", strings.Join(args, " "), gitReason(stderr.String(), out))
	}
	return out, -1, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
}

// gitReason picks the most useful line to show a user out of git's output. git
// puts its refusal on stderr, but a few commands (merge) explain themselves on
// stdout, so fall back to that rather than reporting an empty reason.
func gitReason(stderr, stdout string) string {
	if s := strings.TrimSpace(stderr); s != "" {
		return firstMeaningfulLine(s)
	}
	if s := strings.TrimSpace(stdout); s != "" {
		return firstMeaningfulLine(s)
	}
	return "failed with no output"
}

func firstMeaningfulLine(s string) string {
	lines := strings.Split(s, "\n")
	for _, l := range lines {
		if t := strings.TrimSpace(l); t != "" {
			if len(lines) > 1 {
				return t + " (+" + fmt.Sprint(len(lines)-1) + " more)"
			}
			return t
		}
	}
	return s
}

// lines splits git output into non-empty lines, which is the shape almost every
// porcelain parse below wants.
func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if t := strings.TrimRight(l, "\r"); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// IsRepo reports whether dir is inside a git work tree.
func IsRepo(dir string) bool {
	out, err := git(dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

// refExists reports whether a fully-qualified ref resolves to a commit.
func refExists(dir, ref string) bool {
	_, err := git(dir, "rev-parse", "--verify", "-q", ref+"^{commit}")
	return err == nil
}
