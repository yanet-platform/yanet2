# Rust module SDK: libc/ctest-style proof of concept

A dataplane module (`decap`) in safe Rust on top of hand-written `#[repr(C)]`
mirrors of the YANET C layouts. `ctest` checks the mirrors against the C
headers. It is the comparison point for the bindgen-based proof of concept.
The packet-facing API and the resolver are the same in both. The sys layer
differs, and so does the configuration API: here a module mirrors its own
configuration body with zerocopy.

The module configuration follows a "libc + zerocopy" split:
- the module's C header (`modules/decap/dataplane/config.h`) stays the
  source of truth, so the plugin remains drop-in compatible with the C
  control-plane API and `yanet-cli-decap`;
- the module crate mirrors only its own fields with zerocopy's
  `FromBytes`/`KnownLayout` derives;
- `yanet-sys` is generic over that body and names no module;
- a per-module ctest suite checks the mirror against the header.

Not part of the root Cargo workspace and not wired into meson.

## Layout

| Crate | Role | `unsafe` |
|---|---|---|
| `yanet-sys` | C-library mirrors (`src/ffi.rs`), `Opaque<T>` for C-mutable bytes, the generic `ModuleConfig<B>`/`ConfigView`, strict-provenance resolver (`Root`), `LpmView` with the Rust port of `lpm_lookup` and a control-plane validator, `Packet`/`Front` handles | yes, holds no packet-processing policy and names no module |
| `yanet-sdk` | `Module` trait, the C handler trampoline, the descriptor constructor, `export_module!` | yes, glue only |
| `decap-rs` | the module and its configuration mirror (`src/config.rs`), `libdecap_dp.so` | `#![forbid(unsafe_code)]` |
| `decap-rs/systest` (`decap-systest`) | ctest suite: the decap mirror against `config.h`, plus the name and type binding of the export | generated |
| `systest` | ctest suite for every C-library mirror | generated |
| `yanet-testkit` | test-only C fixtures: real C LPM images, a Miri fixture produced by C at build time, a packet harness over the real C parser, tunnel stripper and C decap handler | test only |
| `yanet-build` | build-script helper: compile flags from the meson compilation database | none |

### Files

```
rust/sdk-poc-ctest/
├── Cargo.toml, Cargo.lock, .gitignore          workspace
├── README.md
│   production (what libdecap_dp.so is built from)
├── yanet-sys/src/{lib,ffi,config,resolve,lpm,packet}.rs
├── yanet-sdk/src/lib.rs
├── decap-rs/src/{lib,config}.rs
│   proof only (never linked into the plugin)
├── systest/{build.rs,src/main.rs,csrc/systest.h}         C-library mirrors
├── decap-rs/systest/{build.rs,src/main.rs}               module mirror
├── decap-rs/tests/differential.rs                        Rust vs C handler
├── yanet-sys/src/*.rs  #[cfg(test)] modules              Miri and C differential
├── yanet-sys/examples/{asm_probe,lookup_bench}.rs
├── yanet-testkit/{build.rs,src/*.rs,csrc/*}              C fixtures, packet harness
├── yanet-build/src/lib.rs                                meson flags for build scripts
├── scripts/{ctest-drift,dlopen-smoke,asm-compare}.sh + C probes
└── lab/{decap-rs-boot,decap-rs-proof}/                   QEMU lab manifests, pcaps
    generated at build time (target/, not tracked)
    ├── systest:        ctest_ffi.{rs,c}
    ├── decap-systest:  shadow.rs (flat C-shaped struct from the mirror),
    │                   checks.rs (layout, name and type assertions),
    │                   ctest_module.{rs,c}
    └── yanet-testkit:  image.bin, keys4.bin, keys6.bin, fixture.rs
```

## Build and run

Run all commands from this directory. The two systests, the `dataplane`
testkit feature and the scripts need a configured meson build directory:
the default is `<repo>/build`, or set `YANET_BUILD_DIR`. Setting it up once:
`git submodule update --init && meson setup build` at the repository root.
`meson setup` is enough for everything except the QEMU lab run, which needs
the full `make all` build.

