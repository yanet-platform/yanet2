//! Derive macros of the Rust module SDK.
//!
//! `ShmLayout` checks storage layout, emits a named initializer and ownership
//! release. It binds the actual SDK and core dependencies with extern-crate
//! items so caller aliases cannot replace validation or ownership cleanup. Use
//! it through `yanet-sdk`; the derive accepts no custom implementation path.

#![forbid(unsafe_code)]

mod api;
mod ffi_record;
mod shm_layout;

use proc_macro::TokenStream;
use syn::{DeriveInput, ItemImpl, parse_macro_input};

/// Derives checked shared-memory layout and ownership release.
///
/// Concrete C-layout records require named fields, alignment at most 64, no
/// implicit padding and no drop glue. Each field must satisfy the storage
/// release contract. The generated initializer writes each field directly at
/// its final address, with exact output types and rollback on errors or panics.
///
/// ```
/// #![forbid(unsafe_code)]
/// use yanet_sdk::{ShmBox, ShmLayout};
/// #[derive(ShmLayout)]
/// #[repr(C)]
/// struct Item {
///     value: u64,
/// }
/// #[derive(ShmLayout)]
/// #[repr(C)]
/// struct Config {
///     first: ShmBox<Item>,
///     flags: u32,
///     pad: u32,
/// }
/// ```
#[proc_macro_derive(ShmLayout)]
pub fn derive_shm_layout(input: TokenStream) -> TokenStream {
    let input = parse_macro_input!(input as DeriveInput);
    shm_layout::expand(&input)
        .unwrap_or_else(syn::Error::into_compile_error)
        .into()
}

/// Derives the C input-record contract for a concrete named-field struct.
///
/// The record requires plain C representation, without explicit alignment or
/// packing. Every field must be an integer, an array of input records or
/// another derived input record. Padding is never read as a field; explicitly
/// selected records are declared in the generated C header.
#[proc_macro_derive(FfiRecord)]
pub fn derive_ffi_record(input: TokenStream) -> TokenStream {
    let input = parse_macro_input!(input as DeriveInput);
    ffi_record::expand(&input)
        .unwrap_or_else(syn::Error::into_compile_error)
        .into()
}

/// Exports public inherent methods and configuration release to C.
///
/// Methods accept one agent plus strings, input records, slices and supported
/// integers, and return a built configuration or native error. Generated
/// exports preserve argument order, add an optional trailing error output and
/// contain panics. Release retains a busy configuration with EAGAIN for a later
/// retry. Authored crates keep their unsafe-code prohibition; the SDK README
/// documents the ABI and examples.
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
use {yanet_sdk as _, yanet_sdk_api as _, yanet_sys as _};

/// Doc tests that pin what the derive refuses, with the compile error each
/// refusal produces, and the accepted twin of each.
#[cfg(doctest)]
mod doctests {
    /// FFI input records preserve their declared C representation.
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
    struct FfiRecordInputAccepted;

    /// Typed API exports preserve the author crate's ban on unsafe code.
    ///
    /// ```no_run
    /// #![forbid(unsafe_code)]
    ///
    /// use yanet_sdk::{Batch, Built, CommitCx, Cx, FfiRecord, Module, Verdict};
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
    ///         agent.build_config::<Mine>(name, input.weight + ranges.len() as u64)
    ///     }
    /// }
    /// ```
    struct ApiExportsForbidUnsafe;

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
}
