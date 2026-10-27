//! Derive macros of the Rust module SDK.
//!
//! The derive is the only implementation of the `unsafe` layout trait a module
//! crate has; its expansion is audited by review, since `forbid(unsafe_code)`
//! in a module crate does not see macro output. It accepts no user-supplied
//! crate path: generated code names `::yanet_sdk::__private` only. Use it
//! through the `yanet-sdk` crate.

#![forbid(unsafe_code)]

mod shm_layout;

use proc_macro::TokenStream;
use syn::{DeriveInput, parse_macro_input};

/// Derives `ShmLayout` and `Build`, with the `<Type>Parts` struct a build
/// writes the value from, for a shared-memory struct.
///
/// Accepts only a non-generic `#[repr(C)]` struct with named fields (an
/// alignment up to 64 may be added), every field of which is `Build` (the SDK's
/// leaves, arrays, edges, counters and devices, embedded C objects such as
/// `Lpm4` and `Lpm6`, and derived structs),
/// with no implicit padding and no drop glue. The generated `<Type>Parts` has
/// one field per field of the struct, holding the parts of that field's type,
/// each written at the field's place, and is `Debug` when they all are. The
/// fields are written in order, and a refused field returns the ones before it.
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
///
/// A field that is not shared-memory data, such as `bool`, is refused:
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
///
/// A number of the width the flag needs is accepted:
///
/// ```
/// use yanet_sdk::{Owner, ShmLayout};
///
/// #[derive(ShmLayout)]
/// #[repr(C)]
/// struct Config {
///     enabled: u8,
/// }
/// ```
///
/// An enum is refused:
///
/// ```compile_fail
/// use yanet_sdk::{Owner, ShmLayout};
///
/// #[derive(ShmLayout)]
/// #[repr(C)]
/// enum Config {
///     Off,
///     On,
/// }
/// ```
///
/// A struct is accepted:
///
/// ```
/// use yanet_sdk::{Owner, ShmLayout};
///
/// #[derive(ShmLayout)]
/// #[repr(C)]
/// struct Config {
///     state: u32,
/// }
/// ```
///
/// A struct without fields is refused; an empty configuration is `()`:
///
/// ```compile_fail
/// use yanet_sdk::{Owner, ShmLayout};
///
/// #[derive(ShmLayout)]
/// #[repr(C)]
/// struct Config {}
/// ```
///
/// A generic struct is refused:
///
/// ```compile_fail
/// use yanet_sdk::{Owner, ShmLayout};
///
/// #[derive(ShmLayout)]
/// #[repr(C)]
/// struct Config<T> {
///     value: T,
/// }
/// ```
///
/// A struct of a concrete type is accepted:
///
/// ```
/// use yanet_sdk::{Owner, ShmLayout};
///
/// #[derive(ShmLayout)]
/// #[repr(C)]
/// struct Config {
///     value: u64,
/// }
/// ```
///
/// Implicit padding is refused:
///
/// ```compile_fail,E0080
/// use yanet_sdk::{Owner, ShmLayout};
///
/// #[derive(ShmLayout)]
/// #[repr(C)]
/// struct Config {
///     narrow: u32,
///     wide: u64,
/// }
/// ```
///
/// Naming the padding as a field is accepted:
///
/// ```
/// use yanet_sdk::{Owner, ShmLayout};
///
/// #[derive(ShmLayout)]
/// #[repr(C)]
/// struct Config {
///     narrow: u32,
///     pad: u32,
///     wide: u64,
/// }
/// ```
///
/// A packed struct is refused, since its fields could be misaligned:
///
/// ```compile_fail
/// use yanet_sdk::{Owner, ShmLayout};
///
/// #[derive(ShmLayout)]
/// #[repr(C, packed)]
/// struct Config {
///     narrow: u8,
///     wide: u64,
/// }
/// ```
///
/// The same fields in C order, padded, are accepted:
///
/// ```
/// use yanet_sdk::{Owner, ShmLayout};
///
/// #[derive(ShmLayout)]
/// #[repr(C)]
/// struct Config {
///     narrow: u8,
///     pad: [u8; 7],
///     wide: u64,
/// }
/// ```
///
/// A struct whose layout is not C's is refused:
///
/// ```compile_fail
/// use yanet_sdk::{Owner, ShmLayout};
///
/// #[derive(ShmLayout)]
/// struct Config {
///     value: u64,
/// }
/// ```
///
/// With `repr(C)` it is accepted:
///
/// ```
/// use yanet_sdk::{Owner, ShmLayout};
///
/// #[derive(ShmLayout)]
/// #[repr(C)]
/// struct Config {
///     value: u64,
/// }
/// ```
///
/// A struct that needs drop is refused, since nothing ever drops shared memory:
///
/// ```compile_fail,E0080
/// use yanet_sdk::{Owner, ShmLayout};
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
///
/// Without the `Drop` impl it is accepted:
///
/// ```
/// use yanet_sdk::{Owner, ShmLayout};
///
/// #[derive(ShmLayout)]
/// #[repr(C)]
/// struct Config {
///     value: u64,
/// }
/// ```
///
/// An alignment beyond the block allocator's 64 is refused:
///
/// ```compile_fail,E0080
/// use yanet_sdk::{Owner, ShmLayout};
///
/// #[derive(ShmLayout)]
/// #[repr(C, align(128))]
/// struct Config {
///     value: [u64; 16],
/// }
/// ```
///
/// An alignment of 64 is accepted:
///
/// ```
/// use yanet_sdk::{Owner, ShmLayout};
///
/// #[derive(ShmLayout)]
/// #[repr(C, align(64))]
/// struct Config {
///     value: [u64; 8],
/// }
/// ```
///
/// The parts name every field:
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
///
/// With every field named they are accepted:
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
///
/// Each field takes the parts of its own type; a string is not a number:
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
///
/// A number is:
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
///
/// Parts of plain fields alone do not name their owner, so outside a build
/// closure, whose return type fixes it, the owner is named in a signature:
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
#[proc_macro_derive(ShmLayout)]
pub fn derive_shm_layout(input: TokenStream) -> TokenStream {
    let input = parse_macro_input!(input as DeriveInput);
    shm_layout::expand(&input)
        .unwrap_or_else(syn::Error::into_compile_error)
        .into()
}

// Doc tests are the only users of the facade, which depends on this crate.
#[cfg(test)]
use yanet_sdk as _;
