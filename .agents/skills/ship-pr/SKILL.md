---
name: ship-pr
description: >-
  The canonical publish/merge runbook for landing a verified change on main:
  branch from confirmed origin/main, stage ONLY the approved publish manifest, open a
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

Take a verified, reviewer-approved change and land it on `main` cleanly: branch from confirmed `origin/main`, stage exactly the approved publish manifest, open a scoped PR, drive CI green, address every review finding, and merge with the right strategy — then tear down the branch and worktree.

You (the architect) drive this. You never write the code — that was already delegated to the coders and verified. This skill is purely the **publish workflow**: local git plus GitHub MCP, and the discipline that keeps a parallel, worktree-heavy repo from corrupting itself.

**Invoking this skill is the explicit publish authorization.** The standing rule is "never commit/push/merge unless the user asks in the current turn" — asking to ship/land/merge IS that ask. Scope follows the verb: **"create a PR"** authorizes commit + push + PR + CI + addressing findings (NOT merge); **"create and merge" / "влей в main"** also authorizes the merge + cleanup. Delegated agents still never commit — you do.

## Non-negotiables

- **Don't publish unverified work.** Before branching, the change must have passed its verification gates AND a `reviewer` APPROVED pass using `review-change` on the complete current candidate (Phase 0). If either is missing, do that first — never ship unreviewed code.
- **Publish from a dedicated worktree.** The primary checkout stays on `main` as a synchronization anchor; only an explicit instruction for the current task allows branching there.
- **Carry approval only by fingerprint equivalence.** The fingerprint is the exact reviewed base, review/publish manifests, file modes/types, and bytes. After transfer, staging, commit, amend, or push, verify the packaged side matches it exactly; a new commit SHA alone is harmless, but any fingerprint difference requires a fresh full-candidate review.
- **Branch from CONFIRMED `origin/main`**, never the session-start branch line and never a bare `git switch -c` (HEAD may be on a parallel WIP branch and drag its commits in).
- **Stage ONLY the approved publish manifest.** The complete review manifest may also contain local-only ignored configuration; preserve it in its source worktree but never transfer or stage it. Never `git add -A`. Never stage pre-existing/unrelated dirty files. `git diff --cached -- <file>` each publish entry, and cross-check every publish-class untracked (`??`) file is included.
- **No destructive git with a dirty tree or without explicit permission** — no `reset --hard`/`checkout`/`restore`/`stash`/index ops that could wipe unrelated uncommitted work. Recovery recipes are in `references/branching-and-recovery.md`.
- **Verify `git branch --show-current` before EVERY commit/amend/push** — a parallel actor can switch branches under you and land your amend on `main`.
- **Discover GitHub MCP before choosing a client.** Lazy `mcp__github__*` tools may be absent from the initial tool list, so actively search the available tool metadata first. If found, call `mcp__github__get_me` before any other GitHub operation; use GitHub MCP for PR list/search/create/read/update, changed files, reviews, review threads/comments, check runs/status, replies, review actions, and merge.
- **Use `gh` only as a recorded fallback.** It is allowed only when GitHub MCP is genuinely unavailable after discovery, lacks the required operation (for example continuous check watching or `--admin` merge bypass), or fails. Record the operation and reason in the run report; return to MCP for later supported operations.
- **No AI attribution footers** in commit messages or PR bodies: neither `Co-Authored-By: Claude ...` / `Generated with Claude Code` nor Codex equivalents. AGENTS.md and the user forbid them. Check the body before creating the PR.
- **Conventional, scoped subjects.** Commit + PR title: `<feat|fix|refactor|perf|chore|docs|test|build|ci|style>(<scope>): <short description>`. A scopeless title is a convention violation.
- **PR body**: capitalized, period-ended bullets; high-level (no symbol names, no code-level detail); `Closes #<n>.` when applicable. No `## Summary` header, no `Test plan` section.
- **One logical change = one PR.** Out-of-scope prerequisites get their own PR first (see special cases).
- **NEVER merge with an unaddressed review finding** — read BOTH PR-level reviews and inline Codex comments; fix or reply to every one.
- **Never delete a branch (local or remote) until GitHub reports the PR MERGED** — a failed merge on a torn-down branch auto-closes the PR.

## Pipeline

### Phase 0 — Precondition: is it ready to ship?

1. **Materialize the complete candidate before gates or review.** If the change touched any `Cargo.toml` dependency, regenerate the root `Cargo.lock` with `cargo build`, inspect `git diff -- Cargo.lock`, and include it in the publish manifest — CI can pass while its omission remains silent.
2. **Gates green, run from repo ROOT** (each catches what the narrower command misses):
   - Go → `go build ./...` AND `make test` (per-module CGO test pkgs under `modules/*/tests/...` are missed from `controlplane/`; use `make test-functional` only when the functional/QEMU scope requires it).
   - C → `meson compile -C build` AND `meson test -C build` AND `make fuzz`.
   - Rust → `cargo test --workspace`.
   - Web, only when the PR touches `web/**`, `modules/*/web/**`, `operators/*/web/**`, or `devices/*/web/**` → `npm ci`, `npm run test -w web`, AND `npm run build -w web`. For browser-visible changes, also run Playwright on the real path and inspect its screenshot after asserting the relevant row count.
   - For C changes reachable across the CGO boundary (`lib/controlplane`, `api/`, anything called from `controlplane/ffi` or `bindings/go`) → also `make test-asan` (meson-only ASan never crosses into the Go cgo tests).
