//! Layout, fingerprint and strict-provenance checks, run natively and
//! under Miri.

use core::{
    alloc::Layout,
    mem::{align_of, offset_of, size_of},
    ops::Range,
    ptr::NonNull,
};

use super::{Opaque, RelPtr, RelSlice, Root, ShmError, ShmItem, ShmLayout, Validator, item_layout};

#[derive(ShmLayout)]
#[repr(C)]
struct Leaf {
    value: u32,
}

#[derive(ShmLayout)]
#[repr(C)]
struct Node {
    tag: u64,
    leaf: RelPtr<Leaf>,
    leaves: RelSlice<Leaf>,
}

#[derive(ShmLayout)]
#[repr(C)]
struct Pair {
    a: u32,
    b: u16,
}

#[derive(ShmLayout)]
#[repr(C)]
struct SwappedPair {
    b: u16,
    a: u32,
}

#[derive(ShmLayout)]
#[repr(C)]
struct WiderPair {
    a: u64,
    b: u16,
}

#[derive(ShmLayout)]
#[repr(C)]
struct WithOpaque {
    c_owned: Opaque<[u64; 3]>,
    value: u32,
}

const ARENA_SIZE: usize = 4096;

/// A zeroed, 64-byte aligned arena standing in for a mapping.
struct Arena {
    base: NonNull<u8>,
}

impl Arena {
    fn layout() -> Layout {
        Layout::from_size_align(ARENA_SIZE, 64).expect("valid arena layout")
    }

    fn new() -> Self {
        // SAFETY: the layout has a nonzero size.
        let base = unsafe { std::alloc::alloc_zeroed(Self::layout()) };
        Self {
            base: NonNull::new(base).expect("arena allocation"),
        }
    }

    fn addr(&self, offset: usize) -> usize {
        self.base.addr().get() + offset
    }

    fn write<T>(&self, offset: usize, value: T) {
        assert!(offset + size_of::<T>() <= ARENA_SIZE);
        // SAFETY: in bounds; callers pass aligned offsets.
        unsafe { self.base.add(offset).cast::<T>().write(value) };
    }

    /// Writes a relative offset in the slot at offset `slot` pointing to
    /// offset `target`.
    fn link(&self, slot: usize, target: usize) {
        self.write::<u64>(slot, (target as u64).wrapping_sub(slot as u64));
    }

    fn region(&self) -> Range<usize> {
        self.addr(0)..self.addr(ARENA_SIZE)
    }

    fn validate<T: ShmLayout>(&self, offset: usize) -> Result<(), ShmError> {
        let regions = [self.region()];
        // SAFETY: the region is the arena the root points into.
        let mut validator = unsafe { Validator::new(self.base, &regions) };
        T::validate(&mut validator, self.addr(offset))
    }

    fn node(&self, offset: usize) -> &Node {
        // SAFETY: in bounds, aligned and zero-initialized at least.
        unsafe { self.base.add(offset).cast::<Node>().as_ref() }
    }

    fn root(&self) -> Root<'_> {
        // SAFETY: the arena pointer covers every target the tests link.
        unsafe { Root::new(self.base) }
    }
}

impl Drop for Arena {
    fn drop(&mut self) {
        // SAFETY: allocated in the constructor with the same layout.
        unsafe { std::alloc::dealloc(self.base.as_ptr(), Self::layout()) };
    }
}

/// Places a node at 0 whose pointer targets a leaf at 256 and whose slice
/// targets two leaves at 512.
fn linked_arena() -> Arena {
    let arena = Arena::new();
    arena.write::<u64>(0, 7);
    arena.write::<u32>(256, 11);
    arena.write::<u32>(512, 21);
    arena.write::<u32>(516, 22);
    arena.link(offset_of!(Node, leaf), 256);
    arena.link(offset_of!(Node, leaves), 512);
    arena.write::<u64>(offset_of!(Node, leaves) + 8, 2);
    arena
}

#[test]
fn test_fingerprint_differs_for_swapped_fields() {
    assert_ne!(Pair::FINGERPRINT, SwappedPair::FINGERPRINT);
}

