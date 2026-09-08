# YANET2 Lab Roadmap

## Vision

Make a local YANET2 dataplane easy to boot, inspect, change, probe, reset, and
share as a reproducible manifest without requiring knowledge of the functional
test harness internals.

## Now

- Exercise `doctor`, lifecycle, and the built-in scenarios on Linux/KVM and
  macOS/TCG in regular development.
- Improve report diagnostics with bounded dataplane/control-plane log tails.
- Stabilize the JSON output and document its compatibility policy.

## Next

- Record an interactive traffic experiment into a starter manifest.
- Support named packet ports and richer layer-aware packet diffs.
- Add manifest composition for reusable setup fragments.
- Publish CI smoke coverage for schema examples and fixture regeneration.

## Later / Experiments

- Custom multi-VM and multi-link topologies.
- Throughput, latency, and counter-based performance probes.
- Bare-metal preflight and user-facing reports.
- Container and Kubernetes deployment experiments.
- A web view for scenario progress and report artifacts.

## Delivered

- Strict Draft 2020-12 JSON Schema stored next to the lab manifests.
- YAML loading plus semantic checks for paths, durations, and unique names.
- Persistent local QEMU supervisor with lifecycle, exec, shell, reset, and
  report commands.
- Guided `forward-route`, `decap`, and `nat64` scenarios.
- Deterministic PCAP fixture generation and exact packet expectations.
- Exact transformation probes for the `decap` and `nat64` walkthroughs.
- Repository documentation, Just entry point, and agent skill.

## Decision log

- 2026-08-01: Reuse the functional QEMU harness and its baseline snapshot
  instead of creating a second VM stack.
- 2026-08-01: Keep the initial topology fixed to two packet ports.
- 2026-08-01: Use argv arrays for guest commands and reject unknown manifest
  fields to keep automation predictable.
- 2026-08-01: Keep the roadmap tracked in `lab/ROADMAP.md`; `.arch/` is ignored.
