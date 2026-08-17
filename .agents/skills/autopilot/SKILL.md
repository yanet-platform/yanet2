---
name: autopilot
description: >-
  Drive one GitHub issue autonomously from unassigned to merged main: admit,
  decompose, worktree, delegate every change to specialists, verify through a
  reviewer APPROVED pass, publish with ship-pr, close the issue. Use when the
  user hands over an issue end to end ("/autopilot 1234", "реши issue 1234",
  "возьми 1234", "доведи 1234 до влития").
---

# autopilot

Take issue `<n>` (plus optional instructions) to merged `main` without asking for decisions along the way, and stop cleanly with one question when a decision is not yours. `ship-pr` is the publish runbook — invoke it, never restate it. `planner` owns issue lifecycle writes (`start`, `close`, `ingest`); you own the engineering and write no code yourself.

## Non-negotiables

- One issue, one PR; an epic yields exactly one child per run; prerequisites ship first in their own PR.
- Autonomy lifts no gate: reviewer APPROVED on the complete candidate, green CI, every review/Codex finding addressed, `needs-architect` block, all as usual.
- Two reviewer rounds at most; a defect class surviving both means the task shape is wrong — stop and re-decompose.
- Every assumption you took is a bullet in the PR body, in words a reader who never saw the issue understands.
- Brief only what you read in the code; a specialist's claim is a claim to check.
- You run no builds, tests or benchmarks in your own thread — a specialist runs them and returns a ≤ 20-line result; read code and diffs yourself.

## Hard stops (report the stop and the one thing that would clear it)

Ambiguous number (both repos hold it, no repo named) · issue closed or is a PR · `needs-architect` label · any assignee already set (`@me` = an earlier run took it; anyone else = externally owned) · `blocked` label or an open declared dependency · the deliverable is a decision, not a change (give the recommendation instead) · an external contract left to invent (proto fields, CLI surface, shm layout/ABI, on-wire format) · a REFUTED bug report (close as not_planned via planner, do not invent a fix).

## Pipeline

0. **Admit.** Resolve `<n>` (`gh issue view <n> --repo yanet-platform/yanet2` then `yanet2-private`); check the hard stops; `planner start <n>`.
1. **Understand.** Read the code the issue touches; send `fast-explorer` for bounded facts. A reported defect goes to `bug-hunter confirm` first; a speedup deliverable goes to `performance-engineer` for a baseline first. Decompose into file-scoped briefs in layer order — C API, proto + Go, Rust CLI, web UI — naming every shared surface and its consumers (defining grep, generated and gitignored trees included).
2. **Worktree.** `AGENTS.md` → Worktrees; seed what the gates need (its own `build/` if a gate produces or consumes it, `node_modules` for web).
3. **Delegate.** One brief = one coherent change; each names the worktree's absolute root, the property to reach, the files in scope, the gate, and where to stop and report. Sequential when briefs share files, parallel otherwise.
4. **Verify.** `reviewer` on the complete candidate; route findings back as minimal fixes; re-review the changed regions; at most two rounds.
5. **Publish.** `ship-pr` with merge authorized ("влей в main") unless the user said otherwise. Body: what and why, assumptions, `Closes #<n>.`.
6. **Close & sweep.** After MERGED: `planner close <n>` (names the PR); leftovers the change exposed → `planner ingest`, never for agent-setup work; remove the worktree.
7. **Report.** Issue, PR, state, stops fired, assumptions, follow-ups filed. Under 30 lines.
