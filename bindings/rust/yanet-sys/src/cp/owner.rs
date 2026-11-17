//! Memory a configuration is built in, and the C routines it is built with.

use core::error::Error;

use yanet_shm::root::ShmRoot;

use super::{BlockAddr, Context, Freed, Fresh, Live, LiveContext, ModuleKind, ObjectKind, Retiring};

/// Shared memory and C registries a configuration is built with.
///
/// The builder issues capabilities over one owner's blocks and headers.
/// Token-bearing operations have no independent receiver. Implementations use
/// the owner carried by the capability.
///
/// # Safety
///
/// `root` retains raw-pointer provenance over every block `alloc` and
/// `alloc_in` return, never derived from a Rust reference. Blocks belong to
/// the caller until returned. Their alignment is the smaller of 64 and their
/// size rounded up to a power of two, or [`LINE_BLOCK_ALIGN`] for blocks of
/// 128 bytes or more.
///
/// Each operation acts only on the capability's owner, block or header.
/// Allocation and registration must retain no borrowed capability. Freeing
/// returns exactly the named block with its recorded size and context.
/// Finalization and freeing must not panic. Header initialization must not
/// panic and must leave nothing to finalize on failure.
///
/// When `C_MEMORY` is true, initialization leaves a live C memory context in
/// the header, as `cp_module_init` and `cp_object_init` do. C-facing methods
/// run the routines they name.
///
/// `worker_count` is fixed for the shared memory's life and equals the
/// dataplane's worker count. A worker indexes its per-worker state unchecked.
/// The builder confines header capabilities to one thread. `Sync` alone does
/// not promise concurrent access to a header's C context or registries.
///
/// [`LINE_BLOCK_ALIGN`]: crate::common::LINE_BLOCK_ALIGN
pub unsafe trait Owner: 'static {
    /// Whether initialized headers carry a live C memory context.
    ///
    /// Owners without one refuse embedded C objects with
    /// [`super::Refusal::NoCMemory`].
    const C_MEMORY: bool;

    /// The failure of a C routine.
    type Error: Error;

    /// Provenance root of the owner's memory.
    fn root(&self) -> ShmRoot;

    /// Number of workers the owner represents.
    #[doc(hidden)]
    fn worker_count(&self) -> u64;

    /// Allocates an owned block of the given size in bytes.
    fn alloc(&self, size: usize) -> Option<BlockAddr>;

    /// Returns an unreferenced root block to its owner.
    fn free(block: Freed<'_, Self>);

    /// Allocates a block in a live header's memory context.
    fn alloc_in(context: &LiveContext<'_, Self>, size: usize) -> Option<BlockAddr>;

    /// Returns an unreferenced block to its live header's context.
    fn free_in(block: Freed<'_, Self, Context>);

    /// Initializes a module header in a fresh root block.
    #[doc(alias = "cp_module_init")]
    fn init_header(block: Fresh<'_, Self, ModuleKind>, module: &str, name: &str) -> Result<(), Self::Error>;

    /// Finalizes an unreferenced module header.
    #[doc(alias = "cp_module_fini")]
    fn fini_header(header: Retiring<'_, Self, ModuleKind>);

    /// Initializes an object header in a fresh root block.
    #[doc(alias = "cp_object_init")]
    fn init_object_header(block: Fresh<'_, Self, ObjectKind>, ty: &str, name: &str) -> Result<(), Self::Error>;

    /// Finalizes an unreferenced object header.
    #[doc(alias = "cp_object_fini")]
    fn fini_object_header(header: Retiring<'_, Self, ObjectKind>);

    /// Registers a counter in an unpublished module's registry.
    fn register_counter(header: &Live<'_, Self, ModuleKind>, name: &str, size: u64) -> Result<u64, Self::Error>;

    /// Finds or creates a module's tagged registry without a counter.
    #[doc(alias = "cp_module_counter_registry")]
    fn ensure_counter_registry(header: &Live<'_, Self, ModuleKind>, tag: &str) -> Result<u64, Self::Error>;

    /// Registers a counter in an unpublished module's tagged registry.
    #[doc(alias = "cp_module_counter_registry")]
    fn register_counter_in(
        header: &Live<'_, Self, ModuleKind>,
        tag: &str,
        name: &str,
        size: u64,
    ) -> Result<(u64, u64), Self::Error>;

    /// Registers a counter in an unpublished object's registry.
    fn register_object_counter(header: &Live<'_, Self, ObjectKind>, name: &str, size: u64) -> Result<u64, Self::Error>;

    /// Links a device to an unpublished module.
    fn link_device(header: &Live<'_, Self, ModuleKind>, name: &str) -> Result<u64, Self::Error>;

    /// Links an object by type and name to an unpublished module.
    #[doc(alias = "cp_module_link_object")]
    fn link_object(header: &Live<'_, Self, ModuleKind>, ty: &str, name: &str) -> Result<u64, Self::Error>;
}
