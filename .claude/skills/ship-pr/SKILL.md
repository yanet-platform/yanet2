---
name: ship-pr
description: >-
  The canonical publish/merge runbook for landing a verified change on main:
  branch from confirmed origin/main, stage ONLY the intended files, open a
  scoped PR (no Claude footers), drive CI to green, address EVERY review/Codex
  finding, and merge with the correct squash/rebase strategy — then clean up the
  branch and worktree. Use this skill WHENEVER the user asks to ship, land,
  publish, or merge work: "create a PR", "open a PR", "create and merge", "ship
  this", "land this change", "оформи PR", "сделай PR", "залей", "влей в main",
  "добейся вливания в main", "смержи", or any time finished, reviewer-approved
  code needs to reach main. Covers parallel PRs, stacked PRs, prerequisite-
  refactor splits, workflow-file OAuth scope, CI-flake reruns, and the recovery
  recipes when a push/merge goes wrong.
---

# ship-pr

Take a verified, reviewer-approved change and land it on `main` cleanly: branch from confirmed `origin/main`, stage exactly the intended files, open a scoped PR, drive CI green, address every review finding, and merge with the right strategy — then tear down the branch and worktree.

You (the architect) drive this. You never write the code — that was already delegated to the coders and verified. This skill is purely the **publish workflow**: the git/`gh` choreography and the discipline that keeps a parallel, worktree-heavy repo from corrupting itself.

**Invoking this skill is the explicit publish authorization.** The standing rule is "never commit/push/merge unless the user asks in the current turn" — asking to ship/land/merge IS that ask. Scope follows the verb: **"create a PR"** authorizes commit + push + PR + CI + addressing findings (NOT merge); **"create and merge" / "влей в main"** also authorizes the merge + cleanup. Delegated agents still never commit — you do.

## Non-negotiables

- **Don't publish unverified work.** Before branching, the change must have passed its verification gates AND a `reviewer` APPROVED pass (Phase 0). If either is missing, do that first — never ship unreviewed code.
- **Branch from CONFIRMED `origin/main`**, never the session-start branch line and never a bare `git checkout -b` (HEAD may be on a parallel WIP branch and drag its commits in).
- **Stage ONLY this change's files.** Never `git add -A`. Never stage pre-existing/unrelated dirty files. `git diff --cached -- <file>` each one, and cross-check every new untracked (`??`) file the change created is included.
- **No destructive git with a dirty tree or without explicit permission** — no `reset --hard`/`checkout`/`restore`/`stash`/index ops that could wipe unrelated uncommitted work. Recovery recipes are in `references/branching-and-recovery.md`.
- **Verify `git branch --show-current` before EVERY commit/amend/push** — a parallel actor can switch branches under you and land your amend on `main`.
- **No `Co-Authored-By` / "Generated with Claude Code" footers** in commit messages or PR bodies. The harness suggests them; CLAUDE.md and the user forbid them. Check the body before `gh pr create`.
- **Conventional, scoped subjects.** Commit + PR title: `<feat|fix|refactor|chore|perf|docs>(<scope>): <short description>`. A scopeless title is a convention violation.
- **PR body**: capitalized, period-ended bullets; high-level (no symbol names, no code-level detail); `Closes #<n>.` when applicable. No `## Summary` header, no `Test plan` section.
- **One logical change = one PR.** Out-of-scope prerequisites get their own PR first (see special cases).
- **NEVER merge with an unaddressed review finding** — read BOTH PR-level reviews and inline Codex comments; fix or reply to every one.
- **Never delete a branch (local or remote) until `gh pr merge` reports MERGED** — a failed merge on a torn-down branch auto-closes the PR.

## Pipeline

### Phase 0 — Precondition: is it ready to ship?

1. **Gates green, run from repo ROOT** (each catches what the narrower command misses):
   - Go → `go build ./...` (per-module CGO test pkgs under `modules/*/tests/...` are missed from `controlplane/`).
   - C → `meson compile -C build` AND `meson test -C build` AND `make fuzz`.
   - Rust → `cargo test --workspace`.
   - For C changes reachable across the CGO boundary (`lib/controlplane`, `api/`, anything called from `controlplane/ffi` or `bindings/go`) → also `make test-asan` (meson-only ASan never crosses into the Go cgo tests).
2. **A `reviewer` APPROVED pass exists** for this change. If the work is uncommitted, the reviewer ran WITHOUT isolation at main-tree paths; never in the same batch as the coder. If not yet done, run it now.
3. If the change touched any `Cargo.toml` dependency, regenerate and stage the root `Cargo.lock` in THIS PR (`cargo build`, then `git diff -- Cargo.lock`) — CI passes without it, so the omission is silent.

### Phase 1 — Branch from confirmed origin/main

1. `git fetch origin main`.
2. Create the branch off confirmed `origin/main`. Use a **shell-safe** name — `<type>/<scope>-<what>` (e.g. `chore/claude-ship-pr`): no `(`/`)` (bash mis-parses them unquoted in the `git`/`gh` commands below), and avoid a name that is also a path prefix of another branch (see stacked-PR ref):
   - Worktree (preferred for isolation): `git worktree add .claude/worktrees/<name> -b <branch> origin/main` (gitignored location, never sibling dirs).
   - Main checkout: `git checkout main && git fetch origin main && git checkout -b <branch> origin/main`.
