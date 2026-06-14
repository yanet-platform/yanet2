---
name: raii-sweep
description: >-
  Weekly maintenance sweep that restores RAII symmetry in the YANET2 C code:
  every lifecycle pair must follow new/init/fini/free semantics (new allocates
  the struct, init fills fields, fini releases field memory, free deallocates
  the struct). Finds misnamed destructors (free that is semantically fini),
  non-canonical names (create/destroy/cleanup/release/deinit), missing
  destructors (leaks), and unpaired allocators — then fixes a bounded batch per
  run, one prefix family per PR. Use this skill whenever the user invokes the
  weekly RAII sweep, asks to "fix RAII symmetry", "приведи lifecycle-функции к
  паттерну", "найди несимметричные init/fini", or wants a C lifecycle cleanup
  pass.
---

# raii-sweep

Run the periodic RAII-symmetry sweep over the C codebase: scan for lifecycle asymmetries, triage them semantically against the project convention, fix 2–3 prefix families per run (one family = one PR), and keep a persistent ledger so weekly runs never re-litigate old verdicts.

You (the architect) drive this. You never edit C or Go code yourself — fixes are delegated to **coder-c** (and **coder-go** when renamed symbols cross the CGO boundary); you own scanning, triage, verification gates, and the publish workflow.

**Invoking this skill is the explicit publish request**: commits, pushes, PR creation, and merges (after green CI and addressed review findings) are authorized within the run's budget. Delegated agents still never commit — you do.

## The convention (target state)

- `<prefix>_new` — allocates the struct, returns a pointer (typically calls init). Pairs with `free`.
- `<prefix>_init` — initializes fields in place, may allocate field memory. Pairs with `fini`.
- `<prefix>_fini` — releases field memory, does NOT free the struct itself. Idempotent on zero-init.
- `<prefix>_free` — deallocates the struct (typically calls fini first). NULL-safe.
- `_create`/`_destroy`/`_cleanup`/`_release`/`_deinit` are banned names. `_attach`/`_detach` are legitimate only for shared-memory multi-process attach semantics (yanet_shm, agent).

This matches coder-c's standardized lifecycle lessons (`lifecycle-classes.md`, `lifecycle-fini-special-cases.md` in its memory) — the skill enforces repo-wide what coder-c already applies to new code.

## Non-negotiables

- **Symbol renames only, never layout changes.** This sweep renames and restructures lifecycle functions; it NEVER alters shared-memory struct layout, field order, or sizes. If a fix seems to require a layout change, defer the candidate and flag it to the user.
- **A rename ships with ALL its consumers in the same PR**: every C caller, Go bindings (`bindings/go/*/cgo.go` or legacy `controlplane/ffi.go`), tests, fuzzing targets, comments. Zero references to the old name may remain repo-wide.
- **Semantic verdicts are read-code verdicts.** Never classify from the scan table alone — read the function bodies before deciding rename vs. real bug vs. false positive.
- **One prefix family = one PR**, each in its own worktree. Closely coupled clones (e.g. `btree_u16/u32/u64`) count as one family.
- **Budget: 2–3 PRs per run.** Semantic bugs (leaks, ownership traps) outrank pure renames when picking.
- **Never touch**: `new_module_<name>` defsym entry points, DPDK `rte_*` APIs, vendored code under `subprojects/`.

## Pipeline

### Phase 1 — Scan & ledger sync

1. `git fetch origin main`; work off `origin/main`.
2. Run the scan: `bash .claude/skills/raii-sweep/references/scan.sh`. It inventories lifecycle function definitions across `lib/ dataplane/ common/ api/ modules/ devices/` and pairs them by prefix, flagging asymmetries.
3. Sync the ledger at `.arch/raii-sweep/LEDGER.md` (gitignored; create on first run):
   - Add newly flagged prefixes as `pending`.
   - Mark entries whose asymmetry disappeared from the scan as `resolved-externally`.
   - Preserve all existing verdicts (`merged`, `deferred`, `fp-*`) — never re-triage a prefix with a false-positive verdict unless its code changed.

Ledger format — one table:

```
| prefix | area | class | priority | status | notes |
```

`status` ∈ `pending` / `in-progress` / `merged #PR` / `deferred` / `fp-no-fini-needed` / `fp-other` / `resolved-externally`.