3. **A `reviewer` APPROVED pass using `review-change` exists** for the complete current candidate. Give the reviewer the task brief, exact base, candidate worktree or PR, and full review manifest, with every entry classified as publish or local-only and an exact publish manifest. If the work is uncommitted, the reviewer inspects the exact on-disk paths in the worktree that contains it, without creating a clean isolation fork; never in the same batch as the coder. Any approval-fingerprint change invalidates the approval, so after a rebase or fix re-review the whole candidate, not only the new diff. If approval is missing or stale, run the review now.

### Phase 1 — Branch from confirmed origin/main

1. `git fetch origin main`.
2. Create the branch off confirmed `origin/main`. Use a **shell-safe** name — `<type>/<scope>-<what>` (e.g. `chore/ai-ship-pr`): no `(`/`)` (bash mis-parses them unquoted in git commands), and avoid a name that is also a path prefix of another branch (see stacked-PR ref):
   - Worktree (the default, and mandatory unless the user waived isolation for this task): `git worktree add .agent-state/worktrees/<name> -b <branch> origin/main` (root-local gitignored runtime state, never sibling dirs). Put a task that needs its own full C/DPDK build on a volume with room for it instead.
   - Primary checkout, only under that explicit waiver and only when it is clean, unshared, and on `main`: `git switch main && git fetch origin main && git switch -c <branch> origin/main`.
3. If reviewed publish work is uncommitted in ANY source worktree (primary or coder), transfer ONLY the exact publish manifest to the fresh worktree with verified temporary patches, never a direct pipe between producer and applier. Leave every reviewed local-only entry untouched in its source worktree. Run transfer separately for staged tracked changes (with `--cached`) and unstaged tracked changes (without it); either may be empty.

   ```bash
   patch_path=$(mktemp) || exit 1
   trap 'rm -f -- "$patch_path"' EXIT
   if ! git -C <source-worktree> diff --cached --binary -- <publish-files> >"$patch_path"; then
     exit 1
   fi
   if [ -s "$patch_path" ]; then
     git -C <fresh-worktree> apply --check "$patch_path" || exit 1
     git -C <fresh-worktree> apply "$patch_path" || exit 1
   fi
   rm -f -- "$patch_path" || exit 1
   trap - EXIT
   ```

   For each publish-class untracked file, use a fresh temporary patch and handle `git diff --no-index` explicitly:

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

   Here `1` with a non-empty patch is success, `0` means no difference, and any other status is an error. Apply only after this check. Track every non-empty category and fail if none transferred; then inspect `git -C <fresh-worktree> diff -- <publish-files>` and `git -C <fresh-worktree> status --short -- <publish-files>`, and prove the transferred publish fingerprint equals the approved one before rerunning the affected Phase 0 gates. Do not copy the tree or touch unrelated dirty state.
4. Only for clean, committed work on a coder's worktree branch, confirm that branch was forked from current `origin/main`; rebase it if stale (`git rebase origin/main`, never `git merge origin/main`). Any rebase changes the reviewed base, even without conflicts: rerun the affected gates and obtain a fresh full-candidate `review-change` approval before publishing. Never rebase a dirty coder worktree.
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

   Inspect the listed destinations before building; the Git manifest selects only ignored `*.pb.go` at matching relative paths and creates no replacement symlink. This seeding is legal only when no gate produces or consumes `build` — it only links against the archives already there, or ignores it, as `go build`, `go test`, `cargo`, `npm` and a lint-only `make lint/comments` do. NEVER run `make` or `meson` against the symlink: `make test` alone expands to `meson compile -C build` plus `meson test -C build`, which compiles the PRIMARY checkout's sources into the developer's shared directory and reports its results as this candidate's gate.
6. When a gate produces or consumes `build` — `make test` drives meson at it, `make test-functional` mounts it into the VM — skip the symlink in item 5 and give the worktree its own real `build`, so the usual `meson compile -C build` recipes stay correct: `meson setup build` initialises the empty `subprojects/dpdk` and `subprojects/libpcap` itself, but a linked worktree does not share the superproject's submodule objects, so git clones them from the remote (network required) into `.git/worktrees/<name>/modules/` — roughly 255 MB before the build itself. Phase 0's Go gate is `make test`, so a Go-only candidate needs this too — and it does not also need item 5's protobuf copy, because `meson compile -C build` generates the `*.pb.go` into the worktree as custom targets.

### Phase 2 — Stage exactly this change

`git add` the explicit publish manifest (never `-A`). `git diff --cached -- <file>` EACH staged file — an index entry can pick up unrelated hunks from a concurrent dirty file. Cross-check every publish-class untracked (`??`) file is staged and no local-only entry is staged, then prove the staged modes/types/bytes equal the approved publish fingerprint. Omitting a publish entry can ship a dangling import, while including local state leaks it into the PR.

