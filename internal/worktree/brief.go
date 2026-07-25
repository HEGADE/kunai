package worktree

import (
	"fmt"
	"strings"
)

// briefOutputTail bounds how much failing setup output is repeated to the model.
// Enough to act on, not enough to spend real context on.
const briefOutputTail = 600

// Brief renders the facts about a worktree for the agent working in it.
//
// It is delivered as an appended system prompt rather than as a first turn in the
// conversation, and that choice is load-bearing: a turn can be compacted away,
// and it would be, precisely in the long unattended run where forgetting which
// checkout you are in does the most damage. A system prompt stays resident for
// the life of the process.
//
// The voice follows internal/project's Brief: it states what is true and where,
// and only gives an instruction where getting it wrong would damage something
// outside this worktree.
func Brief(info Info, setup SetupResult, shared []string) string {
	var b strings.Builder

	b.WriteString("You are working in a git worktree, not the repository's main checkout.\n\n")
	fmt.Fprintf(&b, "worktree: %s\n", info.Path)
	fmt.Fprintf(&b, "branch: %s\n", info.Branch)
	if info.Base != "" {
		fmt.Fprintf(&b, "based on: %s", info.Base)
		if len(info.BaseSHA) >= 7 {
			fmt.Fprintf(&b, " at %s", info.BaseSHA[:7])
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "main checkout: %s\n", info.Repo)

	b.WriteString("\nThis worktree and the main checkout share one git object store but have " +
		"separate working trees and separate HEADs. Other agents may be working in the " +
		"main checkout or in sibling worktrees at the same time.\n")

	b.WriteString("\nWork here. Do not edit files under the main checkout, and do not run " +
		"`git checkout` or `git switch` there: it has its own branch checked out and " +
		"changing it would derail whoever is using it.\n")

	writeSetup(&b, setup)
	writeShared(&b, shared)

	b.WriteString("\nWhen the work is done, commit it on this branch. Landing it (merging, " +
		"opening a pull request, or discarding it) is offered in the app, so there is no " +
		"need to merge or push unless you are asked to.\n")

	return b.String()
}

func writeSetup(b *strings.Builder, setup SetupResult) {
	switch setup.State {
	case SetupOK:
		fmt.Fprintf(b, "\nThis worktree was prepared with `%s`, which succeeded.\n", setup.Command)
	case SetupFailed, SetupTimedOut:
		fmt.Fprintf(b, "\nPreparing this worktree with `%s` did not succeed", setup.Command)
		if setup.ExitCode != 0 {
			fmt.Fprintf(b, " (exit %d)", setup.ExitCode)
		}
		b.WriteString(". Dependencies may be missing or incomplete, so expect a build or " +
			"test run to fail for that reason before assuming the code is at fault. The " +
			"end of its output was:\n\n")
		b.WriteString(indent(tail(setup.Output, briefOutputTail), "  "))
		b.WriteString("\n")
	case SetupNone, SetupSkipped:
		b.WriteString("\nNo setup command ran for this worktree, so anything the repository " +
			"does not track (installed dependencies, local environment files, build caches) " +
			"is absent. Set it up yourself if you need it.\n")
	}
}

func writeShared(b *strings.Builder, shared []string) {
	if len(shared) == 0 {
		return
	}
	b.WriteString("\nThese paths are symlinks into the main checkout, so they are SHARED " +
		"rather than copies:\n")
	for _, p := range shared {
		fmt.Fprintf(b, "  %s\n", p)
	}
	b.WriteString("Read them freely. Do not delete, overwrite, reinstall or clean them: " +
		"writing through one of these changes the main checkout's real file, which other " +
		"work depends on.\n")
}

// tail keeps the last n bytes, cut at a line boundary so the excerpt starts on a
// whole line rather than mid-word.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[len(s)-n:]
	if i := strings.IndexByte(cut, '\n'); i >= 0 && i < len(cut)-1 {
		cut = cut[i+1:]
	}
	return cut
}

func indent(s, prefix string) string {
	if s == "" {
		return s
	}
	return prefix + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n"+prefix)
}
