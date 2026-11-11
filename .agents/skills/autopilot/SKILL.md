---
name: autopilot
description: >-
  Drive one GitHub issue autonomously from admission to merged main: admit,
  worktree, implement the change yourself, verify through a review-change
  APPROVED pass, publish with ship-pr, close the issue. Use when the
  user hands over an issue end to end ("/autopilot 1234", "реши issue 1234",
  "возьми 1234", "доведи 1234 до влития").
---

# autopilot

Take issue `<n>` (plus optional instructions) to merged `main` without asking for decisions along the way, and stop cleanly with one question when a decision is not yours. `ship-pr` is the publish runbook — invoke it, never restate it. `issue-create` planning modes own issue lifecycle writes (`close`, `ingest`); you own the engineering and write the code yourself — one writer per change.

## Non-negotiables

- One issue, one PR; an epic yields exactly one child per run; prerequisites ship first in their own PR.
- Autonomy lifts no gate: `make gate` green, review-change APPROVED on the complete candidate, green CI, every review/Codex finding addressed, `needs-architect` block, all as usual.
- Two review rounds at most; a defect class surviving both means the task shape is wrong — stop and re-decompose.
- Every assumption you took is a bullet in the PR body, in words a reader who never saw the issue understands.
- Subagents only explore, reproduce, measure or review; their claims are claims to check against the code.
- Parallel writers only for independent PRs of an epic, each in its own worktree.

## Hard stops (report the stop and the one thing that would clear it)

Ambiguous number (both repos hold it, no repo named) · issue closed or is a PR · `needs-architect` label · an issue assigned to someone other than `@me` (unassigned issues and issues assigned to `@me` may proceed) · `blocked` label or an open declared dependency · the deliverable is a decision, not a change (give the recommendation instead) · an external contract left to invent (proto fields, CLI surface, shm layout/ABI, on-wire format) · a REFUTED bug report (close as not_planned via `issue-create close`, do not invent a fix).

## Pipeline

0. **Admit.** Resolve `<n>` (`gh issue view <n> --repo yanet-platform/yanet2` then `yanet2-private`); check the hard stops and the assignee, allowing no assignee or `@me` and stopping for any other assignee; check open PRs (`gh pr list --search <n>`) so you do not compete with one; `.agents/scripts/issue-start.sh <owner>/<repo> <n>`. Never revert or overwrite work you did not create without asking.
1. **Understand.** Read the code the issue touches; an Explore subagent takes wide searches. A reported defect goes to `hunt-bug confirm` first; a speedup deliverable goes to `measure-perf` for a baseline first; a protocol question goes to `networking`. Name every shared surface and its consumers (grep generated and gitignored trees too).
2. **Worktree.** `AGENTS.md` → Worktrees; seed what the gates need (its own `build/` and submodules for C or cgo, `node_modules` for web).
3. **Implement.** Write the change across its layers in order — C API, proto + Go, Rust CLI, web UI — following the language conventions; run `$better-comment` in Author mode, then `make gate` until green.
4. **Verify.** `review-change` on the complete candidate (fresh context); fix findings minimally; re-review the changed regions; at most two rounds.
5. **Publish.** `ship-pr` with merge authorized ("влей в main") unless the user said otherwise. Body: what and why, assumptions, `Closes #<n>.`.
6. **Close & sweep.** After MERGED: `issue-create close <n>` (names the PR); leftovers the change exposed → `issue-create ingest`, never for agent-setup work; remove the worktree.
7. **Report.** Issue, PR, state, stops fired, assumptions, follow-ups filed. Under 30 lines.
