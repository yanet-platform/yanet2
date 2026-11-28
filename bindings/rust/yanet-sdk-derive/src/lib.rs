//! Derive macros of the Rust module SDK.
//!
//! The derive is the only implementation of the `unsafe` layout trait a module
//! crate has; its expansion is audited by review, since `forbid(unsafe_code)`
//! in a module crate does not see macro output. SDK and core paths bind
//! explicitly to external crates so caller aliases cannot replace trusted
//! checks or helpers. Use it through the `yanet-sdk` crate.

#![forbid(unsafe_code)]

mod shm_layout;

use proc_macro::TokenStream;
use syn::{DeriveInput, parse_macro_input};

/// Derives `ShmLayout` for a shared-memory struct.
///
/// Accepts only a `#[repr(C)]` struct with named fields (an alignment up to 64
/// may be added), every field of which is `ShmLayout`, with no drop glue.
/// Padding is never read as data.
///
/// A struct may take type and const parameters, but no lifetime: each type
/// parameter is bound by `ShmLayout`, and every field must be `ShmLayout` for
/// every argument the bounds admit.
///
/// The layout description of the struct, which the Go views are generated
/// from, gives its path, its size, its alignment and each field's offset as
/// Rust lays them out.
///
/// ```
/// use yanet_sdk::{ShmLayout, ShmSlice};
///
/// #[derive(ShmLayout)]
/// #[repr(C)]
/// struct Table<T, const N: usize> {
///     slots: ShmSlice<T>,
///     heads: [u32; N],
/// }
///
/// fn is_layout<T: ShmLayout>() {}
/// is_layout::<Table<u64, 4>>();
/// ```
///
/// ```
/// #![forbid(unsafe_code)]
///
/// use yanet_sdk::{ShmBox, ShmLayout};
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
///     // Reserved bytes remain explicit when they are part of an ABI.
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

    /// A struct with a lifetime is refused, as shared memory holds only
    /// `'static` data.
    ///
    /// ```compile_fail
    /// use yanet_sdk::ShmLayout;
    ///
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// struct Config<'a> {
    ///     value: &'a u64,
    /// }
    /// ```
    struct LifetimeRefused;

    /// A struct generic over its field type is accepted, and each instance is
    /// shared-memory data.
    ///
    /// ```
    /// use yanet_sdk::{ShmBox, ShmLayout};
    ///
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// struct Config<T> {
    ///     value: T,
    ///     boxed: ShmBox<T>,
    /// }
    ///
    /// fn is_layout<T: ShmLayout>() {}
    /// is_layout::<Config<u64>>();
    /// is_layout::<Config<Config<u32>>>();
    /// ```
    struct GenericAccepted;

    /// A generic struct instantiated with a type that is not shared-memory
    /// data is refused.
    ///
    /// ```compile_fail,E0277
    /// use yanet_sdk::ShmLayout;
    ///
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// struct Config<T> {
    ///     value: T,
    /// }
    ///
    /// fn is_layout<T: ShmLayout>() {}
    /// is_layout::<Config<bool>>();
    /// ```
    struct GenericBoolArgumentRefused;

    /// A field that needs more of a parameter than `ShmLayout`, as a mutex
    /// needs `Copy`, is refused unless the struct bounds the parameter.
    ///
    /// ```compile_fail,E0277
    /// use yanet_sdk::{Mutex, ShmLayout};
    ///
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// struct Config<T> {
    ///     locked: Mutex<T>,
    /// }
    /// ```
    struct GenericFieldUnboundRefused;

    /// With the bounds the field needs, it is accepted.
    ///
    /// ```
    /// use yanet_sdk::{Mutex, ShmLayout};
    ///
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// struct Config<T: Copy + ShmLayout> {
    ///     locked: Mutex<T>,
    /// }
    /// ```
    struct GenericFieldBoundAccepted;

    /// A const parameter is accepted.
    ///
    /// ```
    /// use yanet_sdk::ShmLayout;
    ///
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// struct Config<const N: usize> {
    ///     values: [u64; N],
    /// }
    ///
    /// fn is_layout<T: ShmLayout>() {}
    /// is_layout::<Config<4>>();
    /// ```
    struct ConstGenericAccepted;

    /// A generic struct that needs drop is refused where it is declared.
    ///
    /// ```compile_fail,E0119
    /// use yanet_sdk::ShmLayout;
    ///
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// struct Config<T> {
    ///     value: T,
    /// }
    ///
    /// impl<T> Drop for Config<T> {
    ///     fn drop(&mut self) {}
    /// }
    /// ```
    struct GenericDropRefused;

    /// A generic struct aligned beyond 64 is refused where it is declared.
    ///
    /// ```compile_fail,E0080
    /// use yanet_sdk::ShmLayout;
    ///
    /// #[derive(ShmLayout)]
    /// #[repr(C, align(128))]
    /// struct Config<T> {
    ///     value: T,
    /// }
    /// ```
    struct GenericAlign128Refused;

    /// A generic struct with a hint beyond 64 among several is refused, as
    /// the compiler takes the largest.
    ///
    /// ```compile_fail,E0080
    /// use yanet_sdk::ShmLayout;
    ///
    /// #[derive(ShmLayout)]
    /// #[repr(C, align(256), align(8))]
    /// struct Config<T> {
    ///     value: T,
    /// }
    /// ```
    struct GenericAlignHintsRefused;

    /// A generic struct aligned to 64 is accepted.
    ///
    /// ```
    /// use yanet_sdk::ShmLayout;
    ///
    /// #[derive(ShmLayout)]
    /// #[repr(C, align(64))]
    /// struct Config<T> {
    ///     value: T,
    /// }
    /// ```
    struct GenericAlign64Accepted;

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

    /// Implicit padding is accepted for typed shared-memory access.
    ///
    /// ```
    /// use yanet_sdk::ShmLayout;
    ///
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// struct Config {
    ///     narrow: u32,
    ///     wide: u64,
    /// }
    /// ```
    struct PaddingAccepted;

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
