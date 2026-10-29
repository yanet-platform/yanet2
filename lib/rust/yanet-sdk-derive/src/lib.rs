//! Derive macros of the Rust module SDK.
//!
//! `ShmLayout` checks storage layout, emits a named initializer and ownership
//! release. It binds the actual SDK and core dependencies with extern-crate
//! items so caller aliases cannot replace validation or ownership cleanup. Use
//! it through `yanet-sdk`; the derive accepts no custom implementation path.

#![forbid(unsafe_code)]

mod shm_layout;

use proc_macro::TokenStream;
use syn::{DeriveInput, parse_macro_input};

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

// Doc tests are the only users of the facade, which depends on this crate.
#[cfg(test)]
use {yanet_sdk as _, yanet_sys as _};

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
}
