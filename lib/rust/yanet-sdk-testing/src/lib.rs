//! Shared memory of the SDK tests, including Miri.
//!
//! [`BlockArena`] carves every block out of one region and roots its
//! pointers at the region's base, as a product roots them at its one
//! mapping. [`View`] resolves edges through the arena's root. The `cp` module
//! (feature `cp`) is the owner of the configuration builder built on it.

extern crate alloc;

#[cfg(feature = "cp")]
pub mod cp;

use core::{
    alloc::Layout,
    cell::Cell,
    marker::PhantomData,
    ptr::{self, NonNull},
    sync::atomic::AtomicPtr,
};
use std::{
    alloc::{alloc, handle_alloc_error},
    sync::Mutex,
};

use yanet_shm::{resolve::Resolve, root::ShmRoot};

/// Bytes of the region an arena carves its blocks from; the pages are
/// touched only when a test writes them.
const REGION_SIZE: usize = 1 << 20;

/// Alignment of a block, and bytes left unwritten after each one.
const BLOCK: usize = 64;

/// Shared memory of a test: one leaked region, whose base is the root.
///
/// It is the model of a product, which has one mapping. Blocks are never
/// freed.
///
/// The region starts uninitialised and only a handed-out block is zeroed,
/// with an unwritten gap after it. A read from a block into the next one is
/// therefore a read of uninitialised memory, which Miri reports; one that
/// leaves the block by more than its gap, or reaches a block already
/// handed out, is not.
///
/// Every arena is leaked and never dropped, so a root safe code keeps stays
/// valid.
pub struct BlockArena {
    /// Base of the region, whose provenance covers every block.
    region: NonNull<u8>,
    /// Bytes of the region handed out.
    used: Cell<usize>,
}

/// Every arena created: memory reachable from a static is not a leak to
/// Miri. The list is kept only for that and never read; `AtomicPtr` only
/// makes the entries `Send`, which a reference to an arena (it holds a
/// `Cell`) is not.
static ARENAS: Mutex<Vec<AtomicPtr<BlockArena>>> = Mutex::new(Vec::new());

impl BlockArena {
    /// Empty arena, leaked.
    pub fn new() -> &'static Self {
        let layout = Layout::from_size_align(REGION_SIZE, BLOCK).expect("region layout");
        // SAFETY: the layout has a non-zero size.
        let region = NonNull::new(unsafe { alloc(layout) }).unwrap_or_else(|| handle_alloc_error(layout));
        let arena: &'static Self = Box::leak(Box::new(Self { region, used: Cell::new(0) }));
        ARENAS
            .lock()
            .unwrap()
            .push(AtomicPtr::new(ptr::from_ref(arena).cast_mut()));
        arena
    }

    /// Allocates a zeroed block aligned to 64, followed by a gap of at least
    /// 64 unwritten bytes.
    pub fn alloc(&self, size: usize) -> NonNull<u8> {
        let size = size.max(1);
        let offset = self.used.get();
        let end = size
            .next_multiple_of(BLOCK)
            .checked_add(BLOCK)
            .and_then(|step| offset.checked_add(step))
            .filter(|&end| end <= REGION_SIZE)
            .expect("the test arena is out of memory");
        self.used.set(end);
        // SAFETY: the block lies inside the region, whose base carries the
        // provenance of all of it.
        unsafe {
            let block = self.region.add(offset);
            block.write_bytes(0, size);
            block
        }
    }

    /// Root of this arena: its region's base.
    pub fn root(&'static self) -> ShmRoot {
        // SAFETY: the base is a pointer to the one allocation the arena's
        // blocks lie in, never freed; it is not derived from a reference, so
        // its provenance covers all of the region.
        unsafe { ShmRoot::from_mapping(self.region) }
    }
}

/// A resolving context over a test root, as the runtimes' contexts are over
/// the mapping.
pub struct View<'g> {
    root: ShmRoot,
    // Invariant brand: values of two different contexts never unify, so they cannot be mixed.
    _brand: PhantomData<fn(&'g ()) -> &'g ()>,
}

impl View<'_> {
    /// View resolving through a test root.
    ///
    /// # Safety
    ///
    /// The [`Resolve`] contract: every `'g` reference passed to the view
    /// belongs to a well-formed built graph, inside the memory the root
    /// covers, and stays frozen for the view's lifetime.
    pub unsafe fn new(root: ShmRoot) -> Self {
        Self { root, _brand: PhantomData }
    }
}

// SAFETY: forwarded to the caller that made the view.
unsafe impl<'g> Resolve<'g> for View<'g> {
    fn shm_root(&self) -> ShmRoot {
        self.root
    }
}
