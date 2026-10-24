# Rust module SDK

A cargo workspace, separate from the CLI workspace at the repository root,
with its own `Cargo.lock` and `rust-version = "1.88"`, for writing
dataplane modules in Rust. It starts with the packet header views.

| Crate | Environment | Role |
|-------|-------------|------|
| `yanet-packet` | `no_std` | packet header views, extractable to its own repository |

## Build

```sh
cd lib/rust
cargo test --workspace
```
