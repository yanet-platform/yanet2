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
/// rounded up to a power of two, and a block of 128 bytes or more to
/// [`LINE_BLOCK_ALIGN`], as the C block allocator aligns them; the C-facing
/// methods run the C routines they name.
///
/// [`LINE_BLOCK_ALIGN`]: crate::common::LINE_BLOCK_ALIGN
///
/// [`Owner::free`], [`Owner::free_in`], [`Owner::fini_header`] and
/// [`Owner::fini_object_header`] must not panic: the builder calls them from
/// destructors, also while unwinding. [`Owner::init_header`] and
/// [`Owner::init_object_header`] must not panic either: the root block is not
/// yet guarded when they run.
///
/// [`Owner::init_header`] and [`Owner::init_object_header`] must leave a live
/// C memory context in the header, over memory of this owner, as
/// `cp_module_init` and `cp_object_init` do: an embedded C object opens its
/// own context under it. A refused initialisation leaves nothing to finalise.
///
/// `Owner::worker_count` must be the number of workers of the dataplane the
/// configurations are published to, fixed for the life of the shared memory:
/// a worker reads and writes its per-worker state at its index below it,
/// unchecked.
///
/// The builder never makes two calls on one header at once, though the owner
/// may be shared, because the header's memory context and registries are not
/// promised to take concurrent calls; an owner may rely on that, and being
/// `Sync` alone does not say it.
pub unsafe trait Owner: 'static {
    /// The failure of a C routine.
    type Error: Error;

    /// Provenance root of the owner's memory.
    fn root(&self) -> ShmRoot;

    /// Number of workers of the dataplane, each indexed from zero; the SDK
    /// sizes per-worker state by it.
    #[doc(hidden)]
    fn worker_count(&self) -> u64;

    /// Block of the given size in bytes, `None` when the memory is exhausted.
    fn alloc(&self, size: usize) -> Option<BlockAddr>;

    /// Returns a block.
    ///
    /// # Safety
    ///
    /// The block must come from [`Owner::alloc`] with the same size and be no
    /// longer referenced.
    unsafe fn free(&self, addr: BlockAddr, size: usize);

    /// Block of the given size in bytes from a memory context, the one a C
    /// header of this owner embeds, `None` when the memory is exhausted.
    ///
    /// # Safety
    ///
    /// The context must be the memory context of an initialised header of
    /// this owner.
    unsafe fn alloc_in(&self, context: Context, size: usize) -> Option<BlockAddr>;

    /// Returns a block to the context it came from.
    ///
    /// # Safety
    ///
    /// The block must come from [`Owner::alloc_in`] of the same context with
    /// the same size, the context must still be initialised, and the block
    /// must be no longer referenced.
    unsafe fn free_in(&self, context: Context, addr: BlockAddr, size: usize);

    /// Initialises the C module header of a configuration of a module type
    /// and name, as `cp_module_init` does.
    ///
    /// # Safety
    ///
    /// The header must be at the start of a block of this owner, large enough
    /// for the header, which the routine overwrites whole.
    #[doc(alias = "cp_module_init")]
    unsafe fn init_header(&self, header: Header, module: &str, name: &str) -> Result<(), Self::Error>;

    /// Releases what [`Owner::init_header`] set up, as `cp_module_fini`
    /// does.
    ///
    /// # Safety
    ///
    /// The header must have been initialised by this owner and be no longer
    /// referenced.
    #[doc(alias = "cp_module_fini")]
    unsafe fn fini_header(&self, header: Header);

    /// Initialises the C object header of an object of a type and name, as
    /// `cp_object_init` does; the type must be one the dataplane loaded.
    ///
    /// # Safety
    ///
    /// As for [`Owner::init_header`].
    #[doc(alias = "cp_object_init")]
    unsafe fn init_object_header(&self, header: Header, ty: &str, name: &str) -> Result<(), Self::Error>;

    /// Releases what [`Owner::init_object_header`] set up, as
    /// `cp_object_fini` does.
    ///
    /// # Safety
    ///
    /// The header must have been initialised by
    /// [`Owner::init_object_header`] of this owner and be no longer
    /// referenced.
    #[doc(alias = "cp_object_fini")]
    unsafe fn fini_object_header(&self, header: Header);

    /// Registers a counter of the given number of values (C size) with the
    /// module's registry and returns its id.
    ///
    /// # Safety
    ///
    /// The header must be initialised by this owner and not published.
    unsafe fn register_counter(&self, header: Header, name: &str, size: u64) -> Result<u64, Self::Error>;

    /// Links a device to the module and returns its index.
    ///
    /// # Safety
    ///
    /// As for [`Owner::register_counter`].
    unsafe fn link_device(&self, header: Header, name: &str) -> Result<u64, Self::Error>;

    /// Links the object of a type and name to the module and returns its
    /// index, as `cp_module_link_object` does: a pair linked before keeps its
    /// index.
    ///
    /// # Safety
    ///
    /// As for [`Owner::register_counter`].
    #[doc(alias = "cp_module_link_object")]
    unsafe fn link_object(&self, header: Header, ty: &str, name: &str) -> Result<u64, Self::Error>;
}
