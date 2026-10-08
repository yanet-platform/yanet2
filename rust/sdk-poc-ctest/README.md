# Rust module SDK: libc/ctest-style proof of concept

A dataplane module (`decap`) in safe Rust on top of hand-written `#[repr(C)]`
mirrors of the YANET C layouts. `ctest` checks the mirrors against the C
headers. It is the comparison point for the bindgen-based proof of concept.
The safe API is the same in both; only the sys layer differs.

Not part of the root Cargo workspace and not wired into meson.

## Layout

| Crate | Role | `unsafe` |
|---|---|---|
| `yanet-sys` | C mirrors (`src/ffi.rs`), strict-provenance resolver (`Root`), `LpmView` with the Rust port of `lpm_lookup` and a control-plane validator, decap config view, `Packet`/`Front` handles | yes, holds no packet-processing policy |
| `yanet-sdk` | `Module` trait, the C handler trampoline, the descriptor constructor, `export_module!` | yes, glue only |
| `decap-rs` | the module, `libdecap_dp.so` | `#![forbid(unsafe_code)]` |
| `systest` | ctest suite for every mirror | generated |
| `yanet-testkit` | test-only C fixtures: real C LPM images, a Miri fixture produced by C at build time, a packet harness over the real C parser, tunnel stripper and C decap handler | test only |
| `yanet-build` | build-script helper: compile flags from the meson compilation database | none |

## Build and run

Run all commands from this directory. `systest`, the `dataplane` testkit
feature and the scripts need a configured meson build directory: the
default is `<repo>/build`, or set `YANET_BUILD_DIR`. Setting it up once:
`git submodule update --init && meson setup build` at the repository root.
On this host, meson only found libyaml after adding
`PKG_CONFIG_PATH=/usr/lib/x86_64-linux-gnu/pkgconfig:/usr/share/pkgconfig`.
`meson setup` is enough; no `ninja` step is needed.

```bash
cargo build --release -p decap-rs        # target/release/libdecap_dp.so
cargo run -p systest                     # ctest: "PASSED 417 tests"
cargo test --workspace                   # unit, differential and harness tests
MIRIFLAGS=-Zmiri-strict-provenance cargo +nightly miri test -p yanet-sys -p yanet-sdk
MIRIFLAGS="-Zmiri-strict-provenance -Zmiri-tree-borrows" cargo +nightly miri test -p yanet-sys -p yanet-sdk
MIRIFLAGS=-Zmiri-strict-provenance cargo +nightly miri test -p yanet-sys whole_struct -- --ignored  # must report UB
scripts/ctest-drift.sh                   # added / swapped / retyped C field, each caught
scripts/dlopen-smoke.sh                  # load the .so through the real plugin loader
scripts/asm-compare.sh                   # Rust vs C assembly of resolve and lookup
RUSTFLAGS="-C target-cpu=haswell" cargo run --release -p yanet-sys --example lookup_bench
cargo +1.88 test --workspace             # MSRV check (CI pins 1.88)
```

The plugin exports two symbols, `new_module_decap` and
`yanet_module_abi_version` (33). It leaves `packet_decap` undefined, to be
resolved against the dataplane binary, which is linked with
`export_dynamic`. The dataplane loads plugins before built-ins, so
`libdecap_dp.so` in the plugin directory replaces the C decap module.

## Numbers

Measured on this host (16 cores, rustc 1.98, gcc 13.3):

- Lines with comments, excluding in-file test modules:
  - `ffi.rs` (the mirrors): 301;
  - other `yanet-sys` code: 661 (lib 51, resolve 58, lpm 269, decap 77,
    packet 206);
  - `yanet-sdk`: 151;
  - `decap-rs`: 87, against 123 for the C `dataplane.c`;
  - `systest`: 180 (build.rs, main, header).

  Tests and fixtures add about 1850 Rust and C lines.
- Non-test `unsafe`:
  - `yanet-sys`: 23 blocks, 11 `unsafe fn` and one extern block;
  - `yanet-sdk`: 4 blocks plus the trampoline;
  - `decap-rs`: none.
- Build times: `decap-rs` release from clean is 0.45 s. The production path
  has no third-party dependency: no bindgen, no libclang, no `cc`. The
  `systest` build from clean is 9.4 s (ctest pulls syn, askama and cc),
  1 s after an `ffi.rs` edit; the suite runs in 0.03 s.