```bash
cargo build --release -p decap-rs        # target/release/libdecap_dp.so
cargo run -p systest                     # C-library mirrors: "PASSED 408 tests"
cargo run -p decap-systest               # decap mirror: "PASSED 11 tests"
cargo test --workspace                   # unit, differential and harness tests
MIRIFLAGS=-Zmiri-strict-provenance cargo +nightly miri test -p yanet-sys -p yanet-sdk
MIRIFLAGS="-Zmiri-strict-provenance -Zmiri-tree-borrows" cargo +nightly miri test -p yanet-sys -p yanet-sdk
MIRIFLAGS=-Zmiri-strict-provenance cargo +nightly miri test -p yanet-sys naive_mirror -- --ignored  # must report UB
scripts/ctest-drift.sh                   # added / swapped / retyped field, library and module, each caught
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

Measured on this host (16 cores, rustc 1.99, gcc 13.3):

- Lines with comments, excluding in-file test modules:
  - `ffi.rs` (the C-library mirrors): 318;
  - other `yanet-sys` code: 670 (lib 26, config 114, resolve 58,
    lpm 266, packet 206);
  - `yanet-sdk`: 174;
  - `decap-rs`: 120 (lib 102, mirror 18), against 123 for the C
    `dataplane.c`;
  - `systest`: 183; `decap-systest`: 168.

  Tests and fixtures add about 1900 Rust and C lines.
- Non-test `unsafe`:
  - `yanet-sys`: 22 blocks, 9 `unsafe fn` and one extern block, plus the
    zerocopy derives on its own mirrors;
  - `yanet-sdk`: 4 blocks plus the trampoline;
  - `decap-rs`: none written; the zerocopy derive on the mirror expands
    `unsafe impl`s.
- Build times:
  - `decap-rs` release from clean: 8.5 s, 0.16 s after a module edit. The
    production dependencies are zerocopy and its derive (syn, quote,
    proc-macro2); there is no bindgen, libclang or `cc`.
  - `decap-systest` from clean: 9.1 s, 0.4 s after a mirror edit; `systest`
    then adds 1.9 s.
  - Before the zerocopy split, the release build from clean was 0.45 s.
- ctest, C-library mirrors (`systest`): 408 runtime checks over 20 types
  (15 structs, 1 union, 4 aliases of the generic `RelPtr`/`Opaque`), 117
  fields and 17 constants (5 of them mbuf field offsets and sizes).
  - Each type gets 2 size/alignment checks, each field 2 offset/size checks
    and 1 field-address check, and each constant 1 check.
  - The C compiler also checks each of the 117 field types through typed
    field accessors under `-Werror`.
  - One `_Static_assert` in the systest header pins the `packet_decap`
    prototype.
- ctest, decap mirror (`decap-systest`): 11 runtime checks over
  `struct decap_module_config` (size, alignment and 3 fields), 3
  compile-time field type checks, 7 constant assertions and one typed
  projection per mirror field:
  - shadow and `ModuleConfig<DecapConfig>` agree on size, alignment and
    every field offset;
  - each mirror field has the SDK type its name stands for;
  - the header sits at offset 0;
  - the exported module name is `"decap"`;
  - the exported module's `Config` is `DecapConfig`.
- The C plugins' `plugin_abi_assert.h` size tripwires are not compiled into
  the Rust `.so`; for the mirrored structs ctest checks sizes and much more.
- Lookup, 1 Mi random keys, 50 k v4 / 20 k v6 prefixes, `-march=haswell`:
  C 22–25 ns, Rust 16–22 ns per lookup. Rust is not faster per hop:
  `LpmView` resolves the first page once per attach, while C `lpm_lookup`
  pays two `ADDR_OF` resolves with NULL tests on every call.
- Miri, strict provenance, `yanet-sys` + `yanet-sdk`: 16 tests pass in
  each model; the C-calling test and the negative control are ignored.
  Wall time, including the Miri build of the crates:
  - Stacked Borrows: 593 s;
  - Tree Borrows: 157 s.

  The negative control fails as intended in both models.

## Module configuration: libc + zerocopy

The C module allocates one block with `struct cp_module` at offset 0 and its
own fields after it. `yanet-sys` describes that shape once, generically:

```rust
#[repr(C)]
pub struct ModuleConfig<B> { header: Opaque<cp_module>, body: B }
```

The module mirrors only `B`, from its C header:

```rust
#[derive(FromBytes, KnownLayout)]
#[repr(C)]
pub struct DecapConfig { pub prefixes4: Lpm, pub prefixes6: Lpm }
```

`Lpm` (`ffi::lpm`) is a C-library mirror owned by `yanet-sys`. Its
embedded `struct memory_context`, which C rewrites after publication, is
`Opaque<memory_context>`: `UnsafeCell<MaybeUninit<T>>`.

The handler wrapper calls `ConfigView::<M::Config>::attach(root)`. A module
gets `&B` from `body()` and LPM views from `lpm(&body.prefixes4)`.

### Why the `&B` cast is sound

zerocopy's checked conversions (`ref_from_bytes` and friends) require
`Immutable`. A body containing `Opaque` (an `UnsafeCell`) cannot be
`Immutable`. So `yanet-sys` forms `&B` itself, with a pointer cast at the
body's offset, and relies on:
- **`B: FromBytes`.** Every initialised byte pattern is a valid `B`, so no
  validity invariant can be broken by what C wrote. The mapping is
  initialised: zero-filled at `mmap`, then written by C.
- **`B: KnownLayout` (and `Sized`).** Size and alignment are fixed at
  compile time. The cast checks the configuration pointer's alignment at
  run time. The size, that the C block holds `size_of::<ModuleConfig<B>>()`
  bytes, is a contract on `attach`, discharged by the systest: it proves
  the mirror and the C struct have the same size and offsets.
- **`Opaque` for every C-mutable byte.** A shared reference covering an
  `UnsafeCell` does not assert the bytes stay unchanged. So C may rewrite
  the header and the memory contexts while Rust holds `&B`, and Rust never
  reads them, so there is no data race either. Miri checks this under both
  aliasing models (see "What is proven").
- **Frozen after publication.** All other body bytes are not written after
  publication: the existing environment contract.

The derives also make `Lpm` and `RelPtr` `FromBytes`, so safe code can now
conjure such values (`Lpm::new_zeroed()`). An offset alone grants no
access: resolution needs a `Root`, which modules never get. `lpm()` still
checks at run time that the tree lies inside the attached body before
building a view, because a conjured tree has no validated graph. That check
is two comparisons per view, once per handler invocation.

zerocopy forbids hand-written implementations of its traits (they carry a
hidden `only_derive_is_allowed_to_implement_this_trait` item). `yanet-sys`
therefore uses zerocopy's derives on its own mirrors (`RelPtr`, `Opaque`,
`lpm_value`, `lpm_page`, `lpm`) rather than `unsafe impl` blocks. The
derives' safety rests on zerocopy's own checks, which reject any field
type that is not `FromBytes`. The semantic reason each mirror may be
`FromBytes` (an offset is inert data; opaque bytes are never read) is in
their doc comments. The derives sit behind `cfg_attr(not(ctest))`, so
ctest's standalone expansion of `ffi.rs` needs no dependencies.

### How the mirror is checked

ctest compares a Rust struct with a C struct of the same shape. Here, C
has one flat struct and Rust has header plus body. The `decap-systest`
build script therefore:
1. parses `decap-rs/src/config.rs` and requires `DecapConfig` to be
   `repr(C)` with named fields;
2. writes a flat shadow `struct decap_module_config { cp_module, prefixes4,
   prefixes6 }`, copying the field types verbatim;
3. runs ctest on the shadow against `config.h` with the meson flags:
   size, alignment, every field offset and size, and every C field type;
4. writes constant assertions that the shadow and
   `ModuleConfig<DecapConfig>` agree on size, alignment and each field's
   offset (body offset plus field offset), and pins each mirror field to the
   SDK type its name must mean (`yanet_sys::Lpm`), so a look-alike type of
   the same size fails;
5. checks that the module exports the name `"decap"` and the mirror type
   `DecapConfig`. The export macro publishes both as
   `decap_dp::YANET_MODULE_NAME` and `decap_dp::YanetModule`.

The mirror is the only hand-written description of the body; the shadow is
derived from it.

## What is proven, and how

- **Layout parity.** ctest covers every mirrored struct, field, size,
  alignment and constant. It runs with gcc 13 and exactly the meson
  defines, include directories and forced `rte_config.h` of the C decap
  module, taken from `build/compile_commands.json`. Drift demonstration
  (`scripts/ctest-drift.sh`) uses build-time scratch copies of the
  headers; tracked headers are untouched.
  - In `common/lpm.h` (`systest`):
    - an added field gives `bad lpm size: rust: 144 != c 152`;
    - swapped fields give `bad field offset pages of lpm: rust: 128 != c
      136`;
    - a retyped field (`size_t` to `int64_t`, same size and alignment)
      fails the systest build: `pointer targets in returning 'int64_t *'
      ... differ in signedness`.
  - In `modules/decap/dataplane/config.h` (`decap-systest`):
    - a field added after the header gives `bad decap_module_config size:
      rust: 832 != c 840` and shifted offsets;
    - swapped fields give `bad field offset prefixes4 of
      decap_module_config: rust: 544 != c 688`;
    - `struct lpm prefixes6` retyped to `uint8_t prefixes6[sizeof(struct
      lpm)]` (same size and offset) fails the build: `returning 'uint8_t
      (*)[144]' ... incompatible return type ... 'struct lpm *'`.
