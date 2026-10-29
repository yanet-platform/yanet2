//! Derive macros of the Rust module SDK.
//!
//! The derive is the only implementation of the `unsafe` layout trait a module
//! crate has; its expansion is audited by review, since `forbid(unsafe_code)`
//! in a module crate does not see macro output. SDK and core paths bind
//! explicitly to external crates so caller aliases cannot replace trusted
//! checks or helpers. Use it through the `yanet-sdk` crate.

#![forbid(unsafe_code)]

mod api;
mod ffi_record;
mod shm_layout;

use proc_macro::TokenStream;
use syn::{DeriveInput, ItemImpl, parse_macro_input};

/// Derives `ShmLayout` and `Build`, with the `<Type>Parts` struct a build
/// writes the value from, for a shared-memory struct.
///
/// Accepts only a non-generic `#[repr(C)]` struct with named fields (an
/// alignment up to 64 may be added), every field of which is `Build`, with no
/// implicit padding and no drop glue. The generated `<Type>Parts` has one field
/// per field of the struct, holding the parts of that field's type, each
/// written at the field's place. The fields are written in order, and a
/// refused field returns the ones before it.
///
/// ```
/// #![forbid(unsafe_code)]
///
/// use yanet_sdk::{Owned, Owner, ShmBox, ShmLayout};
///
/// #[derive(ShmLayout)]
/// #[repr(C)]
/// struct Item {
///     value: u64,
/// }
///
/// #[derive(ShmLayout)]
/// #[repr(C)]
/// struct Config {
///     first: ShmBox<Item>,
///     flags: u32,
///     // Explicit padding keeps the struct free of implicit padding.
///     pad: u32,
/// }
///
/// // The parts of a box are the block that holds its value, and the parts of a
/// // configuration name the owner that builds it.
/// fn parts<W: Owner>(first: Owned<'_, Item, W>) -> ConfigParts<'_, W> {
///     ConfigParts { first, flags: 0, pad: 0 }
/// }
/// ```
#[proc_macro_derive(ShmLayout)]
pub fn derive_shm_layout(input: TokenStream) -> TokenStream {
    let input = parse_macro_input!(input as DeriveInput);
    shm_layout::expand(&input)
        .unwrap_or_else(syn::Error::into_compile_error)
        .into()
}

/// Derives `FfiRecord` for a struct an api crate's exports take by pointer.
///
/// Accepts only a non-generic struct with named fields and the plain
/// `#[repr(C)]` (no alignment or packing), every field of which is a record: a
/// fixed-width integer, an array of records or another derived struct. Padding
/// is never read as a field, so it needs no naming. The C header generated for
/// the api crate declares the struct.
///
/// ```
/// use yanet_sdk::FfiRecord;
///
/// #[derive(FfiRecord)]
/// #[repr(C)]
/// struct Range {
///     from: [u8; 4],
///     to: [u8; 4],
///     value: u32,
/// }
/// ```
#[proc_macro_derive(FfiRecord)]
pub fn derive_ffi_record(input: TokenStream) -> TokenStream {
    let input = parse_macro_input!(input as DeriveInput);
    ffi_record::expand(&input)
        .unwrap_or_else(syn::Error::into_compile_error)
        .into()
}