### Phase 3 — Commit

Verify the branch first. Commit with a conventional scoped subject, high-level body, no footers. Then prove the committed diff against the recorded base has the approved publish manifest, modes/types, and bytes; a mismatch invalidates approval and must not be pushed. (Authorized only because invoking this skill is the publish ask.)

### Phase 4 — Push & open the PR

1. `git push -u origin <branch>` (from inside the worktree if used).
2. Create the PR through GitHub MCP with explicit head `<branch>` and base `main`. Scoped title; body per the non-negotiables; **check the body for footers before creating**. Use MCP PR search/list/read to find an existing PR rather than creating a duplicate.
3. Read its changed files and diff through GitHub MCP and confirm the PR base plus publish manifest, modes/types, and bytes match the approval fingerprint exactly. Extras (inherited WIP or local-only state) → `git rebase --onto origin/main <wip-tip> <branch>`, rerun the affected gates, obtain a fresh full-candidate `review-change` approval, force-push-with-lease, and re-verify the fingerprint.
4. **Workflow-file PRs**: pushing a commit touching `.github/workflows/` needs the git token to have `workflow` OAuth scope — you can't self-grant. If GitHub MCP cannot repair credentials, ask the user to run `gh auth refresh -h github.com -s workflow` as a recorded fallback. A path-filtered workflow won't run when only its own YAML changes — its file must be in its own `paths:` filter to self-trigger.

### Phase 5 — CI & review

1. Wait with `gh pr checks <pr> --watch` directly — no upfront `sleep`. Continuous check watching is an enumerated GitHub MCP gap; record that fallback. Read outcomes and failure detail through GitHub MCP once the watch returns. One short retry is justified only when the watch exits immediately with "no checks" right after a push.
2. **Flaky/infra failure** not attributable to the change: read the run details through MCP and compare the SAME workflow on the LATEST `origin/main` runs (a flake window can span several consecutive main runs and mimic determinism). Re-run through MCP when supported; otherwise use `gh run rerun <run-id> --failed` and record why. The standalone `funtests` pull_request workflow is chronically broken — distinct from the build-matrix `Run Functional Tests` job.
3. **Review findings**: use GitHub MCP to read BOTH PR-level reviews and inline review threads/comments. The `chatgpt-codex-connector` reviewer's inline P1/P2/P3 findings are often REAL. Reproduce each finding, then FIX it or REPLY through MCP with evidence that it is wrong.
4. If a finding changes any candidate file, rerun the affected local verification gates AND obtain a fresh `reviewer` APPROVED pass using `review-change` on the whole current candidate before amend. After amend, prove the committed fingerprint equals the approved one; push only when equivalent.
5. After pushing a fix, reply to and resolve the addressed thread through MCP when supported; otherwise use `gh api graphql` and record why. The reply and resolve steps take different IDs: the reply's numeric comment ID comes from the comment's `html_url` `#discussion_r...` anchor, not the GraphQL thread ID used to resolve it. Re-summon with a `@codex review` comment through MCP because force-push alone does not retrigger review.
6. Return to step 1 for the new head and reread both PR-level reviews and inline threads. After at least one addressed round with green checks and no unresolved findings, proceed to Phase 6 only when merge was authorized; otherwise leave the PR ready to merge and report it.

### Phase 6 — Merge (only if "merge" was authorized)

- Merge through GitHub MCP. **Single-commit PR** → squash. **Multi-commit, deliberately structured history** → rebase (preserve it). **Multi-commit where extras are review fixups** → squash with an explicit clean `<type>(<scope>): <title> (#N)` title and high-level or empty body. Never let GitHub's default squash body (a bullet list of every intermediate commit) land — the user calls that "каша" and it is a convention violation.
- Updating the branch with newer main before merge → REBASE, rerun the affected gates, obtain a fresh full-candidate `review-change` approval, then force-with-lease and return to Phase 5 for the new head; never `git merge origin/main`.

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

- **Parallel PRs touching a shared barrel** (`web/src/components/index.ts`, `hooks/index.ts`) — merge one at a time; after rebasing each survivor, gates + fresh review precede push.
- **Stacked PRs** — child off parent branch; restack after a parent rebase with the OLD parent tip SHA, then gates + fresh review precede push.
- **Prerequisite refactor** — split out-of-scope cleanup into its own PR first; after merging it and rebasing the feature, gates + fresh review precede push.
- **Iterating an open PR's design** — gates + fresh review precede amend, fingerprint equivalence precedes force-push, and the PR is never closed and reopened.
- **Recovery** — amend landed on the wrong branch; wrong hunks staged; branch deleted before merge; never `reset --hard` with unrelated dirty files present.

## End of run

Report to the user: PR number(s) and state (open / merged), any review findings addressed, any flakes you re-ran, every `gh` fallback with its reason, and (if "create a PR" not "merge") that CI is green and it's ready to merge on their word.