- **Strict provenance resolution.** `Root` wraps a raw pointer from C (in
  tests, a raw allocation pointer). A target is
  `root.with_addr(slot.addr() + offset)`. The slot reference contributes
  only its address, and no exposed provenance is used. Miri passes under
  `-Zmiri-strict-provenance` in both aliasing models for:
  - an interior root, negative offsets, NULL;
  - a remap (copy, drop the original, resolve in the copy);
  - the LPM view on a C-built image, and on that image after a second
    remap;
  - the full trampoline with fake packets and mbufs, through `ConfigView`.
- **Codegen equals `ADDR_OF`.** `scripts/asm-compare.sh`:
  - `resolve_nonnull` is `mov; add; ret`, the same as `ADDR_OF_NONNULL`;
  - the per-hop lookup loop has the same load / test / lea / add sequence
    as C.
- **References over C-mutable bytes are sound.**
  - **Positive:** a whole-body `&B` is held as a protected argument while
    C-like writes hit the `cp_module` reference count and a memory-context
    sibling link. Lookups through it stay correct, and Miri accepts this
    under both models.
  - **Negative control (ignored):** the same with a naive tree mirror whose
    memory context is not opaque. Miri reports UB:
    - Stacked Borrows: `not granting access ... strongly protected`;
    - Tree Borrows: `foreign write access would cause the protected tag ...
      to become Disabled`.
