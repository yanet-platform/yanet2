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

You (the architect) drive this. You never write the code — that was already delegated to the coders and verified. This skill is purely the **publish workflow**: local `git` choreography plus GitHub access through the `github` MCP server, and the discipline that keeps a parallel, worktree-heavy repo from corrupting itself.

**Invoking this skill is the explicit publish authorization.** The standing rule is "never commit/push/merge unless the user asks in the current turn" — asking to ship/land/merge IS that ask. Scope follows the verb: **"create a PR"** authorizes commit + push + PR + CI + addressing findings (NOT merge); **"create and merge" / "влей в main"** also authorizes the merge + cleanup. Delegated agents still never commit — you do.

## Non-negotiables

- **Don't publish unverified work.** Before branching, the change must have passed its verification gates AND a `reviewer` APPROVED pass (Phase 0). If either is missing, do that first — never ship unreviewed code.
- **Publish from a dedicated worktree.** The primary checkout stays on `main` as a synchronization anchor; only an explicit instruction for the current task allows branching there.
- **Branch from CONFIRMED `origin/main`**, never the session-start branch line and never a bare `git checkout -b` (HEAD may be on a parallel WIP branch and drag its commits in).
- **Stage ONLY this change's files.** Never `git add -A`. Never stage pre-existing/unrelated dirty files. `git diff --cached -- <file>` each one, and cross-check every new untracked (`??`) file the change created is included.
- **No destructive git with a dirty tree or without explicit permission** — no `reset --hard`/`checkout`/`restore`/`stash`/index ops that could wipe unrelated uncommitted work. Recovery recipes are in `references/branching-and-recovery.md`.
- **Verify `git branch --show-current` before EVERY commit/amend/push** — a parallel actor can switch branches under you and land your amend on `main`.
- **No `Co-Authored-By` / "Generated with Claude Code" footers** in commit messages or PR bodies. The harness suggests them; CLAUDE.md and the user forbid them. Check the body before `create_pull_request`.
- **Conventional, scoped subjects.** Commit + PR title: `<feat|fix|refactor|chore|perf|docs>(<scope>): <short description>`. A scopeless title is a convention violation.
- **PR body**: capitalized, period-ended bullets; high-level (no symbol names, no code-level detail); `Closes #<n>.` when applicable. No `## Summary` header, no `Test plan` section.
- **One logical change = one PR.** Out-of-scope prerequisites get their own PR first (see special cases).
- **NEVER merge with an unaddressed review finding** — read BOTH PR-level reviews and inline Codex comments; fix or reply to every one. An empty review list isn't proof Codex reviewed: confirm Codex's completion signal (Phase 5 step 3) before merging.
- **Never delete a branch (local or remote) until `merge_pull_request` (or `gh pr merge`, for the admin-bypass case) reports MERGED** — a failed merge on a torn-down branch auto-closes the PR.

### MCP is not a git substitute

The `github` MCP server's file-write tools (`create_or_update_file`, `push_files`, `delete_file`, `create_branch`, `update_pull_request_branch`) commit server-side. Never use them to land this change — they bypass staging discipline, the `commit-msg` hook, and every local verification gate in Phase 0. Branching, staging, committing, and pushing stay local `git`; MCP is for reading GitHub state and for the PR/review/merge operations that have no local-git equivalent.

## Pipeline

### Phase 0 — Precondition: is it ready to ship?

1. **Gates green, run from repo ROOT** (each catches what the narrower command misses):
   - Go → `go build ./...` (per-module CGO test pkgs under `modules/*/tests/...` are missed from `controlplane/`).
   - C → `meson compile -C build` AND `meson test -C build` AND `make fuzz`.
   - Rust → `cargo test --workspace`.
   - For C changes reachable across the CGO boundary (`lib/controlplane`, `api/`, anything called from `controlplane/ffi` or `bindings/go`) → also `make test-asan` (meson-only ASan never crosses into the Go cgo tests).
2. **A `reviewer` APPROVED pass exists** for this change. If the work is uncommitted, the reviewer inspected the exact on-disk paths in the worktree that holds it, without creating a clean isolation fork; never in the same batch as the coder. If not yet done, run it now.
3. If the change touched any `Cargo.toml` dependency, regenerate and stage the root `Cargo.lock` in THIS PR (`cargo build`, then `git diff -- Cargo.lock`) — CI passes without it, so the omission is silent.

### Phase 1 — Branch from confirmed origin/main

