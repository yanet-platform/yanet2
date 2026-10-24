# Rust module SDK

A cargo workspace, separate from the CLI workspace at the repository root,
with its own `Cargo.lock` and `rust-version = "1.88"`, for writing
dataplane modules in Rust: the boundary crate both sides of the SDK
share and the packet header views.

| Crate | Environment | Role |
|-------|-------------|------|
| `yanet-shm` | `no_std`, no `alloc` | the provenance root, relative-pointer field types, `Opaque`, `ShmLayout` leaves |
| `yanet-sdk-testing` | `no_std` + `alloc`, dev-only | the block arena and resolving context of the tests, Miri included |
| `yanet-packet` | `no_std` | packet header views, extractable to its own repository |

## Build

```sh
cd lib/rust
cargo test --workspace                      # native suites
cargo xtask miri [stacked|tree|all]        # Miri, strict provenance, both models in parallel
```