- **Conjured trees are refused.** `ConfigView::lpm` panics on an `Lpm`
  created outside the configuration (`test_lpm_rejects_tree_outside_body`).
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
  front's output and drop counters. A deliberate flow-label mutation of the
  Rust module makes the test fail; a fragment-check mutation did too on the
  previous layout.
- **Real loader path.** `scripts/dlopen-smoke.sh` uses the dataplane's own
  `dp_load_plugins`: the ABI check passes, the constructor resolves, and
  `packet_decap` binds.
- **End to end in the QEMU lab.** The real dataplane loads the plugin from
  its `plugin_dir`, and the unmodified built-in `decap` scenario passes
  through it (see below).
- **Validation is control-plane side.** `validate_lpm` bounds-checks and
  alignment-checks the page table, chunks and every child edge. It requires
  each intermediate slot to point at the start of one of the tree's own
  pages. The page table and pages, which the view borrows, must also stay
  clear of given C-mutable ranges; `config_range::<B>` gives the whole
  configuration (header and memory contexts). It accepts the C fixture and
  rejects:
  - a child shifted by 8 bytes;
  - an out-of-range page table;
  - a page table aimed at the configuration header.

  The dataplane path has `debug_assert!`s only.

## End to end on the QEMU lab

The plugin ran in the lab VM in place of the C decap module, and the
unmodified built-in `decap` scenario passed through it. The recorded run
is of the current code, with the zerocopy mirror in `decap-rs`. It used:
- the full `make all` build of this worktree;
- the shared functional-test image
  `/extra_disk_1/github-repos/yanet2/tests/functional/yanet-test.qcow2`;
