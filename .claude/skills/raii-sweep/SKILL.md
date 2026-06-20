---
name: raii-sweep
description: >-
  Weekly maintenance sweep that restores RAII symmetry in the YANET2 C code.
  The convention is four orthogonal primitives: new allocates ONLY the struct,
  init fills fields (and on any error jumps to a single err: label that calls
  fini), fini releases ONLY field memory and is idempotent, free deallocates
  ONLY the struct and is NULL-safe — paired new<->free and init<->fini. The
  sweep has two axes: a NAMING axis (misnamed/non-canonical destructors, found
  by scan.sh) and a STRUCTURAL axis (init error-path leaks/double-free/UAF,
  non-idempotent fini, non-NULL-safe free, new/free orthogonality, found by
  structural-scan.sh plus the leak-hunt workflow). It fixes a bounded batch per
  run, one prefix family per PR, with a persistent ledger. Use whenever the user
  invokes the weekly RAII sweep, asks to "fix RAII symmetry", "приведи
  lifecycle-функции к паттерну", "найди несимметричные init/fini", "сделай
  fini/free идемпотентными", wants the single-err-label init discipline, or
  wants a C lifecycle cleanup pass.
---

# raii-sweep

Run the periodic RAII-symmetry sweep over the C codebase: scan for lifecycle asymmetries on two axes (naming, structure), triage them semantically against the project convention, fix a bounded batch per run (one family = one PR), and keep a persistent ledger so weekly runs never re-litigate old verdicts.

You (the architect) drive this. You never edit C or Go code yourself — fixes are delegated to **coder-c** (and **coder-go** when renamed symbols cross the CGO boundary); you own scanning, triage, verification gates, and the publish workflow.

**Invoking this skill is the explicit publish request**: commits, pushes, PR creation, and merges (after green CI and addressed review findings) are authorized within the run's budget. Delegated agents still never commit — you do.

## The convention (target state)

Four orthogonal primitives. `new`/`free` deal ONLY with the struct's own storage; `init`/`fini` deal ONLY with its fields. The pairs are symmetric — `new`↔`free`, `init`↔`fini` — and composable: a heap object is built with `new` then `init`, and torn down with `fini` then `free`.

- `<prefix>_new(args) -> *self` — allocates the struct and returns the pointer; returns NULL on OOM. Does **nothing else** — no field initialization, no child allocation. `new` is expected to be RARE (most structs are caller-owned and only have `init`/`fini`).
- `<prefix>_init(self, args) -> int` — fills fields in place, may allocate field memory. First arg is `self`. On **any** failure it jumps to a single `err:` label that calls `<prefix>_fini(self)` and returns -1. It must establish `fini`'s precondition first (zero-init `self`, or rely on `new` having memset it) so `fini` is safe at every failure point.
- `<prefix>_fini(self, args) -> void` — releases field memory; does **NOT** free the struct. First arg is `self`. Idempotent: NULL-guards each child before releasing, resets it to NULL/zero afterward, and is safe on a zero/partially-initialized struct and safe to call twice.
- `<prefix>_free(self) -> void` — deallocates the struct; does **NOT** call `fini`. Single arg `self`. NULL-safe (`if (self == NULL) return;`).

The reference quartet is **`lib/controlplane/config/cp_chain.c`**: pure `cp_chain_new` (balloc + memset + NULL-on-OOM), single-`err_out:` `cp_chain_init` → `cp_chain_fini`, field-releasing `cp_chain_fini`, NULL-safe pure `cp_chain_free`. The whole `cp_*` config family (`cp_function`, `cp_pipeline`, `cp_device`) follows it.

Why idempotency is mandatory: it is the precondition for the single-`err:` label. Because `init` zero-inits `self` and `fini` resets every child, the same `fini` cleans up correctly whether `init` failed on the first child or the last — so cleanup lives in exactly one place, never scattered across per-error gotos. `memory_bfree(ctx, ptr, size)` is NULL-safe (a NULL block is a no-op, like `free(3)`) and ignores `size == 0`, so `fini` does NOT need to pre-guard NULL purely for the free — but it MUST still `SET_OFFSET_OF(&self->x, NULL)` (or zero the field) after releasing each child, so a second `fini` does not double-free the still-non-NULL pointer. (Historically `memory_bfree` was not NULL-safe: a NULL block with nonzero size wrote the free-list link through the pointer and corrupted the arena — the L-RIDX class, now closed at the primitive.)

Banned names: `_create`/`_destroy`/`_cleanup`/`_release`/`_deinit`/`_teardown`. `_attach`/`_detach` are legitimate only for shared-memory multi-process attach semantics (`yanet_shm`, `agent`).

## The two axes & their violation classes

Triage rules, false-positive criteria, and the priority ladder for both axes live in `references/triage.md`. Summary:

