# Rust module SDK

A cargo workspace, separate from the CLI workspace at the repository root,
with its own `Cargo.lock` and `rust-version = "1.99"`, for writing
dataplane modules in Rust: the boundary crate both sides of the SDK
share, the C bindings with the dataplane runtime and the configuration
builder, the derive, the module-crate facade and the packet header views.

| Crate | Environment | Role |
|-------|-------------|------|
| `yanet-shm` | `no_std`, no `alloc` | the provenance root, relative-pointer field types, `Opaque`, `RawSpinlock`, `ShmLayout` leaves |
| `yanet-sys` | `no_std`, no `alloc` | the C bindings of the SDK, modules mirroring the C tree, one bindgen output (generated into `OUT_DIR`, only here) and one module per C library: `common` (LPM type with its lookup, and its construction by the C routines), `counters` (the counter value address), `dp` (bindings of `lib/dataplane`, the `Module` trait, `Cx`/`CommitCx`/`ObjectCommitCx`, `Batch`/`Packet` with the packet operations (allocation, copy, freeing, resizing, parsing, tunnels), counters, devices and object links (`ObjectLink`, resolved through `ModuleCx`), per-worker state (`PerWorker` slots and `PerWorkerSlice` regions, one block per worker, reached only through the `WorkerSlot` and `WorkerSlice` handles a prepare step makes through `PrepareCx` and the handler uses with its `Cx`; a module's prepare finds its worker through the dp shim's `yanet_sys_dp_module_worker_idx`, an object's reads its context's; no author API names a worker index or count), the `Object` trait, the handler runtime, `export_module!` and `export_object!`), `cp` (mirror of `lib/controlplane`: the configuration `Builder` and the `ObjectBuilder` over one build core, with its `Copy` `Scope` handle, kind-tagged so that only a module scope registers counters and devices and links objects, and the `Owned`/`OwnedSlice`/`OwnedSlots`/`OwnedRegions` blocks it allocates, the `Built` configuration and the `BuiltObject` it returns, the `Owner` seam to the C control plane (`Header`, `Context` and `BlockAddr` newtypes for its addresses) and the release of a build by walking its owning edges; makes no C call itself but those of the C objects a configuration embeds, such as the LPM); the C shims of `common/` and the dataplane |
| `yanet-sdk-derive` | proc macro | `#[derive(ShmLayout)]`: the layout contract check, `Build` and the `<Type>Parts` struct a build writes from; `#[derive(FfiRecord)]` and `#[yanet_sdk::api]` |
| `yanet-sdk` | `no_std`, `forbid(unsafe_code)` | the module-crate surface: `Module`, `Object`, contexts, batches, field types (`Counter`, `TaggedCounter`, `TaggedCounters` with its prepared `TaggedCountersRef` handle, `Device`, `PerWorker` and `PerWorkerSlice` with their prepared `WorkerSlot` and `WorkerSlice` handles, ...), the derive and the builders |
| `yanet-sdk-api` | `std` | the control-plane side of a module: `Agent`, a non-owning handle on an attached C agent that owns the configurations and objects an API builds (the builder's `Owner` over the C routines a C module's API calls, the release of a configuration as those do, the release of an object as a C object's owner does, and `Agent::read`, the control-plane read of an object's body under a branded `ObjectCx` its edges resolve through), `Error`, the C error chain, and the guards and conversions the exports of `#[yanet_sdk::api]` expand to (`catch_unwind`, so an api crate is built with unwinding); a crate of its own because a dataplane plugin never links these C calls |
| `yanet-sdk-build` | `std`, build-dependency | `generate_header`: an api crate's `build.rs` writes the C header of its records and constants |
| `yanet-sdk-plugin` | `no_std` | a dataplane plugin's runtime: the panic handler (C `abort`), `export_module!` and `export_object!`, so a plugin is a `#![no_std]` cdylib of one line (a module's line lists the object types its plugin carries; an object type that ships without a module is a plugin of its own), built under `--profile plugin` (`panic = "abort"`) |
| `yanet-sdk-testing` | `std`, dev-only | the block arena (one region, as one mapping) and resolving context of the tests, Miri included; with the `cp` feature `TestOwner`, the owner of the configuration builder under Miri and natively, with no C memory |
| `yanet-sdk-fixture` | library `no_std`, `forbid(unsafe_code)` | minimal modules, `Fixture` (a test program whose mode word selects what it does with a batch), `Tally` (a prepared value and counters, those of tagged registries included, by id among them), `Fib` (forwards by a route FIB of two C LPM tables) and `Assign` (classify) and `Stash` (writes a linked `State`), and the object type `State` (atomic slots and their prepared seed), with no tests; their plugins `lib{fixture,tally,fib,assign,fixture_state,stash}_dp.so` and `libstash_state_plugin.so` (`Stash` with `State`, named outside the loader's pattern) are examples of one line over `yanet-sdk-plugin`, built on request (`--features plugin`, `--profile plugin`) |
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
```

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
