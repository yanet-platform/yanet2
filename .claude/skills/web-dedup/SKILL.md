---
name: web-dedup
description: >-
  De-duplicate the YANET2 web/ frontend (TypeScript/React/SCSS) by finding
  copy-pasted code across pages and extracting it into shared components, hooks,
  SCSS, and utils — then proving the refactor is visually identical and shipping
  one PR per extraction. Use this skill WHENEVER the user asks to "find the
  common parts", "extract shared components", "pull out the duplicated code",
  "DRY up the pages", "this got copy-pasted between pages", "decompose the web
  code", or runs a cleanup pass after a large web feature push — even if they
  never say the word "dedup". It is the right tool any time the goal is reducing
  duplication in web/src.
---

# web-dedup

Run the team's proven de-duplication pipeline over `web/`: detect copy-paste,
triage it conservatively, delegate the extraction to **coder-ui**, prove the
result is visually identical, and ship one PR per logical extraction.

You (the architect) drive this. You never edit web code yourself — extraction is
delegated to `coder-ui`; you own detection, triage, the zero-diff proof gate, and
the publish workflow.

## Non-negotiables

- **Mandatory zero-diff gate.** Every extraction must pass the appropriate
  zero-visual-diff proof in `references/verification.md`. If a candidate cannot be
  proven zero-diff, **defer it** — never merge an unproven "should be identical"
  refactor.
- **Audit the full component, not the happy path.** Over-classifying near-identical
  copies as "trivially shared" is the #1 failure mode. Inspect every branch and
  variant before calling a family mergeable.
- **One logical extraction = one PR**, each in its own worktree.
- **No `git commit` unless the user asks** in the current turn; same instruction to
  every delegated agent.

## Pipeline

### Phase 1 — Baseline & scan scope (auto)

1. `git fetch origin main`. Treat `origin/main` HEAD as the baseline for both
   detection and the zero-diff proofs.
2. Determine the hunt surface automatically: the web files touched by the recent big
   work. Use `git diff --name-only origin/main...HEAD -- web/` plus the working-tree
   diff, and `git log --oneline origin/main..HEAD -- web/` for context. That changed
   set is where fresh copy-paste lives. Let the user narrow it by naming pages.
3. **Re-verify each duplication still exists against the freshly-fetched
   `origin/main`.** Concurrent dedup PRs land mid-session and can supersede a
   candidate (a planned extraction has been made redundant by an already-merged PR
   before). Drop candidates that no longer duplicate anything.

### Phase 2 — Detect & triage

Hunt all four categories across the scoped files:

- **React components / JSX** — repeated drawer/modal/card/table-chrome markup.
- **Hooks / logic** — duplicated stateful logic, reducers, effect plumbing.
- **SCSS / styles** — repeated style blocks.
- **Utils / formatters / types** — duplicated formatters, parsers, TS types.

For each candidate, produce:

- the duplicated locations (file + symbol),
- the proposed extraction target (see `references/extraction-targets.md`),
- a **divergence audit**: walk *every* branch/variant of the full component, not
  just the default render. Families that look like identical copy-paste routinely
  diverge in an edge branch (sparkline empty-state markup, an icon path, a chip's
  base padding). If a divergence can't be collapsed to zero-diff (e.g. by
  parameterizing the one differing value), **defer** the candidate and state why.

Rank surviving candidates by impact and safety, then present the list to the user
and confirm which to extract.

### Phase 3 — Extract (delegate to coder-ui, parallel worktrees)

For each approved extraction:

1. Create a worktree off confirmed `origin/main`:
   `git worktree add .claude/worktrees/<name> -b refactor/web-<what> origin/main`.
2. Delegate to **coder-ui** with `isolation` NOT used — point it at the absolute
   worktree path (`.claude/worktrees/<name>/web`) and tell it to `cd` there first and
   `git rev-parse --show-toplevel` to confirm before editing (relative `web/src/...`
   paths otherwise resolve against the main checkout). The brief's FIRST step:
   `ln -s <main-checkout>/web/node_modules node_modules` from the worktree `web/`
   dir — a fresh worktree has no `node_modules`, and without the symlink the coder's
   tsc/build/vitest "verification" silently runs against the MAIN checkout (it has
   happened). Never stage the symlink.
3. The brief must name: the reference page/component to mirror, the exact target
   path, the barrel-export update (`web/src/components/index.ts`,
   `web/src/hooks/index.ts`, or `web/src/styles/chrome.scss`), and the relevant
   guardrails from `references/extraction-targets.md`.
4. The brief must forbid destructive git ops (`git stash`/`checkout`/`restore`/
   `reset`/index ops), forbid `git commit` unless asked, and ban staging
   Playwright/pixelmatch/pngjs into `package.json`/`package-lock.json` (they are
   pre-cached — see verification reference).

### Phase 4 — Prove zero-visual-diff (you run the proof)

Pick the strongest proof for the change type from `references/verification.md`:

- presentation-only / SCSS-mixin → **CSS-hash-set**
- type-only / pure-logic-no-render → **dist JS+CSS hash**
- logic feeding rendered values (formatters) → **exhaustive Node sweep**
- visual / JSX → **Playwright pixelmatch** (you Read the PNGs)

The gate is mandatory. No clean proof → defer the candidate; do not proceed to PR.

### Phase 5 — Publish (one PR per extraction)

1. Stage only this extraction's files. Verify each with
   `git diff --cached -- <file>`, and confirm every new untracked file the change
   created (barrel-referenced `?? ` lines in `git status -s`) is included — a missing
   file ships a broken module graph that the vitest-only web CI won't catch.
2. Run a **`reviewer`** pass over the worktree. Because the work is uncommitted,
   run reviewer WITHOUT isolation, pointed at the worktree's absolute paths, and brief
   it read-only on the changed files. Never launch reviewer in the same batch as the
   coder it reviews. Address findings via the Review-Fix loop before merge.
3. Commit the staged extraction (the user's publish request is the explicit ask
   the no-commit rule requires) with a `refactor(web): <short description>` subject,
   then push the branch with `git push -u origin <branch>`.
4. Open the PR with a scoped title `refactor(web): <short description>`; body is
   plain capitalized period-ended bullets (no `## Summary`, no `Test plan`); add
   `Closes #<n>.` when applicable.
5. Wait for CI green. Read BOTH PR-level reviews and Codex inline comments
   (`gh pr view <pr> --json reviews` and
   `gh api repos/yanet-platform/yanet2/pulls/<pr>/comments`); fix or reply to every
   finding before merging.
6. Merge (single-commit → `--squash`, multi-commit → `--rebase`), update local main,
   delete the branch, and `git worktree remove` from the main checkout (never with
   cwd inside the worktree).

Across parallel extractions: rebase each branch onto `origin/main` (never
`git merge origin/main`) as siblings merge.

## References

- `references/verification.md` — the four zero-diff proof recipes, copy-pasteable.
- `references/extraction-targets.md` — where each kind of extracted code goes in
  `web/src`, plus coder-ui guardrails to pre-empt known drift.
