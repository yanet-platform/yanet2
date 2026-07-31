---
name: review-change
description: Independently review a complete candidate change for behavioral defects, lost contracts, unsafe assumptions, and missing integration work before commit, PR, or merge. Use for code, tests, documentation, agent instructions, skills, CI, build files, packaging, and post-fix re-reviews; also use when comparing a local review with GitHub Codex findings.
---

# Review Change

Treat the change as an untrusted state transition. Prove that the complete candidate preserves old obligations, satisfies the new brief, and behaves correctly outside the happy path. Builds and tests support that proof; they do not replace it.

## Establish the review contract

Obtain or discover all of the following before judging the change:

- The task brief and acceptance criteria.
- The exact base revision and candidate state.
- The candidate worktree or PR number.
- Every intended tracked, staged, unstaged, untracked, and ignored file.

For a local change, build the manifest from the base diff, index diff, worktree diff, and untracked files. Inspect ignored paths explicitly when the task owns them. For a PR, use GitHub MCP for PR metadata, changed files, and the diff.

At the start, record the approval fingerprint: exact base revision plus both manifests and every entry's mode/type/content identity, including an absent marker for deletions. For a local worktree, hash the actual reviewed content, including untracked and explicitly owned ignored files; status shape alone cannot detect a concurrent edit to an already-modified file. For a PR, also record the head SHA as a concurrency guard.

Classify every entry as `publish` or `local-only`. The review manifest contains both classes so local configuration and ignored task artifacts are not hidden from review; the publish manifest is the exact subset authorized for transfer, staging, commit, and PR. State both manifests and any exclusions before reviewing.

Immediately before the verdict, recompute the approval fingerprint and the PR head guard when applicable. If any changed, discard the pass and restart on the new candidate. Never return `APPROVED` when the base is uncertain, an intended file was not inspected, or the base or candidate changed after the review began. A later review round covers the whole current candidate again, not only the files changed by the fix.

Staging, committing, amending, transferring, or pushing is a packaging transition, not a content approval. It may carry approval forward only after proving that the recorded base and publish fingerprint are identical on the packaged side and every local-only entry remains identical in its source location. A new commit SHA alone does not stale an equivalent approval; any fingerprint difference does.

## Reconstruct the contracts

Read the relevant repository instructions and the pre-change versions of modified files. Derive obligations from three sources:

1. The user's brief.
2. Behavior and guarantees that existed at the base revision.
3. Consumers and external interfaces affected by the change.

For a rewrite, deletion, move, or canonical-file consolidation, create a deletion ledger: map every removed requirement or behavior to its new location or justify its removal. Similar wording and token-level equality are not proof of semantic preservation.

When one canonical file replaces client-specific sources, also build a consumer matrix from the base: consumer, distinctive client name/path/environment variable/tool feature, old obligation, and new location. Search every source for those distinctive tokens and account for each occurrence separately; a shared rule can preserve one client's contract while silently dropping another's.

When the change relies on a current external contract, check the authoritative source. Examples include Codex instruction-loading limits, GitHub review APIs, tool discovery, compiler behavior, and packaging rules. Prefer MCP for the service it represents and official documentation for product behavior.

## Perform a clean-room semantic pass

Do the first pass without reading existing review findings. Existing comments are useful after an independent pass, but reading them first anchors the review and turns it into fix confirmation.

For each changed behavior:

1. Enumerate the accepted input and state domain, including empty, zero, minimum, maximum, malformed, partial, concurrent, and platform-dependent cases relevant to the surface.
2. Trace representative boundaries through the changed path to a concrete outcome.
3. Check both ends of every cross-file, cross-language, and external assumption.
4. Look for a legal input or state that the change now mishandles or rejects.
5. Verify every new failure or early-return path leaves outputs, resources, and persistent state in the promised condition.

Report a semantic finding only with a trigger, the path through the change, and the wrong outcome. Record unresolved high-risk input classes instead of silently approving them.

## Execute non-code changes mentally and with probes

Treat instructions, skills, CI, build logic, ignore rules, and packaging as executable programs. Walk their state machines instead of proofreading prose.

For these surfaces, check the relevant classes:

- Authorization: create-only, merge-authorized, cancellation, retry, and failure states cannot cross privilege boundaries.
- Cold start: a fresh clone or worktree has every referenced recipe, generated input, tool, and directory it needs.
- Compatibility: all consumers retain their client-specific rules, casing, paths, imports, limits, and discovery conventions.
- Branching: current, stale, rewritten, stacked, merged, squash-merged, and concurrently advanced refs take the correct path.
- Workspace safety: tracked, untracked, ignored, staged, and concurrent changes are neither lost nor accidentally included.
- Command truth: commands select the intended scope, exit as described, and do not silently include unavailable integration tests or omit required suites.
- Failure handling: every fallible step stops safely, remains retryable, and cannot act on stale observations.

Use small read-only or isolated probes for material claims. Examples include checking byte and instruction budgets, testing ignore behavior and case sensitivity, listing selected packages or tests, parsing configuration, and dry-running ref operations. If a claim was not probed, label it as analysis.

## Review tests and verification evidence

Review tests and benchmarks as production logic: prove that their data reaches the claimed path, assertions distinguish broken behavior, and sampling covers the stated domain. Then run the smallest relevant builds, tests, linters, and formatters from `AGENTS.md`.

Keep verification failures separate from code-review findings. A green command does not cancel a semantic defect, and a command that was not run is `NOT RUN`, not implicitly passing.

## Reconcile external findings

After the clean-room pass, read existing PR reviews and inline threads through GitHub MCP when available. Reproduce each finding against the reviewed candidate. Add missed valid findings, reject false positives with evidence, and note which independently discovered issues overlap.

## Decide the verdict

Return `CHANGES REQUESTED` for any concrete correctness, safety, authorization, data-loss, compatibility, or required-integration defect. Suggestions remain non-blocking and must not obscure the verdict.

Return `APPROVED` only when:

- The complete candidate manifest was reviewed.
- Every entry is explicitly classified as publish or local-only.
- The terminal fingerprint check proves the base and candidate did not change during review.
- Removed obligations were accounted for.
- High-risk paths and boundaries were traced or explicitly resolved.
- Required verification passed.
- No blocking finding remains.

Use this compact report shape:

```markdown
## Review target
- Base: <revision>
- Candidate: <revision/worktree>
- Review manifest: <all publish and local-only entries>
- Publish manifest: <exact files authorized for publication>
- Exclusions: <none or justified list>
- Approval fingerprint: <start and terminal base/manifests/modes/content match; PR head guard when applicable>

## Findings
1. [P1/P2/P3] `path:line` — <defect>; trigger: <state/input> -> <wrong outcome>.

## Coverage
- <surface>: <contracts traced and probes run>.
- Unresolved: <none or explicit input classes>.

## Verification
- <command>: PASS/FAIL/NOT RUN.

## Verdict: APPROVED / CHANGES REQUESTED
```

Lead with findings. Omit an empty Findings section only when the verdict is `APPROVED`; never replace coverage evidence with a generic “no issues found.”
