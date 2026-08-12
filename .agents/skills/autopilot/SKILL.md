---
name: autopilot
description: >-
  Take one GitHub issue number plus optional free-form instructions and drive it
  autonomously from unassigned to merged main: admit the issue, decompose it,
  open a dedicated worktree, delegate every file change to the specialist
  agents, verify through a reviewer APPROVED pass, publish with the ship-pr
  runbook, then close the issue and file what the change left behind. Use this
  skill WHENEVER the user hands over an issue to be solved end to end:
  "/autopilot 1234", "реши issue 1234", "возьми 1234", "сделай 1234", "закрой
  задачу 1234", "разберись с 1234 сам", "solve issue 1234", "take 1234 to main",
  "доведи 1234 до влития". Covers number resolution across the public and
  private repos, the hard stops that end an autonomous run, epic handling, the
  delegation and verification loops, and the report the run owes at the end.
---

# autopilot

Drive one GitHub issue from unassigned to merged `main` without asking for a decision along the way — and stop cleanly, with one question, when a decision is genuinely not yours.

You (the architect) drive this. **The autonomy is in the decisions, never in the typing**: every file change is still delegated to a specialist, and every gate still binds. What this skill removes is the round trip to the user between phases, not the review, not the CI, not the evidence.

**Payload**: an issue number, optionally followed by free-form instructions. The instructions rank above the issue body, which ranks above these defaults. They may narrow a run ("stop at a green PR", "не трогай dataplane", "только Go-слой"), redirect it, or add acceptance criteria. They lift a hard stop only by naming it.

**Invoking this skill is the publish authorization** that `ship-pr` requires, through to the merge. A payload that narrows the run to a PR withdraws the merge half of it.

## What this skill is not

- **Not a second publish runbook.** Phase 5 invokes `ship-pr` and obeys it. Never restate, summarize, or re-derive its branching, staging, CI, review-completion or merge rules here — two copies of that doctrine is how it goes stale.
- **Not a licence to write the fix yourself.** An autonomous run is exactly where a one-line change feels faster to type than to brief. It is not.
- **Not a planner replacement.** `planner` owns the issue's lifecycle writes (`start`, `close`, `ingest`, `decompose`); you own the engineering.

## Non-negotiables

- **One issue, one PR.** An epic yields exactly one child per run (Phase 0), and out-of-scope prerequisites get their own PR first, per `ship-pr`.
- **A hard stop ends the run** (list below). Report the issue, every stop that fired, and the one thing that would clear them — where a decision-only stop is among them, that thing is the recommendation it asks for, and it subsumes any offer to decompose. Leave every artifact inspectable: no half-staged index, no merged half of a two-part change. A stop that lands after Phase 0 leaves the issue assigned and `In Progress` with no work behind it, and no planner mode undoes that, so say so in the report and let the user decide whether to hand it back.
- **Autonomy lifts no gate.** A `reviewer` APPROVED pass, green CI, every automated-review finding addressed, and the `needs-architect` block are the same as in any other task. Nobody is watching this run, which raises the bar rather than lowering it.
- **Every assumption you took is written down** — as bullets in the PR body, in the words a reader who never saw the issue would need. An assumption that only exists in the run's reasoning did not happen.
- **Brief only verified details.** Read the code and cite it; a claim you did not check is not allowed into a brief just because the run is unattended.
- **Budget: at most three reviewer rounds.** If the same defect class survives two rounds, the task shape is wrong — stop, re-decompose, and say so, rather than opening a fourth round.

## Hard stops

Check all of these in Phase 0, and re-check the ones that can change — a PR that appeared over the zone, an assignee, a board Status, a label added since — before Phase 5. The baseline for "changed" is the Phase 0 step 3 read **plus this run's own step 6 writes**: `planner start` sets exactly that assignee and that Status, so finding them is your own footprint, not a competitor. Anything beyond those two marks is a stop.

