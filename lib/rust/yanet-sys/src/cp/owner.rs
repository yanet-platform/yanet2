//! Memory a configuration is built in, and the C routines it is built with.

use core::error::Error;

use yanet_shm::root::ShmRoot;

use super::addr::{BlockAddr, Context, Header};

/// Shared memory and C registries a configuration is built with.
///
/// Typed addresses cross this boundary; pointers are derived only from the
/// owner's provenance root. The owner holds its storage independently of any
/// borrow, so a build can borrow it for any lifetime.
///
/// # Safety
///
/// The root covers every returned block. Blocks belong to the caller until
/// freed and are aligned to min(64, next power of two of the requested size).
/// Header and registry methods implement the C operations specified below.
/// Successful header initialisation opens a C memory context over this owner's
/// storage, usable as a parent by embedded C objects; failure leaves no
/// resources requiring finalisation. Header initialisation cannot panic: the
/// root is still unguarded. Both block-release methods and header finalisation
/// cannot panic, including during unwinding. Calls on one header are serialised
/// even for a shared owner; its context and registries need no concurrent-call
/// support.
pub unsafe trait Owner: 'static {
    /// The failure of a C routine.
    type Error: Error;

    /// Provenance root of the owner's memory.
    fn root(&self) -> ShmRoot;

    /// Transfers a payload block of the requested byte size, or reports
    /// exhausted memory.
    fn alloc(&self, size: usize) -> Option<BlockAddr>;

    /// Returns an unreferenced block to the owner's allocator.
    ///
    /// # Safety
    ///
    /// The block must come from [`Owner::alloc`] on this owner with the same
    /// payload size, and no references to it may remain.
    unsafe fn free(&self, addr: BlockAddr, size: usize);

    /// Transfers a payload block of the requested byte size from a header's
    /// context, or reports exhausted memory.
    ///
    /// # Safety
    ///
    /// The context must belong to an initialised header of this owner.
    unsafe fn alloc_in(&self, context: Context, size: usize) -> Option<BlockAddr>;

    /// Returns a block to the context it came from.
    ///
    /// # Safety
    ///
    /// The block must come from [`Owner::alloc_in`] on this owner and context
    /// with the same payload size. The context must remain initialised, and no
    /// references to the block may remain.
    unsafe fn free_in(&self, context: Context, addr: BlockAddr, size: usize);

    /// Establishes the named module's C header and registries as
    /// `cp_module_init` does.
    ///
    /// # Safety
    ///
    /// The header must start a block of this owner large enough for the entire
    /// header to be overwritten. Failure must leave no resources requiring
    /// finalisation: the caller immediately frees the root block.
    unsafe fn init_header(&self, header: Header, module: &str, name: &str) -> Result<(), Self::Error>;

    /// Releases the header's context and registries as `cp_module_fini` does.
    ///
    /// # Safety
    ///
    /// The header must have been successfully initialised by this owner, and
    /// no references to it may remain.
    unsafe fn fini_header(&self, header: Header);

    /// Registers a single-value counter (C size 1) and returns its registry ID.
    ///
    /// # Safety
    ///
    /// The header must be initialised by this owner and remain unpublished.
    unsafe fn register_counter(&self, header: Header, name: &str) -> Result<u64, Self::Error>;

    /// Registers a packet/byte pair in a named runtime counter registry.
    ///
    /// # Safety
    /// The header must be initialized by this owner and remain unpublished.
    /// Successful indices must identify two adjacent values in that registry.
    unsafe fn register_counter_pair(
        &self,
        header: Header,
        registry: &str,
        name: &str,
    ) -> Result<(u64, u64), Self::Error>;

    /// Links a device to the module and returns its index.
    ///
    /// # Safety
    ///
    /// As for [`Owner::register_counter`].
    unsafe fn link_device(&self, header: Header, name: &str) -> Result<u64, Self::Error>;
}
