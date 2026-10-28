//! Addresses an [`Owner`](super::Owner) is called with.
//!
//! Three kinds of address cross the owner seam; each has its own type, so a
//! header, a memory context and a block cannot be mixed up.

use core::mem::offset_of;

use crate::bindings;

/// Address of a module header, which is the start of the root block of a
/// configuration.
///
/// A header is not a memory context, so it is refused where a context is
/// expected:
///
/// ```compile_fail,E0308
/// use yanet_sys::cp::{Header, Owner};
///
/// // SAFETY: shown for the types only.
/// unsafe fn take<W: Owner>(owner: &W, header: Header) {
///     // SAFETY: shown for the types only.
///     unsafe { owner.alloc_in(header, 8) };
/// }
/// ```
///
/// The header's own context is what a block is allocated in:
///
/// ```
/// use yanet_sys::cp::{Header, Owner};
///
/// // SAFETY: shown for the types only.
/// unsafe fn take<W: Owner>(owner: &W, header: Header) {
///     // SAFETY: shown for the types only.
///     unsafe { owner.alloc_in(header.context(), 8) };
/// }
/// ```
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
#[repr(transparent)]
pub struct Header(usize);

/// Address of a C memory context, the one a module header embeds.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
#[repr(transparent)]
pub struct Context(usize);

/// Address of the payload of a block of an owner, as [`Owner::alloc`] returns
/// it.
///
/// [`Owner::alloc`]: super::Owner::alloc
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
#[repr(transparent)]
pub struct BlockAddr(usize);

impl Header {
    /// The header at the start of a root block.
    ///
    /// # Safety
    ///
    /// The block is large enough for a module header.
    pub unsafe fn at(block: BlockAddr) -> Self {
        Self(block.0)
    }

    /// The memory context the header embeds.
    pub fn context(self) -> Context {
        Context(self.0 + offset_of!(bindings::cp_module, entity.memory_context))
    }

    /// The address as an integer, to resolve through the owner's root.
    pub fn get(self) -> usize {
        self.0
    }
}

impl Context {
    /// The address as an integer, to resolve through the owner's root.
    pub fn get(self) -> usize {
        self.0
    }
}

impl BlockAddr {
    /// The block whose payload starts at an address.
    ///
    /// # Safety
    ///
    /// The address is the payload of a block of the owner.
    pub unsafe fn new(addr: usize) -> Self {
        Self(addr)
    }

    /// The address as an integer, to resolve through the owner's root.
    pub fn get(self) -> usize {
        self.0
    }
}