/// Exports the `pub` functions of an inherent impl to C.
///
/// `#[yanet_sdk::api(module = M)]` goes on an impl of a local type of an api
/// crate and turns each `pub fn` into an `extern "C"` function named
/// `yanet_cp_<crate>__<snake type>_<fn>`, where `<crate>` is the crate's name
/// (`CARGO_CRATE_NAME`); the impl itself is kept as written, and a private fn
/// stays an ordinary one. The impl gains
/// `yanet_cp_<crate>__<snake type>_free(struct cp_module *, struct yanet_error
/// **)`, which releases a configuration, as a C module's
/// `<name>_module_config_free` does: 0, or -1 with errno `EAGAIN` and a busy
/// error while a live generation holds it, the handle then staying valid.
///
/// Parameters keep their order and a trailing `struct yanet_error **err`
/// follows them (null for "no error wanted"):
///
/// | Rust | C |
/// |------|---|
/// | `&Agent` (exactly one) | `struct agent *` |
/// | `&str` | `const char *`, NUL terminated UTF-8 |
/// | `&T`, `T: FfiRecord` | `const T *` |
/// | `&[T]`, `T: FfiRecord` | `const T *x, size_t x_len`; `(NULL, 0)` is empty |
/// | `u8`..`u64`, `i8`..`i64`, `usize` | the same C integer |
/// | returns `Result<Built<M, Agent>, ApiError>` | `struct cp_module *`, `NULL` on failure |
///
/// Anything else is a compile error at the offending type. A null or
/// misaligned pointer for an argument is refused as an invalid argument. A
/// panic of the method is caught and comes back as an error with its message.
///
/// The expansion's `unsafe` carries the macro's own spans, so the api crate
/// keeps `#![forbid(unsafe_code)]`:
///
/// ```no_run
/// #![forbid(unsafe_code)]
///
/// use yanet_sdk::{Batch, Builder, Built, CommitCx, Cx, FfiRecord, Module, Verdict};
/// use yanet_sdk_api::{Agent, ApiError};
///
/// struct Mine;
/// impl Module for Mine {
///     const NAME: &'static str = "mine";
///     type Config = u64;
///     type Prepared<'p> = ();
///     fn prepare(_: &CommitCx<'_, Self>) {}
///     fn handle<'id>(_: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
///         batch.dispatch(|_| Verdict::Pass);
///     }
/// }
///
/// #[derive(FfiRecord)]
/// #[repr(C)]
/// pub struct Input {
///     weight: u64,
/// }
///
/// pub struct MineApi;
///
/// #[yanet_sdk::api(module = Mine)]
/// impl MineApi {
///     pub fn new(
///         agent: &Agent,
///         name: &str,
///         input: &Input,
///         ranges: &[u32],
///     ) -> Result<Built<Mine, Agent>, ApiError> {
///         Builder::<Mine>::build(agent, name, |_| Ok(input.weight + ranges.len() as u64))
///     }
/// }
/// ```
#[proc_macro_attribute]
pub fn api(args: TokenStream, item: TokenStream) -> TokenStream {
    let args = parse_macro_input!(args as api::Args);
    let item = parse_macro_input!(item as ItemImpl);
    api::expand(args, item)
        .unwrap_or_else(syn::Error::into_compile_error)
        .into()
}

// Doc tests are the only users of the facade, which depends on this crate.
#[cfg(test)]
use {yanet_sdk as _, yanet_sdk_api as _};

/// Doc tests that pin what the derive refuses, with the compile error each
/// refusal produces, and the accepted twin of each.
#[cfg(doctest)]
mod doctests {
    /// A field that is not shared-memory data, such as `bool`, is refused.
    ///
    /// ```compile_fail,E0277
    /// use yanet_sdk::ShmLayout;
    ///
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// struct Config {
    ///     enabled: bool,
    /// }
    /// ```
    struct BoolFieldRefused;

    /// A number of the width the flag needs is accepted.
    ///
    /// ```
    /// use yanet_sdk::ShmLayout;
    ///
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// struct Config {
    ///     enabled: u8,
    /// }
    /// ```
    struct U8FlagAccepted;

    /// An enum is refused.
    ///
    /// ```compile_fail
    /// use yanet_sdk::ShmLayout;
    ///
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// enum Config {
    ///     Off,
    ///     On,
    /// }
    /// ```
    struct EnumRefused;

    /// A struct is accepted.
    ///
    /// ```
    /// use yanet_sdk::ShmLayout;
    ///
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// struct Config {
    ///     state: u32,
    /// }
    /// ```
    struct StructAccepted;

    /// A struct without fields is refused; an empty configuration is `()`.
    ///
    /// ```compile_fail
    /// use yanet_sdk::ShmLayout;
    ///
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// struct Config {}
    /// ```
    struct EmptyStructRefused;

    /// A generic struct is refused.
    ///
    /// ```compile_fail
    /// use yanet_sdk::ShmLayout;
    ///
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// struct Config<T> {
    ///     value: T,
    /// }
    /// ```
    struct GenericRefused;

    /// A struct of a concrete type is accepted.
    ///
    /// ```
    /// use yanet_sdk::ShmLayout;
    ///
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// struct Config {
    ///     value: u64,
    /// }
    /// ```
    struct ConcreteAccepted;

    /// Implicit padding is refused.
    ///
    /// ```compile_fail,E0080
    /// use yanet_sdk::ShmLayout;
    ///
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// struct Config {
    ///     narrow: u32,
    ///     wide: u64,
    /// }
    /// ```
    struct PaddingRefused;

