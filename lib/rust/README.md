# Rust module SDK

A cargo workspace, separate from the CLI workspace at the repository root,
with its own `Cargo.lock` and `rust-version = "1.88"`, for writing
dataplane modules in Rust: the boundary crate both sides of the SDK
share, the C bindings with the dataplane runtime and the configuration
builder, the derive, the module-crate facade and the packet header views.

| Crate | Environment | Role |
|-------|-------------|------|
| `yanet-shm` | `no_std`, no `alloc` | the provenance root, relative-pointer field types, `Opaque`, `ShmLayout` leaves |
| `yanet-sys` | `no_std`, no `alloc` | the C bindings of the SDK, modules mirroring the C tree, one bindgen output (generated into `OUT_DIR`, only here) and one module per C library: `common` (LPM type with its lookup, and its construction by the C routines), `counters` (the counter value address), `dp` (bindings of `lib/dataplane`, the `Module` trait, `Cx`/`CommitCx`, `Batch`/`Packet` with the packet operations (allocation, copy, resizing, tunnels), counters and devices, the handler runtime and `export_module!`), `cp` (mirror of `lib/controlplane`: the configuration `Builder` with its `Copy` `Scope` handle and the `Owned`/`OwnedSlice` blocks it allocates, the `Built` configuration it returns, the `Owner` seam to the C control plane (`Header`, `Context` and `BlockAddr` newtypes for its addresses) and the release of a build by walking its owning edges; makes no C call itself but those of the C objects a configuration embeds, such as the LPM); the C shims of `common/` and the dataplane |
| `yanet-sdk-derive` | proc macro | `#[derive(ShmLayout)]`: the layout contract check, `Build` and the `<Type>Parts` struct a build writes from; `#[derive(FfiRecord)]` and `#[yanet_sdk::api]` |
| `yanet-sdk` | `no_std`, `forbid(unsafe_code)` | the module-crate surface: `Module`, contexts, batches, field types, the derive and the builder |
| `yanet-sdk-api` | `std` | the control-plane side of a module: `Agent`, a non-owning handle on an attached C agent that owns the configurations a module's API builds (the builder's `Owner` over the C routines a C module's API calls, and the release of a configuration as those do), `ApiError`, the C error chain, and the guards and conversions the exports of `#[yanet_sdk::api]` expand to (`catch_unwind`, so an api crate is built with unwinding); a crate of its own because a dataplane plugin never links these C calls |
| `yanet-sdk-build` | `std`, build-dependency | `generate_header`: an api crate's `build.rs` writes the C header of its records and constants |
| `yanet-sdk-plugin` | `no_std` | a dataplane plugin's runtime: the panic handler (C `abort`) and `export_module!`, so a plugin is a `#![no_std]` cdylib of one line, built under `--profile plugin` (`panic = "abort"`) |
| `yanet-sdk-testing` | `std`, dev-only | the block arena (one region, as one mapping) and resolving context of the tests, Miri included; with the `cp` feature `TestOwner`, the owner of the configuration builder under Miri and natively, with no C memory |
| `yanet-sdk-fixture` | library `no_std`, `forbid(unsafe_code)` | three minimal modules, `Fixture` (a test program whose mode word selects what it does with a batch), `Tally` (a prepared value and counters) and `Fib` (forwards by a route FIB of two C LPM tables), with no tests; their plugins `lib{fixture,tally,fib}_dp.so` are examples of one line over `yanet-sdk-plugin`, built on request (`--features plugin`, `--profile plugin`) |
| `yanet-sdk-fixture-api` | `std`, `forbid(unsafe_code)` | the api crate of the fixture modules: the records and the `build` and `free` exports per module that the Go tests call to build configurations over an agent; its header carries the records and the modes |
| `yanet-packet` | `no_std` | packet header views, extractable to its own repository |

## Api crates

An api crate is the control-plane side of a module, `#![forbid(unsafe_code)]`,
that Go calls through cgo. Its whole C boundary is the records it owns
(`#[derive(FfiRecord)]`: a `#[repr(C)]` struct of fixed-width integers and
arrays, every bit pattern valid) and the records of the SDK it takes in, such
as `LpmRange`, whose slice a build borrows as C hands it in, the exports
`#[yanet_sdk::api(module = M)]` generates on an inherent impl of a local type,
and errors as `yanet_error` chains. Every `pub fn` of the impl becomes an
`extern "C"` export named `yanet_cp_<crate>__<snake type>_<fn>` (`<crate>` is
the crate name; the crate name has no `__` and does not end in `_`); a private
fn stays ordinary. The parameters keep their order and a trailing
`struct yanet_error **err` follows (null for "no error wanted"; a chain
already in `*err` stays as the cause of the new error, as `yanet_error_add`
keeps it):

| Rust | C |
|------|---|
| `&Agent` (exactly one) | `struct agent *` |
| `&str` | `const char *`, NUL terminated UTF-8 |
| `&T`, `T: FfiRecord` | `const T *` |
| `&[T]`, `T: FfiRecord` | `const T *x, size_t x_len`; `(NULL, 0)` is empty |
| `u8`..`u64`, `i8`..`i64`, `usize` | the same C integer |
| returns `Result<Built<M, Agent>, ApiError>` | `struct cp_module *`, NULL on failure |

A null or misaligned pointer is an invalid-argument error; a panic of the
method is caught and comes back as an error with its message. The impl also
gains `int yanet_cp_<crate>__<snake type>_free(struct cp_module *, struct yanet_error **)`,
which releases a configuration as a C module's `<name>_module_config_free`
does: 0, or -1 with a busy error and errno `EAGAIN` while a live generation
holds it (the handle stays valid for a retry); an invalid pointer is `EINVAL`
and a panic `EIO`.

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
encapsulation sources of `lib/dataplane/packet` it calls, with hidden
visibility and the warning options of the C build, `-Werror` included. A
plugin imports the packet allocator and copy (`worker_packet_alloc`,
`worker_clone_packet`) from the dataplane executable, which exports them. The
tests of `yanet-shm` need neither the compilation database nor a C compiler.

```sh
meson setup build
cd lib/rust
export CLANG_PATH=/usr/bin/clang-23 LIBCLANG_PATH=/usr/lib/llvm-23/lib
cargo test --workspace                      # native suites
MIRIFLAGS="-Zmiri-strict-provenance" cargo +nightly-2026-10-22 miri nextest run --workspace   # Miri (cargo-nextest); add -Zmiri-tree-borrows for Tree Borrows
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
plugins `lib{fixture,tally,fib}_dp.so`, which holds nothing else. `cargo test`
and Miri never build the plugins: a `no_std` cdylib cannot be built with
unwinding, so they have a profile of their own.

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
add bounds, use after free and the C behaviour, on real memory. CI runs both
in `.github/workflows/rust-sdk-ut.yml`.
