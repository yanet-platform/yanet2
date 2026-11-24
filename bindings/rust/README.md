# Rust module SDK

A cargo workspace, separate from the CLI workspace at the repository root,
with its own `Cargo.lock` and `rust-version = "1.100"`, for writing
dataplane modules in Rust: the boundary crate both sides of the SDK
share, the C bindings with the dataplane runtime and the configuration
builder, the derive, the module-crate facade and the packet header views.

The C header generator in `yanet-sdk-build/` is a separate host workspace. API crates still use it as a build dependency at the same path. Keeping it out of the SDK's target graph lets Cargo check the entire SDK workspace without enabling the generator's transitive `std` features in dataplane plugins.

| Crate | Environment | Role |
|-------|-------------|------|
| `yanet-shm` | `no_std`, no `alloc` | the provenance root, relative-pointer field types, `Opaque`, `Mutex` with guard-bound access to plain shared state, the slots of an exact-match index with their placement and the `HashIndexRef` lookup, `ShmLayout` leaves (the fixed-width integers, their atomics and the IPv4 and IPv6 addresses of `core::net`, whose layout the build checks) |
| `yanet-sys` | `no_std`, no `alloc` | the C bindings of the SDK, modules mirroring the C tree, one bindgen output (generated into `OUT_DIR`, only here) and one module per C library: `common` (LPM type with its lookup by an array, a `core::net` address of the key's length or a key type of a module, and its construction by the C routines, and the exact-match `HashIndex`, whose slots a memory context of its own holds), `counters` (the counter value address), `dp` (bindings of `lib/dataplane`, the `Module` trait, `Cx`/`CommitCx`/`ObjectCommitCx`, `Batch`/`Packet` with the packet operations (allocation, copy, freeing, resizing, parsing, tunnels), counters, devices and object links (`ObjectLink`, resolved through `ModuleCx`), an object's own counters (`ObjectCounter`, resolved by the object's prepare step through `ObjectCommitCx`), counter layouts, which declare whether a counter counts or holds a level (`u64` and `Traffic` counts, a `Gauge` level and `[T; N]` values under one name), and the `BoundCounter` handle bound to read and add, which also takes from a gauge and picks a value of `[T; N]` by a constant index, per-worker state (`PerWorker` slots and `PerWorkerSlice` regions, one block per worker, reached only through the `WorkerSlot` and `WorkerSlice` handles a prepare step makes through `PrepareCx` and the handler uses with its `Cx`; a module's prepare finds its worker through the dp shim's `yanet_sys_dp_module_worker_idx`, an object's reads its context's; no author API names a worker index or count), the `Object` trait, the handler runtime, `export_module!` and `export_object!`), `cp` (mirror of `lib/controlplane`: the configuration `Builder` and the `ObjectBuilder` over one build core, with its `Copy` `Scope` handle, kind-tagged so that a module scope registers counters and devices and links objects and an object scope registers counters of the object's own only, and the `Owned`/`OwnedSlice`/`OwnedSlots`/`OwnedRegions` blocks it allocates, the `Built` configuration and the `BuiltObject` it returns, the `Owner` seam to the C control plane (`Header`, `Context` and `BlockAddr` newtypes for its addresses) and the release of a build by walking its owning edges; makes no C call itself but those of the C objects a configuration embeds, such as the LPM); the C shims of `common/` and the dataplane |
| `yanet-sdk-derive` | proc macro | `#[derive(ShmLayout)]`: the layout contract check, `Build` and the `<Type>Parts` struct a build writes from; `#[derive(FfiRecord)]` and `#[yanet_sdk::api]` |
| `yanet-sdk` | `no_std`, `forbid(unsafe_code)` | the module-crate surface: `Module`, `Object`, contexts, batches, field types (`Mutex` and its `MutexGuard`, `HashIndex` with its `HashIndexRef`, `Counter`, `TaggedCounter`, `TaggedCounters` with its prepared `TaggedCountersRef` handle, `ObjectCounter`, `Device`, `PerWorker` and `PerWorkerSlice` with their prepared `WorkerSlot` and `WorkerSlice` handles, ...), the derive and the builders |
| `yanet-sdk-api` | `std` | the control-plane side of a module: `Agent`, a non-owning handle on an attached C agent that owns the configurations and objects an API builds (the builder's `Owner` over the C routines a C module's API calls, the release of a configuration as those do, the release of an object as a C object's owner does, and `Agent::read`, the control-plane read of an object's body under a branded `ObjectCx` its edges resolve through), `Error`, the C error chain, and the guards and conversions the exports of `#[yanet_sdk::api]` expand to (`catch_unwind`, so an api crate is built with unwinding); a crate of its own because a dataplane plugin never links these C calls |
| `yanet-sdk-build` | `std`, build-dependency | `generate_header`: an api crate's `build.rs` writes the C header of its records and constants |
| `yanet-sdk-plugin` | `no_std` | a dataplane plugin's runtime: the panic handler (C `abort`), `export_module!` and `export_object!`, so a plugin is a `#![no_std]` cdylib of one line (a module's line lists the object types its plugin carries; an object type that ships without a module is a plugin of its own), built under `--profile plugin` (`panic = "abort"`) |
| `yanet-sdk-testing` | `std`, dev-only | the block arena (one region, as one mapping) and resolving context of the tests, Miri included; with the `cp` feature `TestOwner`, the owner of the configuration builder under Miri and natively, with no C memory |
| `yanet-sdk-fixture` | library `no_std`, `forbid(unsafe_code)` | minimal modules, `Fixture` (a test program whose mode word selects what it does with a batch), `Tally` (a prepared value and counters, those of tagged registries included, by id among them), `Fib` (forwards by a route FIB of two C LPM tables) and `Assign` (classify) and `Stash` (writes a linked `State`), and the object type `State` (atomic slots and their prepared seed, and a counter of its own that `Stash` keeps as a gauge), with no tests; their plugins `lib{fixture,tally,fib,assign,fixture_state,stash}_dp.so` and `libstash_state_plugin.so` (`Stash` with `State`, named outside the loader's pattern) are examples of one line over `yanet-sdk-plugin`, built on request (`--features plugin`, `--profile plugin`) |
| `yanet-sdk-fixture-api` | `std`, `forbid(unsafe_code)` | the api crate of the fixture modules and object type: the records and the `build` and `free` exports per module and object type, and the queries of the object, that the Go tests call over an agent; its header carries the records and the modes |
| `yanet-packet` | `no_std` | packet header views, extractable to its own repository |

## Api crates

An api crate is the control-plane side of a module, `#![forbid(unsafe_code)]`,
that Go calls through cgo. Its whole C boundary is the records it owns
(`#[derive(FfiRecord)]`: a `#[repr(C)]` struct of fixed-width integers and
arrays, every bit pattern valid) and the records of the SDK it takes in, such
as `LpmRange`, whose slice a build borrows as C hands it in, the exports
`#[yanet_sdk::api(module = M)]` or `#[yanet_sdk::api(object = O)]` generates
on an inherent impl of a local type, and errors as `yanet_error` chains. Every
`pub fn` of the impl becomes an `extern "C"` export named
`yanet_cp_<crate>__<snake type>_<fn>` (`<crate>` is the crate name; the crate
name has no `__` and does not end in `_`); a private fn stays ordinary. The parameters keep their order and a trailing
`struct yanet_error **err` follows (null for "no error wanted"; a chain
already in `*err` stays as the cause of the new error, as `yanet_error_add`
keeps it):

| Rust | C |
|------|---|
| `&Agent` (exactly one) | `struct agent *` |
| `&str` | `const char *`, NUL terminated UTF-8 |
| `&T`, `T: FfiRecord` | `const T *` |
| `&[T]`, `T: FfiRecord` | `const T *x, size_t x_len`; `(NULL, 0)` is empty |
| `&mut T`, `T: FfiRecord` (a query only) | `T *`, a record the query writes |
| `&mut [T]`, `T: FfiRecord` (a query only) | `T *x, size_t x_len`, records the query writes; `(NULL, 0)` is empty |
| `&BuiltObject<O, Agent>` (`object = O` only) | `struct cp_object *`, an object a build of the crate returned and nothing releases; one of another type is refused |
| `u8`..`u64`, `i8`..`i64`, `usize` | the same C integer |
| returns `Result<Built<M, Agent>, Error>` (`module = M`) | `struct cp_module *`, NULL on failure |
| returns `Result<BuiltObject<O, Agent>, Error>` (`object = O`) | `struct cp_object *`, NULL on failure |
| returns `Result<u64, Error>` (a query) | `int`, 0 or -1 on failure, with `uint64_t *out` before `err`, written on success |

A null or misaligned pointer is an invalid-argument error; a panic of the
method is caught and comes back as an error with its message. A query reads
several values in one call through its outputs, which the export zeroes once
it accepts every argument: a refused argument leaves them untouched, and a
query that fills a slice commonly returns how many values it has, so a caller
whose slice was short calls again with a longer one. The impl also
gains `int yanet_cp_<crate>__<snake type>_free(struct cp_module *, struct yanet_error **)`,
which releases a configuration as a C module's `<name>_module_config_free`
does: 0, or -1 with a busy error and errno `EAGAIN` while a live generation
holds it (the handle stays valid for a retry); an invalid pointer is `EINVAL`
and a panic `EIO`. The `_free` of an object api takes a `struct cp_object *`
and releases an object as a C object's owner does, with the same results. A
`_free` handed a header of another type of its kind refuses it as an invalid
argument and leaves it as it is; a header of the other kind (a module's to an
object's `_free`, or the reverse) is a caller error the header cannot show.
Only a `_free` sets errno, which tells a busy refusal from a failure; a build or
a query reports through its result and `err` alone, and errno is unspecified
after it.

### Objects in this release

An object type (`Object`, `ObjectBuilder`, `export_object!`,
`#[yanet_sdk::api(object = O)]`) is state built, published by name and
released by its control plane, and read there through `Agent::read`. What the
dataplane runs, the configuration of a module that the current generation
publishes under a name, is read through `Agent::read_published`, which pins the
generation for the call alone: its `PublishedCx` resolves the configuration's
edges, lists its object links by type and name, and resolves a link to the
object of the same generation. A module
configuration links it by type and name (`Scope::object`, an `ObjectLink`
field), which the handler and the prepare step resolve to the object's
configuration in the running generation and to the object's prepared value:
`Object::prepare` makes it per worker context, run by the object type's commit
hook, which `export_object!` registers only for a non-zero-sized value. An
object registers counters of its own (`Scope::counter` of an object scope, an
`ObjectCounter` field), which readers find by the object's type and name and C
carries over to an object of the same type and name that replaces it; only
the object's prepare step resolves one, into a `CounterRef` its prepared value
keeps for the handlers of the modules linking it. An
object type ships in the plugin of a module that lists it,
`export_module!(name = .., module = .., objects = [<type> = <path>])`, as a C
module's plugin carries its object types, or in a plugin of its own through
`export_object!`: every plugin exports one `yanet_module_abi_version`.

The crate's `build.rs` is `yanet_sdk_build::generate_header(&["Record", ..], &[])`.
With `$YANET_SDK_HEADER_DIR` set it writes `<crate>.h`, which holds the listed
records as `struct <Record>` (an SDK record through a type alias:
`pub type Range4 = LpmRange<4>` is `typedef struct LpmRange_4 Range4`) and the
`pub const`s, not the export prototypes: the macro generates those, and
cbindgen would see them only through a nightly expansion of the crate. The Go
wrapper declares typed prototypes over the header's structs. An api crate is
built with unwinding (the guards need it); a plugin is built with
`--profile plugin`.

### Empty tagged counter registries

`scope.counters_in("rules")` creates a lazy registrar. With no counter
registrations it leaves the module without that registry. Call
`registrar.ensure_registry()?` to create the registry explicitly, even when
no active counters follow. It uses C's `cp_module_counter_registry` without
registering a dummy counter or consuming a counter id. Repeated calls find
the same registry.

An explicitly empty registry participates in native replacement linking:
omitted counters retain their values for one replacement and expire after
two consecutive omissions. This is useful when a configuration temporarily
has no rules. Existing callers keep lazy creation unless they opt in.

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
MIRIFLAGS="-Zmiri-strict-provenance" cargo +nightly-2026-10-22 miri nextest run --workspace --exclude yanet-packet   # Miri (cargo-nextest); add -Zmiri-tree-borrows for Tree Borrows; yanet-packet forbids unsafe code
cargo bench -p yanet-sys --bench dp --no-run                                        # dataplane hot path, built on every core
taskset -c 2 cargo bench -p yanet-sys --bench dp -- --save-baseline main            # run pinned to one core, -- --baseline main on the change
cargo bench -p yanet-sys --bench dp --profile plugin --no-run                       # adds the plugins' thin LTO, keeps unwinding
taskset -c 2 cargo bench -p yanet-sys --bench dp --profile plugin -- --save-baseline main-plugin   # a baseline of its own, -- --baseline main-plugin on the change
```

## Classifier table checks

Classifier lines, row directories and columns check indices in debug builds.
An invalid index panics with the index and bound before an invalid access.
Optimized builds retain unchecked lookups.

The checks rely on C-built dimensions, offsets and allocation extents. They
do not validate arbitrary corrupted storage, and do not change LPM reads.

The existing seeded properties compare IPv4 and IPv6 Rules and projected
Decode scalar/burst lookups against independent first-match oracles:

```sh
PROPTEST_CASES=200 PROPTEST_RNG_SEED=19567271 cargo test -p yanet-sys --locked
PROPTEST_CASES=100000 PROPTEST_RNG_SEED=19567271 cargo test -p yanet-sys --release --test classify_tree_prop --locked
cargo test -p yanet-sys --bench lookup --locked
taskset -c 2 cargo bench -p yanet-sys --bench lookup -- --save-baseline main-lookup
taskset -c 2 cargo bench -p yanet-sys --bench lookup --profile plugin -- --save-baseline main-lookup-plugin
```

The lookup benchmark builds and oracle-checks IPv4, split-path IPv6 and
transport Decode trees before timing scalar and burst lookups. Compare the
same harness and inputs on the base and change, in each profile separately,
with repeated interleaved runs on one core. Preserve run spread and timing
uncertainty rather than treating an interval containing zero as parity.

## Tests against the C dataplane

The behaviour of the runtime and of the builder against the real C dataplane
is tested from Go, not from Rust. The tests are the `cfixture` package, in
`yanet-sdk-fixture-api/bindings/go`, behind the build tag `sdk_rust_fixtures`:
they start the harness of `bindings/go/dataplane_ut`, load the fixture
plugins and build configurations through the exports of
`yanet-sdk-fixture-api`.

Meson builds what they need, through cargo, only when its option is on: the
api archive with only its exports global, its header, and the directory of the
plugins `lib{fixture,tally,fib,assign,fixture_state,stash}_dp.so`, which holds
nothing else the loader picks up: `libstash_state_plugin.so`, the stash module
with the state type in one plugin, is named outside its pattern, and a test
links it into a directory of its own. `cargo test` and Miri never build the
plugins: a `no_std` cdylib
cannot be built with unwinding, so they have a profile of their own.

```sh
meson setup -Dsdk_rust_fixtures=enabled build  # or `meson configure` in place
make test
```

`make test` adds the tag when the configured build has the fixtures. When
`GO_TEST_PACKAGES` is set, `make test-only` and `make test-asan-only` run just
those Go packages and skip the meson tests; CI sets it to the `cfixture` path.

An ASan C build needs the Rust code instrumented too, so that both sides lay
out the red zones of the block allocator. That takes a nightly toolchain:

```sh
RUSTUP_TOOLCHAIN=nightly-2026-10-22 make test-asan
```

Miri stays the layer for the language rules of the unsafe code; these tests
add bounds, use after free and the C behaviour, on real memory. CI runs them
natively in `.github/workflows/rust-sdk-ut.yml` and under ASan in
`.github/workflows/rust-sdk-ut-asan.yml`.

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
publication or aliasing. Those obligations belong to `Graph::new`, the unsafe
`Owner` contract and actual dereferences. Constructing a raw root alone never
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

## Writing configuration parts

`Scope::slice` still calls the author's fill function once per index, in
order. It reserves one block and lends the next uninitialized element to
`Build::write` after each fill. Parts can own children from the enclosing
build. The shorter slot lifetime prevents returning a completion from
another element, even within that same build.

Manual `unsafe impl Build` writers now take a typed `Place` instead of a
memory context and raw address. The place supplies both, and completing it
returns an `Init` for that slot. Until completion, the writer must roll back
its own partial initialization. After completion, the transaction owns
cleanup on refusal or unwind, even when the receipt is forgotten. Accepting
the receipt transfers the whole element to the committed prefix. Relative
edges must still be written at their final addresses, and `Build::free`
retains its unsafe ownership contract.

## Custom configuration owners

Module and object builders keep their existing APIs. Custom implementations of
`yanet_sys::cp::Owner` migrate the thirteen formerly unsafe lifecycle and
registration methods to safe associated functions. The trait itself remains
unsafe: the implementation still guarantees raw provenance, allocation alignment,
correct C operations and non-panicking finalization and freeing.
`root`, `worker_count` and root `alloc` retain their `&self` receiver.

Use the owner stored in each capability, not a separately selected instance.
For example, the freeing seam changes from
`unsafe fn free(&self, addr: BlockAddr, size: usize)` to
`fn free(block: Freed<'_, Self>)`. The implementation obtains its actual owner
from `block.owner()` and the original allocation from `block.addr()` and
`block.size()`. Child freeing instead takes `Freed<'_, Self, Context>` and
also obtains `block.context()`.

The remaining signatures follow the operation's state and header kind:

| Operation | Capability argument |
|-----------|---------------------|
| `init_header` / `init_object_header` | `Fresh<'_, Self, ModuleKind>` / `Fresh<'_, Self, ObjectKind>`, consumed |
| `fini_header` / `fini_object_header` | `Retiring<'_, Self, ModuleKind>` / `Retiring<'_, Self, ObjectKind>`, consumed |
| `alloc_in` | `&LiveContext<'_, Self>` |
| `register_counter`, `ensure_counter_registry`, `register_counter_in`, `link_device`, `link_object` | `&Live<'_, Self, ModuleKind>` |
| `register_object_counter` | `&Live<'_, Self, ObjectKind>` |

Header capabilities expose `owner()` and `header()`; a live-context capability
exposes `owner()` and `context()`. Keep each required unsafe C call and its safety
justification inside the implementation. Safe dispatch does not make C calls safe
or relax the `Build::write` and `Build::free` contracts.

Before, a call such as
`unsafe { owner.register_counter(header, "packets", 1) }` required the caller to
justify that the raw header was an initialized, unpublished module belonging to
that particular owner. Now `W::register_counter(&live, "packets", 1)` carries that
state and the actual owner together. An object's live capability is rejected by
the module operation, and there is no independent receiver on which to select a
foreign owner of the same type.

Capabilities are issued privately by the builder and remain thread-confined.
Initialization, finalization and freeing consume non-copyable permissions, so
safe callers cannot initialize twice, finalize twice, return a block twice or
mix root and child freeing. Raw `Header`, `Context` and `BlockAddr` types and
their address getters remain available, but constructing a raw address grants
none of those permissions. The address-module rustdoc includes compile-fail
examples and matching positive examples for these restrictions.

Custom owners must still declare `C_MEMORY`. Set it to true only when initialized
headers contain real C memory contexts. Arena-only owners keep it false and
preserve the existing `Refusal::NoCMemory` behavior.

## Safe builder tests

With the `cp` feature, `yanet-sdk-testing` provides a safe configuration
lifecycle over an arena without C memory:

```rust
let owner = TestOwner::new();
let built = build(&owner)?;
owner.read_module(&built, |cx, config| {
    assert_eq!(config.rules.as_slice(&cx).len(), 3);
});
owner.release(built);
owner.assert_released();
```

Use `read` for a `BuiltObject` and `read_module` for a module's `Built`.
Both check that the handle belongs to this owner and is still live before
accessing its body. The callback brands its body and resolving context
together. Neither can escape or mix with another read. Release consumes the
handle, so it cannot overlap a read.

Replace `unsafe { TestOwner::new() }` with `TestOwner::new()`. Move body reads
and graph resolution into these callbacks instead of calling `config`,
constructing a raw `ModuleConfig` reference or using `View::new`. Replace
`destroy` with `owner.release(built)`. The low-level methods remain available
but are hidden from documentation.

Custom `Owner` implementations must declare `C_MEMORY`. Set it to true only
when initialized headers carry real C memory contexts. `TestOwner` sets it
to false. Nonempty LPMs and hash indexes and all classifier construction are
refused with `Refusal::NoCMemory` (`YANET_ERROR_FAILED_PRECONDITION`) rather
than entering C. Empty LPMs and indexes still work without C. Tests requiring
real C objects use a C-capable owner and the native fixture harness.
