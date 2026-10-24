---
name: review-change
description: >-
  Independently review a complete candidate change for behavioural defects,
  lost contracts, unsafe assumptions and missing integration work before
  commit, PR or merge; also for re-reviews and for comparing with Codex findings.
context: fork
agent: general-purpose
---

# Review Change

Treat the change as an untrusted state transition: prove the complete candidate keeps the old obligations, meets the brief, and behaves outside the happy path. Builds and tests support that proof; they do not replace it.

Run in a fresh context that has not seen the implementation: Claude Code forks this skill into a subagent; in Codex, spawn a subagent with this skill and the contract below instead of reviewing in the author's thread. The reviewer reads the change from disk and never trusts the author's narration.

## Contract first

The caller names: the brief and acceptance criteria · the candidate root (worktree or PR head SHA) and the exact base · the full manifest, including untracked and gitignored paths · what the author claims was verified. `cd` to the candidate root and confirm `git rev-parse --show-toplevel` and `git branch --show-current`. Review `git diff <base>` plus every named untracked or gitignored path. Narration that disagrees with the diff is a finding.

## Process

1. Reconstruct the contract: what the change claims, and which callers, tests, docs, packaging and registration surfaces it touches (`grep` the class, not the sample).
2. Read the whole diff. For each hunk ask: what input or state makes this wrong, what did the old code guarantee that this drops, what else must change for this to be complete.
3. Invoke `$better-comment` in Review mode for the complete candidate. It inspects changed comments and nearby comments whose claims the changed behaviour may invalidate. Require `Result: APPROVED`; propagate `CHANGES REQUESTED` findings and do not approve the outer review.
4. Run the gate once from the candidate root: `make gate` (the CI checks for the touched layers; it prints only failures). A gate that produces or consumes `build/` needs the candidate's own real `build/`.
5. Check tests: each new test must fail on the defect it pins. Reason it through, or mutate without touching the tree: `go test -overlay f.json` with `{"Replace":{"<abs src>":"<abs mutated copy>"}}` compiles the copy in place of the source for one run. A green suite is not evidence when the assertion is vacuous.
6. Rank findings, most severe first.

## Look for

- **Lost obligations**: a guarantee, error path, counter, log line, registration or packaging entry the old code had and the new one drops; a caller left on the old contract (grep the class).
- **Predicate defects**: absent vs empty vs unknown; `""` matching everything; boundary off by one; snapshot standing in for live state; a cap loop with no rule for the capped exit.
- **Unsafe assumptions**: FFI pointer lifetime and pinning; shm publish/consume ordering; lock held across the backend call and cache update; a `defer` or error handled by nobody.
- **Missing integration**: three-place registrations (CLI, page, module), generated code (`*.pb.go`), meson/Cargo/npm manifests, docs that now lie.
- **Tests that cannot fail**: derived oracles, unset modes, assertions on the setup rather than the behaviour; a benchmark not exercising the claimed path.
- **Non-code changes**: execute instructions and configs mentally against the cases they name and the default they leave unnamed; a rule's restatements elsewhere go stale.
- **CLI surface**: a diff under `cli/`, `modules|devices|objects|operators/*/cli/` or `common/rust` is checked against the `code-cli` skill — binary shape, typed arguments, verb and flag dictionary, output, errors and test policy.

## Rules

- Never edit, format or "fix" the candidate; write only the report. No git writes, `gh` writes, package installs, or the built CLI against a live control plane.
- GitHub reads (`gh pr view/diff/checks`, `gh api` GET) are fine; a PR review is of its head SHA — name it.
- Prefer reasoning from the code over building an experiment; build one only when the code cannot settle the question, and say so.
- Blocking means: wrong behaviour, lost guarantee, unsafe FFI/shm/lock-free ordering, missing integration (registration surface, packaging, generated code), a test that cannot fail. Everything else is a note. Do not report style the linters already enforce, restate conventions, or dress remedies as findings.

## Verdict and report

At most ten findings, each `file:line` + defect + concrete failure scenario + blocking/note. `APPROVED` — no blocking finding; `CHANGES REQUESTED` — the blocking list. Round 2 re-reads only the closed findings and the changed regions with their collaborators; there is no round 3 — report that the task shape is wrong.

Report in at most 40 lines: verdict · head SHA / base · gate commands and results · findings (ranked) · notes.
