# Rust module SDK

A cargo workspace, separate from the CLI workspace at the repository root,
with its own `Cargo.lock` and `rust-version = "1.88"`, for writing
dataplane modules in Rust: the boundary crate both sides of the SDK
share, the C bindings with the dataplane runtime and the packet header views.

| Crate | Environment | Role |
|-------|-------------|------|
| `yanet-shm` | `no_std`, no `alloc` | the provenance root, relative-pointer field types, `Opaque`, `ShmLayout` leaves |
| `yanet-sys` | `no_std`, no `alloc` | the C bindings of the SDK, modules mirroring the C tree, one bindgen output (generated into `OUT_DIR`, only here) and one module per C library: `common` (LPM type with its lookup), `counters` (the counter value address), `dp` (bindings of `lib/dataplane`, the `Module` trait, `Cx`/`CommitCx`, `Batch`/`Packet`, counters and devices, the handler runtime and `export_module!`); the dataplane C shim |
| `yanet-sdk-testing` | `no_std` + `alloc`, dev-only | the block arena and resolving context of the tests, Miri included; with the `dp` feature (enabled only by `yanet-sys`' tests) the dataplane invocation harness |
| `yanet-packet` | `no_std` | packet header views, extractable to its own repository |

## Build

The build script of `yanet-sys` reads the C flags of the dataplane
(compiler, include directories, defines) from the `compile_commands.json`
that `meson setup build` writes at the repository root; `YANET_BUILD_DIR`
names another build directory, and `.cargo/config.toml` of this directory
defaults it to `build`. It also needs a clang
and a libclang of one LLVM major (`CLANG_PATH`, `LIBCLANG_PATH`); CI uses 18.
The same build script compiles the dataplane C shim with hidden
visibility and the warning options of the C build, `-Werror` included. The
tests of `yanet-shm` need neither the compilation database nor a C compiler.

```sh
meson setup build
cd lib/rust
export CLANG_PATH=/usr/bin/clang-18 LIBCLANG_PATH=/usr/lib/llvm-18/lib
cargo test --workspace                      # native suites
cargo xtask miri [stacked|tree|all]        # Miri, strict provenance, both models in parallel
```
