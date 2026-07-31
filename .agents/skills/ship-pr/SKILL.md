---
name: ship-pr
description: >-
  The canonical publish/merge runbook for landing a verified change on main:
  branch from confirmed origin/main, stage ONLY the intended files, open a
  scoped PR (no AI attribution footers), drive CI to green, address EVERY review/Codex
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

You (the architect) drive this. You never write the code — that was already delegated to the coders and verified. This skill is purely the **publish workflow**: local git plus GitHub MCP, and the discipline that keeps a parallel, worktree-heavy repo from corrupting itself.

**Invoking this skill is the explicit publish authorization.** The standing rule is "never commit/push/merge unless the user asks in the current turn" — asking to ship/land/merge IS that ask. Scope follows the verb: **"create a PR"** authorizes commit + push + PR + CI + addressing findings (NOT merge); **"create and merge" / "влей в main"** also authorizes the merge + cleanup. Delegated agents still never commit — you do.

## Non-negotiables

- **Don't publish unverified work.** Before branching, the change must have passed its verification gates AND a `reviewer` APPROVED pass (Phase 0). If either is missing, do that first — never ship unreviewed code.
- **Branch from CONFIRMED `origin/main`**, never the session-start branch line and never a bare `git switch -c` (HEAD may be on a parallel WIP branch and drag its commits in).
- **Stage ONLY this change's files.** Never `git add -A`. Never stage pre-existing/unrelated dirty files. `git diff --cached -- <file>` each one, and cross-check every new untracked (`??`) file the change created is included.
- **No destructive git with a dirty tree or without explicit permission** — no `reset --hard`/`checkout`/`restore`/`stash`/index ops that could wipe unrelated uncommitted work. Recovery recipes are in `references/branching-and-recovery.md`.
- **Verify `git branch --show-current` before EVERY commit/amend/push** — a parallel actor can switch branches under you and land your amend on `main`.
- **Discover GitHub MCP before choosing a client.** Lazy `mcp__github__*` tools may be absent from the initial tool list, so actively search the available tool metadata first. If found, call `mcp__github__get_me` before any other GitHub operation; use GitHub MCP for PR list/search/create/read/update, changed files, reviews, review threads/comments, check runs/status, replies, review actions, and merge.
- **Use `gh` only as a recorded fallback.** It is allowed only when GitHub MCP is genuinely unavailable after discovery, lacks the required operation (for example continuous watching or thread-resolution mutation), or fails. Record the operation and reason in the run report; return to MCP for later supported operations.
- **No AI attribution footers** in commit messages or PR bodies: neither `Co-Authored-By: Claude ...` / `Generated with Claude Code` nor Codex equivalents. AGENTS.md and the user forbid them. Check the body before creating the PR.
- **Conventional, scoped subjects.** Commit + PR title: `<feat|fix|refactor|perf|chore|docs|test|build|ci|style>(<scope>): <short description>`. A scopeless title is a convention violation.
- **PR body**: capitalized, period-ended bullets; high-level (no symbol names, no code-level detail); `Closes #<n>.` when applicable. No `## Summary` header, no `Test plan` section.
- **One logical change = one PR.** Out-of-scope prerequisites get their own PR first (see special cases).
- **NEVER merge with an unaddressed review finding** — read BOTH PR-level reviews and inline Codex comments; fix or reply to every one.
- **Never delete a branch (local or remote) until GitHub reports the PR MERGED** — a failed merge on a torn-down branch auto-closes the PR.

## Pipeline

### Phase 0 — Precondition: is it ready to ship?

