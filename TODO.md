# TODO

Things worth doing, with enough of the reasoning that picking one up later does
not mean rediscovering why it matters.

## Review a worktree branch before landing it

An agent works alone on its branch for an hour and twenty turns. Deciding whether
to merge it currently means reading file names: `worktree.changedFiles` returns
paths and an ahead/behind count, nothing more. The only way to actually see the
work is to scroll back through every turn's per-turn diff. So the feature ends one
step early, and kunai will merge a branch you have not read.

Show the whole branch against its base, file by file, expandable to the diff,
beside the merge button. Everything needed exists: `DiffView` and `CodeView` from
the tool cards, the folder tree from `TurnChanges`, and one `git diff base...branch`
on the server.

One thing to know going in: an earlier git-shelling review panel was removed on
purpose, and CLAUDE.md records why. It diffed the *working tree* against a base
commit, so it read as one session-wide blob and went "Clean" the moment the work
was committed. That objection does not apply here. A worktree branch against the
base recorded on it (`branch.<name>.gh-merge-base`) is a well-defined question with
a stable answer, and it stays correct after committing, which is exactly when you
want to read it.