- KVM, and QEMU 11.1.1 from the Nix profile.

An earlier run of the previous layout (QEMU 8.2) gave identical results.

### How the plugin gets in

`lab/decap-rs-boot/dataplane.yaml` is the functional-test dataplane
configuration as the harness writes it when a plugin directory is set
(`cp_memory` 160 MiB instead of the lab baseline's 128 MiB), with
`plugin_dir: /mnt/yanet2/rust/sdk-poc-ctest/target/release`.
`/mnt/yanet2` is the worktree, shared into the guest read-only over 9p.
The loader tries plugins before built-ins, so `new_module_decap` resolves
to the Rust library for every decap module instance: the operator-managed
`decap0` and the scenario's `decap_lab`.

`lab/decap-rs-boot/manifest.yaml` boots YANET with that configuration.
A boot manifest starts from the pre-YANET snapshot, which drops the
operator profile, and `scenario run` refuses a session whose profile is not
ready. The manifest therefore:
- proves the binding (three steps: log lines and the library mapped in the
  dataplane process);
- restarts the operator profile with the start commands of the lab's own
  sequence (`lab/operators.go`); the binaries, configuration and BIRD are
  already staged in that snapshot. Its final wait checks only the readiness
  services, not the full operator health check (processes, the route0
  adapter session, imported routes). `status` and `scenario run` still
  enforce the full check, so a premature run is refused rather than passed.

Repeating the start commands keeps the built-in scenario unmodified, at
the cost of a copy that can drift from `lab/operators.go`. The manifest
descriptions and comments were reworded after the recorded run; the steps
and probes are the ones that ran.

`lab/decap-rs-proof/manifest.yaml` runs after the scenario. It:
- checks that the same dataplane still has the plugin bound and mapped;
- sends the scenario's packet again (`input.pcap` / `expected.pcap`, copies
  of the built-in fixtures);
- sends `fragment.pcap`, the same frame with the outer "more fragments" bit
  set and the header checksum recomputed, which must be dropped.

### Commands

From the repository root:

```bash
make all                                       # every artifact `just lab doctor` lists
cargo build --release --manifest-path rust/sdk-poc-ctest/Cargo.toml -p decap-rs
export YANET_QEMU_IMAGE=/extra_disk_1/github-repos/yanet2/tests/functional/yanet-test.qcow2
L="flock -o /tmp/yanet2-lab.lock just lab --session ctest-poc"
$L doctor
$L up
$L scenario run decap                          # control: C module, baseline
$L reset
$L manifest run rust/sdk-poc-ctest/lab/decap-rs-boot/manifest.yaml
$L status
$L scenario run decap                          # the same scenario, Rust module
$L manifest run rust/sdk-poc-ctest/lab/decap-rs-proof/manifest.yaml
$L exec -- /tmp/yanet/cli/yanet-cli-counters --module-type decap --format json
$L down
```

Use `flock -o`. A plain `flock` around `up` leaves the lock held by the
forked supervisor and QEMU, which inherit its descriptor, for the whole
session.

### Outputs

Control on the C baseline: `scenario run decap` gives
`PASS probe decap-ipv4`, and the dataplane log has
`load module decap` but no "found in plugin" line.

`manifest run .../decap-rs-boot/manifest.yaml` (all 15 steps pass; output
trimmed):

```
PASS boot     custom-config
PASS step     plugin-loaded
2026-10-09T08:32:49.656 [INFO ][plugin_loader.c:161]: loaded plugin decap from /mnt/yanet2/rust/sdk-poc-ctest/target/release/libdecap_dp.so
PASS step     decap-bound-to-plugin
2026-10-09T08:32:50.804 [INFO ][module_loader.c:36]: module decap found in plugin decap
PASS step     plugin-mapped
73757afc3000-73757affc000 r-xp 00011000 00:2e 6832731  /mnt/yanet2/rust/sdk-poc-ctest/target/release/libdecap_dp.so
PASS step     kni-addresses
...
PASS step     configure-bird-adapter
PASS step     create-lab-function
PASS step     start-pipeline-operator
PASS step     wait-ready
```