1. **Gates green, run from repo ROOT** (each catches what the narrower command misses):
   - Go → `go build ./...` AND `make test` (per-module CGO test pkgs under `modules/*/tests/...` are missed from `controlplane/`; use `make test-functional` only when the functional/QEMU scope requires it).
   - C → `meson compile -C build` AND `meson test -C build` AND `make fuzz`.
   - Rust → `cargo test --workspace`.
   - Web, only when the PR touches `web/**`, `modules/*/web/**`, `operators/*/web/**`, or `devices/*/web/**` → `npm ci`, `npm run test -w web`, AND `npm run build -w web`. For browser-visible changes, also run Playwright on the real path and inspect its screenshot after asserting the relevant row count.
   - For C changes reachable across the CGO boundary (`lib/controlplane`, `api/`, anything called from `controlplane/ffi` or `bindings/go`) → also `make test-asan` (meson-only ASan never crosses into the Go cgo tests).
2. **A `reviewer` APPROVED pass exists** for this change. If the work is uncommitted, the reviewer inspects the exact on-disk paths in the worktree that contains it, without creating a clean isolation fork; never in the same batch as the coder. If not yet done, run it now.
3. If the change touched any `Cargo.toml` dependency, regenerate and stage the root `Cargo.lock` in THIS PR (`cargo build`, then `git diff -- Cargo.lock`) — CI passes without it, so the omission is silent.

### Phase 1 — Branch from confirmed origin/main

1. `git fetch origin main`.
2. Create the branch off confirmed `origin/main`. Use a **shell-safe** name — `<type>/<scope>-<what>` (e.g. `chore/ai-ship-pr`): no `(`/`)` (bash mis-parses them unquoted in git commands), and avoid a name that is also a path prefix of another branch (see stacked-PR ref):
   - Worktree (preferred for isolation): `git worktree add .agent-state/worktrees/<name> -b <branch> origin/main` (root-local gitignored runtime state, never sibling dirs).
   - Main checkout (only when clean and unshared): `git switch main && git fetch origin main && git switch -c <branch> origin/main`.
3. If reviewed work is uncommitted in ANY source worktree (primary or coder), transfer ONLY its exact reviewed diff/file set to the fresh worktree with verified temporary patches, never a direct pipe between producer and applier. Run this separately for staged tracked changes (with `--cached`) and unstaged tracked changes (without it); either may be empty.

   ```bash
   patch_path=$(mktemp) || exit 1
   trap 'rm -f -- "$patch_path"' EXIT
   if ! git -C <source-worktree> diff --cached --binary -- <reviewed-files> >"$patch_path"; then
     exit 1
   fi
   if [ -s "$patch_path" ]; then
     git -C <fresh-worktree> apply --check "$patch_path" || exit 1
     git -C <fresh-worktree> apply "$patch_path" || exit 1
   fi
   rm -f -- "$patch_path" || exit 1
   trap - EXIT
   ```

   For each reviewed untracked file, use a fresh temporary patch and handle `git diff --no-index` explicitly:

   ```bash
   patch_path=$(mktemp) || exit 1
   trap 'rm -f -- "$patch_path"' EXIT
   if git -C <source-worktree> diff --no-index --binary /dev/null <file> >"$patch_path"; then
     diff_status=0
   else
     diff_status=$?
   fi
   if [ "$diff_status" -ne 1 ] || [ ! -s "$patch_path" ]; then
     exit 1
   fi
   git -C <fresh-worktree> apply --check "$patch_path" || exit 1
   git -C <fresh-worktree> apply "$patch_path" || exit 1
   rm -f -- "$patch_path" || exit 1
   trap - EXIT
   ```

   Here `1` with a non-empty patch is success, `0` means no difference, and any other status is an error. Apply only after this check. Track every non-empty category and fail if none transferred; then inspect `git -C <fresh-worktree> diff -- <reviewed-files>` and `git -C <fresh-worktree> status --short -- <reviewed-files>` before rerunning the affected Phase 0 gates. Do not copy the tree or touch unrelated dirty state.
