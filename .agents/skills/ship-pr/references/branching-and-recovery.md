# ship-pr — special cases & recovery

Situational branches of the linear flow in `SKILL.md`.

## Parallel PRs touching a shared barrel

Several in-flight PRs appending to the same barrel (`web/src/components/index.ts`, `hooks/index.ts`, a Go re-export, a `mod.rs`) conflict once the first lands. Merge them one at a time; before each next merge rebase that branch onto `origin/main` (never merge main in), rerun the affected gates, get a fresh review of the changed regions, force-with-lease, and re-watch checks.

## Stacked PRs

Child branch off the parent branch. After the parent is rebased, restack with `git rebase --onto origin/main <old-parent-tip> <child>` (record the old tip SHA before touching the parent), then gates, review, force-with-lease. Merge the parent first; GitHub retargets the child to `main`.

## Prerequisite refactor

Split out-of-scope cleanup into its own PR, land it, rebase the feature branch onto the new `origin/main`, gates, review, push.

## Recovery

- **Branch deleted before merge / PR auto-closed.** The commit survives: `git branch recovery/<b> <sha>` (find it via `git reflog` or `gh pr view --json headRefOid`), new worktree from it, rebase onto `origin/main`, gates, review, push, `gh pr create` again.
- **Amend landed on the wrong branch.** `git reflog` to find the pre-amend commit; `git branch recovery/<name> <sha>`; move the amend to the intended branch with `git cherry-pick`; never `reset --hard` on a tree that has other work.
- **Force-push rejected (`--force-with-lease`).** Someone pushed to the branch: `git fetch`, inspect `git log origin/<branch> --not HEAD`, rebase on top or ask.
- **Squash merge produced a "каша" body.** Do not amend main; note it in the report so the user can decide.
- **Hook rejects the subject.** Fix the message (`git commit --amend`) — never `--no-verify`.