- **Ambiguous number.** The two repos number independently: when both hold that number and the payload names no repo, stop rather than guess.
- **Not actionable.** The issue is closed, or the number is a pull request.
- **`needs-architect` label.** The decomposition was never validated, so the work may be the wrong shape. This is `ship-pr`'s own merge gate read early rather than a second rule: the label blocks the merge either way, so catching it here costs a minute instead of a full delegate, review and CI cycle. Report it and offer to decompose in an ordinary turn.
- **Already in flight, or someone else's.** *Any* assignee found at Phase 0 stops the run. The assignee axis is two-valued: the user's own login means an earlier run already took this issue, and any other login means it is externally owned. Board Status `In Progress` stops it too, on its own, because work can be active with nobody assigned; that field answers to no issue search, only to listing the board's items with the `Status` field. An externally owned workstream, or an open PR already covering the zone, is the same stop — drive or review that PR rather than compete with it. Only the payload saying to resume or take over lifts this, and it must, because the run cannot tell an earlier attempt from a live one. Past Phase 0 this stop reads against the baseline above, so the marks step 6 wrote are never a stop for the run that wrote them.
- **Blocked.** A `blocked` label, or an open dependency the issue declares.
- **The deliverable is a decision, not a change.** An issue whose acceptance is that a decision is *recorded* has nothing to delegate in Phase 3 and nothing to publish in Phase 5. Stop, and give the recommendation you would have made — that recommendation is the run's useful output. Read the acceptance for this rather than trusting a label to mark it.
- **An external contract left to be invented.** The issue is under-specified precisely where the answer becomes visible outside the process: proto field names or semantics, a CLI surface, shared-memory layout or ABI, an on-disk or YAML config schema, a metric name or its labels. Stop and ask. When the issue already fixes the contract, that is specification, not ambiguity — proceed and decide the internals yourself. A contract fixed only in part, or offered as a menu of options, is the common case and splits on blast radius: when every option on the menu is compatible with deployed clients, choose one and record it; when one of them breaks them — a renumbered wire field, an incompatible rename — stop, and stop equally when the menu leaves the compatibility strategy itself open, even where the issue invites the implementer to decide. An invitation to decide is not authority to break a deployment.

Everything else you decide. Under-specification inside a process boundary — which helper, which file, which test shape, how to structure the change — is the run's own business, recorded per the non-negotiables.

## Pipeline

### Phase 0 — Admit the issue

1. **Resolve the repo.** When the payload names one, read the issue there. When it does not, probe **both** before resolving anything: they number independently, so finding the number in the public repo is no evidence the private one lacks it, and a resolution that stops probing on the first hit makes the ambiguous-number stop unreachable. Both → hard stop. A private-repo issue is worked entirely in the private checkout — its worktree, its gates, its PR — and is never referenced from anything committable in the public tree.
2. **Read it whole**: body, labels, type, comments, parent and sub-issues, and — when the issue carries one — the `Source:` line naming the change that spawned it, usually the fastest route to the real context. An epic carries none, since its provenance is whatever its children bring.
3. **Look for an owner**: assignee, board Status, and open PRs that reference the number or touch the same zone.
4. **Apply the hard stops.**
5. **Epics.** An `epic`-labelled issue, or one with children, is never the unit of work. One with no children is not ready either: report it and stop, rather than pointing `planner decompose` at it, which would file an epic of its own beside it. Otherwise pick a single child from those that clear the hard stops, applying them at the selection so that one blocked or externally owned child does not end a run the epic's other children could still serve — and when no child clears them, report that and stop, exactly as for a childless one. Rank the survivors by the packet-path-safety-first ranking the planner's north star defines, then by the order the epic's own body gives them, and finally by the lowest number, because an epic's prose routinely leaves several children deliberately unordered. Then **restate the run against that child's number and re-enter this phase from step 2 against it**: labels, assignee, board Status and children all belong to the child, and a sub-issue list routinely leads with closed or `needs-architect` children about which the parent's clean read says nothing. Exactly one child per run, so the next is a fresh invocation. Report the remaining children, and any acceptance the epic keeps for itself, at the end.
6. **`planner start <#issue>`** — assignee plus board Status `In Progress`. Re-read the owner state immediately before this call, and stop on anything that changed since step 3: `planner start` refuses a closed issue and an existing assignee, but nothing in it reads the board, so an issue that went `In Progress` in the gap would be taken anyway — and the Phase 5 baseline would then mistake that value for this run's own mark. This is the moment the run becomes visible to everyone else, so it happens before the first brief, not after the work.