4. Only for clean, committed work on a coder's worktree branch, confirm that branch was forked from current `origin/main`; rebase it if stale (`git rebase origin/main`, never `git merge origin/main`). After rebase, rerun the affected gates; if conflict resolution changed content, obtain a fresh reviewer pass before publishing. Never rebase a dirty coder worktree.
5. To run Go commands in a fresh worktree, set `$source_worktree` and `$fresh_worktree` to verified absolute paths, then seed only generated protobufs and the verified build directory. Do not overwrite any existing destination:

   ```bash
   pb_list_path=$(mktemp) || exit 1
   trap 'rm -f -- "$pb_list_path"' EXIT
   if ! git -C "$source_worktree" ls-files --others -i --exclude-standard -- '*.pb.go' >"$pb_list_path"; then
     exit 1
   fi
   if [ ! -s "$pb_list_path" ]; then
     exit 1
   fi
   while IFS= read -r source_file; do
     relative_path="$source_file"
     source_file="$source_worktree/$relative_path"
     if [ ! -f "$source_file" ]; then
       exit 1
     fi
     destination_file="$fresh_worktree/$relative_path"
     if [ -e "$destination_file" ]; then
       cmp -s "$source_file" "$destination_file" || exit 1
       continue
     fi
     destination_directory=$(dirname "$destination_file") || exit 1
     mkdir -p -- "$destination_directory" || exit 1
     cp -- "$source_file" "$destination_file" || exit 1
   done <"$pb_list_path"
   if [ ! -d "$source_worktree/build" ] || [ -e "$fresh_worktree/build" ] || [ -L "$fresh_worktree/build" ]; then
     exit 1
   fi
   ln -s "$source_worktree/build" "$fresh_worktree/build" || exit 1
   while IFS= read -r relative_path; do
     ls -ld "$fresh_worktree/$relative_path" || exit 1
   done <"$pb_list_path"
   ls -ld "$fresh_worktree/build" || exit 1
   rm -f -- "$pb_list_path" || exit 1
   trap - EXIT
   ```

   Inspect the listed destinations before building; the Git manifest selects only ignored `*.pb.go` at matching relative paths and creates no replacement symlink.

### Phase 2 — Stage exactly this change

`git add` the explicit file list (never `-A`). `git diff --cached -- <file>` EACH staged file — an index entry can pick up unrelated hunks from a concurrent dirty file. Cross-check every new untracked (`??`) file is staged: omitting one ships a dangling import, and web CI is vitest-only (no build/tsc gate) so it stays green on a broken module graph.

### Phase 3 — Commit

Verify the branch first. Commit with a conventional scoped subject, high-level body, no footers. (Authorized only because invoking this skill is the publish ask.)

### Phase 4 — Push & open the PR

1. `git push -u origin <branch>` (from inside the worktree if used).
2. Create the PR through GitHub MCP with explicit head `<branch>` and base `main`. Scoped title; body per the non-negotiables; **check the body for footers before creating**. Use MCP PR search/list/read to find an existing PR rather than creating a duplicate.
3. Read its changed files through GitHub MCP and confirm ONLY this change's files are present. Extras (inherited WIP) → `git rebase --onto origin/main <wip-tip> <branch>`, force-push-with-lease, re-verify.
4. **Workflow-file PRs**: pushing a commit touching `.github/workflows/` needs the git token to have `workflow` OAuth scope — you can't self-grant. If GitHub MCP cannot repair credentials, ask the user to run `gh auth refresh -h github.com -s workflow` as a recorded fallback. A path-filtered workflow won't run when only its own YAML changes — its file must be in its own `paths:` filter to self-trigger.

### Phase 5 — CI & review

1. Inspect PR status and check runs through GitHub MCP. Poll MCP status without an upfront `sleep`; use `gh pr checks <pr> --watch` only when MCP lacks continuous watch, and record that fallback. One short retry is justified only when the watch exits immediately with "no checks" right after a push.
2. **Flaky/infra failure** not attributable to the change: read the run details through MCP and compare the SAME workflow on the LATEST `origin/main` runs (a flake window can span several consecutive main runs and mimic determinism). Re-run through MCP when supported; otherwise use `gh run rerun <run-id> --failed` and record why. The standalone `funtests` pull_request workflow is chronically broken — distinct from the build-matrix `Run Functional Tests` job.
3. **Review findings**: use GitHub MCP to read BOTH PR-level reviews and inline review threads/comments. The `chatgpt-codex-connector` reviewer's inline P1/P2/P3 findings are often REAL. For each: FIX (amend pre-merge or follow-up) or REPLY through MCP why it's wrong. Main's ruleset requires thread resolution — after a fix, resolve the thread through MCP if supported; otherwise use `gh api graphql` and record why. Re-summon with a `@codex review` comment through MCP (force-push alone doesn't retrigger). After one addressed round per PR, merge only when authorized; otherwise leave CI green and report it ready to merge.
4. After addressing a finding, rerun the affected local verification gates before amend, push, or merge.

