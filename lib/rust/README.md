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
| `yanet-sdk-api` | Native Agent ownership, C errors and panic guards. |
| `yanet-sdk-build` | Explicit record/constant headers for API crates. |
| `yanet-sdk-plugin` | Minimal no_std plugin runtime and exports. |

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

For a normalized device declaration table, wrap the top-level initializer in
`with_devices(device_count, read_name, |devices| Ok(ConfigInit { ... }))`.
Each name is decoded and linked once; route callbacks capture the Copy map
with `move` and use `devices.get(logical_index)` to obtain a device initializer.
Out-of-range logical indices remain ordinary domain errors. The table must
contain unique names, with the prelinked empty default name allowed once in
any position. Repeated or noncontiguous native indices refuse construction.
The map and its indices use no SDK allocation. They describe positions in a
configuration's device namespace and do not prove registration in another
configuration. The simple `device(name)` helper remains useful for individual
fields. Counters continue to register through `counter(name)`.

Completed fields and prefixes remain guarded until their containing value
succeeds. Failures and panics release partial storage, C objects and header
registrations. Finished configurations retain no input borrow. Release walks
owning edges in reverse construction order; allocations contain only payload.
The original owner and absence of readers are required for explicit destruction.

The C-free test owner initializes no C contexts and cannot build configurations
containing embedded C objects. The API crate provides an owner backed by an
attached native agent for building configurations that include native objects.

Build the repository's native libraries before testing this workspace. The
workspace supports Rust 1.88. Formatting uses nightly-2026-10-22. CI runs native
unit tests and doctests, Clippy, bare-metal checks and Miri with strict
provenance under both borrow models.

`yanet-sys` requires clang and libclang from the same LLVM major, selected by
`CLANG_PATH` and `LIBCLANG_PATH`; CI uses LLVM 23.

API crates put `#[yanet_sdk::api(module = MyModule)]` on a local inherent impl.
Each public associated function returns `Result<Built<MyModule, Agent>,
ApiError>` and gains a typed C entrypoint; a matching free entrypoint preserves
busy handles for retry. Functions accept an agent, borrowed names, FFI records,
record slices and fixed-width integers. The record derive and generated headers
keep C/Go layout tied to Rust; cgo declares the typed function prototypes.

The C symbol stem is `yanet_cp_<crate>__<snake type>`; exported methods use
`<stem>_<method>` and each implementation also supplies `<stem>_free`.
Parameters retain their authored order and end with an optional writable
`struct yanet_error **`. Build returns
`struct cp_module *`, or `NULL` on failure. Free returns zero on success and
minus one on failure: a refused destruction gate uses `EAGAIN` independently
of the native error chain's kind, invalid input uses `EINVAL`, and a panic uses
`EIO`. A busy configuration remains valid for retry while its owner is current.

| Rust argument | C argument |
| --- | --- |
| `&Agent` (exactly one) | `struct agent *` |
| `&str` | NUL terminated UTF-8 `const char *` |
| `&T` for an FFI record | `const struct T *` |
| `&[T]` for FFI records | Record pointer and `size_t` length; `(NULL, 0)` is empty. |
| Fixed-width integers or `usize` | Corresponding integer or `size_t`. |

Null or misaligned required arguments are invalid argument errors. Method
panics return a native error containing their message; unwinding stays inside
the Rust boundary. The macro's trusted expansion preserves the API crate's
ban on authored unsafe code. Executable accepted and rejected examples live
with the derive crate's doctests.

`Agent::build_config::<MyModule>` fixes the error type to `ApiError`. A callback
can use ordinary `?` conversions from the author's validation errors. Helpers
that return nested initializers can use `impl Init<ApiError, Output = Record>`.
The free core function also supports an API's own error type directly.

The plugin and API profiles use thin LTO; plugins abort on panic, while API
archives unwind into their guards. Meson localizes each independent archive's
defined private symbols and preserves native imports. The fixture API is built
once and split into two archives with disjoint exports and private Rust runtime
copies. The Go coexistence test links both beside ordinary `librure` without the
dataplane harness.

Opt into native fixtures with `meson setup build -Dsdk_rust_fixtures=enabled`.
Then `make test-only` includes their Go suites under the `sdk_rust_fixtures` tag.
Plugin artifacts and generated headers live under `build/lib/rust`; plugin
source examples are tracked with the fixture crate.
The Go tests in `yanet-sdk-fixture-api/bindings/go/cfixture` use the shared
harness for Agent lifetime, FIB, routing, counters and packet allocation. Native
and ASan CI run both suites; native CI additionally enables CGO pointer checks.
