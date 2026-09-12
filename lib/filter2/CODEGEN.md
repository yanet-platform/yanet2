# Straight-C code generation for filter queries — draft

Status: design draft, nothing here is implemented. The interpreter in
`query.h` stays the fallback and the source of truth for semantics; the
differential harness (same rules, same packets, identical rule indexes)
is the acceptance gate for any generated path.

## Motivation

The query path is already dispatch-free per packet: attribute lookups
are pasted per variant and the getter is inlined. What remains generic:

- the per-attribute batch loop (one pass over the packet array per
  attribute, results spilled to a stack array, then joint passes);
- the joint chain (a table load per attribute pair, sequential);
- value-table and LPM walks that could specialize on the *compiled*
  shape: table dims become constants, bounds checks disappear, sparse
  tries become unrolled walks or flat arrays.

A generated per-ruleset C function removes all of it: one pass over the
batch computing every attribute value and the whole joint chain per
packet, with every dim and mask baked in as a literal.

## What is generated

One C translation unit per (filter, signature), exporting:

```c
void filter_query_sig(const struct filter *filter,
                      const struct packet **packets,
                      uint32_t *results,
                      uint32_t packet_count);
```

The body is emitted from the compiled artifacts (not from the rules):

- for each attribute, a specialized value expression:
  - `device`/`vlan`/`port_*`/`proto_range`: a direct table load with the
    table base loaded once outside the loop; when the compacted table is
    dense and small (<= 64 KiB), emit it as a `static const` array in
    the TU so the compiler folds the lookup;
  - `net4`: the LPM walk emitted as a fixed-depth loop over the pages
    with fanout and depth taken from the compiled trie; a trie of depth
    one or two collapses to a single load;
  - `net6`: two half walks plus the comb load, dims as literals;
- the joint chain as straight-line loads: `v = joints[0][a0][a1]`, then
  `v = joints[1][v][a3]`, ..., with dims as literals and the final
  `FILTER_RULE_INVALID` comparison left to the consumer;
- all stack spills removed: per-packet values stay in locals, and the
  compiler (not the generator) schedules the loads.

Shared-memory discipline: the tables live in the filter's shm block, so
every load goes through the `filter` pointer argument. No shm address is
ever baked into the TU (attachments are per-process and ASLR'd). Tables
small enough to inline are copied into the TU at generation time and are
immutable snapshots — acceptable only when the whole generated object is
tied to one config generation, which it is (see lifecycle).

## Toolchain options

| option | compile time | code quality | dependency | notes |
|---|---|---|---|---|
| clang (libclang C API, in-process) | ~50-200 ms | best | libclang on the control plane | shared CCACHE-style cache keyed by the artifact hash |
| libgccjit | ~20-80 ms | near-clang | libgccjit | runtime library exception license; simplest API |
| tcc (libtcc) | ~1-5 ms | poor | libtcc | fine for table-heavy sigs; hurts trie-walk codegen |
| hand-emitted machine code (mmap W^X) | <1 ms | manual | none | per-arch maintenance; last resort |

Recommendation: prototype with clang (already in every dev/build
environment), measure the compile-time cost against the config-apply
budget, then decide. tcc is the likely production candidate if the clang
dependency on control-plane hosts is unacceptable. All options share the
same emitted-C source, so the backend is swappable.

## Pipeline placement and lifecycle

1. Generate: the control plane, after `filter_compile`, renders the TU
   from the compiled artifacts (pure function of the artifacts, so it is
   cacheable by artifact hash).
2. Compile: to a shared object in a tmpfs (`/dev/shm/yanet/filter/<agent>/<hash>.so`).
3. Load: the dataplane `dlopen`s the object at config-generation switch,
   exactly like the config-context flip: the previous generation keeps
   running until its workers quiesce, then `dlclose`. A failed load
   falls back to the interpreter — generated code is an optimization,
   never a dependency.
4. Teardown: `dlclose` under the same ruleset that reclaims shm blocks.

The query vtable gains one entry: `query_fast` — NULL when the
generation was not produced (fallback to the generic path).

## Safety

- the generated source is deterministic and hash-pinned; the `.so` name
  carries the hash and loads are refused on mismatch;
- generated code only reads through the `filter` pointer and the packet
  array; a hardened build can compile it with `-fno-strict-aliasing`,
  `_FORTIFY_SOURCE=0` and CFI off, but standard flags are the default;
- the kill switch (`YANET_FILTER_CODEGEN_DISABLE`) makes config apply
  skip generation entirely.

## Expected gains

Table-heavy signatures (acl `ip6_port` shape): the interpreter's per
batch cost is dominated by scattered table loads; the generated version
loads each table base once and keeps per-packet values in registers —
expected 2-4x on the query path. Trie-heavy signatures (route-mpls):
depth-specialized walks — expected 1.5-3x. The compile side is
unaffected; this is orthogonal to the net6 compile work.

## Phases

1. emit + compile + load behind a flag, `device, vlan` signature only,
   differential-gated;
2. all attributes, all consumer modules, benchmark on the acl.in query
   path;
3. cache, lifecycle, fallback hardening, kill switch;
4. (optional) backend swap to tcc/libgccjit.
