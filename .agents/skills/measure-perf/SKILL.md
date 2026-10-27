---
name: measure-perf
description: >-
  Measure and locate YANET2 dataplane bottlenecks, A/B-benchmark a change for
  regression or improvement, or give an advisory perf review of a hot-path
  diff. Reports numbers with variance and a benchmark recipe; never optimizes.
  Use for a speedup baseline, a suspected regression, or a hot-path review.
context: fork
agent: general-purpose
---

# measure-perf

Your product is numbers with variance and a recipe; the optimization goes to
whoever owns the change. In Codex, run this skill in a fresh subagent and
return only the report.

## Hard constraints

- Write only under `.arch/perfeng/` (standalone microbenches, notes). Never edit production, test, build, proto or docs files; no git writes, `gh` writes or package installs.
- Never clobber the developer's `build/`: measure in a release `build-perf` dir.
- Work in the checkout the brief names; confirm `git rev-parse --show-toplevel` and `git branch --show-current` first.

## Measurement discipline

- Always A/B in the same configuration: baseline (merge-base or `origin/main`) vs candidate; report absolute numbers, delta, and min/median/max over repeats.
- Establish the noise floor first (baseline vs itself); a delta inside it is NEUTRAL, not a win. Pin a core with `taskset` where possible, use warm-up and `-count`; state what you could not control.
- Prefer existing harnesses: in-repo Go benchmarks (`go test -bench -benchmem -run=^$`, `benchstat`), `tests/fwstate/*_bench_test.c`, `lib/filter/tests/bench_net*.c`. In-process Go benches + `benchstat` are the trustworthy tool; the live trafgen rig drifts ±15–20 %.
- Tag every claim *measured* or *analysis*; cite `file:line` of the hot path.

## Modes (the brief names one)

- `profile <scope>` — trace what runs per packet (LSP call hierarchy), put numbers on candidate hot spots, rank bottlenecks by impact with a suggested optimization.
- `regression <change>` — A/B the change; verdict REGRESSION / IMPROVEMENT / NEUTRAL / INCONCLUSIVE with numbers.
- `review <diff>` — flag per-packet allocation, added copies, lost batching, branchy inner loops, struct growth across cache lines, missing prefetch; back with a microbench where cheap. Advisory, not a gate.

## Report (≤ 40 lines)

Verdict · scope and confidence (measured/analysis) · benchmark recipe (verbatim) · evidence (numbers, variance) · bottleneck/root cause · suggested optimization (advisory) · scratch artifacts under `.arch/perfeng/`.
