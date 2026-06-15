# ship-pr — special cases & recovery recipes

Reach for these when a PR is not a single self-contained slice off `main`, or
when a push/merge has gone wrong. The core linear flow is in `SKILL.md`; this
file holds the situational branches.

## Parallel PRs touching a shared barrel

Multiple in-flight PRs that ALL append to the same barrel file
(`web/src/components/index.ts`, `web/src/hooks/index.ts`, a Go re-export, a
`mod.rs`) will CONFLICT on merge once the first lands.

- Merge them **one at a time**. Before each subsequent merge, REBASE that
  branch onto `origin/main` (never `git merge origin/main`) and force-push-with-lease.
- **Never `git worktree remove` or delete a branch (local OR remote) until
  `gh pr merge` reports the PR MERGED.** A `--squash` that hits a barrel
  conflict fails mid-command; if you already tore down, you may delete an
  UNMERGED branch and auto-close its PR.
- Recovery if that happened: the commit survives in the object store —
  `git branch <b> <sha>`, rebase onto `origin/main`, resolve the barrel
  conflict (delegate the edit — it's code), re-push, open a fresh PR.
- `gh pr merge --delete-branch` skips the REMOTE delete when the LOCAL delete
  errors on a worktree-held branch — always `git ls-remote --heads origin
  '<pattern>'` after cleanup and `git push origin --delete` any survivor.

## Stacked PRs (child depends on parent's unmerged change)

When PR-B's files depend on PR-A's not-yet-merged change:

1. Build each branch in its own worktree. Fork B off A's branch
   (`git worktree add .claude/worktrees/<b> -b <b-branch> <a-branch>`), and
   populate B's worktree by copying the FINAL intended files so B's
   diff-vs-base is exactly its own slice. Only the first PR usually needs a
   trimmed-file edit via a coder.
2. Open B with `gh pr edit --base <a-branch>` (or `--base` on create) so its
   diff shows only B's slice.
3. After A merges OR is rewritten (force-pushed): restack B with the OLD parent
   tip SHA B was forked from, not the branch name —
   `git rebase --onto origin/main <old-A-tip-sha> <b-branch>`, then
   `git push --force-with-lease` and `gh pr edit --base main`. Passing the
   branch name breaks once A itself has been rebased.
4. **Branch names use dashes, not a name that is also a path prefix**: a branch
   `bird-adapter/x` is rejected by the remote when a branch `bird-adapter`
   exists (ref-directory conflict). Use `bird-adapter-x`.

## Prerequisite refactor blocking a feature

When a feature is blocked by a cleanup in shared/low-level code (dead-code
removal, a shared-infra rename, an enum collision), land the cleanup as its OWN
scoped `refactor`/`chore` PR first, merge it, then rebase the feature on top.
Do NOT bundle the cleanup into the feature PR — the user explicitly prefers this
decomposition ("that's out of scope") so each diff is independently justified.

## Iterating an open PR's design

Refining a design on an already-open PR → amend that branch + force-push-with-lease.
Never close-and-reopen (churns PR numbers and loses the review thread).

## Recovery recipes

All of these avoid `git reset --hard`, which must NEVER run while unrelated
dirty files sit in the tree (a partial `git stash push <paths>` covers only the
named paths; everything else is wiped). To drop a stale local commit, use
`git reset --soft`/`--mixed`, or `git stash push` WITHOUT paths then pop.

### Amend landed on the wrong branch (parallel `git checkout main` under you)

A parallel actor checked out `main` between your push and amend, so the amend
clobbered upstream main and dropped your files. Recover without `reset --hard`:

```
git switch <intended-branch>
git checkout <bad-commit> -- <path>     # pull your files off the stray commit
# re-amend the correct branch
git branch -f main origin/main          # restore main
git push --force-with-lease
gh pr view <pr> --json files            # verify the PR file list
```

### Wrong hunks got staged

`git restore --staged <file>`, then `git apply --cached` a hand-crafted patch of
just the intended hunks. Preserves any parallel dirty work in the same file.

### Branch deleted before merge / PR auto-closed

The commit is still in the object store: `git branch <b> <sha>` (find the SHA in
the reflog or `gh`/CI logs), rebase onto `origin/main`, re-push, open a fresh PR.

### A file was wrongly reverted

Reconstruct from the captured diff in the conversation context; regenerate
`go.sum` via `go` tooling (not by hand) and `Cargo.lock` via `cargo build`.