1. `git fetch origin main`.
2. Create the branch off confirmed `origin/main`. Use a **shell-safe** name — `<type>/<scope>-<what>` (e.g. `chore/claude-ship-pr`): no `(`/`)` (bash mis-parses them unquoted in the `git`/`gh` commands below), and avoid a name that is also a path prefix of another branch (see stacked-PR ref):
   - Worktree (the default, and mandatory unless the user waived isolation for this task): `git worktree add .claude/worktrees/<name> -b <branch> origin/main` (gitignored location, never sibling dirs). Put a task that needs its own full C/DPDK build on a volume with room for it instead.
   - Primary checkout, only under that explicit waiver and only when it is clean, unshared, and on `main`: `git checkout main && git fetch origin main && git checkout -b <branch> origin/main`.
3. If the work already lives on a coder's worktree branch, just confirm that branch was forked from current `origin/main`; rebase it if stale (`git rebase origin/main`, never `git merge origin/main`).
4. When no gate produces or consumes `build` — it only links against the archives already there, or ignores it, as `go build`, `go test`, `cargo`, `npm` and a lint-only `make lint/comments` do — seed the gitignored generated files, see `worktree-isolation-and-seeding` (architect memory): rsync the `*.pb.go` and symlink `build/`. NEVER run `make` or `meson` against that symlink: `meson compile -C build` does not reconfigure, it compiles the PRIMARY checkout's sources into the shared directory, so the gate goes green on stale archives, and `--reconfigure`/`--wipe` retargets the developer's shared directory at the worktree.
5. When a gate produces or consumes `build` — `make test` expands to `meson compile -C build` plus `meson test -C build`, and `make test-functional` mounts the directory into the VM — the worktree needs its own real `build` instead of the symlink, and the usual `meson compile -C build` recipes then stay correct: `meson setup build` initialises the empty `subprojects/dpdk` and `subprojects/libpcap` itself, but a linked worktree does not share the superproject's submodule objects, so git clones them from the remote (network required) into `.git/worktrees/<name>/modules/` — roughly 255 MB before the build itself. Once `meson compile -C build` has run there it does not also need step 4's protobuf copy, because that compile generates the `*.pb.go` into the worktree as custom targets; before it, a bare `go build ./...` still sees them missing.

### Phase 2 — Stage exactly this change

`git add` the explicit file list (never `-A`). `git diff --cached -- <file>` EACH staged file — an index entry can pick up unrelated hunks from a concurrent dirty file. Cross-check every new untracked (`??`) file is staged: omitting one ships a dangling import, and web CI is vitest-only (no build/tsc gate) so it stays green on a broken module graph.

### Phase 3 — Commit

Verify the branch first. Commit with a conventional scoped subject, high-level body, no footers. (Authorized only because invoking this skill is the publish ask.)

### Phase 4 — Push & open the PR

1. `git push -u origin <branch>` (from inside the worktree if used).
2. `create_pull_request` with explicit `head` and `base: main`. Scoped title; body per the non-negotiables; **check the body for footers before creating**.
3. `pull_request_read` method `get_files` — confirm ONLY this change's files are present. `perPage` caps at 100 and the tool returns one page at a time, so request `perPage: 100` and keep requesting successive `page` values until a short (or empty) page returns, then compare the UNION of every page against the intended manifest — checking only the first page can pass on a partial file list. Extras (inherited WIP) → `git rebase --onto origin/main <wip-tip> <branch>`, force-push-with-lease, re-verify.
4. **Workflow-file PRs**: pushing a commit touching `.github/workflows/` needs the git token to have `workflow` OAuth scope — you can't self-grant; the user must run `gh auth refresh -h github.com -s workflow`. A path-filtered workflow won't run when only its own YAML changes — its file must be in its own `paths:` filter to self-trigger.

### Phase 5 — CI & review

1. Wait with `gh pr checks <pr> --watch` DIRECTLY — no upfront `sleep`, no sleep+re-poll loops. This is one of the enumerated MCP gaps: there is no continuous watch, and hand-rolling a poll loop over `get_check_runs` risks reading a partial check-run set as green, because runs materialise progressively and the response carries no aggregate completion flag.
   - Once the watch returns, read the outcome and any failure detail through MCP: `pull_request_read` method `get_check_runs` for per-check conclusions, `get_job_logs` for logs.
   - Keep the existing carve-out: one short retry if the watch exits immediately with "no checks" right after a push.
   - If you ever do poll `get_check_runs` instead, completeness is "no run is `queued` or `in_progress`" — never a count of successes.
   - `get_status` is the legacy commit-status API and stays useless here: every check is GitHub Actions, which reports only as check runs. Its actual failure mode is worse than "empty" — verified on merged, all-green PR #1584, it returns `state: "pending"` forever, which is what would make an agent using it wait indefinitely.
