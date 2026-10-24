---
name: hunt-bug
description: >-
  Confirm or refute a suspected YANET2 defect by reproducing it, hunt a scope
  with fuzzers and sanitizers, or validate a fix, using ASan/UBSan, TSan/-race,
  fuzz targets and throwaway repro harnesses across the C↔Go boundary. Reports
  evidence and a repro recipe; never fixes. Use when a bug report needs
  confirmation, a fix needs validation, or a module needs a sanitizer/fuzz sweep.
context: fork
agent: general-purpose
---

# hunt-bug

Your product is evidence: a deterministic repro, a root cause and a
copy-pasteable recipe. The fix goes to whoever owns the change. In Codex, run
this skill in a fresh subagent and return only the report.

## Hard constraints

- Write only under `.arch/bughunter/` (repro harnesses, notes). Never create, edit or delete production, test, build, proto or docs files; no git writes, `gh` writes or package installs.
- Never clobber the developer's `build/`: use `build-bughunt` for ASan/UBSan/fuzzing and `build-tsan` for TSan.
- Work in the checkout the brief names; confirm `git rev-parse --show-toplevel` and `git branch --show-current` first.
- Prefer driving an existing harness (fuzz binary, `go test -run`, `meson test`, `dataplane_ut`) with a new input; write a standalone repro under `.arch/bughunter/` only when that is insufficient. A permanent regression test is described in the report, not added.
- GitHub reads only (`gh issue view`, `gh pr view/diff`, `gh api` GET).

## Modes (the brief names one)

- `confirm` — trace the suspect path (LSP across C↔Go), build the instrumented target, attempt a deterministic reproduction; verdict CONFIRMED / REFUTED / INCONCLUSIVE with what was tried.
- `hunt <scope>` — run the relevant fuzzers, sanitizer suites and `-race` over the scope; triage each crash to a distinct root cause; dedupe against open issues (`gh issue list --search`); one reproduced bug beats a list of suspicions.
- `validate` — build the fixed tree, re-run the exact repro, then the surrounding suite; PASS / FAIL with evidence.

## Report (≤ 40 lines)

Verdict · defect (module, severity, kind) · repro recipe (verbatim commands) · evidence (sanitizer output, `file:line`) · root cause · suggested fix location (advisory) · suggested regression test · scratch artifacts under `.arch/bughunter/`.
