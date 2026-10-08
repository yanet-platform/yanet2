# Rust module SDK proof of concept: bindgen sys path, decap

A self-contained workspace (not a member of the root `Cargo.toml`) that ports
the C `decap` dataplane module to Rust on a kernel-style bindgen sys crate.
The module crate is `#![forbid(unsafe_code)]`; every `unsafe` block lives in
`yanet-sys`, including the export macro it provides.

| Crate          | Role                                                                                       |
|----------------|--------------------------------------------------------------------------------------------|
| `yanet-sys`    | bindgen bindings, crate-private `layout!` classification and strict pointer check, `RelPtr`/resolvers, views, packet front, `register_module!`; test support behind the `testing` feature (C arena, image copies, block resolver) |
| `yanet-sdk`    | Safe surface: re-exports and the Rust port of the C `lpm_lookup`; `#![forbid(unsafe_code)]` |
| `decap-rs`     | The module, `cdylib` named `libdecap_dp.so` for the plugin loader; `#![forbid(unsafe_code)]` |
| `decap-oracle` | Test-only: the unmodified C `modules/decap/dataplane/dataplane.c` and C packet construction, compiled from the `DEP_YANET_*` flags |
| `decap-bench`  | Bench-only: per-packet cost of the C module and Rust plugins on identical fronts, see [Bitcode variant](#bitcode-variant-c-helpers-inlined-into-rust) |

## Build and run

Prerequisites: a configured meson build directory (the sys build script reads
its `compile_commands.json`) and a libclang with resource headers.

```sh
git submodule update --init
PKG_CONFIG=/usr/bin/pkg-config meson setup build   # host pkg-config, as the devshell does
meson compile -C build                              # only needed for scripts/e2e.sh
cd rust/sdk-poc-bindgen
cargo test --offline                                # native suite
./scripts/miri.sh                                   # Miri, both borrow models
./scripts/e2e.sh                                    # Rust plugin inside the real dataplane harness
./scripts/asm.sh                                    # Rust vs C machine code
cargo run --release -p yanet-sdk --example lookup_bench
```

`.cargo/config.toml` defaults `LIBCLANG_PATH` to `/usr/lib/llvm-18/lib`: on
the reference host the newest libclang (19) has no resource headers, and
bindgen then fails on `stdatomic.h`. `YANET_BUILD_DIR` and `YANET_ROOT`
override the defaults (`<repo>/build`, the repository root).

## How it fits together

- **One source of C flags.** `yanet-sys/build.rs` takes the include
  directories, defines, forced includes and `-march` of the meson
  `modules/decap/dataplane/dataplane.c` command. The same set drives bindgen,
  the C shim compile (`lib/dataplane/packet/{decap,packet}.c` and the test
  shim) and the gcc layout probe, and is exported as `DEP_YANET_ROOT`,
  `_INCLUDE`, `_DEFINES`, `_FORCED_INCLUDES`, `_MACHINE`, `_COMPILER`
  (`links = "yanet"`). `decap-oracle/build.rs` consumes only that metadata.
- **Strict provenance resolver.** `Resolver::resolve(&RelPtr<T>)` uses the slot
  only for its address: the target is `root.with_addr(slot + offset)`. The
  production `MapResolver` root is the raw `module_ectx *` the dataplane
  passes to the handler; absolute fields (`abs_cp_module`) are also turned
  into pointers by `root.with_addr(addr)`. Packet, mbuf and list pointers live
  outside the mapping and are used as loaded (`ffi` class). No
  `with_exposed_provenance` or integer-to-pointer cast anywhere.
- **`RelPtr<T>` / `RelRef<T>`**: `repr(transparent)` `isize`, private field,
  no constructor, not `Copy`/`Clone`/`Unpin`; views return `&RelPtr` in
  place. `RelRef` is a slot the validated graph guarantees non-null
  (`ADDR_OF_NONNULL`); the LPM projection decides it from the slot flag.
- **`layout!`** (`yanet-sys/src/views.rs`) classifies every field of each
  aggregate the SDK projects into: `rel`, `abs`, `ffi`, `plain`, `embed`,
  `opaque`. The build script walks the bindgen output with `syn`, marks every
  field whose bytes carry a pointer (transitively through nested aggregates
  and unions; opaque bindgen blobs count as pointer-carrying; an unrecognised
  type form or name fails the build instead of counting as plain) and
  generates a const check: a declared aggregate with an unclassified
  pointer-carrying field fails const evaluation with the field name (75 such
  checks over 206 bindgen fields). Declared classes are also checked:
  `rel`/`abs`/`ffi` must be pointers and `plain` must carry none; `plain`
  and `ffi` types must equal the bindgen types exactly, `rel` sizes must
  match. `module_ectx` alone forces 21 pointer fields to be classified. The
  macro is crate-private: generated accessors dereference the view pointer
  and mirror types are constructible from their fields, so a declaration is
  a statement about C memory only the sys crate may make. Integer fields that
  hold addresses or offsets are invisible to the check.
- **No reference over C-mutable bytes.** `frozen` views (`Lpm`,
  `DecapConfig`, `ModuleEctx`) hold a raw pointer and hand out references to
  single declared fields only. `lpm.memory_context` and the `cp_module`
  header are `opaque`, so C may rewrite a sibling link or a registry refcount
  while a worker runs the configuration. `mirror` types (`LpmPage`,
  `LpmValue`) are layout-asserted `repr(C)` copies of fully frozen
  aggregates.
- **Hybrid helpers.** Cold path in C: LPMs are built with the C `lpm_insert`
  (test shim) and `packet_decap` is the C function compiled into the module
  (`Packet::decap`), because porting it means porting mbuf adjustment and the
  inner header parser. Hot path in Rust: the LPM lookup (`yanet-sdk`).
- **Export macro.** `register_module!{ name: decap, config: Decap, handler:
  handle_packets }` emits `new_module_decap` (descriptor allocated with C
  `malloc`, since the loader `free`s it) and `yanet_module_abi_version`
  (generated from `YANET_MODULE_ABI_VERSION`, 33). The grammar accepts only
  identifiers and `::` paths, and the caller's tokens are used only outside
  the macro's `unsafe` block. **Panic policy: abort.** The handler is wrapped
  in `catch_unwind` + `process::abort()` (and the profiles set
  `panic = "abort"`): unwinding into C is undefined and a half-processed front
  has no consistent state to return. A context without a configuration drops
  every packet.

## Results (commands run on this host)

| Check | Command | Result |
|-------|---------|--------|
| Native suite | `cargo test --offline` | all pass: sdk 8 (+4 ignored UB cases), sys 6, decap 4 |
| Miri, strict provenance, Stacked + Tree Borrows | `./scripts/miri.sh` | positive suites pass under both models (6 LPM tests, 2 packet-front tests each) |
| Miri expected-UB cases | `./scripts/miri.sh` | all 8 outcomes as expected, see below |
| Differential LPM, 200k keys x {v4, v6} | `test_lookup_differential_against_c` | Rust == C on the C arena, on a relocated copy after freeing the arena, and C on the copy |
| Differential decap handler | `decap-rs/tests/differential.rs` | 20 named cases (IP-in-IP, IPv6-in-IPv4, GRE with key / checksum + sequence / unknown payload, VLAN, IPv6 tunnels with and without a hop-by-hop header, outer fragments, truncated inner TCP, non-tunnel and non-IP), a batch of all of them and 20 000 random mutations (>10 000 parsable): verdict, bytes, metadata, list order and front counters equal to C; expected verdicts asserted separately |
| Loader + real publish path | `./scripts/e2e.sh` | plugin exports exactly `new_module_decap yanet_module_abi_version`; dataplane_ut loads it from `plugin_dir` (handler address in the `.so`), 6 frames identical to the built-in C decap |
| gcc layout cross-check | `tests/gcc_layout.rs` | 141 entries (15 aggregate size/align pairs, 126 field offset/size pairs) equal; every `layout!` field is probed |
| Unclassified pointer check | `yanet-sys/tests/negative_build.rs` | builds a copy of `yanet-sys` with one declaration line removed or changed: unclassified `lpm.pages`, `lpm.memory_context` and `module_ectx.abs_object_links`, a pointer declared `plain`, a plain field declared `rel` each fail the build with the field named; the unmodified copy builds |
| Macro grammar, forbidden unsafe | `yanet-sys/tests/compile_fail/*.rs` (trybuild) | a block as handler is rejected by the macro grammar; `unsafe` in a module crate is rejected |
| Rust 1.88 | `cargo +1.88 test --offline --target-dir target/msrv` | builds and passes, including the negative builds and the trybuild cases |
| Format, lints | `cargo +nightly-2026-08-28 fmt --all -- --check`, `cargo clippy --offline --all-targets` | clean |

Expected-UB matrix (`-Zmiri-strict-provenance`):

| Case | Stacked | Tree |
|------|---------|------|
| Block resolver, chunk block freed, then lookup | UB (use-after-free) | UB |
| Block resolver, chunk pointer aimed at the last 8 bytes of the header block | UB (beyond allocation) | UB |
| `&lpm` over the whole header held across a C write to its memory context | UB (protected SharedReadOnly) | UB (write forbidden) |
| Resolution with provenance from the slot reference (the PR #2890 model) | UB | passes |

Machine code (`./scripts/asm.sh`, release, x86-64): `resolve_ref` is
`mov; add (%rsi); ret`, the same load + add as gcc's `ADDR_OF_NONNULL`. The
IPv4 lookup has no bounds checks or fences; it is fully unrolled over the four
hops, while gcc keeps a loop and uses `cmov` for the two `ADDR_OF` NULL tests
where Rust has one predictable branch.

Lookup throughput (`lookup_bench`, 1M keys, half inside 4000 v4 / 1000 v6
random prefixes, best of 20, three runs): v4 C 5.9-6.1 ns, Rust 6.7-7.1 ns
(+14-17%); v6 C 14.9-15.8 ns, Rust 15.7-16.0 ns (+1-6%). The C loop is the
inline `lpm_lookup` compiled with `-O2 -march=haswell`; Rust uses the default
x86-64 target. This is a microbenchmark of one helper, not a dataplane
throughput comparison.

Line counts (`wc -l`):

| Part | Lines |
|------|-------|
| `yanet-sys` hand-written library (`src/`, without `testing.rs`) | 1205 |
| `yanet-sys/build.rs` | 628 |
| `yanet-sys` C shim (`shim/`) | 199 |
| `yanet-sys/src/testing.rs` (test support) | 550 |
| `yanet-sdk/src` | 56 |
| `decap-rs/src` (the module) | 88 |
| Tests, oracle, e2e harness, examples, scripts | 1930 |
| Generated: `bindings.rs` / field table and checks / gcc + Rust layout tables | 1594 / 440 / 290 |

Build times (16 cores, separate target directory): clean release build of
`decap-rs` including bindgen and all dependencies 11.4 s; incremental after
editing the module 0.2 s, after editing a `yanet-sys` source 1.6 s, after
touching a bound C header (bindgen, gcc probe and shim rerun) 3.9 s. The
release `libdecap_dp.so` is 410 KiB, most of it std panic and formatting
machinery.

## What this proves, and what it does not

Proven, for the executions tested:

- A module crate under `forbid(unsafe_code)` can implement decap with
  behaviour equal to the C module, as a plugin the existing loader accepts.
- Resolution through a raw FFI root with `with_addr` is accepted by Miri
  under both borrow models with strict provenance, survives copying the image
  and freeing the original, and costs what `ADDR_OF` costs.
- With one Rust allocation per allocator block, Miri reports a dangling or
  overrunning resolution natively; with a single mapping allocation it
  cannot (expected, and why the block resolver exists).
- A whole-aggregate reference over the LPM header would be UB once C rewrites
  the embedded memory context; the field-only view is not.
- bindgen (libclang) and gcc with the meson flags agree on every probed
  layout, and removing or misclassifying a pointer field of a declared
  aggregate fails the build. The check sees pointer types only, not
  integers used as addresses.

Not proven:

- No config validator exists yet. Views trust the graph (the resolver
  constructor's contract); the test-support constructors over arbitrary
  bytes are `unsafe` for that reason. The CP-side api crate that would
  validate edges against `cp_module->agent->arenas[]` before publish is not
  built.
- Resolvers are not branded. A view hands out its resolver, so safe code
  can resolve a slot of one graph through another graph's resolver. With the
  production root every graph is in the one mapping the root covers, so the
  result is the correct target; with two separate test images it is UB, which
  is one reason the test support is feature-gated. The block resolver panics
  instead. Generative branding is open.
- `decap-rs` drops a packet whose first segment is shorter than the outer
  IPv4/IPv6 header; the C module reads such a header past the segment
  unchecked. The differential tests only use frames the C parser accepts, so
  they never reach this difference. Multi-segment mbufs are not exercised.
- Miri never executes C: fixtures are C-built images replayed in Rust, and
  the claim that the `module_ectx *` handed over by C carries mapping-wide
  provenance is an FFI assumption, not something Miri checks.
- The e2e harness drops every frame after the decap stage (the output
  pipeline has no route); it compares bytes and metadata, while verdicts are
  compared by the handler-level differential test.
- No packet-loop throughput benchmark of the whole module, no TSan/ASan run.

## Friction found

- `forbid(unsafe_code)` does not fire on code expanded from another crate's
  macro: `#[unsafe(export_name)]` and the macro's `unsafe` block compile in the
  module crate (the compile-fail case shows only the user's own block
  rejected). The macro input grammar is therefore part of the safety
  boundary, and is restricted to identifiers and paths.
- A `#[macro_export]` macro produced by `include!` cannot be referred to by
  `$crate::` path, so the generated per-field check is a `const fn` with one
  literal panic per pointer field rather than a generated macro.
- `layout!` had to become crate-private: an exported macro whose expansion
  contains `unsafe` lets a `forbid(unsafe_code)` crate build a view over an
  arbitrary pointer (found in review). The negative build test therefore
  compiles a mutated copy of the sys crate instead of a trybuild case, and
  module-defined config layouts need a different, non-`unsafe`-expanding
  declaration surface.
- trybuild snapshots of const-evaluation errors differ between rustc 1.88
  and 1.98 (the panic text moves from the header to a label), so the layout
  negative tests match message substrings instead.
- bindgen picked the newest libclang (19) without resource headers; the
  build needs `LIBCLANG_PATH`. meson needs the host `pkg-config` for
  `yaml-0.1`.
- `unused_crate_dependencies = "deny"` applies to every test target, so test
  files carry `use x as _;` lines; `clippy --all-targets` builds the
  library's test target even with `test = false`.
- `Option<&T>` from a resolved pointer kept a NULL test in the loop until
  `assert_unchecked(!ptr.is_null())` was added in the resolver.
- `cargo test` does not build a `cdylib`, so loader checks live in
  `scripts/e2e.sh`.

## Open issues

- CP-side Rust api crate with full graph validation before `agent_update_modules`.
- Module-owned config layouts: here the decap layout is C-defined and bound
  in `yanet-sys`; a Rust-defined config needs a declaration surface in the
  module crate whose macro input stays identifier-only.
- Resolver branding: views of two different graphs share the resolver type;
  mixing them is harmless with one mapping-wide root, UB across separate
  test images, and not prevented by types.
- Worker-owned zones (`Worker<'round>`, prepared buffers, counters) are not
  modelled; decap needs none.
- `commit_handler`/`commit_ectx_handler` are left NULL; the header check and
  its defined failure state are not implemented.
- Meson integration of cargo (per-module `.so`, pinned toolchain) and the
  ctest-style comparison PoC.

## Bitcode variant: C helpers inlined into Rust

An opt-in build compiles the C helpers the module calls per packet to LLVM
bitcode and lets lld inline them into the Rust handler at link time, the
idea behind the kernel's `RUST_INLINE_HELPERS`. The default build is
unchanged and keeps working with any toolchain.

### Mechanism

- **Stable cross-language ThinLTO.** With the `bitcode` feature
  (`decap-rs/bitcode` → `yanet-sdk/bitcode` → `yanet-sys/bitcode`), the sys
  build script compiles the shim with `clang-20 -flto=thin` (same include
  directories, defines and `-march` as the meson decap module, archived by
  `llvm-ar-20`). `.cargo/bitcode.toml` builds Rust with
  `-Clinker-plugin-lto -Ctarget-cpu=haswell`, links with `clang-20
  -fuse-ld=lld` and raises the ThinLTO import limit. lld then runs one LTO
  over Rust and C bitcode and emits the cdylib. The kernel-style fallback
  (`llvm-link` of `.bc` helpers into the crate bitcode) was not needed.
- **Three levers were needed, each found from inliner remarks**
  (`-Wl,--plugin-opt=opt-remarks-filename=...`):
  1. ThinLTO imports only functions below 100 instructions: `packet_decap`
     was "definition unavailable" to the Rust module until
     `-import-instr-limit=1000`.
  2. Once imported, the cost model declined it ("TooCostly", cost 625,
     threshold 625; `parse_ipv6_header` into it: 400 vs 325). The C module
     calls it out of line too, since it lives in another unit.
     `yanet-sys/shim/inline_helpers.c` compiles the unmodified `decap.c` and
     `packet.c` into one unit with `always_inline` merged into the
     declarations of `packet_decap`, `parse_ipv4_header` and
     `parse_ipv6_header`. No repository C file changes.
  3. The Rust wrapper `Packet::decap` is `#[inline(always)]` in this build
     only (`cfg_attr`), or the body moves into the wrapper and the call
     stays.
- **Version rule: one LLVM major on all three sides.** rustc 1.88.0 is LLVM
  20.1.5, clang-20 and lld-20 are 20.1.2. Stable rustc 1.98 is LLVM 22 with
  no matching distribution clang, so the build pins `+1.88.0`. The sys build
  script (`yanet-sys/build_bitcode.rs`) fails the build with a message when:
  - rustc's LLVM major has no `clang-<major>` (stable: "rustc uses LLVM 22
    and no clang-22 is installed");
  - `YANET_BITCODE_CLANG` points at another major;
  - the linker is not clang of that major or lld is another major;
  - `-Clinker-plugin-lto` is missing;
  - the last `-Ctarget-cpu` is not the meson `-march` (LLVM refuses to
    inline a callee built for more CPU features than its caller).

### Commands

```sh
./scripts/bitcode.sh     # build, check exports and inlining, run the tests
./scripts/bench.sh       # handler and LPM benchmarks, pinned to BENCH_CPU (2)
# by hand:
cargo +1.88.0 --config .cargo/bitcode.toml build --release -p decap-rs --features decap-rs/bitcode
cargo +1.88.0 clippy --config .cargo/bitcode.toml --all-targets --features decap-rs/bitcode
```

The plugin is `target/bitcode/x86_64-unknown-linux-gnu/release/libdecap_dp.so`.
`cargo clippy` does not forward a `--config` given before the subcommand;
put it after `clippy`. `bitcode.sh` skips the trybuild cases: trybuild
rebuilds the crate with the feature but without this build's link flags, and
the build script then refuses it. They do not depend on the C toolchain and
run in the default build.

### Evidence that the inlining happened

`scripts/bitcode.sh` checks the release cdylib:

- it exports exactly `new_module_decap` and `yanet_module_abi_version`;
- no `packet_decap`, `parse_ipv4_header` or `parse_ipv6_header` symbol is
  left in it;
- the handler trampoline (the Rust handler is inlined into it, 700
  instructions) calls none of them, and does call `memmove` through the GOT
  itself: that call is the Ethernet header move inside `packet_decap`.

The same checks fail on the default build, which keeps all three helpers
out of line. Calls left in the bitcode handler: `memmove` (the C module
calls it too) and the Rust packet-front accessors `PacketFrontRaw::{input,
output, drop}`, which `PacketFront::list` takes as function pointers.

### Correctness

- Every PoC test passes in this mode (`bitcode.sh`, release): differential
  decap against C (20 cases, batch, 20 000 mutations), LPM differential,
  packet front, gcc layout cross-check, negative builds. In this mode the
  oracle's C module calls the clang-compiled `packet_decap`.
- `scripts/e2e.sh`'s dataplane_ut harness, pointed at a plugin directory with
  the bitcode library: 6 frames identical to the built-in C decap.
- Miri does not execute C, and the bitcode feature only changes C
  compilation and one inline attribute, so `scripts/miri.sh` runs on the
  default build: all positive suites and the 8 expected-UB outcomes as
  before.
- Not run: ASan/UBSan/TSan of the bitcode build. Sanitizer instrumentation
  would have to be enabled consistently in rustc (nightly `-Zsanitizer`) and
  clang across the LTO unit.

### QEMU lab

`lab/decap-bitcode-boot` and `lab/decap-bitcode-proof` follow the ctest
PoC's lab manifests, with `plugin_dir` set to the bitcode release directory.
The boot manifest also checks that the mapped library has no `packet_decap`
symbol (the default build has one). With the worktree's full `make all`
build, QEMU 11.1.1 and KVM:

```sh
export YANET_QEMU_IMAGE=$PWD/tests/functional/yanet-test.qcow2   # from the repository root
L="flock -o /tmp/yanet2-lab.lock just lab --session bitcode-poc"
$L up && $L scenario run decap                      # control: C module
$L reset
$L manifest run rust/sdk-poc-bindgen/lab/decap-bitcode-boot/manifest.yaml
$L scenario run decap                               # the same scenario, bitcode Rust module
$L manifest run rust/sdk-poc-bindgen/lab/decap-bitcode-proof/manifest.yaml
$L down
```

Result: the dataplane log has `loaded plugin decap from
/mnt/yanet2/rust/sdk-poc-bindgen/target/bitcode/x86_64-unknown-linux-gnu/release/libdecap_dp.so`
and `module decap found in plugin decap`, the library is mapped in the
dataplane process, and the unmodified `decap` scenario passes on it. The
proof manifest passes: one dataplane start, the scenario packet decapsulated
again, the outer fragment dropped (decap0 counters: rx 3, tx 2, drop 1).

### Benchmark

`decap-bench` runs the C module (`modules/decap/dataplane/dataplane.c` as
compiled into the oracle: gcc 13.3, `-O2 -march=haswell`, the meson flags,
`packet_decap` from a separate gcc unit, as in the dataplane) and every
plugin through the same C handler pointer:

- **Input.** 2048 C-parsed packets: 60% tunnels to a decap prefix
  (IP-in-IP, GRE with key, IPv6 in IPv6, IPv4 in IPv6), 30% TCP/UDP to
  uncovered addresses, 5% outer fragments, 5% ARP. They are cut into fronts
  of 32 or 64 against 1000 IPv4 prefixes (/16-/32) and 500 IPv6 prefixes
  (/32-/64).
- **Timing.** Each front is restored from a snapshot right before its call,
  so the packets are cache-warm. Only the handler call sits between
  `lfence`-serialised `rdtsc` reads, and the timer overhead (26 cycles) is
  subtracted.
- **Sampling.** A sample is 50 passes over all fronts (102 400 packets).
  There are 31 samples per variant, round-robin across variants after a
  warm-up sample whose timings are discarded, pinned with `taskset -c 2`.
- **Agreement.** A hash of every packet's bytes, metadata, list placement
  and the front counters must equal the C module's, or the run fails.

All Rust variants use `-Ctarget-cpu=haswell` (the meson `-march=haswell`):

| Variant | Build |
|---------|-------|
| `bindgen` | the default build, rustc 1.88, no LTO, C helpers out of line |
| `bindgen-stable` | the same with rustc 1.98.0, which also links with its bundled rust-lld |
| `bindgen-lto` | rustc 1.88, fat Rust LTO (`--crate-type cdylib`, see below), C helpers out of line |
| `bitcode` | this variant, rustc 1.88, C helpers inlined |

Host: Xeon Gold 6230 (Cascade Lake), 16 CPUs, shared with other jobs. The
load average was 0.4 during run 4 and 2.1-2.3 during runs 2 and 3; run 1
overlapped a load peak above 20 and is left out. There is no isolated CPU
and the frequency is not pinned, so treat differences under about 3% as
noise. Values are ns per packet over runs 2-4 with fronts of 32, and one
run with fronts of 64:

| Variant | best | median (3 runs) | p10-p90, run 3 | Mpps (median) | vs C (median) | front 64: median, vs C |
|---------|------|-----------------|----------------|---------------|---------------|------------------------|
| C | 54.8 | 56.4-59.0 | 54.9-59.6 | 16.9-17.7 | — | 58.9 |
| bindgen | 85.4 | 88.1-91.0 | 86.3-94.1 | 11.0-11.3 | +54 to +56% | 89.0, +51% |
| bindgen-stable | 61.4 | 63.3-66.4 | 62.0-65.8 | 15.1-15.8 | +11 to +12% | 66.5, +13% |
| bindgen-lto | 53.6 | 55.5-59.1 | 54.2-59.8 | 16.9-18.0 | −2.5 to +0.1% | 58.5, −0.7% |
| bitcode | 54.7 | 56.0-58.9 | 54.8-58.4 | 17.0-17.9 | −1.3 to −0.2% | 58.7, −0.4% |

What the numbers say:

- **The C call is not where the bindgen variant loses.** `bindgen-lto`
  still calls `packet_decap` out of line and is as fast as C, and so is
  `bitcode`. Inlining the C helpers adds nothing measurable over Rust-only
  LTO: `bitcode` was within 1.2% of `bindgen-lto` in every run, on either
  side.
- **The loss is Rust to Rust.** Without LTO, rustc 1.88 leaves every
  `layout!` accessor (`PacketRaw::mbuf`, `MbufRaw::data_off`,
  `PacketListRaw::first`, ...) an out-of-line cross-crate call through the
  GOT: 36 calls in the 1.88 `handle_packets` against 4 with rustc 1.98,
  whose cross-crate inlining takes most of them. In a `perf` profile of the
  bindgen and bitcode handlers in one run, the bindgen handler body has
  14.8% of the samples, its out-of-line `packet_decap` 0.9%, and the whole
  bitcode handler 11.6%.
- **Cargo does not apply `lto` to `decap-rs`**, because the crate is also
  an `rlib` (for the differential tests): `CARGO_PROFILE_RELEASE_LTO=fat
  cargo build` passes no `-C lto` to it. `bench.sh` builds `bindgen-lto`
  with `cargo rustc --crate-type cdylib`. The bitcode build gets cross-crate
  ThinLTO over the workspace crates anyway, because lld runs it over all
  bitcode it links (the precompiled std takes part as object code).
- One-off experiments (single runs, front 32, not in `bench.sh`):
  - the bitcode build with the helpers marked `noinline` instead of
    `always_inline`: +3.4% vs C;
  - linker-plugin LTO with the C helpers left as gcc objects (bitcode config
    without the feature): +7.4%;
  - the default build linked by lld: no change;
  - the default build with `-x86-branches-within-32B-boundaries`: no
    change.

LPM lookup micro-benchmark (`lookup_bench`, best of 20 x 1M keys, same
CPU). Rust is rustc 1.88 with `-Ctarget-cpu=haswell`; the C loop is the
test shim's inline `lpm_lookup`:

| Build | IPv4: C / Rust ns | IPv6: C / Rust ns |
|-------|-------------------|-------------------|
| default (C from gcc) | 6.18 / 7.64 (+24%) | 16.00 / 17.23 (+8%) |
| bitcode (C from clang-20, LTO) | 5.37 / 7.62 (+42%) | 14.95 / 17.29 (+16%) |

The Rust lookup is pure Rust and unaffected by the build; the C side gets
faster from clang. The handler benchmark does not show this gap, because
there the lookup shares the time with header loads and list handling.

### CI

`.github/workflows/rust-sdk-poc-bitcode.yml` runs on ubuntu-24.04:

1. installs `clang-20 lld-20 llvm-20` with the meson dependencies through
   `.github/actions/apt-packages`, the cached apt helper the hosted-runner
   Rust jobs use (the CI base image serves the container builds, and adding
   LLVM 20 there would grow every image for one job);
2. pins rustc 1.88.0 and asserts that rustc, clang-20 and lld-20 report
   one LLVM major;
3. configures meson (`-Ddataplane_only=true`, no compilation) for the
   compile database;
4. runs `scripts/bitcode.sh`: build, export and inlining checks, tests.

Its paths cover the PoC and every repository source and header the helpers,
bindings and oracle include. The benchmark is not run in CI (too noisy on
shared runners).

### Limitations

- **Toolchain coupling.** The bitcode build is tied to a rustc whose LLVM
  major has a matching distribution clang and lld: today rustc 1.88, ten
  releases behind stable 1.98. A production build would pin rustc and
  clang to one LLVM, or build clang from rustc's LLVM sources.
- **Forced inlining is a choice per helper.** The cost model declines
  `packet_decap` with LTO alone. Forcing it into both call sites grows the
  handler from 1240 to 2951 bytes (2456 counting the three out-of-line
  helpers) and, here, buys nothing measurable. Inlining pays for small
  helpers; the decap module has no such per-packet C call.
- **The flags live in a cargo config, not in the crate.** A feature cannot
  set the linker or rustflags, so builds go through `--config
  .cargo/bitcode.toml`, and the build script refuses the feature without
  them. trybuild cases must run in the default build.
- **No sanitizer or Miri coverage of the bitcode build** (see Correctness).
- **A production meson integration would need** a pinned rustc and clang
  pair in the build environment; the shim compiled by meson's clang to
  bitcode (or by the sys build script as here); the cdylib linked by
  clang+lld with the same `-march`; and the inlining check as a meson test.
  The C module calls `packet_decap` out of line too; meson's `b_lto` is off.