**Naming axis (A)** — symbol renames, surfaced by `scan.sh`, tracked in `LEDGER.md`:
- A1 misnamed `fini` (a `_free` that releases fields but never frees the struct).
- A2 non-canonical name (`create`/`destroy`/`cleanup`/`release`/`deinit`/`teardown`).
- A3 `free`/`fini` with no visible allocator pair.

**Structural axis (B)** — correctness & contract, surfaced by `structural-scan.sh` + the leak-hunt workflow, tracked in `LEAKS.md`:
- B1 missing destructor entirely (init/new allocates, nothing releases → leak).
- B2 init error-path defect (leak, double-free, or UAF on a failure path — the filter-compiler `*_attr_init` family is the archetype).
- B3 non-idempotent `fini` (frees a child unconditionally, or does not reset it → unsafe on partial/zero state or on a second call).
- B4 non-NULL-safe `free`.
- B5 orthogonality break (`new` that also inits/allocates children; `free` that calls `fini`).

Structural defects outrank renames: a leak/double-free/UAF (B1/B2) is a real bug; A-class and B5 are hygiene.

## Non-negotiables

- **Symbol renames and error-path restructuring only — never layout changes.** This sweep renames lifecycle functions and rewrites their internal cleanup; it NEVER alters shared-memory struct layout, field order, or sizes. If a fix seems to require a layout change, defer the candidate and flag it to the user.
- **A rename ships with ALL its consumers in the same PR**: every C caller, Go bindings (`bindings/go/*/cgo.go` or legacy `controlplane/ffi.go`), tests, fuzzing targets, comments. Zero references to the old name may remain repo-wide.
- **Verdicts are read-code verdicts.** Never classify from a scan table alone — read the function bodies before deciding rename vs. real bug vs. false positive. The scanners point; reading decides.
- **One prefix family = one PR; one B-class defect = one PR** — each in its own worktree. Closely coupled clones (e.g. `btree_u16/u32/u64`) count as one family.
- **Budget: 2–3 PRs per run.** Structural defects (B1/B2/B3) outrank pure renames and B5 orthogonality when picking.
- **Never touch**: `new_module_<name>` defsym entry points, DPDK `rte_*` APIs, vendored code under `subprojects/`.

## Pipeline

### Phase 1 — Scan & ledger sync

1. `git fetch origin main`; work off `origin/main`.
2. Run both scanners (read-only):
   - `bash .claude/skills/raii-sweep/references/scan.sh` — naming axis (A): inventories lifecycle definitions across `lib/ dataplane/ common/ api/ modules/ devices/` and pairs them by prefix, flagging asymmetries.
   - `bash .claude/skills/raii-sweep/references/structural-scan.sh` — structural axis. Two precision tiers: LIKELY-DEFECT (`D-ZEROINIT` constructor with no zero-init before its error path, `D-OOM` unchecked alloc, `D-IDEMP` fini that bfrees a member without reset) and DECISION/STYLE INVENTORY (`S-STYLE` multi-label inits, `S-NULLSAFE` frees that deref self unguarded, `S-ORTHO-NEW` fused constructors, `S-ORTHO-FREE` composite teardowns). The inventory tier is mostly conforming/decision-gated — read it for the orthogonality decision, not as a bug oracle.
3. Sync the ledgers (both gitignored; create on first run):
   - `.arch/raii-sweep/LEDGER.md` — naming-axis verdicts (A). Add newly flagged prefixes as `pending`; mark vanished asymmetries `resolved-externally`; preserve `merged`/`deferred`/`fp-*` verdicts — never re-triage an `fp-*` prefix unless its code changed.
   - `.arch/raii-sweep/LEAKS.md` — structural-axis backlog (B), one entry per confirmed defect (each fixed as its own PR). Mark fixed entries; rebase deferred entries when their blocking PR lands.

Naming ledger format — one table: `| prefix | area | class | priority | status | notes |`. `status` ∈ `pending` / `in-progress` / `merged #PR` / `deferred` / `fp-no-fini-needed` / `fp-other` / `resolved-externally`.

### Phase 2a — Triage the naming axis (semantic, bounded)

Take `pending` A-entries in priority order and read the actual code until you have confirmed candidates. For each record: violation class, exact rename map (old → new symbol list), full consumer list (`grep -rn` across C, Go, tests, fuzzing, comments), and blast-radius notes (FFI-crossing? hot-path header?). Skip (and mark) candidates whose files overlap an open PR (`gh pr list --json files`).

### Phase 2b — Hunt the structural axis (read-the-code, workflow)

