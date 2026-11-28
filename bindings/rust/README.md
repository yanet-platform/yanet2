# Rust module SDK

A cargo workspace, separate from the CLI workspace at the repository root,
with its own `Cargo.lock` and `rust-version = "1.100"`, for writing
dataplane modules in Rust: the boundary crate both sides of the SDK
share, the C bindings with the dataplane runtime, the derive, the
module-crate facade and the packet header views. The control plane that
builds, publishes and reads the configurations is the native Go one
(`controlplane/native`), over the Go views generated from the layouts.

| Crate | Environment | Role |
|-------|-------------|------|
| `yanet-shm` | `no_std`, no `alloc` | the provenance root, relative-pointer field types, `Opaque`, `Mutex` with guard-bound access to plain shared state, the slots of an exact-match index and the `HashIndexRef` lookup, `ShmLayout` leaves (the fixed-width integers and their atomics, `isize`, and the IPv4 and IPv6 addresses of `core::net`, whose layout the build checks), the layout description the Go views are generated from |
| `yanet-sys` | `no_std`, no `alloc` | the C bindings of the SDK, modules mirroring the C tree, one bindgen output (generated into `OUT_DIR`, only here) and one module per C library: `common` (LPM type with its lookup by an array, a `core::net` address of the key's length or a key type of a module, and the exact-match `HashIndex`, whose slots a memory context of its own holds), `counters` (the counter value address), `dp` (bindings of `lib/dataplane`, the `Module` trait, `Cx`/`CommitCx`/`ObjectCommitCx`, `Batch`/`Packet` with the packet operations (allocation, copy, freeing, resizing, parsing, tunnels), counters, devices and object links (`ObjectLink`, resolved through `ModuleCx`), an object's own counters (`ObjectCounter`, resolved by the object's prepare step through `ObjectCommitCx`), counter layouts, which declare whether a counter counts or holds a level (`u64` and `Traffic` counts, a `Gauge` level and `[T; N]` values under one name), and the `BoundCounter` handle bound to read and add, which also takes from a gauge and picks a value of `[T; N]` by a constant index, per-worker state (`PerWorker` slots and `PerWorkerSlice` regions, one block per worker, reached only through the `WorkerSlot` and `WorkerSlice` handles a prepare step makes through `PrepareCx` and the handler uses with its `Cx`; a module's prepare finds its worker through the dp shim's `yanet_sys_dp_module_worker_idx`, an object's reads its context's; no author API names a worker index or count), the `Object` trait, the handler runtime, `export_module!` and `export_object!`); the C shim of the dataplane |
| `yanet-sdk-derive` | proc macro | `#[derive(ShmLayout)]`: the layout contract check and the layout description |
| `yanet-sdk` | `no_std`, `forbid(unsafe_code)` | the module-crate surface: `Module`, `Object`, contexts, batches, field types (`Mutex` and its `MutexGuard`, `HashIndex` with its `HashIndexRef`, `Counter`, `TaggedCounter`, `TaggedCounters` with its prepared `TaggedCountersRef` handle, `ObjectCounter`, `Device`, `PerWorker` and `PerWorkerSlice` with their prepared `WorkerSlot` and `WorkerSlice` handles, ...) and the derive |
| `yanet-layout` | `std`, outside the default members | the generator of the Go views of the layouts, and its binary that lists the fixture modules' roots |
| `yanet-sdk-plugin` | `no_std` | a dataplane plugin's runtime: the panic handler (C `abort`), `export_module!` and `export_object!`, so a plugin is a `#![no_std]` cdylib of one line (a module's line lists the object types its plugin carries; an object type that ships without a module is a plugin of its own), built under `--profile plugin` (`panic = "abort"`) |
| `yanet-sdk-testing` | `std`, dev-only | the block arena (one region, as one mapping) and resolving context of the tests that lay dataplane memory by hand, Miri included |
| `yanet-sdk-fixture` | library `no_std`, `forbid(unsafe_code)` | minimal modules, `Fixture` (a test program whose mode word selects what it does with a batch), `Tally` (a prepared value and counters, those of tagged registries included, by id among them), `Fib` (forwards by a route FIB of two C LPM tables), `Assign` (classify), `Hosts` (counts by an exact-match `HashIndex`) and `Stash` (writes a linked `State`), and the object type `State` (atomic slots and their prepared seed, and a counter of its own that `Stash` keeps as a gauge), with no Rust tests: its Go tests are in `tests/dataplane`; their plugins `lib{fixture,tally,fib,assign,hosts,fixture_state,stash}_dp.so` and `libstash_state_plugin.so` (`Stash` with `State`, named outside the loader's pattern) are examples of one line over `yanet-sdk-plugin`, built on request (`--features plugin`, `--profile plugin`) |
| `yanet-packet` | `no_std` | packet header views, extractable to its own repository |

## Objects

An object type (`Object`, `export_object!`) is state the control plane builds,
publishes by name and releases. A module configuration links it by type and
name in an `ObjectLink` field, which the handler and the prepare step resolve
to the object's configuration in the running generation and to the object's
prepared value: `Object::prepare` makes it per worker context, run by the
object type's commit hook, which `export_object!` registers only for a
non-zero-sized value. An object's counters of its own are `ObjectCounter`
fields, which readers find by the object's type and name and C carries over to
an object of the same type and name that replaces it; only the object's
prepare step resolves one, into a `CounterRef` its prepared value keeps for
the handlers of the modules linking it. An object type ships in the plugin of a
module that lists it,
`export_module!(name = .., module = .., objects = [<type> = <path>])`, as a C
module's plugin carries its object types, or in a plugin of its own through
`export_object!`: every plugin exports one `yanet_module_abi_version`.

## Build

The build script of `yanet-sys` reads the C flags of the dataplane
(compiler, include directories, defines) from the `compile_commands.json`
that `meson setup build` writes at the repository root; `YANET_BUILD_DIR`
names another build directory, and `.cargo/config.toml` of this directory
defaults it to `build`. It also needs a clang
and a libclang of one LLVM major (`CLANG_PATH`, `LIBCLANG_PATH`); CI uses 23.
The same build script compiles the dataplane C shim, with the packet
parsing and encapsulation sources of `lib/dataplane/packet` it calls, with hidden
visibility and the warning options of the C build, `-Werror` included. A
plugin imports the packet allocator, copy and free (`worker_packet_alloc`,
`worker_clone_packet`, `worker_packet_free`) from the dataplane executable,
which exports them. The tests of `yanet-shm` need neither the compilation
database nor a C compiler.

```sh
meson setup build
cd bindings/rust
export CLANG_PATH=/usr/bin/clang-23 LIBCLANG_PATH=/usr/lib/llvm-23/lib
cargo test --workspace                      # native suites
MIRIFLAGS="-Zmiri-strict-provenance" cargo +nightly-2026-10-22 miri nextest run --workspace --exclude yanet-packet --exclude yanet-layout   # Miri (cargo-nextest); add -Zmiri-tree-borrows for Tree Borrows; yanet-packet forbids unsafe code; yanet-layout writes files, which Miri isolation refuses
cargo bench -p yanet-sys --bench dp --no-run                                        # dataplane hot path, built on every core
taskset -c 2 cargo bench -p yanet-sys --bench dp -- --save-baseline main            # run pinned to one core, -- --baseline main on the change
cargo bench -p yanet-sys --bench dp --profile plugin --no-run                       # adds the plugins' thin LTO, keeps unwinding
taskset -c 2 cargo bench -p yanet-sys --bench dp --profile plugin -- --save-baseline main-plugin   # a baseline of its own, -- --baseline main-plugin on the change
```

The `lookup` benchmark times classifier lookups, one key and a burst at a
time, over trees the native Go builder of the `assign` fixture lays out in
one block: a Go test writes the block, with the keys and the first rule each
matches by a first-match scan of the rules, to the file
`YANET_CLASSIFY_LOOKUP_TREES` names, and the benchmark checks its lookups
against those rules before it times them. It needs a `build/` configured with
the fixtures (see below); CI runs the checks alone, without timing, through
`make test-classify-bench`:

```sh
make bench-classify                                   # from the repository root: the Go step, then cargo bench
make bench-classify BENCH_ARGS='--save-baseline main' # criterion's arguments, --baseline main on the change
make test-classify-bench                              # the checks only, unoptimised and optimised, as CI runs them
```

## Classifier table checks

Classifier lines, row directories and columns check indices in debug builds.
An invalid index panics with the index and bound before an invalid access.
Optimized builds retain unchecked lookups.

The checks rely on C-built dimensions, offsets and allocation extents. They
do not validate arbitrary corrupted storage, and do not change LPM reads.

The lookups against first-match rulesets are covered from Go, through the
`assign` fixture (see below).

## Tests against the C dataplane

The behaviour of the runtime against the real C dataplane is tested from Go,
not from Rust. The tests are the package in `yanet-sdk-fixture/tests/dataplane`
(cargo ignores a `tests/` subdirectory without `main.rs`), behind the build tag
`sdk_rust_fixtures`: they start the harness of `bindings/go/dataplane_ut`, load
the fixture plugins, and build, publish and read the configurations through the
native Go control plane: the fixture builders of
`controlplane/native/sdk/sdkfixture` over the Go views of the fixture modules'
layouts, and agent attach, publication, counters and memory accounting of
`controlplane/native`. The harness only runs the dataplane.

Meson builds what they need, through cargo, only when its option is on: the
directory of the plugins `lib{fixture,tally,fib,assign,hosts,fixture_state,stash}_dp.so`,
which holds nothing else the loader picks up: `libstash_state_plugin.so`, the
stash module with the state type in one plugin, is named outside its pattern,
and a test links it into a directory of its own; and, with `make native-layout`,
the Go views. `cargo test` and Miri never build the plugins: a `no_std` cdylib
cannot be built with unwinding, so they have a profile of their own.

```sh
meson setup -Dsdk_rust_fixtures=enabled build  # or `meson configure` in place
make native-layout
make test
```

`make test` adds the tag when the configured build has the fixtures. When
`GO_TEST_PACKAGES` is set, `make test-only` and `make test-asan-only` run just
those Go packages and skip the meson tests; CI sets it to the test package and
`./controlplane/native/sdk/...`.

An ASan C build needs the Rust code instrumented too. That takes a nightly
toolchain:

```sh
RUSTUP_TOOLCHAIN=nightly-2026-10-22 make test-asan
```

Miri stays the layer for the language rules of the unsafe code; these tests
add bounds, use after free and the C behaviour, on real memory. CI runs them
natively in `.github/workflows/rust-sdk-ut.yml` and under ASan in
`.github/workflows/rust-sdk-ut-asan.yml`.

## Go views of the layouts

The native Go control plane reads a configuration through Go views generated
from the layout description of its `ShmLayout` types: the derive gives rustc's
own offsets of every field, private ones included, and each SDK field type its
members over placeholder type arguments. `HashIndex` takes no type argument
in its description: its slots hold the key, so a module's indices take one key
type. `Mutex` is its 32-bit lock word and its value, which Go places by the
value's alignment as Rust does; the native control plane takes the lock with
`shm.Lock`. A generic struct reached in several instances has a view of each,
named with a suffix of its type arguments (`Pair4x16`). A type without a
description and an atomic narrower than 32 bits fail generation.
`yanet-layout` lists the fixture modules' roots;
`make native-layout` runs it, with the plugins' profile and target, when the
fixtures are on (see the `sdkfixture` package for what it writes). Views for
arm64 come from `make native-layout` in a build directory configured on arm64.

## Worker counter cells

Counter layouts use typed worker-local `Cell<u64>` storage: one cell for
`u64` and `Gauge`, packet then byte cells for `Traffic`, and recursive arrays
in row-major order. Adds and gauge subtraction wrap. Prepared handles retain
mapping-provenance pointers and only become shared cell views when bound to a
worker call. Copies and overlapping direct or bound views observe the same
worker values. Bound views stay call-branded, neither `Send` nor `Sync`, and
preserve both unwind-safety markers. Multi-value updates are not transactional.
Zero-length arrays retain word alignment without accessing storage. C counter
registration rejects zero values, so zero-sized cell-view support does not make
zero-value counters registerable.

This changes the sealed, hidden layout API: `CounterLayout::Storage: 'static`
and safe `add(&Storage, Value)` replace unsafe `add_to(Value, *mut u64)`, and
`read` now takes `&Storage` rather than a raw pointer. `BoundCounter`, its
`Copy`/`Clone` impls, and `CounterRef::bind` require `T: CounterLayout`.
Other configuration and prepared-handle generic domains are unchanged.

## Graph resolution

`Resolve<'g>` is a safe delegation trait returning an invariant `Graph<'g>`
token. Existing module code taking `&impl Resolve<'g>` is unchanged. A custom
context implements `graph()` by copying a token from a legitimate context of
the same brand, rather than implementing an unsafe trait. `Graph` is `Copy`,
neither `Send` nor `Sync`, and exported beside `Resolve` by `yanet-sdk`.

`Graph::new` is unsafe. Its issuer guarantees mapping-wide raw provenance and
that every accessible graph stays published and frozen for the token's
lifetime, except for opaque bytes, atomics and guarded mutex payloads.
Worker-local slots and regions retain their call-borrowed access discipline.
Compatible sibling graphs in the same mapping remain resolvable.

`ShmRoot::from_mapping` is now safe because it only stores a base for raw
pointer computation. It does not validate a mapping or prove liveness,
publication or aliasing. Those obligations belong to `Graph::new` and actual
dereferences. Constructing a raw root alone never
authorizes graph resolution.

External implementations replace `unsafe impl Resolve` and its hidden
`shm_root()` method with an ordinary implementation returning `Graph` from
`graph()`. Remove obsolete unsafe blocks around `ShmRoot::from_mapping`.

## Dataplane attachment

The callback boundary captures the incoming raw mapping provenance before
borrowing individual structures. Its type-bound attachments give contexts
shared references to frozen module/object execution-context bytes and give
the batch exclusive ownership of its packet front. Module and object APIs
are unchanged.

The worker remains an opaque, call-branded pointer because C can change
unrelated fields. Safe TCB accessors read only its stable index and round
time on demand. Modules that do not use them perform no eager snapshot work.

Prepared output is a separate allocation and stays raw until the prepare
callback returns its initialized value. C must not reassign a context while
a callback is live, even when reassignment would write identical values.
Redirect temporarily reborrows the exclusive front and changes pointed-to
scheduling state, not the referenced execution-context or target-table bytes.
The runtime never borrows the whole generation or mutable scheduling entries
as shared references.

Counter and link projections keep the graph brand and registered type.
Empty tagged fields and missing registries do not reference their storage
table. A zero-sized prepared value does not read a missing prepared buffer.
The `dp` benchmark's harness group also covers whole-front passing and reads
of stable worker fields.