### Phase 1 — Understand and decompose

Yours to do, on the strongest model available, before any specialist is briefed.

- **Bounded factual questions go to `fast-explorer`** — definitions, callers, registration surfaces, one execution path, the shape of an existing pattern. Verify anything material yourself; its report focuses your reading rather than replacing it.
- **A reported defect is confirmed before it is fixed.** `bug-hunter confirm` first. **REFUTED** ends the run as a close, not a fix: report the evidence and let `planner close` record `not_planned` — inventing a fix for a bug that is not there is the worst outcome an unattended run can produce.
- **A performance claim is measured before it is optimized.** The trigger is the issue's deliverable being a speedup, read off its acceptance — not a label, since the repo has no `perf` one. `performance-engineer profile` locates the bottleneck when the issue has not; when the issue already names it with evidence, go straight to the fix and let Phase 4 decide what proving the result takes. `networking-expert` advises when the lever is protocol or algorithm.
- **Then decompose into file-scoped sub-tasks** and fix the layer order — C API, then proto and Go control plane, then Rust CLI, then Web UI — noting every shared surface the change touches and every consumer of it.

### Phase 2 — Worktree

Open one dedicated worktree for the run, forked from confirmed `origin/main`, and name it in every brief. The seeding rules and the one real decision — whether the task's gates *produce or consume* `build`, which decides a real build directory against a borrowed symlink — are in AGENTS.md under worktree isolation and in the architect's `worktree-isolation-and-seeding` memory. Do not re-derive them here.

### Phase 3 — Delegate

One specialist per layer, parallel only where the sub-tasks are genuinely independent. Every brief carries: the worktree's absolute root and an instruction to `cd` there first; the contract stated first, with exact file paths, symbol, field and RPC names; the directly relevant rules from that specialist's own memory, restated; the gate the work must pass; a ban on destructive git while the tree is dirty; and an instruction to STOP and report rather than invent a missing input.

### Phase 4 — Verify

1. **Run the language gates** listed in `ship-pr` Phase 0, from the repository root.
2. **Run a `reviewer` pass — always, and without offering it first.** An unattended run has no user to say "looks fine". Route each finding back to the specialist that owns it, then re-review, within the three-round budget.
3. **A confirmed defect closes only on a `bug-hunter validate` PASS**, re-running the exact repro. A perf fix closes on the evidence its own acceptance asks for: a throughput claim owes a `performance-engineer regression` improvement beyond the noise floor, while an acceptance stated as a deterministic count — allocations, copies or syscalls per operation — owes a test asserting that count, to which no noise floor applies. Never demand a throughput verdict from a change that never claimed one, or it can satisfy every criterion it was given and still be unclosable.

### Phase 5 — Publish

Invoke `ship-pr`. Give it a scoped conventional title, a body whose bullets carry the assumptions from the non-negotiables, and `Closes #<n>.` so the issue closes on merge. Its gates are its own — a `needs-architect` hit or an unaddressed review finding stops this run exactly as it stops any other.

### Phase 6 — Close and sweep

Only once the merge actually happened. A payload that narrowed the run to a PR ends at Phase 7 instead, reporting a green PR ready to merge on the user's word: nothing has closed the issue, so nothing may mark it closed. Steps 2 and 3 still run, since a withdrawn merge leaves the same tails and the same lessons.

1. **`planner close`** with the merged PR — usually verification plus the board move, since the PR body already closed the issue, but the board lies until it runs.
2. **`planner ingest` every tail the run left**: a shortcut you took, a follow-up the reviewer raised and you deferred, a gap you noticed in passing. Name the PR in the payload so the item lands on that change's own timeline. An unattended run generates more of these than a supervised one, and they are the first thing lost.
3. **Write the memory the run earned** — a specialist that drifted, a coordination trap, a delegation heuristic — per the memory hygiene rules, at the primary checkout.

### Phase 7 — Report

Close the run with: the issue and what it turned out to be; the PR number and state; the assumptions you took; what you delegated to whom; the gates that ran and their verdicts; anything ingested as follow-up; and the remaining children when the payload was an epic. If the run stopped, say where and give the one question that clears it.