- ctest: 417 runtime checks over 20 types (16 structs, 1 union, 3
  relative-pointer aliases), 120 fields and 17 constants (5 of them mbuf
  field offsets and sizes). The checks are 2 size/alignment per type,
  2 offset/size per field, 1 field-address check per field and 1 per
  constant. In addition:
  - the C compiler checks each of the 120 field types through typed field
    accessors under `-Werror`;
  - one `_Static_assert` in the systest header pins the `packet_decap`
    prototype.

  The C plugins' `plugin_abi_assert.h` size tripwires are not compiled into
  the Rust `.so`; for the mirrored structs ctest checks sizes and much more.
- Lookup, 1 Mi random keys, 50 k v4 / 20 k v6 prefixes, `-march=haswell`:
  C 22–25 ns, Rust 17–22 ns per lookup. Rust is not faster per hop:
  `LpmView` resolves the first page once per attach, while C `lpm_lookup`
  pays two `ADDR_OF` resolves with NULL tests on every call.
- Miri, strict provenance, `yanet-sys` + `yanet-sdk`: 15 tests pass in
  each model; the C-calling test and the negative control are ignored.
  Wall time per model, including the Miri build of the crates:
  - Stacked Borrows: 6.5 to 12 min over three runs;
  - Tree Borrows: about 2.3 min.

  The negative control fails as intended in both models.

## What is proven, and how

- **Layout parity.** ctest covers every mirrored struct, field, size,
  alignment and constant. It runs with gcc 13 and exactly the meson
  defines, include directories and forced `rte_config.h` of the C decap
  module, taken from `build/compile_commands.json`. Drift demonstration
  (`scripts/ctest-drift.sh`): build-time scratch copies of `common/lpm.h`,
  tracked headers untouched.
  - An added field is caught by size and offset checks:
    `bad lpm size: rust: 144 != c 152` and the shifted `decap_module_config`
    fields.
  - A swapped field is caught by offset checks:
    `bad field offset pages of lpm: rust: 128 != c 136`.
  - A retyped field (`size_t` to `int64_t`, same size and alignment) is
    caught only by the compile-time type check: `pointer targets in
    returning 'int64_t *' ... differ in signedness` fails the systest build.
- **Strict provenance resolution.** `Root` wraps a raw pointer from C (in
  tests, a raw allocation pointer). A target is
  `root.with_addr(slot.addr() + offset)`. The slot reference contributes
  only its address, and no exposed provenance is used. Miri passes under
  `-Zmiri-strict-provenance` in both aliasing models for:
  - an interior root,
  - negative offsets,
  - NULL,
  - a remap (copy, drop the original, resolve in the copy),
  - the LPM view on a C-built image,
  - that image after a second remap,
  - the full trampoline with fake packets and mbufs.
- **Codegen equals `ADDR_OF`.** `scripts/asm-compare.sh`:
  - `resolve_nonnull` is `mov; add; ret`, the same as `ADDR_OF_NONNULL`.
  - The per-hop lookup loop has the same load / test / lea / add sequence
    as C.
- **No reference over C-mutable bytes.** `LpmView` borrows only the page
  table slot and pages. The decap view never covers the `cp_module` header
  or the embedded `memory_context`. Under both models, Miri accepts C-like
  writes to `refcnt` and to the sibling link while a view is held as a
  protected argument. The ignored negative control does the same with a
  whole `&struct lpm`, and Miri reports UB:
  - Stacked Borrows: `not granting access ... strongly protected`.
  - Tree Borrows: `foreign write access would cause the protected tag ...
    to become Disabled`.
- **Hybrid helpers, differential.** The cold path is C: images are built
  with the real `lpm_init`/`lpm_insert`. The hot path is the Rust port.
  - 200 000 random keys per family (about half near inserted prefixes)
    match C `lpm_lookup` on the C image.
  - They match again on a byte copy after the C image is freed.
  - Miri cannot call C. Its fixture is produced by the C code in a build
    script and embedded with its C lookup results.
