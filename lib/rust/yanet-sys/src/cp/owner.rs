//! Memory a configuration is built in, and the C routines it is built with.

use core::error::Error;

use yanet_shm::root::ShmRoot;

use super::addr::{BlockAddr, Context, Header};

/// Shared memory and C registries a configuration is built with.
///
/// Addresses cross this interface as [`Header`], [`Context`] and
/// [`BlockAddr`]; pointers are minted from [`Owner::root`] only. An owner is an
/// owning type (`'static`), so a build can borrow it for any lifetime.
///
/// # Safety
///
/// The builder trusts every method: [`Owner::root`] must cover every block
/// [`Owner::alloc`] and [`Owner::alloc_in`] return; both return blocks owned
/// by the caller until freed, aligned to the smaller of 64 and the size
/// rounded up to a power of two; the C-facing methods run the C routines they
/// name.
///
/// [`Owner::free`], [`Owner::free_in`] and [`Owner::fini_header`] must not
/// panic: the builder calls them from `Drop`, also while unwinding.
/// [`Owner::init_header`] must not panic either: the root block is not yet
/// guarded when it runs.
///
/// [`Owner::init_header`] must leave a live C memory context in the header,
/// over memory of this owner, as `cp_module_init` does: an embedded C object
/// opens its own context under it.
///
/// The builder never makes two calls on one header at once, though `&W` may
/// be shared, because the header's memory context and registries are not
/// promised to take concurrent calls; an owner may rely on that, and a bound
/// `W: Sync` alone does not say it.
pub unsafe trait Owner: 'static {
    /// The failure of a C routine.
    type Error: Error;

    /// Provenance root of the owner's memory.
    fn root(&self) -> ShmRoot;

    /// Block of `size` bytes, `None` when the memory is exhausted.
    fn alloc(&self, size: usize) -> Option<BlockAddr>;

    /// Returns a block.
    ///
    /// # Safety
    ///
    /// `addr` must come from [`Owner::alloc`] with the same size and be no
    /// longer referenced.
    unsafe fn free(&self, addr: BlockAddr, size: usize);

    /// Block of `size` bytes from the memory context at `context`, the one a
    /// C header of this owner embeds, `None` when the memory is exhausted.
    ///
    /// # Safety
    ///
    /// `context` must be the memory context of an initialised header of
    /// this owner.
    unsafe fn alloc_in(&self, context: Context, size: usize) -> Option<BlockAddr>;

    /// Returns a block to the context it came from.
    ///
    /// # Safety
    ///
    /// `addr` must come from [`Owner::alloc_in`] of the same context with
    /// the same size, the context must still be initialised, and the block
    /// must be no longer referenced.
    unsafe fn free_in(&self, context: Context, addr: BlockAddr, size: usize);

    /// Initialises the C module header of a configuration of module type
    /// `module` named `name`, as `cp_module_init` does.
    ///
    /// # Safety
    ///
    /// `header` must be at the start of a block of this owner, large enough
    /// for the header, which the routine overwrites whole.
    unsafe fn init_header(&self, header: Header, module: &str, name: &str) -> Result<(), Self::Error>;

    /// Releases what [`Owner::init_header`] set up, as `cp_module_fini`
    /// does.
    ///
    /// # Safety
    ///
    /// `header` must have been initialised by this owner and be no longer
    /// referenced.
    unsafe fn fini_header(&self, header: Header);

    /// Registers a single-value counter (C size 1) with the module's
    /// registry and returns its id.
    ///
    /// # Safety
    ///
    /// `header` must be initialised by this owner and not published.
    unsafe fn register_counter(&self, header: Header, name: &str) -> Result<u64, Self::Error>;

    /// Links a device to the module and returns its index.
    ///
    /// # Safety
    ///
    /// As for [`Owner::register_counter`].
    unsafe fn link_device(&self, header: Header, name: &str) -> Result<u64, Self::Error>;
}