`status` then reports `READY`. `scenario run decap` gives:

```
READY
PASS step     configure-decap
[✓] Updated config 'decap_lab'.
PASS step     attach-decap
[✓] Updated function 'fn:lab'.
PASS step     inspect-decap
prefixes: 4.5.6.7/32
1:2:3:4::abcd/128
PASS probe    decap-ipv4
```

`manifest run .../decap-rs-proof/manifest.yaml` passes all steps and
probes:
- `single-dataplane-start`: exactly one plugin binding in the log.
- `decap-bound-to-plugin` and `plugin-mapped`: the binding is still in
  place and the library is still mapped.
- `pipeline`: shows `fn:forward -> fn:decap -> fn:lab -> fn:route`, with
  `decap:decap0` and `decap:decap_lab`.
- `decap-ipv4-again` and `outer-fragment-dropped`: both probes pass.

Decap module counters afterwards (worker 0, `[packets, bytes]`):

| module | rx | tx | drop |
|---|---|---|---|
| `decap0` | 3, 222 | 2, 148 | 1, 74 |
| `decap_lab` | 2, 148 | 2, 108 | 0, 0 |

How the counters read:
- `decap0` has no prefixes. It passed both tunnel packets and dropped the
  fragment: the fragment check runs before the prefix lookup, as in C.
- `decap_lab` stripped the 20-byte outer header from both tunnel packets
  (74 to 54 bytes each).

Both instances are of the plugin-bound type. These counters are not a
Rust-only observable; the Rust module deliberately behaves like C, so the
proof that Rust handled the packets is the plugin binding and mapping of
the live dataplane process.

## Design notes

- **Panic policy.** A panic in a handler aborts the process. The
  trampolines are `extern "C"`, which aborts on unwind since Rust 1.81, and
  release uses `panic = "abort"`. Modules express bad input as
  `Verdict::Drop`.
- **`export_module!(decap, Decap)`** accepts only an ident and a path. It
  emits:
  - `#[unsafe(export_name = "new_module_decap")]` calling the safe
    `yanet_sdk::new_module`;
  - the ABI version static;
  - `YANET_MODULE_NAME` and `YanetModule` for the module's systest.

  The descriptor is `calloc`ed because the loader `free`s it. No commit
  hooks are set. The C module sets an empty commit hook; the loader skips
  NULL hooks, so the two behave the same.
- **No `config_layout` key.** A key of size, alignment and type name in the
  export would only help if the dataplane could compare it with something
  the C control plane wrote. `struct cp_module` has no such field on this
  base, and adding one is a C change, so the key is not implemented. This
  variant relies on two things:
  - the module systest for layout correctness and the name/type binding;
  - the plugin ABI version for the C-library layouts.
- **One attach per invocation.** `Module::attach` builds the views once
  per handler call; `handle_packet` gets them by reference. Each LPM view
  costs a range check and two resolves per invocation, not per packet.
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
  2024. Both systests therefore build as edition 2021, and the mirrors use
  `r#gen` and `r#type`.
- ctest needs one Rust struct per C struct. A body-only mirror needs the
  generated flat shadow and the constant assertions described above, about
  170 lines of build script that every module systest repeats with its own
  constants.
- The standalone expansion resolves field types, so the generated shadow
  carries `cfg(ctest)` placeholders for `Lpm` and `cp_module`, and
  `ffi.rs` puts its zerocopy derives behind `cfg_attr(not(ctest))`.
- zerocopy has no impls for `PhantomPinned`, so `RelPtr` lost its `!Unpin`
  marker; it was not load-bearing, since no `&mut RelPtr` is ever handed
  out.
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
- Mirrors are written by hand: about 320 lines for the C-library slice and
  one small struct per module. Every C change needs a matching Rust edit.
  ctest catches a miss only for items that are mirrored. It does not catch:
  - semantics: whether a C pointer is relative or absolute. Raw `*mut`
    mirrors of relative fields such as the memory-context links are
    documented, not checked;
  - fields behind opaque types;
  - C structs no one mirrored;
  - which body bytes C mutates after publication. A module that mirrors a
    C-mutated field without `Opaque` passes the systest and is unsound.

## Open issues and places the design did not hold up

