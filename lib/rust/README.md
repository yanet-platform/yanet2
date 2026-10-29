# Rust module SDK

The SDK is a separate Cargo workspace. Module storage and handlers are
`#![no_std]` and `#![forbid(unsafe_code)]`; API crates can forbid unsafe code too.
The sys crate owns the trusted C boundary and final-address construction.

| Crate | Purpose |
| --- | --- |
| `yanet-sdk` | Safe storage, initialization and dataplane facade. |
| `yanet-sdk-derive` | Checked storage layout, generated initializers and release. |
| `yanet-shm` | Relative pointers, root provenance and layout contracts. |
| `yanet-sys` | C bindings, contexts, runtime and direct writer. |
| `yanet-packet` | Packet header views. |
| `yanet-sdk-testing` | C-free region and owner for runtime and Miri tests. |

Declare storage fields once. The storage derive generates a named initializer
with the same field visibility; each initializer field must encode the exact
corresponding storage type. Integer literals in generic initializer fields need
a width suffix, such as `0u32`; typed values retain their declared widths.

```rust
use yanet_sdk::{ShmLayout, ShmSlice, slice};
type MyError = yanet_sdk::BuildError<core::convert::Infallible>;

#[derive(ShmLayout)]
#[repr(C)]
pub struct Config {
    pub ports: ShmSlice<u64>,
}

let ports = [80u64, 443];
let input = ConfigInit {
    ports: slice(ports.len(), |index| Ok::<_, MyError>(ports[index])),
};
// build_config::<MyModule, _, MyError>(&owner, "policy", input)?;
```

A slice callback returns one fallible record at a time. Nested callbacks can
borrow the original input; there is no intermediate collection, erased callback
or construction context exposed to authors. `boxed`, `counter`, `device` and
`lpm` compose with ordinary scalars, arrays and actual atomic values. LPM ranges
are read and validated once immediately before C insertion. Domain validation
belongs in the callback and retains the API's error type through
`From<BuildError<OwnerError>>`.

Completed fields and prefixes remain guarded until their containing value
succeeds. Failures and panics release partial storage, C objects and header
registrations. Finished configurations retain no input borrow. Release walks
owning edges in reverse construction order; allocations contain only payload.
The original owner and absence of readers are required for explicit destruction.

The C-free test owner initializes no C contexts. Embedded C objects and native
registrations are tested with the real agent through the shared Go harness.

Build the repository's native libraries before testing this workspace. The
workspace supports Rust 1.88. Formatting uses nightly-2026-10-22. CI runs native
unit tests and doctests, Clippy, bare-metal checks and Miri with strict
provenance under both borrow models.

`yanet-sys` requires clang and libclang from the same LLVM major, selected by
`CLANG_PATH` and `LIBCLANG_PATH`; CI uses LLVM 23.
