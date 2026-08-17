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

Immediately before the verdict, recompute the approval fingerprint and the PR head guard when applicable. If any changed, discard the pass and restart on the new candidate. Never return `APPROVED` when the base is uncertain, an intended file was not inspected, or the base or candidate changed after the review began.

An edit made after an approval — a batched `Minor` fix, typically — re-derives the regions it changed and carries the rest, instead of restarting the whole candidate. That carry is subject to the interaction rule above: an unchanged region whose collaborator the edit changed is re-examined too, and is not carried. The fingerprint is not narrowed by that: it stays per entry and must still be recomputed and seen to differ, because it is what stops an approval riding onto content nobody reviewed. What narrows is the re-derivation a difference costs, never the duty to notice one.

A later round still covers every entry of the current candidate. A region of an entry whose content is identical to what an earlier round of this same review examined needs no fresh derivation: that round's result carries forward. A region that changed since is reviewed fresh, and a pass launched as an independent full-scope review is a new review that carries nothing forward.

Carry-forward is a licence not to re-derive, never a bar on noticing. Identity is per region, but correctness is not: an earlier clean result stops being evidence once something that region interacts with has changed. Re-examine an unchanged region whose changed collaborator is itself in the candidate — that one is required, and the carried result does not count as coverage of the interaction. What that re-examination surfaces is a defect this candidate created, so it blocks on its own merits whatever its class, exactly as a defect a rewrite introduced is reportable on any ground. Attribute such a finding to the region that changed, not to the unchanged one where the wrong behaviour appears: the trigger is the interaction, and its changed end is in scope. Noticing elsewhere on frozen surface stays permitted, and a finding first raised there is governed by the round rules below.

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

For new permanent behavioral or regression coverage of C, CGO, dataplane, or controlplane behavior, require a Go test and prefer `dataplane_ut` when it can exercise the behavior faithfully, even when the implementation fix is in C. Adding a new case to an existing C test is new coverage, not maintenance. Allow a permanent C test only when the test itself requires direct ASan or TSan instrumentation and Go cannot exercise the behavior faithfully, with that sanitizer-specific reason stated. A C fuzz target may supplement but never replace the required permanent behavioral or regression test. Unrelated fuzz-only work, diagnostic or scratch code under `.arch/bughunter/`, and Rust or TypeScript tests remain outside this rule. Bug-hunter scratch reproducers may use any language.

Keep verification failures separate from code-review findings. A green command does not cancel a semantic defect, and a command that was not run is `NOT RUN`, not implicitly passing.

## Reconcile external findings

After the clean-room pass, read existing PR reviews and inline threads through GitHub MCP when available. Reproduce each finding against the reviewed candidate. Add missed valid findings, reject false positives with evidence, and note which independently discovered issues overlap.

## Keep the finding set converging

- The first round over a candidate states that its blocking set is complete: nothing outside it blocks this change.
- Frozen surface is measured by region, not by entry, and the region is the diff hunk: a hunk unchanged since the round that last examined it stays frozen even when the entry changed elsewhere. A one-line change thaws its own hunk and not the file around it, and both reviewer and requester compute that unit from the same diff.
- From the second round on, a finding *first raised in that round* on a region unchanged since the round that last examined it may block only if it is safety class — memory safety, use-after-free, leak, data race, packet-path correctness, or anything that would corrupt, drop, or mis-forward live traffic. A class the reviewing charter grades always-Critical counts as safety class here too. This is the only channel that reopens frozen surface the candidate did not disturb: a finding the mandated collaborator re-examination surfaced blocks on its own merits, whatever its class.
- An open blocking finding stays blocking until it is closed, whatever happened to its region since. These rules bound what a round may newly raise, never what it may drop: a blocker an attempted fix missed still blocks at round 5, and a change landing in a different region neither closes it nor freezes it.
- Those rules decide blocking, and severity follows them. What they take out of this change is reported as `deferred` whatever its intrinsic gravity, and leaves the verdict where it is. A non-blocking finding keeps its own severity only while it is in scope: raised in the first round, or on a region that changed since the round that last examined it. Report a finding no rule names, and let the verdict stand.
- Report comment findings — a private-symbol or body comment, a comment over one line, an unnecessary reflow — and documentation-prose findings — wording, claim falsifiability — as one batch in the first round that sees that prose. Afterwards, do not reopen prose that did not change, and do not raise a new ground against prose re-cut to satisfy that batch. Prose that did not change on frozen surface answers to this rule rather than the round rules: it is never reopened in this change, though a defect noticed there is still recorded as `deferred`.
- A defect the rewrite itself introduced is a finding on any ground, whatever the batch named — a private-symbol or body comment as much as a false claim, a lost load-bearing rationale, an unnecessary reflow, or a line pushed past one. The prohibition above bars re-litigating prose the batch settled, never reporting a defect the fix created. A first-round batch that found nothing names no rules and restricts nothing: every prose rule stays live for prose that changes later. What bounds the loop is the rewrite cap below, not a limit on correctness.
- Once a comment has been rewritten twice at review request, propose no further rewrite of it: report the artifact as `churn` instead, for the requester to settle by dictating or deleting the text. Count rewrites per artifact, not per round.

## Decide the verdict

Return `CHANGES REQUESTED` when a blocking finding is open, and never otherwise. A finding blocks when it is a concrete correctness, safety, authorization, data-loss, compatibility, or required-integration defect that the round rules above still let block, or when the change fails its stated brief. That list names what must block, so a finding graded `Minor` or `Suggestions` is by construction not in it. A finding those rules exclude is reported as `deferred` however grave, and leaves the verdict where it is. Suggestions remain non-blocking and must not obscure the verdict.

Return `APPROVED` only when:

- Every entry of the candidate manifest was covered, freshly or by a result carried forward from an earlier round of this same review, with no carried result standing in for an interaction whose other end changed in this candidate.
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
1. [Critical/Minor/Suggestions/Deferred/Churn] `path:line` — <defect>; trigger: <state/input> -> <wrong outcome>.

## Coverage
- <surface>: <contracts traced and probes run>.
- Unresolved: <none or explicit input classes>.

## Verification
- <command>: PASS/FAIL/NOT RUN.

## Verdict: APPROVED / CHANGES REQUESTED
```

Lead with findings. Omit an empty Findings section only when the verdict is `APPROVED`; never replace coverage evidence with a generic “no issues found.”

These categories are exhaustive, and every finding carries exactly one: `Critical` while it blocks, `Minor` when it is in scope and should be fixed, `Suggestions` when it is in scope and optional, `Deferred` once the round rules take it out of this change, `Churn` when the rewrite cap tripped on an artifact.
