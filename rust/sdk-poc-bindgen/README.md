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
