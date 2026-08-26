---
name: review-change
description: >-
  Independently review a complete candidate change for behavioural defects,
  lost contracts, unsafe assumptions and missing integration work before
  commit, PR or merge; also for re-reviews and for comparing with Codex findings.
---

# Review Change

Treat the change as an untrusted state transition: prove the complete candidate keeps the old obligations, meets the brief, and behaves outside the happy path. Builds and tests support that proof; they do not replace it. The `reviewer` charter holds the process (contract → whole diff → gate once → tests → ranked findings → verdict); this skill is the checklist it applies.

## Contract first

Brief and acceptance criteria · exact base and candidate (worktree or PR head SHA) · full manifest including untracked and gitignored paths · what the author claims was verified. Narration that disagrees with the diff is a finding.

## Comment obligations

After reconstructing the manifest and before the final verdict, invoke
`$better-comment` in Review mode for the complete candidate. Require
`Result: APPROVED`; propagate `CHANGES REQUESTED` findings and do not approve
the outer review. It must inspect changed comments and nearby comments whose
claims may have been invalidated by changed behavior. A comment-only finding is
still a review finding; do not switch to Author mode in this gate.

## Look for

- **Lost obligations**: a guarantee, error path, counter, log line, registration or packaging entry the old code had and the new one drops; a caller left on the old contract (grep the class).
- **Predicate defects**: absent vs empty vs unknown; `""` matching everything; boundary off by one; snapshot standing in for live state; a cap loop with no rule for the capped exit.
- **Unsafe assumptions**: FFI pointer lifetime and pinning; shm publish/consume ordering; lock held across the backend call and cache update; a `defer` or error handled by nobody.
- **Missing integration**: three-place registrations (CLI, page, module), generated code (`*.pb.go`, charters), meson/Cargo/npm manifests, docs that now lie.
- **Tests that cannot fail**: derived oracles, unset modes, assertions on the setup rather than the behaviour; a benchmark not exercising the claimed path.
- **Non-code changes**: execute instructions and configs mentally against the cases they name and the default they leave unnamed; a rule's restatements elsewhere go stale.

## Findings

At most ten, ranked, each `file:line` + defect + concrete failure scenario + blocking/note. No style the linters enforce, no restated conventions, no remedies dressed as findings. Round 2 checks only closed findings and the changed regions with their collaborators.

## Verdict

`APPROVED` — no blocking finding; `CHANGES REQUESTED` — blocking list. Name the head SHA/tree reviewed and the gate commands run.
