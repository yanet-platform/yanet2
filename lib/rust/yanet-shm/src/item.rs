//! The shared-memory item of a Rust type: the C header, then the body.
//!
//! An item carries no runtime description of its body. The binding of bytes
//! to code is the body's fingerprint, which the dataplane registers with the
//! item type and the control plane must present when it creates an item, so
//! the check runs once per created item and never on the packet path.

use core::mem::{align_of, size_of};

use crate::{Opaque, ShmLayout};

/// A shared-memory item: the C header, then a Rust body.
#[repr(C)]
pub struct ShmItem<H, B> {
    pub header: Opaque<H>,
    pub body: B,
}

/// Body offset and size of a [`ShmItem`] whose C header size and alignment
/// are known only at run time.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct ItemLayout {
    pub body_offset: usize,
    pub size: usize,
    pub align: usize,
}

const fn round_up(value: usize, align: usize) -> usize {
    value.div_ceil(align) * align
}

const fn max(a: usize, b: usize) -> usize {
    if a > b { a } else { b }
}

/// The `#[repr(C)]` layout of a [`ShmItem`] with a body `B` after a C header
/// of the given size and alignment.
pub const fn item_layout<B: ShmLayout>(header_size: usize, header_align: usize) -> ItemLayout {
    let body_offset = round_up(header_size, align_of::<B>());
    let align = max(header_align, align_of::<B>());
    ItemLayout {
        body_offset,
        size: round_up(body_offset + size_of::<B>(), align),
        align,
    }
}
