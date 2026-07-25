package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// A fresh worktree has none of the things a repository needs but does not track:
// no node_modules, no .env, no virtualenv, no build cache. The answer here is a
// per-repo shell command rather than a list of paths kunai copies, because a
// command handles every package manager without this package knowing any of
// them, and because the right move differs by ecosystem: a pnpm repo relinks
// from its global store in seconds, where copying its node_modules would be
// slower and larger for no benefit.
//
// The pattern (and the good idea) is t3code's: run one command in the new
// worktree with the main checkout's path in the environment, so a setup command
// can both install and reach back for the files it needs.
//
// Sharing a dependency tree between worktrees is deliberately not offered.
// Agents install packages, and `npm ci` deletes node_modules before it
// reinstalls, so a second agent's dependencies would vanish mid-turn. Reaching
// back for small read-mostly files like .env is a different thing entirely, and
// a setup command can symlink those freely.

// Environment variables a setup command can rely on.
const (
	// EnvProjectRoot is the main checkout, so a command can symlink an untracked
	// file out of it: ln -sf $KUNAI_PROJECT_ROOT/.env .env
	EnvProjectRoot = "KUNAI_PROJECT_ROOT"
	// EnvWorktreePath is the new worktree, which is also the command's cwd.
	EnvWorktreePath = "KUNAI_WORKTREE_PATH"
)

// setupOutputLimit bounds what is kept from a command that prints a lot (an
// install prints thousands of lines). The tail is what matters, because that is
// where the failure is.
const setupOutputLimit = 16 << 10

// DefaultSetupTimeout is generous because a cold install genuinely takes minutes.
// It exists to stop a command that is waiting on a prompt nobody will answer.
const DefaultSetupTimeout = 15 * time.Minute

// waitDelay bounds how long Wait keeps reading output after the command has been
// cancelled. Short, because by this point the process group has been killed and
// anything still holding the pipe is something we are not going to get rid of.
const waitDelay = 2 * time.Second

// SetupState is where a worktree's preparation has got to.
type SetupState string

const (
	SetupNone     SetupState = "none"      // nothing to run
	SetupRunning  SetupState = "running"   // in progress; a session must wait
	SetupOK       SetupState = "ok"        // finished cleanly
	SetupFailed   SetupState = "failed"    // exited non-zero
	SetupTimedOut SetupState = "timed_out" // never exited
	SetupSkipped  SetupState = "skipped"   // the user declined to run one
)

// SetupResult is the outcome of running a setup command.
type SetupResult struct {
	State    SetupState `json:"state"`
	Command  string     `json:"command,omitempty"`
	Output   string     `json:"output,omitempty"` // tail, bounded
	ExitCode int        `json:"exit_code,omitempty"`
	Duration float64    `json:"duration_s,omitempty"`
}

// Failed reports whether the worktree is not fit to work in.
func (r SetupResult) Failed() bool {
	return r.State == SetupFailed || r.State == SetupTimedOut
}

// RunSetup executes command in the worktree with the two environment variables
// above set, and returns what happened. It never returns an error for a command
// that merely failed: a failing setup is a result to show the user, not a fault
// in kunai. An error is returned only when the command could not be started.
//
// The command is user-authored shell run with the server's privileges, which is
// why nothing calls this with a command the user has not seen. That check belongs
// at the edge, where there is a person to show it to.
func RunSetup(ctx context.Context, info Info, command string, timeout time.Duration) (SetupResult, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return SetupResult{State: SetupNone}, nil
	}
	if timeout <= 0 {
		timeout = DefaultSetupTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = info.Path
	cmd.Env = append(os.Environ(),
		EnvProjectRoot+"="+info.Repo,
		EnvWorktreePath+"="+info.Path,
	)
	out := &tailBuffer{limit: setupOutputLimit}
	cmd.Stdout = out
	cmd.Stderr = out
	isolate(cmd) // so cancelling kills the whole command, not just the shell
	// Backstop for the platforms isolate cannot cover, and for a killed process
	// whose output pipe is somehow still held: Wait gives up after this.
	cmd.WaitDelay = waitDelay

	started := time.Now()
	err := cmd.Run()
	res := SetupResult{
		Command:  command,
		Output:   out.String(),
		Duration: time.Since(started).Seconds(),
	}

	switch {
	case err == nil:
		res.State = SetupOK
	case ctx.Err() == context.DeadlineExceeded:
		res.State = SetupTimedOut
		res.Output = strings.TrimSpace(res.Output + "\n\nkunai: setup did not finish within " + timeout.String())
	default:
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			// The command could not be started at all (no shell, bad cwd). That is
			// a fault here, not a failing setup.
			return res, fmt.Errorf("worktree: run setup: %w", err)
		}
		res.State = SetupFailed
		res.ExitCode = exit.ExitCode()
	}
	return res, nil
}

// tailBuffer keeps only the last limit bytes written to it. An install's useful
// output is at the end, and keeping all of it would mean holding megabytes per
// worktree for no gain.
type tailBuffer struct {
	limit int
	buf   []byte
	// dropped records that the head was discarded, so the tail is never
	// presented as if it were the whole story.
	dropped bool
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	n := len(p)
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.limit {
		t.buf = t.buf[len(t.buf)-t.limit:]
		t.dropped = true
	}
	return n, nil
}

func (t *tailBuffer) String() string {
	s := strings.TrimSpace(string(t.buf))
	if t.dropped {
		return "[earlier output trimmed]\n" + s
	}
	return s
}