    /// Naming the padding as a field is accepted.
    ///
    /// ```
    /// use yanet_sdk::ShmLayout;
    ///
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// struct Config {
    ///     narrow: u32,
    ///     pad: u32,
    ///     wide: u64,
    /// }
    /// ```
    struct PaddingNamedAccepted;

    /// A packed struct is refused, since its fields could be misaligned.
    ///
    /// ```compile_fail
    /// use yanet_sdk::ShmLayout;
    ///
    /// #[derive(ShmLayout)]
    /// #[repr(C, packed)]
    /// struct Config {
    ///     narrow: u8,
    ///     wide: u64,
    /// }
    /// ```
    struct PackedRefused;

    /// The same fields in C order, padded, are accepted.
    ///
    /// ```
    /// use yanet_sdk::ShmLayout;
    ///
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// struct Config {
    ///     narrow: u8,
    ///     pad: [u8; 7],
    ///     wide: u64,
    /// }
    /// ```
    struct CLayoutAccepted;

    /// A struct whose layout is not C's is refused.
    ///
    /// ```compile_fail
    /// use yanet_sdk::ShmLayout;
    ///
    /// #[derive(ShmLayout)]
    /// struct Config {
    ///     value: u64,
    /// }
    /// ```
    struct NonCReprRefused;

    /// With `repr(C)` it is accepted.
    ///
    /// ```
    /// use yanet_sdk::ShmLayout;
    ///
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// struct Config {
    ///     value: u64,
    /// }
    /// ```
    struct ReprCAccepted;

    /// A struct that needs drop is refused, as nothing drops shared memory.
    ///
    /// ```compile_fail,E0080
    /// use yanet_sdk::ShmLayout;
    ///
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// struct Config {
    ///     value: u64,
    /// }
    ///
    /// impl Drop for Config {
    ///     fn drop(&mut self) {}
    /// }
    /// ```
    struct DropRefused;

    /// Without the `Drop` impl it is accepted.
    ///
    /// ```
    /// use yanet_sdk::ShmLayout;
    ///
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// struct Config {
    ///     value: u64,
    /// }
    /// ```
    struct NoDropAccepted;

    /// An alignment beyond the block allocator's 64 is refused.
    ///
    /// ```compile_fail,E0080
    /// use yanet_sdk::ShmLayout;
    ///
    /// #[derive(ShmLayout)]
    /// #[repr(C, align(128))]
    /// struct Config {
    ///     value: [u64; 16],
    /// }
    /// ```
    struct Align128Refused;

    /// An alignment of 64 is accepted.
    ///
    /// ```
    /// use yanet_sdk::ShmLayout;
    ///
    /// #[derive(ShmLayout)]
    /// #[repr(C, align(64))]
    /// struct Config {
    ///     value: [u64; 8],
    /// }
    /// ```
    struct Align64Accepted;

    /// Parts that leave a field out are refused.
    ///
    /// ```compile_fail,E0063
    /// use yanet_sdk::{Owner, ShmLayout};
    ///
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// struct Config {
    ///     first: u64,
    ///     second: u64,
    /// }
    ///
    /// fn parts<'b, W: Owner>() -> ConfigParts<'b, W> {
    ///     ConfigParts { first: 1 }
    /// }
    /// ```
    struct PartsMissingFieldRefused;

    /// With every field named they are accepted.
    ///
    /// ```
    /// use yanet_sdk::{Owner, ShmLayout};
    ///
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// struct Config {
    ///     first: u64,
    ///     second: u64,
    /// }
    ///
    /// fn parts<'b, W: Owner>() -> ConfigParts<'b, W> {
    ///     ConfigParts { first: 1, second: 2 }
    /// }
    /// ```
    struct PartsAllFieldsAccepted;

    /// Each field takes the parts of its own type; a string is not a number.
    ///
    /// ```compile_fail,E0308
    /// use yanet_sdk::{Owner, ShmLayout};
    ///
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// struct Config {
    ///     first: u64,
    /// }
    ///
    /// fn parts<'b, W: Owner>() -> ConfigParts<'b, W> {
    ///     ConfigParts { first: "one" }
    /// }
    /// ```
    struct PartsWrongTypeRefused;

