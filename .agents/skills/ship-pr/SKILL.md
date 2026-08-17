---
name: ship-pr
description: >-
  Publish runbook: land a verified change on main — branch from origin/main,
  stage only the approved files, open a scoped PR with gh, drive CI green,
  address every review/Codex finding, merge with the right strategy, clean up.
  Use whenever the user asks to ship, land, publish or merge ("create a PR",
  "оформи PR", "залей", "влей в main", "смержи").
---

# ship-pr

Land a verified, reviewer-approved change on `main`. Invoking this skill is the publish authorization: "create a PR" authorizes commit + push + PR + CI + addressing findings; "create and merge" / "влей в main" also authorizes merge + cleanup. You drive git and `gh` yourself; coders never commit.

## Non-negotiables

- Ship only a change that passed its gates and has a reviewer `APPROVED` on the complete current candidate. Any file change after approval → rerun the affected gates and get a fresh review of what changed.
- Branch from **confirmed** `origin/main` (`git fetch origin main` first), never from the session's current HEAD. Publish from a dedicated worktree unless the user waived isolation for this task.
- Stage exactly the publish manifest: `git add <paths>` per file, never `git add -A`; never stage pre-existing dirty files or local-only ignored config; `git diff --cached --stat` before committing.
- Verify `git branch --show-current` before every commit, amend and push — a parallel actor can switch branches under you.
- No destructive git on a dirty tree (`reset --hard`, `checkout`, `restore`, `stash`); recovery recipes are in `references/branching-and-recovery.md`.
- Subjects: `<type>(<scope>): <brief>` (see `AGENTS.md` → Commits & PRs); the PR title is the same; body = capitalized, period-ended, high-level bullets, `Closes #<n>.` when applicable, no `## Summary`, no test plan, no AI footers.
- One logical change = one PR; out-of-scope prerequisites ship first in their own PR.
- Never merge with an unaddressed review finding, a red check (a proven flake is still red — fix or rerun it first), `reviewDecision: REVIEW_REQUIRED`, or a PR tied to a `needs-architect` issue (stop, leave it open, report).
- Never delete a branch until `gh pr view --json state` says `MERGED`.

## Phases

0. **Ready?** Gates green in the candidate tree; reviewer APPROVED exists for this exact content. If not, do that first.
1. **Branch.** `git fetch origin main`; worktree: `git worktree add .claude/worktrees/<name> -b <type>/<scope>-<what> origin/main`; primary (waiver only, clean, on main): `git switch -c <branch> origin/main`. Shell-safe names, no parentheses.
2. **Transfer & stage.** Bring the approved files over (`git diff <base>..<coder-branch> -- <paths> | git apply --index`, or `git add` in place); confirm `git diff --cached --stat` equals the manifest.
3. **Commit.** Conventional subject, high-level body, no footers; `make lint-commit` must pass (the `commit-msg` hook runs it).
4. **Push & PR.** `git push -u origin <branch>`; `gh pr create --title "<subject>" --body "<bullets>"` (add `--draft` if the user asked); pushes touching `.github/workflows/` need the git token's `workflow` scope — ask the user if refused.
5. **CI & review.** `gh pr checks <n> --watch`; on a failure attributable to the change → fix, gates, re-review, `git commit --amend` or a fixup, force-with-lease; on a flake → compare the same workflow on latest `origin/main` runs, then `gh run rerun <id> --failed`. Read every review thread (`gh api repos/{owner}/{repo}/pulls/<n>/comments`, `gh pr view --comments`) and PR-level review; Codex may post findings as an issue comment with no review. Fix or answer each; after any push to a reviewed PR post `@codex review`; resolve addressed threads (`gh api graphql` `resolveReviewThread`).
6. **Merge** (only if authorized). Check the `needs-architect` label on every issue the body names (`gh issue view <n> --json labels`, both repos). Single commit → `gh pr merge <n> --squash`; deliberately structured multi-commit → `--rebase`; review fixups → `--squash --subject "<type>(<scope>): <title> (#n)" --body ""` (never GitHub's default squash body). Blocked by a ruleset → `--admin` with the same strategy, and say why. Behind main → rebase (never merge main in), gates, re-review, force-with-lease, back to 5.
7. **Cleanup.** After `MERGED`: `git worktree remove <path>`, `git branch -D <branch>` (squash/rebase leaves it unmerged locally), `git push origin --delete <branch>` if GitHub did not; update the primary `main` only if it is clean and on `main`.

## Report

PR number(s) and state, findings addressed, flakes rerun, any `needs-architect` block, anything left for the user.
