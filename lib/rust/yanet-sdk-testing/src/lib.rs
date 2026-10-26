//! Shared memory of the SDK tests, including Miri.
//!
//! [`BlockArena`] carves every block out of one zeroed region and roots its
//! pointers at the region's base, as a product roots them at its one
//! mapping. Under Miri every block is instead its own Rust allocation and
//! addresses resolve through a block table, so Miri reports an access
//! crossing from one block into another, or to an address no live block
//! holds (a freed block's among them) as out of bounds of the fallback
//! allocation. [`View`], native and under Miri alike, resolves edges through
//! the arena's root. The `dp` module (feature `dp`) is the invocation harness
//! of the dataplane runtime built on it.

#![no_std]

extern crate alloc;

#[cfg(feature = "dp")]
pub mod dp;

use alloc::{boxed::Box, vec::Vec};
use core::{
    alloc::Layout,
    cell::RefCell,
    marker::PhantomData,
    mem::{align_of, size_of},
    ptr::NonNull,
};
#[cfg(miri)]
use core::{
    ptr,
    sync::atomic::{AtomicPtr, Ordering},
};

#[cfg(miri)]
use yanet_shm::root::BlockLookup;
use yanet_shm::{resolve::Resolve, root::ShmRoot};

/// A C-like object header: bytes C updates while Rust holds a reference to
/// the block around them.
///
/// Stands in for the C object that precedes a configuration body in a block
/// (a registry reference count, a memory-context sibling link).
#[repr(C)]
pub struct CHeader {
    /// Sibling link, rewritten by C.
    pub link: u64,
    /// Counter, updated by C.
    pub counter: u64,
}

/// Bytes of the region a native arena carves its blocks from; the pages
/// are touched only when a test writes them.
#[cfg(not(miri))]
const REGION_SIZE: usize = 1 << 20;

/// Shared memory of a test.
///
/// A native arena is one leaked zeroed region and its root the region's base:
/// the model of a product, which has one mapping. Under Miri every block is a
/// zeroed Rust allocation of its own, freed one by one; a native free only
/// forgets the block, so its memory is never handed out again.
///
/// Every arena is leaked and never dropped: under Miri a root is `Copy` and
/// holds a reference to the block table, so the table must outlive any root
/// safe code can keep.
pub struct BlockArena {
    blocks: RefCell<Vec<(NonNull<u8>, Layout)>>,
    /// Base of the region, whose provenance covers every block.
    #[cfg(not(miri))]
    region: NonNull<u8>,
    /// Bytes of the region handed out.
    #[cfg(not(miri))]
    used: core::cell::Cell<usize>,
    #[cfg(miri)]
    fallback: Box<u8>,
    /// The arena created before this one, in [`ARENAS`].
    #[cfg(miri)]
    next: AtomicPtr<BlockArena>,
}

/// Every arena created, newest first: memory reachable from a static is not
/// a leak to Miri.
#[cfg(miri)]
static ARENAS: AtomicPtr<BlockArena> = AtomicPtr::new(ptr::null_mut());

