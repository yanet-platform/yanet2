# ship-pr — special cases & recovery recipes

Reach for these when a PR is not a single self-contained slice off `main`, or
when a push/merge has gone wrong. The core linear flow is in `SKILL.md`; this
file holds the situational branches.

## Parallel PRs touching a shared barrel

Multiple in-flight PRs that ALL append to the same barrel file
(`web/src/components/index.ts`, `web/src/hooks/index.ts`, a Go re-export, a
`mod.rs`) will CONFLICT on merge once the first lands.

- Merge them **one at a time**. Before each subsequent merge, REBASE that
  branch onto `origin/main` (never `git merge origin/main`), rerun the affected
  gates, obtain a fresh full-candidate `review-change` approval, then
  force-push-with-lease and return to Phase 5.
- **Never `git worktree remove` or delete a branch (local OR remote) until
  GitHub MCP `merge_pull_request` reports the PR MERGED.** A squash that hits a barrel
  conflict fails mid-command; if you already tore down, you may delete an
  UNMERGED branch and auto-close its PR.
- Recovery if that happened: the commit survives in the object store —
  create a safety ref with `git branch recovery/<b> <sha>`, then a dedicated
  worktree, rebase onto `origin/main`, resolve the barrel conflict (delegate the
  edit — it's code), rerun the affected gates, obtain a fresh full-candidate
  `review-change` approval, re-push, and create a fresh PR through MCP.
- `merge_pull_request` has no branch-delete parameter, so an MCP merge always
  leaves both branches for Phase 7. On the `gh` path, `gh pr merge
  --delete-branch` skips the REMOTE delete when the LOCAL delete errors on a
  worktree-held branch. After `merge_pull_request` succeeds, clean up only
  through Phase 7's exact-ref procedure: compare the recorded PR head, delete
  with its expected-old-SHA lease, and verify the exact ref is absent. Never
  pattern-delete survivors.

## Stacked PRs (child depends on parent's unmerged change)

When PR-B's files depend on PR-A's not-yet-merged change:

1. Build each branch in its own worktree. Fork B off A's branch
   (`git worktree add <worktrees>/<b> -b <b-branch> <a-branch>`), and
   populate B's worktree by copying the FINAL intended files so B's
   diff-vs-base is exactly its own slice. Only the first PR usually needs a
   trimmed-file edit via a coder.
2. Open B with MCP `update_pull_request` using base `<a-branch>` (or set that
   base when creating it) so its diff shows only B's slice.
3. After A MERGES, restack B with the OLD parent tip SHA B was forked from,
   not the branch name: `git rebase --onto origin/main <old-A-tip-sha>
   <b-branch>`, then rerun the affected gates, obtain a fresh full-candidate
   `review-change` approval, `git push --force-with-lease`, and use MCP
   `update_pull_request` with base `main`; return to Phase 5.
4. After A is force-pushed but remains unmerged, restack B onto the rewritten
   parent: `git rebase --onto <new-A-tip-sha> <old-A-tip-sha> <b-branch>`, then
   rerun the affected gates, obtain a fresh full-candidate `review-change`
   approval, and `git push --force-with-lease`. Keep B's PR base on A's branch,
   do not update it to `main`, and return to Phase 5.
5. **Branch names use dashes, not a name that is also a path prefix**: a branch
   `bird-adapter/x` is rejected by the remote when a branch `bird-adapter`
   exists (ref-directory conflict). Use `bird-adapter-x`.

## Prerequisite refactor blocking a feature

When a feature is blocked by a cleanup in shared/low-level code (dead-code
removal, a shared-infra rename, an enum collision), land the cleanup as its OWN
scoped `refactor`/`chore` PR first, merge it, then rebase the feature on top.
Do NOT bundle the cleanup into the feature PR — the user explicitly prefers this
decomposition ("that's out of scope") so each diff is independently justified.
After rebasing the feature, rerun its affected gates and obtain a fresh
full-candidate `review-change` approval before pushing.

## Iterating an open PR's design

Refining a design on an already-open PR → rerun the affected gates, obtain a
fresh full-candidate `review-change` approval, amend that branch, prove the
committed fingerprint equals the approved one, and force-push-with-lease. Never
close-and-reopen (churns PR numbers and loses the review thread); return to
Phase 5 for the new head.

## Recovery recipes

All of these preserve shared-tree state: never use `git reset`, `git stash`,
`git checkout`, or `git restore` to recover from a dirty shared tree. Create a
safety ref and use a dedicated worktree before applying any recovery patch.

### Amend landed on the wrong branch (a parallel actor switched `main`)

A parallel actor switched to `main` between your push and amend, so the amend
landed on the wrong local branch. Preserve the commit without changing the
shared checkout. Before EVERY amend, record `git rev-parse <intended-branch>`
as the intended PR branch's tip `A` and `git rev-parse main` as the current
`main` tip `M`; if either was not captured, recover both exact tips from the
reflog and stop unless they can be verified. Recover only the accidental fix
`F` in a dedicated worktree:

```
git branch recovery/<name> <bad-amended-commit-B>
git worktree add <worktrees>/recover-<name> -b <intended-branch>-recover <pre-amend-intended-tip-A>
patch_path=$(mktemp) || exit 1
trap 'rm -f -- "$patch_path"' EXIT
if ! git diff --binary <pre-amend-main-tip-M> <bad-amended-commit-B> -- <paths> >"$patch_path"; then
  exit 1
fi
if [ ! -s "$patch_path" ]; then
  exit 1
fi
git -C <worktrees>/recover-<name> apply --check "$patch_path" || exit 1
git -C <worktrees>/recover-<name> apply --index "$patch_path" || exit 1
rm -f -- "$patch_path" || exit 1
trap - EXIT
```

Inspect the staged recovery diff, rerun the affected gates, and obtain a fresh
full-candidate `review-change` approval. Only then amend:

```
git -C <worktrees>/recover-<name> commit --amend --no-edit
```

Prove the committed fingerprint equals the approved one. Stop instead of
pushing if it differs, otherwise continue:

```
git -C <worktrees>/recover-<name> push --force-with-lease origin HEAD:<intended-branch>
```

Use a new commit instead of `--amend` when that preserves the intended PR
history. The `M..B` patch is required to be non-empty, so inspect the staged
recovery diff before committing. Then use MCP `pull_request_read` with files
and state to verify the existing PR head. Do not repoint `main` in a shared
checkout.

### Wrong hunks got staged

Leave the shared index untouched. Create a recovery branch and dedicated
worktree from the last good commit, derive the exact intended hunks with
read-only `git diff --cached` or `git diff`, then apply them there with
`git apply --index`. Inspect the result, rerun the affected gates, obtain a
fresh full-candidate `review-change` approval, and only then commit. Prove the
committed fingerprint equals the approved one before pushing from that recovery
worktree; return to Phase 5 for the new head.

### Branch deleted before merge / PR auto-closed

The commit is still in the object store: find its SHA in the reflog,
MCP `pull_request_read`, or check-run details; create `git branch recovery/<b>
<sha>`, then a dedicated worktree, rebase onto `origin/main`, rerun the affected
gates, obtain a fresh full-candidate `review-change` approval, re-push, and open
a fresh PR through MCP.

### A file was wrongly reverted

In a dedicated recovery worktree, derive the exact pre-revert change with
read-only `git show` or `git diff`, then use `git apply` to restore it. Regenerate
`go.sum` via `go` tooling (not by hand) and `Cargo.lock` via `cargo build`.
Rerun the affected gates and obtain a fresh full-candidate `review-change`
approval before committing the recovery. Prove the committed fingerprint equals
the approved one before pushing.