- **`forbid(unsafe_code)` does not see unsafe from external macros.** It
  misses:
  - unsafe expanded from another crate's `macro_rules!`: with rustc 1.88
    and 1.98, an `unsafe { }` block or `#[unsafe(export_name)]` compiles
    inside a `#![forbid(unsafe_code)]` crate without a lint. The export
    macro relies on exactly this;
  - the `unsafe impl`s that zerocopy's derive, an external proc macro,
    expands on the decap mirror. `decap-rs` builds under `forbid` with
    them.

  The guarantee is therefore "no unsafe written in the module crate, plus
  audited macros from `yanet-sdk` and zerocopy", not "no unsafe reachable".
  Other macro crates would need a dependency policy.
- **The name and layout binding is checked by a test, not by the build.**
  The dataplane hands a module every configuration of its exported type
  name. A module exported under a foreign name would attach its mirror to
  another module's memory, and `FromBytes` would not save it: the size may
  differ, and its relative pointers would be garbage. The previous layout
  rejected that at compile time, because a sealed sys layout carried the
  name. Now the module's systest asserts the name and the mirror type; a
  module crate built without running its systest is not checked.
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
- **Every C-mutable body byte must be `Opaque`, unchecked.** The
  `&B` cast is sound only if every body byte C writes after publication is
  inside an `Opaque` field. That is a property of the C code, which neither
  ctest nor zerocopy can see. In decap, only the trees' memory contexts
  qualify, and the sys mirror of `struct lpm` handles them.
- **The size of the C block is a contract.** The dataplane cannot check
  that the C block is as large as `ModuleConfig<B>`; the systest proves the
  types agree, and the control plane allocates `sizeof` of the C type.
- **Not exercised:**
  - worker-owned mutable zones (`WorkerLocal`, `Worker<'round>`);
  - generation leases;
  - `commit_ectx`;
  - the Go-side validate call;
  - meson/cargo integration.
- **Performance evidence** is a single-core microbenchmark of the lookup,
  not a dataplane throughput measurement.

## Where this is weaker than a layout derive

A layout derive owned by the SDK (`ShmLayout`-style), with a C-side
`config_layout` field, would generate the mirror's accessors and a
structural fingerprint. Against it, this variant gives up:

- **Load-time rejection.** Nothing at load or commit compares the Rust
  mirror with the configuration C built. A derive scheme can reject a
  mismatched configuration by fingerprint before attaching. Here,
  correctness rests on the systest having been run against the same
  headers the dataplane was built from, plus the plugin ABI version.
- **Name/type binding at build time.** The binding is a systest assertion,
  so skipping the systest skips the check. A derive can bind the type name
  at compile time.
- **Classification of C-mutable bytes.** A derive can require every field to
  be declared frozen, atomic or C-mutable and fail on an unclassified one.
  Here, marking C-mutated bytes `Opaque` is the module author's unchecked
  duty.
- **No conjured values.** `FromBytes` lets safe code create `Lpm` and
  `RelPtr` values, so `ConfigView::lpm` needs a run-time range check. A
  derive can generate accessors that only project from the attached root.
- **Per-module tooling.** Each module needs its own systest crate with a
  shadow-generating build script (about 170 lines, mostly boilerplate)
  instead of one derive invocation.
- **Unsafe outside the SDK.** The `unsafe impl`s come from zerocopy's proc
  macro. That is well audited, but it is outside the SDK, and `forbid`
  does not see it; an SDK derive would have the same lint blind spot.

What it keeps:
- The C header stays the source of truth with no C change, so the plugin
  is drop-in for the C control-plane API and `yanet-cli-decap`. The lab
  run shows exactly that.
- The checks run with the real C compiler and meson flags.
- zerocopy's derives are maintained and checked by its own authors.

## Compared with a bindgen sys layer

bindgen generates the mirrors from the headers with libclang at build time,
so layout drift becomes a rebuild instead of a test failure:

- no hand-written `ffi.rs`;
- no C-spelling or opaque-type maps;
- no ctest edition quirks.

The costs move elsewhere:

- libclang becomes a build dependency of every module build; the ctest
  production path needs only zerocopy and its derive, and builds in 8.5 s
  from clean;
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