#[test]
fn test_fingerprint_differs_for_wider_field() {
    assert_ne!(Pair::FINGERPRINT, WiderPair::FINGERPRINT);
}

#[test]
fn test_fingerprint_is_deterministic() {
    #[derive(ShmLayout)]
    #[repr(C)]
    struct SamePair {
        a: u32,
        b: u16,
    }

    assert_eq!(Pair::FINGERPRINT, SamePair::FINGERPRINT);
}

#[test]
fn test_has_rel_propagates() {
    assert_eq!(
        [false, true, true],
        [Pair::HAS_REL, Node::HAS_REL, <[Node; 2]>::HAS_REL]
    );
}

#[test]
fn test_item_layout_matches_repr_c() {
    fn check<H, B: ShmLayout>() {
        let layout = item_layout::<B>(size_of::<H>(), align_of::<H>());
        assert_eq!(offset_of!(ShmItem<H, B>, body), layout.body_offset);
        assert_eq!(size_of::<ShmItem<H, B>>(), layout.size);
        assert_eq!(align_of::<ShmItem<H, B>>(), layout.align);
    }

    check::<[u8; 3], Pair>();
    check::<[u64; 5], Node>();
    check::<[u16; 7], u8>();
    check::<[u8; 1], WithOpaque>();
    check::<[u64; 69], [u8; 6]>();
}

#[test]
fn test_validate_accepts_linked_node() {
    let arena = linked_arena();

    assert_eq!(Ok(()), arena.validate::<Node>(0));
}

#[test]
fn test_validate_accepts_null_pointers() {
    let arena = Arena::new();

    assert_eq!(Ok(()), arena.validate::<Node>(0));
}

#[test]
fn test_validate_rejects_pointer_outside_regions() {
    let arena = linked_arena();
    arena.write::<u64>(offset_of!(Node, leaf), ARENA_SIZE as u64);

    assert!(matches!(arena.validate::<Node>(0), Err(ShmError::OutOfBounds { .. })));
}

#[test]
fn test_validate_rejects_misaligned_target() {
    let arena = linked_arena();
    arena.link(offset_of!(Node, leaf), 257);

    assert!(matches!(arena.validate::<Node>(0), Err(ShmError::Misaligned { .. })));
}

#[test]
fn test_validate_rejects_slice_past_region() {
    let arena = linked_arena();
    arena.write::<u64>(offset_of!(Node, leaves) + 8, 1 << 20);

    assert!(matches!(arena.validate::<Node>(0), Err(ShmError::OutOfBounds { .. })));
}

#[test]
fn test_validate_rejects_value_crossing_region_end() {
    let arena = Arena::new();

    assert!(matches!(
        arena.validate::<Node>(ARENA_SIZE - 8),
        Err(ShmError::OutOfBounds { .. })
    ));
}

#[test]
fn test_rel_ptr_resolves_with_root_provenance() {
    let arena = linked_arena();
    let node = arena.node(0);

    let leaf = node.leaf.get(arena.root()).expect("linked leaf");
    let leaves: Vec<u32> = node.leaves.get(arena.root()).iter().map(|leaf| leaf.value).collect();

    assert_eq!(7, node.tag);
    assert_eq!(11, leaf.value);
    assert_eq!(vec![21, 22], leaves);
}

#[test]
fn test_rel_ptr_null_resolves_to_none() {
    let arena = Arena::new();

    assert!(arena.node(0).leaf.get(arena.root()).is_none());
    assert!(arena.node(0).leaves.get(arena.root()).is_empty());
}

#[test]
fn test_reference_over_opaque_is_sound_while_c_writes() {
    let arena = Arena::new();
    // SAFETY: in bounds and aligned.
    let item = unsafe { arena.base.cast::<WithOpaque>().as_ref() };
    let c_owned = item.c_owned.0.get().cast::<u64>();

    // A C-side write through the cell while the shared reference lives.
    // SAFETY: the cell grants mutation; nothing reads it through Rust.
    unsafe { c_owned.write(5) };

    assert_eq!(0, item.value);
}
