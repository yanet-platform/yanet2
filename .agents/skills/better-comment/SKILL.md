---
name: better-comment
description: >-
  Improve or audit source comments in a bounded code change without touching
  executable code or unrelated files. Use when adding or editing comments,
  reviewing noisy, stale, overlong, or AI-generated comments, or before a
  complete-change review.
---

# Better Comment

Read `.agents/conventions/comments.md` before acting. It is the only source of
truth for comment policy; do not copy its rules into this skill.

## Select a mode

- Author mode is for an explicit fix, rewrite, cleanup, or authoring request.
  It may edit comment text only within the supplied candidate manifest.
- Review mode is for an explicit review, audit, or reviewer-gate request. It
  never edits files and reports findings only.
- If the request does not identify a mode, use Review mode. Never infer write
  access from a general request to inspect a change.

## Establish the candidate

Use the caller's explicit file manifest when one is supplied. Otherwise use the
requested diff boundary (`base...candidate` or the staged/unstaged working
tree), and include untracked or ignored paths named by the caller. If neither a
manifest nor a diff boundary is available, stop and request one.

Inspect comments that were added or edited, plus nearby existing comments whose
claims may have been invalidated by changed executable behavior. Do not expand
the candidate to unrelated comments or files.

## Pass

Classify every candidate comment as `preserve`, `rewrite`, `delete`, or
`blocker`.

Apply the loaded convention when choosing among these classifications. If a
claim cannot be verified, report a blocker instead of deleting it.

Preserve compiler, build, generator, formatter, linter, and generated-file
directives exactly, including unknown directive-like comments. If it is unclear
whether a comment is a directive, classify it as a blocker. Do not rename
symbols or change executable code when a comment exposes a design problem.

Re-read the candidate after the pass. Confirm that only comment text changed,
the complete manifest was considered, and every candidate comment has a
classification.

## Output

Author mode:

```text
Mode: Author
Classifications: <file:line=preserve|rewrite|delete|blocker; ... or none>
Changed: <file:line or none>
Deleted: <file:line or none>
Blockers: <finding or none>
Result: COMPLETE
```

Review mode:

```text
Mode: Review
Classifications: <file:line=preserve|rewrite|delete|blocker; ... or none>
Findings:
- <severity> <file:line> <finding and concrete consequence>
Result: APPROVED
```

Use `Result: APPROVED` only when there are no findings. In Review mode, use
`Result: CHANGES REQUESTED` when any finding remains. In Author mode, use
`Result: BLOCKED` when any blocker remains; otherwise use `COMPLETE`.