- **Behavioural equality with C decap.** The C handler
  (`modules/decap/dataplane/dataplane.c`) and the Rust handler run on
  20 000 random frames parsed by the real `parse_packet`. Both use one
  C-built configuration and the real `packet_decap`. The frames cover:
  - IPIP, IPv6-in-IPv6 and GRE with optional fields, reserved bits,
    version and foreign protocols;
  - outer and inner fragments, the IPv6 Fragment header, VLAN, ARP;
  - truncated inner transport, non-matching destinations, non-tunnel
    payloads.
  
  Verdict, frame bytes and packet descriptor state are equal for every
  frame (6 262 dropped, 8 574 decapsulated, 5 164 passed). So are the
  front's output and drop counters. Two deliberate mutations of the Rust
  module (flow label, fragment check) make the test fail.
- **Real loader path.** `scripts/dlopen-smoke.sh` uses the dataplane's own
  `dp_load_plugins`: the ABI check passes, the constructor resolves, and
  `packet_decap` binds.
- **Validation is control-plane side.** `validate_lpm` bounds-checks and
  alignment-checks the page table, chunks and every child edge. It requires
  each intermediate slot to point at the start of one of the tree's own
  pages. The page table and pages, which the view borrows, must also stay
  clear of given C-mutable ranges; the decap validator passes the whole
  configuration structure (header and memory contexts). It accepts the C
  fixture and rejects:
  - a child shifted by 8 bytes;
  - an out-of-range page table;
  - a page table aimed at the configuration header.

  The dataplane path has `debug_assert!`s only.
- **Name and layout cannot be mismatched.** The dataplane hands a module
  the configurations of its own type, so a module exported under a foreign
  name would attach its layout to foreign memory. `ConfigLayout` is sealed
  and carries the module type (`"decap"`). `export_module!` const-asserts
  that the exported name equals it; `export_module!(route, Decap)` fails to
  compile.

## Design notes

- **Panic policy.** A panic in a handler aborts the process. The
  trampolines are `extern "C"`, which aborts on unwind since Rust 1.81, and
  release uses `panic = "abort"`. Modules express bad input as
  `Verdict::Drop`.
- **`export_module!(decap, Decap)`** accepts only an ident and a path. It
  emits `#[unsafe(export_name = "new_module_decap")]` calling the safe
  `yanet_sdk::new_module`, and the ABI version static. The descriptor is
  `calloc`ed because the loader `free`s it. No commit hooks are set. The
  C module sets an empty commit hook; the loader skips NULL hooks, so the
  two behave the same.
- **Generic `RelPtr<T>` and ctest.** ctest 0.5 cannot translate a generic
  path into a C type. Every relative-pointer field names a monomorphic alias
  instead, such as `rel_lpm_chunks = RelPtr<RelPtr<lpm_page>>`. The
  systest build maps each alias to the C type it shadows
  (`struct lpm_page **`) with `rename_type` and `rename_alias`, so size,
  alignment, offset and the C field type are all checked. A generic path
  used directly fails the systest build (`NotFfiCompatible`), and an
  unmapped alias fails the C compile, so neither mistake is silent.
- **`decap` header reads.** The module reads header bytes through
  `Packet::network_header::<N>()`, which checks `N` against the first
  segment. C trusts the parser there. The only divergence is a header
  shorter than the parser guarantees, which the dataplane never produces;
  Rust drops such a packet.

## Friction (ctest-specific)

- ctest expands `ffi.rs` as a standalone crate with rustc
  `-Zunpretty=expanded` (via `RUSTC_BOOTSTRAP`). As a result `ffi.rs`:
  - must be self-contained, with no `crate::` paths and no inner
    attributes;
  - keeps C spelling (`packet`, `lpm_page`);
  - names opaque C types as uninhabited enums, which need a `rename_type`
    entry each.
- ctest 0.5.1 emits field names unescaped, and `gen` is reserved in edition
  2024. `systest` therefore builds as edition 2021, and the mirrors use
  `r#gen` and `r#type`.
- Function-pointer fields must be spelled inline. Through an alias, ctest
  only compares the alias name with the C typedef, not the signature.
- ctest does not check foreign function signatures, only addresses, and
  that needs linking the dataplane. `packet_decap`'s prototype is pinned by
  a `__builtin_types_compatible_p` static assert instead.
- The DPDK mbuf is not mirrored. The three fields read (`buf_addr`,
  `data_off`, `data_len`) are located by ctest-checked constants that a
  systest header computes with `offsetof`. Header reads are bounded by the
  mbuf's own segment length, not by the descriptor's cached copy.
