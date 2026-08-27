---
name: issue-create
description: >-
  Draft, create, or triage one complete GitHub issue in yanet2 or
  yanet2-private, including evidence, deduplication, planning metadata and
  dependencies; also create planner-owned epic containers. Use when asked to
  formulate, file, create or triage an issue, and whenever planner creates one.
---

# issue-create

Turn one repository-visible outcome into the shortest self-contained English
issue that another engineer can close. Inspect code and GitHub, but never edit
repository files, run git writes, or exceed the caller's build/test posture.

## Modes and authorization

- `draft` — return one exact GitHub-ready leaf and its metadata; no writes.
- `create` — an explicit create/file/open verb authorizes publication and all
  metadata writes without a second preview.
- `triage <n>` — complete an existing issue without changing its lifecycle.
- `container` — planner-only epic after its complete child tree is designed.
- `decompose` belongs to planner. One leaf has one independently closable
  outcome: usually one PR, or one bounded evidence-backed verdict/decision.

## Admission

- Repositories are public `yanet-platform/yanet2` and private
  `yanet-platform/yanet2-private`. Prefer public for shared code; a private-only
  delta may depend on public work. Never expose private context or literal
  secrets publicly. Treat security findings as ordinary Bugs under this rule.
- Search open and closed issues and PRs in both repositories by concepts and
  exact symbols. An exact open issue wins. A closed match needs a regression or
  independent delta. An active matching PR requires user reconfirmation before
  a tracking issue is created.
- Multiple independently deliverable outcomes route to `planner decompose`.
  An uncreated prerequisite does too. Existing prerequisites use a native
  dependency plus `blocked`; all issues remain visible on their primary board.
- The outcome must be deliverable through tracked repository changes. Findings
  confined to agent memory, `.agent-state`, `.arch`, or other ignored/local
  trees stay in prose and never become GitHub issues.
- A current change's follow-up needs explicit author agreement, independent
  value, and a still-correct parent change that meets its acceptance criteria.
  Link it and block it until merge; never defer a correctness/security defect
  introduced by that change.
- A clear outcome with an unresolved external contract may be filed with
  `needs-architect` and explicit decision points. If the closing outcome,
  privacy boundary, or repository cannot be established, ask one blocking
  question instead of guessing.
- A user-observed unexpected symptom is a Bug without a known root cause. A
  static hypothesis not otherwise proven is a Task whose outcome is confirmation.

## Issue contract

- Title is `<type>(<scope>): <lowercase brief>` using an allowed commit type.
  Bugs normally use `fix`, Features `feat`, and Tasks the semantic type. An epic
  keeps the semantic type and gains label `epic`; never use `epic(scope)`.
- Body schemas, omitting empty optional sections:
  - Bug: `Motivation` (observed vs expected behavior, impact), `Evidence`
    (reproduction steps, environment, logs, code references), `Scope`,
    `Acceptance`.
  - Feature: `Motivation`, `Outcome`, `Scope`, `Acceptance`.
  - Task: `Motivation`, `Closing outcome`, `Scope`, `Acceptance`.
  - Container: `Goal`, `Scope`, `Children`, `Done`.
- A leaf body describes the problem and how to observe or reproduce it, never
  how to fix it. No body proposes a solution path, design, candidate change, or
  the files to edit, even when one seems obvious: there is usually more than
  one, and choosing is the job of whoever closes the issue. A contract the user
  has already decided is part of the outcome, not a proposal. A proven cause
  belongs in Evidence, but a fix does not. `Scope` bounds the affected
  behavior, and `Outcome`/`Acceptance` state observable results rather than
  implementation steps. Add Constraints, Out of scope, or Source only when
  informative. Acceptance is a short checklist; investigations close on a
  verdict plus evidence/repro, and decisions close on a recorded choice plus
  affected contracts.
- Distinguish reproduced, code-proven, user-reported, and hypothetical claims.
  Use stable blob links and explain what each proves. Do not invent impact,
  cause, or tests. The issue must stand without inaccessible links.

## Metadata

- Type is `Bug | Feature | Task`. New issues use applicable existing labels
  only: `debt`, `chore`, `epic`, `blocked`, `needs-architect`, and minimal
  `C:*`/`M:*` owners. Type replaces `bug`/`enhancement`; never add legacy `T:*`,
  `go`, or `refactoring`, and never create a label.
- Priority measures impact and urgency: Urgent/P0 active severe production or
  safety; High/P1 serious correctness or production blocker; Medium/P2 meaningful
  behavior or reliability; Low/P3 latent gaps, debt and polish. Board and effort
  do not raise it.
- Effort covers the whole path to closure: Low/S known/local, Medium/M several
  linked surfaces or bounded investigation, High/L tightly coupled uncertainty.
  Independently splittable High work routes to planner. A container's Issue Type
  follows its overall Bug, Feature, or Task outcome.
- Primary board: private → #10; public packet-path/shared-memory/CGO safety →
  #7; CI/packaging/deployment/dev-test infra → #9; other public work → #8.
  Add #11 only when its live description matches; never mutate legacy #5.
- A new issue is unassigned and Todo. Do not set milestones. Existing blockers
  use GitHub's native relation and label `blocked`.

## Publish and triage

Before any write, read [the GitHub operation map](references/github.md). Discover
all IDs live, create with `gh issue create --body-file -`, apply every field and
relation, then read back the complete postconditions. After an ambiguous 5xx,
reconcile exact title/repository/author/time before at most one retry. A partial
write resumes the same issue through triage; never compensate by closing or
creating another.

Triage preserves state, assignee, milestone and In Progress/Done. Rewrite title
or body only for the current user's issue or with explicit permission; otherwise
leave one concrete suggestion. Missing blocking facts get label `question`, one
specific comment and an `INCOMPLETE` result. Exact duplicates get label
`duplicate` and a canonical link but are not closed. Remove only redundant
`bug`/`enhancement`, keep unknown labels, leave exactly one correct primary board,
and preserve projects outside #7–#11.

## Report

Draft reports the exact repository, title, body and metadata separately from
assumptions/stops. Publish reports the URL and read-back Type, labels,
Priority/Effort, projects/Status, dependencies and any unmet postcondition.
