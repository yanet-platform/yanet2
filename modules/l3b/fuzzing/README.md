# l3b fuzz target

A bounded packet-path fuzz target for the l3b balancer module. It reuses
the shared libFuzzer/reproducer registration (`tests/fuzzing/`) and the
shared in-process dataplane harness (`lib/dataplane_ut/`); there is no
target-local runner.

## Fixture

One persistent harness publishes, through the public control-plane APIs:

- a plain device whose output entry carries one pipeline with a single
  l3b module stage;
- the module configuration with one IPv4 and one IPv6 destination rule
  (service protocols ICMP, TCP, UDP) linked to the service;
- one virtual service with an admitting IPv4/IPv6 source filter, no real
  servers and an empty scheduler ring;
- the per-worker session table the service requires.

The module is attached at the device output entry, while production
attaches it at device input. This is a topology note, not a defect: the
observed outcome is output-versus-drop membership either way, and the
output entry removes the need for a second routing module.

Worker time is fixed; every parsed input leaves the round fully released
(an allocation-survival check aborts the target otherwise). The published
execution context the counter oracle reads is captured once after the
fixture is wired; the fixture never installs another generation, so the
capture cannot go stale.

## Input encoding

- Inputs of 34 to 1500 bytes are raw Ethernet frames.
- Shorter inputs select a built-in seed frame by `data[0] % 10`; the
  remaining bytes extend seed frames whose transport region is already
  populated. The short-ICMP seed ignores trailing bytes, because they
  would change its classification.
- Empty and oversize inputs are rejected before allocation.

Selector seeds and their asserted outcomes (service counter deltas in
`incoming`, `filter_rejected`, `ring_empty`, then drop/forward):

| Selector | Frame | Expected |
|----------|-------|----------|
| 0 | TCP4 admitted client to VIP | incoming+1, ring_empty+1, drop |
| 1 | TCP4 from a non-admitted source | incoming+1, filter_rejected+1, drop |
| 2 | TCP4 to an unmatched address | drop, no service contact |
| 3 | TCP6 admitted client to VIP | incoming+1, ring_empty+1, drop |
| 4 | TCP6 from a non-admitted source | incoming+1, filter_rejected+1, drop |
| 5 | Echo request to the VIP | incoming+1, drop (no reals configured) |
| 6 | Echo request elsewhere | forward untouched |
| 7 | IPv4 fragment (MF set) to the VIP | drop before classification |
| 8 | ICMP4 without transport bytes | forward untouched |
| 9 | UDP4 admitted client to VIP | incoming+1, ring_empty+1, drop |

Raw frames assert only the universal invariant: a parsed frame leaves
exactly one output or drop packet.

## Covered and not covered

Covered: destination classification for both families, fragment guards,
unsupported-transport and short-header pass-through, source admission
and rejection, matched-Echo refusal without reals, empty-ring refusal,
unmatched-Echo pass-through.

Not covered (out of scope for this target): session pinning and its
lifetime, real-server encapsulation, echo reply rewrite, health checks,
WLC and publisher paths. An empty ring never selects a real, so no
session record can be created from these inputs.

## Running

```sh
just dfuzz l3b            # unbounded interactive run (build + fuzzer)
```

Bounded smoke, single-seed replay and coverage collection, all inside
the dev container from the repository root:

```sh
just drun 'mkdir -p corpus/l3b findings/l3b && \
  ./build/tests/fuzzing/l3b corpus/l3b/ -max_total_time=60 \
  -timeout=10 -rss_limit_mb=2048 -print_coverage=1 \
  -artifact_prefix=findings/l3b/'
just drun 'printf "\x00" > /tmp/seed0 && \
  ./build/tests/fuzzing/l3b /tmp/seed0'   # reproducer: selector 0
```

The nightly fuzz workflow discovers the built binary automatically; no
matrix entry is required.