- The Rust `packet_decap` declaration is tied to a signature alias at
  compile time. That alias and the C static assert are both hand-written
  from the same prototype; no tool compares the two sides.
- Mirrors are written by hand: about 300 lines for decap's slice of the
  ABI, and every C change needs a matching Rust edit. ctest catches a miss
  only for items that are mirrored. It does not catch:
  - semantics: whether a C pointer is relative or absolute. Raw `*mut`
    mirrors of relative fields such as the memory-context links are
    documented, not checked;
  - fields behind opaque types;
  - C structs no one mirrored.

## Open issues and places the design did not hold up

- **`forbid(unsafe_code)` does not see unsafe from an external
  `macro_rules!`.** With rustc 1.88 and 1.98, an `unsafe { }` block or
  `#[unsafe(export_name)]` expanded from another crate's macro compiles
  inside a `#![forbid(unsafe_code)]` crate without a lint. The export macro
  relies on exactly this. The guarantee is therefore "no unsafe written in
  the module crate, plus audited macros from `yanet-sdk`", not "no unsafe
  reachable". The macro's arguments still matter: the name and layout check
  above closes the one hole a reviewer found, where a safe module could have
  attached its layout to another module's configuration. Third-party macro
  crates would need a dependency policy.
- **The validator knows only the C-mutable bytes it is told about.** It
  excludes the module's own configuration structure. Other C-mutable bytes
  that a corrupt edge could reach inside the bounds go unnoticed: allocator
  free lists, other contexts in the agent arena.
- **Rust 1.81+ abort-on-unwind is relied on.** Panic payload logging before
  the abort is not implemented. The `.so` is 416 736 bytes, including
  std's panic and backtrace support; its size was not minimised.
- **Mapping provenance is a contract, not a check.** That the C-provided
  `abs_cp_module` carries provenance over the whole mapping is stated on
  `Root::from_raw`. Miri checks the Rust side with fixture allocations, not
  the C-to-Rust handover. A single fixture allocation also cannot detect a
  logical use-after-free or a cross-owner read inside the mapping; a
  per-block Miri allocator is not built here.
- **Header-check failure action.** The C-built decap configuration has no
  magic or version header. Neither the commit hook check from the design
  nor its failure policy is exercised.
- **`Root::resolve` has one extra branch.** The nullable resolve returns
  `Option<NonNull<T>>` and tests the computed address as well; C's
  `ADDR_OF` uses one `cmov`. The hot path uses only `resolve_nonnull`,
  which is identical to C.
- **Per-module layout in sys.** The decap config mirror and view live in
  `yanet-sys::decap`. A real SDK needs a per-module sys crate or a layout
  macro, and the latter would have to be trusted like the export macro.
- **Not exercised:**
  - worker-owned mutable zones (`WorkerLocal`, `Worker<'round>`);
  - generation leases;
  - `commit_ectx`;
  - the Go-side validate call;
  - meson/cargo integration.
- **Performance evidence** is a single-core microbenchmark of the lookup,
  not a dataplane throughput measurement.

## Compared with a bindgen sys layer

bindgen generates the mirrors from the headers with libclang at build time,
so layout drift becomes a rebuild instead of a test failure:

- no hand-written `ffi.rs`;
- no C-spelling or opaque-type maps;
- no ctest edition quirks.

The costs move elsewhere:

- libclang becomes a build dependency of every module build; the ctest
  production path has none and builds in under half a second;
- bindgen's raw output is not the API. Relative-pointer fields come out as
  plain C pointers, so `RelPtr` typing has to be added by
  `--blocklist`/`--opaque-type` plus wrapper types or post-processing;
  ctest checks hand-chosen Rust types against C instead;
- the static inline helpers (`ADDR_OF`, `lpm_lookup`, `packet_list_*`) are
  invisible to both; bindgen's `--wrap-static-fns` would generate C shims
  for them, at a call per use;
- bindgen offers its own layout tests, but they compare against libclang's
  view of the headers, not against the C compiler and flags the dataplane
  is built with; ctest compiles the probes with gcc 13 and the meson flags.

The safe layer (`Root`, `LpmView`, `Packet`, the trampoline, the export
macro) is independent of the choice.