    /// A number is accepted as the parts of a number field.
    ///
    /// ```
    /// use yanet_sdk::{Owner, ShmLayout};
    ///
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// struct Config {
    ///     first: u64,
    /// }
    ///
    /// fn parts<'b, W: Owner>() -> ConfigParts<'b, W> {
    ///     ConfigParts { first: 1 }
    /// }
    /// ```
    struct PartsRightTypeAccepted;

    /// Parts of plain fields alone do not name their owner; refused.
    ///
    /// Outside a build closure, whose return type fixes it, a signature does.
    ///
    /// ```compile_fail,E0283
    /// use yanet_sdk::ShmLayout;
    ///
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// struct Config {
    ///     first: u64,
    /// }
    ///
    /// let _ = ConfigParts { first: 1 };
    /// ```
    struct PartsOwnerUnnamedRefused;

    /// A parameter outside the table, such as `bool`, is refused.
    ///
    /// ```compile_fail
    /// # use yanet_sdk::{Batch, Builder, Built, CommitCx, Cx, FfiRecord, Module, Verdict};
    /// # use yanet_sdk_api::{Agent, ApiError};
    /// # struct Mine;
    /// # struct Other;
    /// # impl Module for Mine {
    /// #     const NAME: &'static str = "mine";
    /// #     type Config = u64;
    /// #     type Prepared<'p> = ();
    /// #     fn prepare(_: &CommitCx<'_, Self>) {}
    /// #     fn handle<'id>(_: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
    /// #         batch.dispatch(|_| Verdict::Pass);
    /// #     }
    /// # }
    /// # pub struct Api;
    /// #[yanet_sdk::api(module = Mine)]
    /// impl Api {
    ///     pub fn new(agent: &Agent, name: &str, _flag: bool) -> Result<Built<Mine, Agent>, ApiError> {
    ///         Builder::<Mine>::build(agent, name, |_| Ok(0))
    ///     }
    /// }
    /// ```
    struct BoolParameterRefused;

    /// A parameter of the table is accepted.
    ///
    /// ```no_run
    /// # use yanet_sdk::{Batch, Builder, Built, CommitCx, Cx, FfiRecord, Module, Verdict};
    /// # use yanet_sdk_api::{Agent, ApiError};
    /// # struct Mine;
    /// # struct Other;
    /// # impl Module for Mine {
    /// #     const NAME: &'static str = "mine";
    /// #     type Config = u64;
    /// #     type Prepared<'p> = ();
    /// #     fn prepare(_: &CommitCx<'_, Self>) {}
    /// #     fn handle<'id>(_: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
    /// #         batch.dispatch(|_| Verdict::Pass);
    /// #     }
    /// # }
    /// # pub struct Api;
    /// #[yanet_sdk::api(module = Mine)]
    /// impl Api {
    ///     pub fn new(agent: &Agent, name: &str, _flag: u8) -> Result<Built<Mine, Agent>, ApiError> {
    ///         Builder::<Mine>::build(agent, name, |_| Ok(0))
    ///     }
    /// }
    /// ```
    struct IntParameterAccepted;

    /// A result other than the build's is refused.
    ///
    /// ```compile_fail
    /// # use yanet_sdk::{Batch, Builder, Built, CommitCx, Cx, FfiRecord, Module, Verdict};
    /// # use yanet_sdk_api::{Agent, ApiError};
    /// # struct Mine;
    /// # struct Other;
    /// # impl Module for Mine {
    /// #     const NAME: &'static str = "mine";
    /// #     type Config = u64;
    /// #     type Prepared<'p> = ();
    /// #     fn prepare(_: &CommitCx<'_, Self>) {}
    /// #     fn handle<'id>(_: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
    /// #         batch.dispatch(|_| Verdict::Pass);
    /// #     }
    /// # }
    /// # pub struct Api;
    /// #[yanet_sdk::api(module = Mine)]
    /// impl Api {
    ///     pub fn new(agent: &Agent, name: &str, _flag: u8) -> u32 {
    ///         0
    ///     }
    /// }
    /// ```
    struct PlainResultRefused;

