# l3b fuzz target

A packet-path fuzz target for the l3b balancer module, following the
shared fuzz-target pattern of the other modules: the common
libFuzzer/reproducer registration (`tests/fuzzing/`) and the shared
packet helper (`lib/fuzzing/fuzzing.h`), which allocates one mbuf per
input, parses it and hands it to the module handler.

## Fixture

The module configuration is allocated by hand from the fuzzing arena in
the zero-rule state of an installed module before its first update:

- no destination rules, so the destination classifiers stay zeroed and
  are never queried;
- a zeroed module execution context, so no linked virtual service and
  no device targets ever resolve.

## Covered and not covered

Covered: the module's own packet guards — fragment exclusion, transport
eligibility (TCP/UDP/complete Echo), required-header head residency, the
unmatched-transport drop and the unmatched-Echo pass-through — over
arbitrary malformed, truncated and valid frames.

Not covered (need a published configuration generation carrying rules
and a linked service): destination classification for both families,
virtual-service dispatch, source admission, scheduling and sessions.
A deeper target would require either an agent-backed shared-memory
fixture or a published-pipeline harness, neither of which this pattern
provides.

## Running

```sh
just dfuzz l3b            # unbounded interactive run (build + fuzzer)
```

Bounded smoke inside the dev container from the repository root:

```sh
just drun 'mkdir -p corpus/l3b findings/l3b && \
  ./build/tests/fuzzing/l3b corpus/l3b/ -max_total_time=60 \
  -timeout=10 -rss_limit_mb=2048 -print_coverage=1 \
  -artifact_prefix=findings/l3b/'
```

The nightly fuzz workflow discovers the built binary automatically; no
matrix entry is required.