### Phase 6 — Merge (only if "merge" was authorized)

- Merge through GitHub MCP. **Single-commit PR** → squash. **Multi-commit, deliberately structured history** → rebase (preserve it). **Multi-commit where extras are review fixups** → squash with an explicit clean `<type>(<scope>): <title> (#N)` title and high-level or empty body. Never let GitHub's default squash body (a bullet list of every intermediate commit) land — the user calls that "каша" and it is a convention violation.
- Updating the branch with newer main before merge → REBASE + force-with-lease, never `git merge origin/main`.

### Phase 7 — Cleanup without touching an unsafe main checkout

Run from a separate cwd, never the worktree being removed:

1. `git worktree remove .agent-state/worktrees/<name>` (if used).
2. Identify the primary `main` checkout with `git worktree list`. Update it only if it is already on `main`, clean (`git -C <primary-main> status --porcelain` is empty), and not in use by another actor: `git -C <primary-main> fetch origin main && git -C <primary-main> merge --ff-only origin/main`. Otherwise leave it untouched and report that the remote `main` is canonical.
3. After MCP confirms the PR MERGED and `git rev-parse --verify "$branch"` matches `$recorded_pr_head_sha`, attempt `git branch -d "$branch"`. If it refuses because a GitHub squash or rebase merge left the PR head non-ancestral, then and only then use `git branch -D "$branch"`.
4. Before remote deletion, query the exact remote ref. If it is absent, skip deletion. If it differs from `$recorded_pr_head_sha`, stop and report it. Use the expected-old-SHA lease for deletion, so a race that advances the remote ref fails safely:

   ```bash
   remote_ref_path=$(mktemp) || exit 1
   trap 'rm -f -- "$remote_ref_path"' EXIT
   if ! git ls-remote --heads origin "refs/heads/$branch" >"$remote_ref_path"; then
     exit 1
   fi
   if [ -s "$remote_ref_path" ]; then
     if ! awk -v expected="$recorded_pr_head_sha" -v ref="refs/heads/$branch" '$1 == expected && $2 == ref { matched=1 } END { exit matched ? 0 : 1 }' "$remote_ref_path"; then
       exit 1
     fi
     git push "--force-with-lease=refs/heads/$branch:$recorded_pr_head_sha" origin ":refs/heads/$branch" || exit 1
   fi
   rm -f -- "$remote_ref_path" || exit 1
   trap - EXIT
   ```

5. Confirm no survivor with `git ls-remote --heads origin "refs/heads/$branch"`.

## Special cases & recovery

Detailed recipes live in `references/branching-and-recovery.md`:

- **Parallel PRs touching a shared barrel** (`web/src/components/index.ts`, `hooks/index.ts`) — merge one at a time, rebase the rest first; never tear down a branch before MERGED.
- **Stacked PRs** — child off parent branch, restack after a parent rebase with the OLD parent tip SHA.
- **Prerequisite refactor** — split out-of-scope cleanup into its own PR first, merge, rebase the feature.
- **Iterating an open PR's design** — amend + force-push, never close-and-reopen.
- **Recovery** — amend landed on the wrong branch; wrong hunks staged; branch deleted before merge; never `reset --hard` with unrelated dirty files present.

## End of run

Report to the user: PR number(s) and state (open / merged), any review findings addressed, any flakes you re-ran, every `gh` fallback with its reason, and (if "create a PR" not "merge") that CI is green and it's ready to merge on their word.
