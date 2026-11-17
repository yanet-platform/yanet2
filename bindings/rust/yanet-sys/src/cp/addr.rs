//! Raw addresses exposed to owner implementations, not lifecycle capabilities.
//!
//! An address alone proves neither initialization nor ownership. The builder
//! supplies owner-bound capabilities for operations on headers and allocations.
//!
//! # Header kinds
//!
//! A live object cannot register a module counter:
//!
//! ```compile_fail,E0308
//! use yanet_sys::cp::{Live, ObjectKind, Owner};
//!
//! fn object_as_module<W: Owner>(header: &Live<'_, W, ObjectKind>) {
//!     let _ = W::register_counter(header, "packets", 1);
//! }
//! ```
//!
//! A live module cannot register an object counter either:
//!
//! ```compile_fail,E0308
//! use yanet_sys::cp::{Live, ModuleKind, Owner};
//!
//! fn module_as_object<W: Owner>(header: &Live<'_, W, ModuleKind>) {
//!     let _ = W::register_object_counter(header, "packets", 1);
//! }
//! ```
//!
//! Matching kinds permit their corresponding registrations:
//!
//! ```
//! use yanet_sys::cp::{Live, ModuleKind, ObjectKind, Owner};
//!
//! fn matching_kinds<W: Owner>(
//!     module: &Live<'_, W, ModuleKind>,
//!     object: &Live<'_, W, ObjectKind>,
//! ) -> Result<(), W::Error> {
//!     W::register_counter(module, "packets", 1)?;
//!     W::register_object_counter(object, "packets", 1)?;
//!     Ok(())
//! }
//! ```
//!
//! A module's retirement permission cannot finalize an object:
//!
//! ```compile_fail,E0308
//! use yanet_sys::cp::{ModuleKind, Owner, Retiring};
//!
//! fn finalize_module_as_object<W: Owner>(header: Retiring<'_, W, ModuleKind>) {
//!     W::fini_object_header(header);
//! }
//! ```
//!
//! # Allocation contexts
//!
//! A root allocation cannot be returned as a child block:
//!
//! ```compile_fail,E0308
//! use yanet_sys::cp::{Freed, Owner};
//!
//! fn root_as_child<W: Owner>(block: Freed<'_, W>) {
//!     W::free_in(block);
//! }
//! ```
//!
//! A child block cannot be returned as a root allocation:
//!
//! ```compile_fail,E0308
//! use yanet_sys::cp::{Context, Freed, Owner};
//!
//! fn child_as_root<W: Owner>(block: Freed<'_, W, Context>) {
//!     W::free(block);
//! }
//! ```
//!
//! Matching operations retain the recorded context, address and size:
//!
//! ```
//! use yanet_sys::cp::{Context, Freed, Owner};
//!
//! fn matching_allocations<W: Owner>(root: Freed<'_, W>, child: Freed<'_, W, Context>) {
//!     let _ = (root.owner(), root.addr(), root.size());
//!     let _ = (child.owner(), child.context(), child.addr(), child.size());
//!     W::free(root);
//!     W::free_in(child);
//! }
//! ```
//!
//! # Consuming operations
//!
//! Initialization consumes its permission even if the attempt fails:
//!
//! ```compile_fail,E0382
//! use yanet_sys::cp::{Fresh, ModuleKind, Owner};
//!
//! fn initialize_twice<W: Owner>(block: Fresh<'_, W, ModuleKind>) {
//!     let _ = W::init_header(block, "module", "name");
//!     let _ = W::init_header(block, "module", "name");
//! }
//! ```
//!
//! Finalization cannot reuse its retirement permission:
//!
//! ```compile_fail,E0382
//! use yanet_sys::cp::{ModuleKind, Owner, Retiring};
//!
//! fn finalize_twice<W: Owner>(header: Retiring<'_, W, ModuleKind>) {
//!     W::fini_header(header);
//!     W::fini_header(header);
//! }
//! ```
//!
//! Returning a block consumes its freeing permission:
//!
//! ```compile_fail,E0382
//! use yanet_sys::cp::{Freed, Owner};
//!
//! fn free_twice<W: Owner>(block: Freed<'_, W>) {
//!     W::free(block);
//!     W::free(block);
//! }
//! ```
//!
//! Each permission permits one corresponding operation:
//!
//! ```
//! use yanet_sys::cp::{Freed, Fresh, ModuleKind, Owner, Retiring};
//!
//! fn initialize_once<W: Owner>(block: Fresh<'_, W, ModuleKind>) -> Result<(), W::Error> {
//!     W::init_header(block, "module", "name")
//! }
//!
//! fn finalize_once<W: Owner>(header: Retiring<'_, W, ModuleKind>) {
//!     W::fini_header(header);
//! }
//!
//! fn free_once<W: Owner>(block: Freed<'_, W>) {
//!     W::free(block);
//! }
//! ```
//!
//! # Private issuance
//!
//! A raw allocation address does not let a caller issue a fresh permission:
//!
//! ```compile_fail,E0624
//! use yanet_sys::cp::{BlockAddr, Fresh, ModuleKind, Owner};
//!
//! fn forge_fresh<W: Owner>(owner: &W, addr: BlockAddr) {
//!     let _: Fresh<'_, W, ModuleKind> = Fresh::new(owner, addr);
//! }
//! ```
//!
//! Nor can a caller manufacture a live header from raw parts:
//!
//! ```compile_fail,E0451
//! use core::marker::PhantomData;
//! use yanet_sys::cp::{Header, Live, ModuleKind, Owner};
//!
//! fn forge_live<W: Owner>(owner: &W, header: Header) {
//!     let _: Live<'_, W, ModuleKind> = Live {
//!         owner,
//!         header,
//!         _kind: PhantomData,
//!         _local: PhantomData,
//!     };
//! }
//! ```
//!
//! Owner implementations inspect capabilities the builder supplies instead:
//!
//! ```
//! use yanet_sys::cp::{Fresh, Header, Live, ModuleKind, Owner};
//!
//! fn supplied_fresh<W: Owner>(block: Fresh<'_, W, ModuleKind>) -> Header {
//!     let _ = block.owner();
//!     block.header()
//! }
//!
//! fn supplied_live<W: Owner>(header: &Live<'_, W, ModuleKind>) -> Header {
//!     let _ = header.owner();
//!     header.header()
//! }
//! ```
//!
//! # Owner-bound dispatch
//!
//! Even another instance of the same owner type cannot be selected as a
//! receiver for a capability:
//!
//! ```compile_fail,E0599
//! use yanet_sys::cp::{Live, ModuleKind, Owner};
//!
//! fn foreign_receiver<W: Owner>(foreign: &W, header: &Live<'_, W, ModuleKind>) {
//!     let _ = foreign.register_counter(header, "packets", 1);
//! }
//! ```
//!
//! Dispatch has no receiver to select. The implementation uses the owner
//! carried by the supplied capability:
//!
//! ```
//! use yanet_sys::cp::{Live, ModuleKind, Owner};
//!
//! fn bound_dispatch<W: Owner>(
//!     _foreign: &W,
//!     header: &Live<'_, W, ModuleKind>,
//! ) -> Result<u64, W::Error> {
//!     let _owner = header.owner();
//!     W::register_counter(header, "packets", 1)
//! }
//! ```

use core::mem::offset_of;

use crate::bindings;

/// Address of a module or object header, which is the start of the root block
/// of a configuration.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
#[repr(transparent)]
pub struct Header(usize);

/// Address of a C memory context, the one a module or object header embeds.
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
    /// Only the address is taken, without proving initialization or ownership.
    pub fn at(block: BlockAddr) -> Self {
        Self(block.0)
    }

    /// The memory context the header embeds, at the same offset in a module
    /// and an object header, as both start with the C entity base.
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
    /// Only the address is taken, without proving liveness or ownership.
    pub fn new(addr: usize) -> Self {
        Self(addr)
    }

    /// The address as an integer, to resolve through the owner's root.
    pub fn get(self) -> usize {
        self.0
    }
}

// A module and an object header embed their memory context at one offset.
const _: () = assert!(
    offset_of!(bindings::cp_module, entity.memory_context) == offset_of!(bindings::cp_object, entity.memory_context)
);
