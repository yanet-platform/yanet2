//! The description of shared-memory layouts, which the Go views of a module's
//! configuration are generated from.
//!
//! A [`ShmLayout`] type describes itself in its constant: a derived struct by
//! Rust's own offsets of its fields and by its type arguments, a leaf as an
//! integer or an array, a mirror of a C struct by its tag name, and an SDK
//! field type by its members, each with its type over placeholder type
//! arguments; the rest stay opaque. Each number is evaluated by rustc in the
//! crate that defines the type, so the description holds private fields and the
//! SDK's internals as well.
//!
//! [`ShmLayout`]: crate::shm::ShmLayout

use core::mem::{align_of, size_of};

use crate::shm::ShmLayout;

/// The layout of a type: its size, its alignment and what it is.
#[derive(Debug, PartialEq)]
pub struct Node {
    /// Bytes of the type.
    pub size: usize,
    /// Alignment of the type.
    pub align: usize,
    /// What the type is.
    pub kind: Kind,
}

impl Node {
    /// The layout of `T`, described as `kind`.
    pub const fn of<T>(kind: Kind) -> Self {
        Self {
            size: size_of::<T>(),
            align: align_of::<T>(),
            kind,
        }
    }
}

/// What a type is.
#[derive(Debug, PartialEq)]
pub enum Kind {
    /// An integer.
    Int {
        /// Whether the integer is signed.
        signed: bool,
        /// Whether only atomic accesses touch the integer.
        atomic: bool,
    },
    /// An array.
    Array {
        /// Number of elements.
        len: usize,
        /// Layout of an element.
        elem: &'static Node,
    },
    /// A struct of `#[derive(ShmLayout)]`.
    Struct {
        /// Path of the struct, without its type arguments.
        name: &'static str,
        /// Fields, in declaration order.
        fields: &'static [Field],
        /// Layouts of its type arguments, which tell its instances apart.
        args: &'static [&'static Node],
    },
    /// A field type of the SDK.
    Edge {
        /// Which field type it is.
        kind: &'static str,
        /// Its words, and the C table of a classifier node.
        members: &'static [Field],
        /// Layouts of its type arguments.
        args: &'static [&'static Node],
    },
    /// A relative pointer.
    Ptr {
        /// Layout of the target.
        target: &'static Node,
    },
    /// The type argument of a field type at this index, in its members.
    Param {
        /// Index of the type argument.
        index: usize,
    },
    /// A mirror of a C struct, whose layout the C views describe.
    C {
        /// Tag name of the C struct.
        name: &'static str,
    },
    /// A type with no description, which the generator refuses.
    Opaque,
}

/// A field of a struct, or a member of an SDK field type.
#[derive(Debug, PartialEq)]
pub struct Field {
    /// Name of the field.
    pub name: &'static str,
    /// Byte offset of the field in its type.
    pub offset: usize,
    /// Layout of the field's type; a member's is over [`Param`] type
    /// arguments.
    pub ty: &'static Node,
}

/// The placeholder of the type argument at index `N` of a field type, which
/// its members are described over.
pub struct Param<const N: usize>;

/// The layout of the type of a member, which `member` selects.
pub const fn member_type<S, T: ShmLayout>(_member: fn(&S) -> &T) -> &'static Node {
    T::LAYOUT
}