impl BlockArena {
    /// Empty arena, leaked.
    pub fn new() -> &'static Self {
        #[cfg(not(miri))]
        {
            // An alignment of 16 keeps the allocator on its lazily zeroed
            // path, which an alignment of 64 does not: the base is rounded
            // up instead, with room for it.
            let layout = Layout::from_size_align(REGION_SIZE + 64, 16).expect("region layout");
            // SAFETY: the layout has a non-zero size.
            let raw = NonNull::new(unsafe { alloc::alloc::alloc_zeroed(layout) }).expect("region allocation");
            // SAFETY: the rounding stays inside the allocation, whose
            // provenance the result keeps.
            let region = unsafe { raw.add(raw.align_offset(64)) };
            Box::leak(Box::new(Self {
                blocks: RefCell::new(Vec::new()),
                region,
                used: core::cell::Cell::new(0),
            }))
        }
        #[cfg(miri)]
        {
            let arena: &'static Self = Box::leak(Box::new(Self {
                blocks: RefCell::new(Vec::new()),
                fallback: Box::new(0),
                next: AtomicPtr::new(ptr::null_mut()),
            }));
            let this = ptr::from_ref(arena).cast_mut();
            let mut head = ARENAS.load(Ordering::Relaxed);
            loop {
                arena.next.store(head, Ordering::Relaxed);
                match ARENAS.compare_exchange_weak(head, this, Ordering::Release, Ordering::Relaxed) {
                    Ok(_) => return arena,
                    Err(current) => head = current,
                }
            }
        }
    }

    /// Allocates a zeroed block aligned to at least 64.
    pub fn alloc(&self, size: usize) -> NonNull<u8> {
        let layout = Layout::from_size_align(size.max(1), 64).expect("block layout");
        let block = self.carve(layout);
        self.blocks.borrow_mut().push((block, layout));
        block
    }

    /// A zeroed block of `layout`: a slice of the region.
    #[cfg(not(miri))]
    fn carve(&self, layout: Layout) -> NonNull<u8> {
        let offset = self.used.get();
        let end = offset.checked_add(layout.size().next_multiple_of(64));
        assert!(
            end.is_some_and(|end| end <= REGION_SIZE),
            "the test arena is out of memory"
        );
        self.used.set(end.unwrap_or(REGION_SIZE));
        // SAFETY: the block lies inside the region, whose base carries the
        // provenance of all of it.
        unsafe { self.region.add(offset) }
    }

    /// A zeroed block of `layout`: an allocation of its own.
    #[cfg(miri)]
    fn carve(&self, layout: Layout) -> NonNull<u8> {
        // SAFETY: the layout has a non-zero size.
        NonNull::new(unsafe { alloc::alloc::alloc_zeroed(layout) }).expect("block allocation")
    }

    /// Frees the block starting at `addr`.
    ///
    /// Under Miri a later access to an address no live block holds gets the
    /// fallback's provenance and is reported as out of bounds; a block
    /// allocated afterwards may reuse the address, and then the access is to
    /// that block.
    pub fn free(&self, addr: usize) {
        let mut blocks = self.blocks.borrow_mut();
        let idx = blocks
            .iter()
            .position(|(block, _)| block.as_ptr().addr() == addr)
            .expect("freeing an unknown block");
        let (block, layout) = blocks.swap_remove(idx);
        #[cfg(miri)]
        // SAFETY: the block was allocated with this layout and is removed
        // from the table, so the arena never hands it out again.
        unsafe {
            alloc::alloc::dealloc(block.as_ptr(), layout)
        };
        #[cfg(not(miri))]
        let _ = (block, layout);
    }

    /// Start and length of the live block containing `addr`.
    pub fn block_of(&self, addr: usize) -> Option<(usize, usize)> {
        self.blocks.borrow().iter().find_map(|(block, layout)| {
            let start = block.as_ptr().addr();
            (addr >= start && addr < start + layout.size()).then_some((start, layout.size()))
        })
    }

    /// Pointer to a `T` at `addr`, which must lie in one live block, aligned.
    ///
    /// The pointer has root provenance, so a write through it is in bounds of
    /// the block it names under every model.
    fn checked_ptr<T>(&'static self, addr: usize) -> *mut T {
        let inside = self
            .block_of(addr)
            .is_some_and(|(start, len)| addr.checked_add(size_of::<T>()).is_some_and(|end| end <= start + len));
        assert!(inside, "the value is not inside one live block of the arena");
        assert!(addr.is_multiple_of(align_of::<T>()), "the value is misaligned");
        self.root().ptr(addr)
    }

    /// Writes `value` at `addr`, as the control plane or C would store it.
    ///
    /// The old bytes are overwritten, not dropped. The write is raw, so a
    /// reference to the block that outlives it is the caller's to avoid: an
    /// invocation forbids writes to its configuration while it lives.
    ///
    /// # Panics
    ///
    /// Unless the bytes of the value lie in one live block and `addr` is
    /// aligned for `T`.
    fn write<T: Copy>(&'static self, addr: usize, value: T) {
        let ptr = self.checked_ptr::<T>(addr);
        // SAFETY: the pointer is aligned and in bounds of a live block
        // (checked), and a block is plain zeroed or written memory.
        unsafe { ptr.write(value) }
    }

    /// Reads the 8-byte word at `addr`; the panics are those of
    /// [`BlockArena::write`].
    fn read_word(&'static self, addr: usize) -> u64 {
        let ptr = self.checked_ptr::<u64>(addr);
        // SAFETY: as for `write`; every bit pattern is a `u64`.
        unsafe { ptr.read() }
    }

    /// Number of live blocks.
    pub fn live(&self) -> usize {
        self.blocks.borrow().len()
    }

    /// Root of this arena: its region's base, or under Miri a root
    /// resolving through the arena's block table.
    pub fn root(&'static self) -> ShmRoot {
        #[cfg(not(miri))]
        // SAFETY: the base is a pointer to the one allocation the arena's
        // blocks lie in, never freed; it is not derived from a reference, so
        // its provenance covers all of the region.
        return unsafe { ShmRoot::from_mapping(self.region) };
        #[cfg(miri)]
        // SAFETY: the fallback allocation, owned by the arena, lends its
        // provenance to the addresses no block holds; the table is the
        // arena's own.
        return unsafe { ShmRoot::from_blocks(self, NonNull::from(&*self.fallback)) };
    }
}

#[cfg(miri)]
impl BlockLookup for BlockArena {
    fn find(&self, addr: usize) -> Option<NonNull<u8>> {
        self.blocks
            .borrow()
            .iter()
            .find(|(block, layout)| {
                let start = block.as_ptr().addr();
                addr >= start && addr < start + layout.size()
            })
            .map(|(block, _)| *block)
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
    /// View resolving through `root`.
    ///
    /// # Safety
    ///
    /// The [`Resolve`] contract: every `'g` reference passed to the view
    /// belongs to a well-formed built graph, inside
    /// the memory `root` covers, and stays frozen for `'g`.
    pub unsafe fn new(root: ShmRoot) -> Self {
        Self { root, _brand: PhantomData }
    }
}

// SAFETY: forwarded to the caller of `View::new`.
unsafe impl<'g> Resolve<'g> for View<'g> {
    fn shm_root(&self) -> ShmRoot {
        self.root
    }
}