3. If the work already lives on a coder's worktree branch, just confirm that branch was forked from current `origin/main`; rebase it if stale (`git rebase origin/main`, never `git merge origin/main`).
4. To run `go build`/`go test` inside a fresh worktree you must seed gitignored generated files — see `worktree-pb-seeding` (architect memory): rsync the `*.pb.go` and symlink `build/`.

### Phase 2 — Stage exactly this change

`git add` the explicit file list (never `-A`). `git diff --cached -- <file>` EACH staged file — an index entry can pick up unrelated hunks from a concurrent dirty file. Cross-check every new untracked (`??`) file is staged: omitting one ships a dangling import, and web CI is vitest-only (no build/tsc gate) so it stays green on a broken module graph.

### Phase 3 — Commit

Verify the branch first. Commit with a conventional scoped subject, high-level body, no footers. (Authorized only because invoking this skill is the publish ask.)

### Phase 4 — Push & open the PR

1. `git push -u origin <branch>` (from inside the worktree if used).
2. `gh pr create` with explicit `--head <branch> --base main` (from a main checkout on `main`, omitting these errors head==base). Scoped title; body per the non-negotiables; **check the body for footers before creating**.
3. `gh pr view <pr> --json files` — confirm ONLY this change's files are present. Extras (inherited WIP) → `git rebase --onto origin/main <wip-tip> <branch>`, force-push-with-lease, re-verify.
4. **Workflow-file PRs**: pushing a commit touching `.github/workflows/` needs the git token to have `workflow` OAuth scope — you can't self-grant; the user must run `gh auth refresh -h github.com -s workflow`. A path-filtered workflow won't run when only its own YAML changes — its file must be in its own `paths:` filter to self-trigger.

### Phase 5 — CI & review

1. Wait on CI with `gh pr checks <pr> --watch` DIRECTLY — no upfront `sleep`, no sleep+re-poll loops. (Only justified extra: one short retry if `--watch` exits immediately with "no checks" right after a push.)
2. **Flaky/infra failure** not attributable to the change: verify by reading the log and comparing the SAME workflow on the LATEST `origin/main` runs (a flake window can span several consecutive main runs and mimic determinism), then `gh run rerun <run-id> --failed`. The standalone `funtests` pull_request workflow is chronically broken — distinct from the build-matrix `Run Functional Tests` job.
3. **Review findings**: read BOTH `gh pr view <pr> --json reviews` AND inline `gh api repos/yanet-platform/yanet2/pulls/<pr>/comments`. The `chatgpt-codex-connector` reviewer's inline P1/P2/P3 findings are often REAL. For each: FIX (amend pre-merge or follow-up) or REPLY why it's wrong. Main's ruleset requires thread resolution — after a fix, resolve the thread via GraphQL `resolveReviewThread(threadId)` and re-summon with a `@codex review` comment (force-push alone doesn't retrigger). After one addressed round per PR, merge — don't re-summon endlessly.

### Phase 6 — Merge (only if "merge" was authorized)

- **Single-commit PR** → `gh pr merge --admin --squash`.
- **Multi-commit, deliberately structured history** → `--rebase` (preserve it).
- **Multi-commit where extras are review fixups** → `--squash` WITH an explicit clean message: `--subject "<type>(<scope>): <title> (#N)" --body "<high-level or empty>"`. Never let GitHub's default squash body (a bullet list of every intermediate commit) land — the user calls that "каша" and it is a convention violation.
- Updating the branch with newer main before merge → REBASE + force-with-lease, never `git merge origin/main`.

### Phase 7 — Cleanup (from the MAIN checkout)

Run all of this from the main checkout, never with cwd inside the worktree (removing it yanks cwd and chained git dies):

1. `git worktree remove .claude/worktrees/<name>` (if used).
2. `git checkout main && git pull`.
3. Delete the branch local + remote: `git branch -D <branch>` and `git push origin --delete <branch>` (auto-delete is unreliable; ignore "remote ref does not exist"). Confirm no survivor with `git ls-remote --heads origin '<pattern>'`.

## Special cases & recovery

Detailed recipes live in `references/branching-and-recovery.md`:

- **Parallel PRs touching a shared barrel** (`web/src/components/index.ts`, `hooks/index.ts`) — merge one at a time, rebase the rest first; never tear down a branch before MERGED.
- **Stacked PRs** — child off parent branch, restack after a parent rebase with the OLD parent tip SHA.
- **Prerequisite refactor** — split out-of-scope cleanup into its own PR first, merge, rebase the feature.
- **Iterating an open PR's design** — amend + force-push, never close-and-reopen.
- **Recovery** — amend landed on the wrong branch; wrong hunks staged; branch deleted before merge; never `reset --hard` with unrelated dirty files present.

## End of run

Report to the user: PR number(s) and state (open / merged), any review findings addressed, any flakes you re-ran, and (if "create a PR" not "merge") that CI is green and it's ready to merge on their word.