Grep cannot prove a B1/B2/B3 defect — it can only point. Run the **leak-hunt workflow** to find them: fan out area finders (one per subsystem: `lib/filter/compiler`, `lib/controlplane`, `lib/counters`, `lib/dataplane`, each module's `api/`, etc.), each READING the init/fini/free functions in its area against the convention contract, then adversarially verify every candidate (independent skeptic prompted to refute, default-to-false on doubt) before it enters `LEAKS.md`. Drive each finder with the explicit contract: init must establish fini's zero-init precondition and unwind cleanly on every error path; fini must be idempotent; free must be NULL-safe; `*data`/offset children must never dangle past their free. The archetype is the `*_attr_init`/driver contract in `lib/filter/compiler/` (see `references/triage.md` § B2). Record each confirmed defect in `LEAKS.md` with file:line, the exact mechanism (leak / double-free / UAF / OOM-deref), severity, the fix shape, and blockers.

### Phase 3 — Execute (delegate, one worktree per candidate)

For each candidate:

1. `git worktree add .claude/worktrees/raii-<prefix> -b refactor/raii-<prefix> origin/main` (use `fix/raii-<slug>` for B-class defect fixes).
2. If the candidate touches Go bindings, seed gitignored generated files first: `rsync` the `*.pb.go` files from the main checkout into the worktree (a fresh worktree cannot run `go build ./...` without them).
3. Delegate to **coder-c** with a brief that names: the absolute worktree path (cd there, `git rev-parse --show-toplevel` to confirm), the exact rename map / fix shape, the full consumer list from Phase 2, and the relevant guardrails (see `references/triage.md` § Brief template). The brief MUST forbid destructive git ops (`stash`/`checkout`/`restore`/`reset`/index ops) and `git commit`, and restate coder-c's minimal-fix discipline: only the named symbols / the named defect, no adjacent restructuring, no gratuitous doc comments.
4. If renamed symbols cross the CGO boundary, delegate the Go side to **coder-go** afterwards (same worktree, same constraints; receiver naming `m`, no comment drift).
5. Sequence strictly: coder-c → your build check → coder-go (if needed) → your build check. Never run a coder and its reviewer in the same batch.

### Phase 4 — Verify (you run the gates)

From the worktree root:

1. For renames: `grep -rn '<old_symbol>' .` for EVERY renamed symbol — zero hits allowed (code, comments, meson, Go, fuzzing).
2. C gate: `meson setup build` (or reuse) + `meson compile -C build` + `meson test -C build`.
3. Go gate when bindings changed: `go build ./...` in `controlplane/` and the touched module.
4. Local ASan (`make test-asan` or targeted `meson test -C build-asan <name>`) is **mandatory** for any B-class fix and for any change that ALTERS BEHAVIOR (destructor added, ownership/free semantics changed, call order changed, error path rewritten). Pure renames (most A1/A2) skip it: repo CI runs build-asan-test, build-tsan-test, and functional tests on every PR anyway. For a hard-to-see B1/B2 leak, a `bug-hunter confirm` (ASan/fault-injection repro) before the fix and a `bug-hunter validate` after are encouraged.
5. Reviewer pass: launch **reviewer** WITHOUT isolation, read-only, pointed at the worktree's absolute paths (work is uncommitted). Address findings via the Review-Fix loop (max 3 rounds).

### Phase 5 — Publish & close the loop

1. Stage only the candidate's files; verify each with `git diff --cached -- <file>` and confirm no untracked stragglers.
2. Commit: renames `refactor(<scope>): <desc>`; B-class fixes `fix(<scope>): <desc>` (scope = `lib`, `common`, module name…). No Claude footers.
3. Push, open the PR with a scoped title; body is capitalized period-ended bullets, no `## Summary`, no `Test plan`. Add `Closes #<n>.` when applicable.
4. `gh pr checks <pr> --watch`; if it exits with "no checks reported" you ran it before CI registered — re-run it once after a minute. Read PR reviews AND Codex inline comments; fix or reply to every finding. Honor the user's merge policy (e.g. "leave open for Codex + team" vs. "merge after green").
5. Merge (single-commit → squash `--admin`; multi-commit → rebase) WITHOUT `--delete-branch` — it fails while the branch is checked out in a worktree. Then `git worktree remove` from the main checkout, `git branch -D`, and `git push origin --delete <branch>`.
6. **Merge one PR at a time** — these PRs rename callers across wide files; rebase remaining branches onto `origin/main` after each merge.
7. **Stacked families** (candidate B's files depend on candidate A's pending change): branch B off A's branch, work in parallel, and after A squash-merges rebase with `git rebase --onto origin/main <A-branch-sha> refactor/raii-<B>`.
8. Update the ledger: `merged #PR`, date. Append a one-line run summary (date, PRs, verdicts changed) to the ledger's `## Run log`.

### End of run

Report to the user: PRs shipped, new verdicts (especially `deferred` ones needing a human call), remaining queue depth on each axis, and an ETA at the current budget.