### Phase 2 — Triage (semantic, bounded)

Take `pending` entries in priority order and read the actual code until you have 2–3 confirmed candidates. Classification rules, false-positive criteria, and the priority ladder live in `references/triage.md`. For each confirmed candidate record in the ledger: violation class, exact rename map (old → new symbol list) or fix description, full consumer list (`grep -rn` across C, Go, tests, fuzzing, comments), and blast-radius notes (FFI-crossing? hot-path header?).

Skip (and mark) candidates whose files overlap an open PR (`gh pr list --json files`) — cross-PR rename conflicts are not worth it.

### Phase 3 — Execute (delegate, one worktree per candidate)

For each candidate:

1. `git worktree add .claude/worktrees/raii-<prefix> -b refactor/raii-<prefix> origin/main`.
2. If the candidate touches Go bindings, seed gitignored generated files first: `rsync` the `*.pb.go` files from the main checkout into the worktree (a fresh worktree cannot run `go build ./...` without them).
3. Delegate to **coder-c** with a brief that names: the absolute worktree path (cd there, `git rev-parse --show-toplevel` to confirm), the exact rename map / fix, the full consumer list from Phase 2, and the relevant guardrails (see `references/triage.md` § Brief template). The brief MUST forbid destructive git ops (`stash`/`checkout`/`restore`/`reset`/index ops) and `git commit`, and restate coder-c's minimal-fix discipline: only the named symbols, no adjacent restructuring, no gratuitous doc comments.
4. If renamed symbols cross the CGO boundary, delegate the Go side to **coder-go** afterwards (same worktree, same constraints; receiver naming `m`, no comment drift).
5. Sequence strictly: coder-c → your build check → coder-go (if needed) → your build check. Never run a coder and its reviewer in the same batch.

### Phase 4 — Verify (you run the gates)

From the worktree root:

1. `grep -rn '<old_symbol>' .` for EVERY renamed symbol — zero hits allowed (code, comments, meson, Go, fuzzing).
2. C gate: `meson setup build` (or reuse) + `meson compile -C build` + `meson test -C build`.
3. Go gate when bindings changed: `go build ./...` in `controlplane/` and the touched module.
4. Local ASan (`make test-asan` or targeted `meson test -C build-asan <name>`) is required only when the change ALTERS BEHAVIOR — a destructor added (V3), ownership/free semantics changed, call order changed. Pure renames (most V1/V2) skip it: repo CI runs build-asan-test, build-tsan-test, and functional tests on every PR anyway.
5. Reviewer pass: launch **reviewer** WITHOUT isolation, read-only, pointed at the worktree's absolute paths (work is uncommitted). Address findings via the Review-Fix loop (max 3 rounds).

### Phase 5 — Publish & close the loop

1. Stage only the candidate's files; verify each with `git diff --cached -- <file>` and confirm no untracked stragglers.
2. Commit as `refactor(<scope>): <short description>` (scope = `lib`, `common`, module name…). No Claude footers.
3. Push, open the PR with a scoped title; body is capitalized period-ended bullets, no `## Summary`, no `Test plan`.
4. `gh pr checks <pr> --watch`; if it exits with "no checks reported" you ran it before CI registered — re-run it once after a minute. Read PR reviews AND Codex inline comments; fix or reply to every finding.
5. Merge (single-commit → squash `--admin`; multi-commit → rebase) WITHOUT `--delete-branch` — it fails while the branch is checked out in a worktree. Then `git worktree remove` from the main checkout, `git branch -D`, and `git push origin --delete <branch>`.
6. **Merge one PR at a time** — these PRs rename callers across wide files; rebase remaining branches onto `origin/main` after each merge.
7. **Stacked families** (candidate B's files depend on candidate A's pending rename, e.g. btree on big_array): branch B off A's branch, do the work in parallel, and after A squash-merges rebase with `git rebase --onto origin/main <A-branch-sha> refactor/raii-<B>` — A's commit drops out, B replays cleanly.
8. Update the ledger: `merged #PR`, date. Append a one-line run summary (date, PRs, verdicts changed) to the ledger's `## Run log`.

### End of run

Report to the user: PRs shipped, new verdicts (especially `deferred` ones needing a human call), remaining queue depth, and an ETA at the current budget.
