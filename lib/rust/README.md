# Rust module SDK

A cargo workspace, separate from the CLI workspace at the repository root,
with its own `Cargo.lock` and `rust-version = "1.88"`, for writing
dataplane modules in Rust: the boundary crate both sides of the SDK
share, the bindings of the common C headers and the packet header views.

| Crate | Environment | Role |
|-------|-------------|------|
| `yanet-shm` | `no_std`, no `alloc` | the provenance root, relative-pointer field types, `Opaque`, `ShmLayout` leaves |
| `yanet-sys` | `no_std`, no `alloc` | the C bindings of the SDK, modules mirroring the C tree: the one bindgen output (generated into `OUT_DIR`, only here) and, in module `common`, the LPM type with its lookup |
| `yanet-sdk-testing` | `no_std` + `alloc`, dev-only | the block arena and resolving context of the tests, Miri included |
| `yanet-packet` | `no_std` | packet header views, extractable to its own repository |

## Build

The build script of `yanet-sys` reads the C flags of the dataplane
(include directories, defines, forced includes) from the `compile_commands.json`
that `meson setup build` writes at the repository root; `YANET_BUILD_DIR`
names another build directory, and `.cargo/config.toml` of this directory
defaults it to `build`. It also needs a libclang for bindgen
(`LIBCLANG_PATH`, with the matching `CLANG_PATH`); CI uses LLVM 18.

```sh
meson setup build
cd lib/rust
export CLANG_PATH=/usr/bin/clang-18 LIBCLANG_PATH=/usr/lib/llvm-18/lib
cargo test --workspace                      # native suites
cargo xtask miri [stacked|tree|all]        # Miri, strict provenance, both models in parallel
```
