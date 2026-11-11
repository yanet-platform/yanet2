# Planning modes

The plan is GitHub. These modes use the issue rules in `SKILL.md` for every
issue unit; they never write repository files, run git writes, builds or tests.
Read code directly or through a read-only exploration subagent.

## North star

Rank packet-path safety first (crashes, memory/lock-free ordering, shared-memory
and CGO contracts), then firewall-path production readiness (`acl`, `fwstate`,
`route`, `forward`, observability, CLI), then platform quality. Within a tier,
prefer low effort and unblocking value.

## Modes

- `decompose <goal>` — read the relevant code, design and deduplicate the whole
  tree before writes, then create its epic through `container` and 3–10 leaf
  sub-issues through `create`. Create/link leaves one at a time; on failure stop
  and report the resumable partial tree.
- `next` / `prioritize` — recommend at most three open, unassigned items from the
  boards, ranked with the reason and repository; no writes.
- `debt <window> [kind] [repo]` — list matching open items created in the window,
  grouped by board with a one-line reason; no writes.
- `ingest <items>` — pass each independent finding through `create`, or report
  its admission stop.
- `start <n>` — resolve the repository, stopping when both repositories hold
  `<n>` and none was named, then run
  `.agents/scripts/issue-start.sh <owner>/<repo> <n>`.
- `close <n>` — close completed with a comment naming the PR, or not_planned
  with the reason.
- `scan <scope>` — bounded evidence-backed code/GitHub discovery producing
  candidates through `draft`; create only when the user separately said
  file/create.

Do not reassign, close or alter lifecycle for someone else's issue unless the
request explicitly authorizes it. An epic and its children stay in one
repository. Never file issues for agent-setup or AI-tooling work.

## Report

In at most 30 lines: changed issue numbers and boards, recommendations with
reasons, partial trees/stops, and anything not filable.