2. **Flaky/infra failure** not attributable to the change: verify by reading the log (`get_job_logs`) and comparing the SAME workflow on the LATEST `origin/main` runs (a flake window can span several consecutive main runs and mimic determinism), then `actions_run_trigger` method `rerun_failed_jobs`. The standalone `funtests` pull_request workflow is chronically broken — distinct from the build-matrix `Run Functional Tests` job.
3. **Review findings**: first check whether Codex actually finished reviewing — `pull_request_read` methods `get`, `get_reviews`, `get_review_comments`, and `get_comments` are all blind to this (the first carries no reactions field at all, the rest return empty), and `search_pull_requests`'s `reactions` field is a per-content COUNT with no reactor identity, so none of them can tell Codex's own reaction from a teammate's — this is one of the enumerated MCP gaps (a REST/GraphQL endpoint with no tool), so use `gh api --paginate repos/<owner>/<repo>/issues/<pr>/reactions` and look for `user.login: chatgpt-codex-connector[bot]` (the endpoint pages at 30 by default, and `gh api` without `--paginate` only reads the first page). The reaction is two-state: `eyes` when Codex starts, `+1` only once it finishes clean — keep polling while it's still `eyes`. GitHub attaches an issue reaction to the PR itself, not to a commit, so a `+1` left from an earlier clean pass still satisfies the gate after a force-push or a follow-up commit — on a PR pushed again, require the signal to be from that bot AND postdate that push: the reaction's `created_at`, a review from that bot whose `commit_id` matches the current head, or an inline comment from that bot whose `created_at`/`updated_at` postdates the push — `get_review_comments`'s inline-comment payload carries `author`, `created_at`, `updated_at`, no `commit_id`, so the comment's own timestamp is the only usable signal here. Do not use `is_outdated` for this: it reports only whether newer code superseded the thread's lines, not when the thread was posted, so a follow-up push that never touches that hunk leaves an old thread at `is_outdated: false`, which would falsely look like a current pass. Re-reacting is idempotent and does not refresh `created_at`, so if a re-summon is followed by a reaction that is still present but stale and nothing new posted, that means the re-summon didn't take, not that Codex is still working — re-summon again rather than polling forever. Green CI checks never mean Codex is done (the two are unordered — on public #1614 Codex finished 28 seconds after the last check went green, on private #81 half an hour before it; the first ordering is why green checks are not probative), and private-repo PRs get reviewed too, no exception. The gate is a `+1` from that bot, a posted review from that bot, or an inline comment from that bot — a merely non-empty reaction list is not the gate, since `eyes` satisfies a presence check while meaning Codex is still working, and a review or inline comment from a teammate does not mean Codex ran. Then read BOTH `pull_request_read` method `get_reviews` AND method `get_review_comments` — reviews and inline comments are separate and both matter. The `chatgpt-codex-connector` reviewer's inline P1/P2/P3 findings are often REAL. For each: FIX (amend pre-merge or follow-up) or REPLY why it's wrong, using `add_reply_to_pull_request_comment` — its `commentId` is the numeric ID parsed from the comment's `html_url` `#discussion_r...` anchor, not the `threadId` from `get_review_comments` (a GraphQL thread node ID, which the tool rejects). Main's ruleset requires thread resolution — after a fix, resolve the thread with `pull_request_review_write` method `resolve_thread` (the `threadId` comes from `get_review_comments`) and re-summon with a `@codex review` comment via `add_issue_comment` (force-push alone doesn't retrigger). After one addressed round per PR, stop re-summoning for new findings — but re-run this completion gate against the new head every time before merging.

### Phase 6 — Merge (only if "merge" was authorized)

- **Single-commit PR** → `merge_pull_request` with `merge_method: squash`.
- **Multi-commit, deliberately structured history** → `merge_method: rebase` (preserve it).
- **Multi-commit where extras are review fixups** → `merge_method: squash` WITH an explicit clean `commit_title`/`commit_message`: `commit_title: "<type>(<scope>): <title> (#N)"`, `commit_message: "<high-level or empty>"`. Never let GitHub's default squash body (a bullet list of every intermediate commit) land — the user calls that "каша" and it is a convention violation.
- **Ruleset bypass needed** → `merge_pull_request` has no admin-bypass equivalent, so a merge blocked by main's ruleset (unresolved threads, missing approval) fails and that failure is the signal. Fall back to `gh pr merge --admin` with the SAME strategy and message rules as above — a fixup-squash still needs `--subject "<type>(<scope>): <title> (#N)" --body "<high-level or empty>"` — and record why the bypass was needed.
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