    /// The build's result is accepted.
    ///
    /// ```no_run
    /// # use yanet_sdk::{Batch, Builder, Built, CommitCx, Cx, FfiRecord, Module, Verdict};
    /// # use yanet_sdk_api::{Agent, ApiError};
    /// # struct Mine;
    /// # struct Other;
    /// # impl Module for Mine {
    /// #     const NAME: &'static str = "mine";
    /// #     type Config = u64;
    /// #     type Prepared<'p> = ();
    /// #     fn prepare(_: &CommitCx<'_, Self>) {}
    /// #     fn handle<'id>(_: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
    /// #         batch.dispatch(|_| Verdict::Pass);
    /// #     }
    /// # }
    /// # pub struct Api;
    /// #[yanet_sdk::api(module = Mine)]
    /// impl Api {
    ///     pub fn new(agent: &Agent, name: &str, _flag: u8) -> Result<Built<Mine, Agent>, ApiError> {
    ///         Builder::<Mine>::build(agent, name, |_| Ok(0))
    ///     }
    /// }
    /// ```
    struct BuiltResultAccepted;

    /// A configuration of another module than the attribute's is refused.
    ///
    /// ```compile_fail
    /// # use yanet_sdk::{Batch, Builder, Built, CommitCx, Cx, FfiRecord, Module, Verdict};
    /// # use yanet_sdk_api::{Agent, ApiError};
    /// # struct Mine;
    /// # struct Other;
    /// # impl Module for Mine {
    /// #     const NAME: &'static str = "mine";
    /// #     type Config = u64;
    /// #     type Prepared<'p> = ();
    /// #     fn prepare(_: &CommitCx<'_, Self>) {}
    /// #     fn handle<'id>(_: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
    /// #         batch.dispatch(|_| Verdict::Pass);
    /// #     }
    /// # }
    /// # pub struct Api;
    /// #[yanet_sdk::api(module = Mine)]
    /// impl Api {
    ///     pub fn new(agent: &Agent, name: &str, _flag: u8) -> Result<Built<Other, Agent>, ApiError> {
    ///         Err(ApiError::from(yanet_sdk::Refusal::OutOfMemory))
    ///     }
    /// }
    /// ```
    struct OtherModuleRefused;

    /// The attribute's module is accepted.
    ///
    /// ```no_run
    /// # use yanet_sdk::{Batch, Builder, Built, CommitCx, Cx, FfiRecord, Module, Verdict};
    /// # use yanet_sdk_api::{Agent, ApiError};
    /// # struct Mine;
    /// # struct Other;
    /// # impl Module for Mine {
    /// #     const NAME: &'static str = "mine";
    /// #     type Config = u64;
    /// #     type Prepared<'p> = ();
    /// #     fn prepare(_: &CommitCx<'_, Self>) {}
    /// #     fn handle<'id>(_: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
    /// #         batch.dispatch(|_| Verdict::Pass);
    /// #     }
    /// # }
    /// # pub struct Api;
    /// #[yanet_sdk::api(module = Mine)]
    /// impl Api {
    ///     pub fn new(agent: &Agent, name: &str, _flag: u8) -> Result<Built<Mine, Agent>, ApiError> {
    ///         Builder::<Mine>::build(agent, name, |_| Ok(0))
    ///     }
    /// }
    /// ```
    struct OwnModuleAccepted;

    /// A record without `#[repr(C)]` is refused.
    ///
    /// ```compile_fail
    /// use yanet_sdk::FfiRecord;
    ///
    /// #[derive(FfiRecord)]
    /// struct Record {
    ///     value: u32,
    /// }
    /// ```
    struct RecordWithoutReprCRefused;

    /// A `repr(C)` record is accepted.
    ///
    /// ```
    /// use yanet_sdk::FfiRecord;
    ///
    /// #[derive(FfiRecord)]
    /// #[repr(C)]
    /// struct Record {
    ///     value: u32,
    /// }
    /// ```
    struct ReprCRecordAccepted;

    /// A field that is not a record, such as `bool`, is refused.
    ///
    /// ```compile_fail,E0277
    /// use yanet_sdk::FfiRecord;
    ///
    /// #[derive(FfiRecord)]
    /// #[repr(C)]
    /// struct Record {
    ///     flag: bool,
    /// }
    /// ```
    struct BoolFieldRecordRefused;

    /// A field of a fixed-width integer is accepted.
    ///
    /// ```
    /// use yanet_sdk::FfiRecord;
    ///
    /// #[derive(FfiRecord)]
    /// #[repr(C)]
    /// struct Record {
    ///     flag: u8,
    /// }
    /// ```
    struct IntFieldRecordAccepted;
}
