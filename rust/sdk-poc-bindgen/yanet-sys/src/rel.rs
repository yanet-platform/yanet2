//! Self-relative pointers and strict-provenance resolution.
//!
//! A relative pointer stores `target - slot` (zero means NULL), exactly as
//! the C `ADDR_OF` family does. Resolution never derives provenance from the
//! slot reference: the slot contributes only its address, and the target
//! pointer is minted from a resolver root, a raw pointer whose provenance
//! covers the whole graph. No exposed provenance is used anywhere.

use core::{
    marker::{PhantomData, PhantomPinned},
    ptr::NonNull,
};

/// Self-relative pointer slot in shared memory, nullable.
///
/// Neither `Copy` nor `Clone` nor `Unpin`, with a private field and no
/// constructor: safe code only ever sees a `&RelPtr` pointing at the slot
/// in place, so the stored offset is never separated from the address it is
/// relative to.
#[repr(transparent)]
pub struct RelPtr<T> {
    offset: isize,
    _target: PhantomData<*const T>,
    _pinned: PhantomPinned,
}

impl<T> RelPtr<T> {
    /// Reports whether the slot stores NULL.
    pub fn is_null(&self) -> bool {
        self.offset == 0
    }

    /// Raw stored value; the target is `slot address + offset`.
    pub fn offset(&self) -> isize {
        self.offset
    }

    /// The slot reinterpreted as non-null.
    ///
    /// # Safety
    ///
    /// The slot must belong to a validated graph in which this edge is
    /// non-null whenever this call is reached.
    pub(crate) unsafe fn assume_non_null(&self) -> &RelRef<T> {
        debug_assert!(!self.is_null(), "relative pointer assumed non-null is NULL");
        // SAFETY: `RelRef` is a transparent wrapper of `RelPtr`.
        unsafe { &*(self as *const Self).cast::<RelRef<T>>() }
    }
}

/// Self-relative pointer slot that a validated graph guarantees non-null.
///
/// Resolving it skips the NULL test, like `ADDR_OF_NONNULL`.
#[repr(transparent)]
pub struct RelRef<T>(RelPtr<T>);

impl<T> RelRef<T> {
    /// Raw stored value; the target is `slot address + offset`.
    pub fn offset(&self) -> isize {
        self.0.offset
    }
}

/// Absolute address into the shared-memory mapping, without provenance.
///
/// Used for C fields that store an absolute pointer: only the address is
/// taken, the provenance comes from the resolver root like for relative
/// pointers.
pub struct AbsAddr<T> {
    addr: usize,
    _target: PhantomData<*const T>,
}

impl<T> AbsAddr<T> {
    #[doc(hidden)]
    pub fn new(addr: usize) -> Self {
        Self { addr, _target: PhantomData }
    }

    /// Stored address, zero for NULL.
    pub fn addr(&self) -> usize {
        self.addr
    }
}

pub(crate) mod sealed {
    pub trait RelSlot {}
    pub trait Resolver {}
}

impl<T> sealed::RelSlot for RelPtr<T> {}
impl<T> sealed::RelSlot for RelRef<T> {}

/// Compile-time witness that a declared `rel` type is a relative slot.
pub const fn assert_rel_slot<T: sealed::RelSlot>() {}

/// Turns relative slots of one shared-memory graph into references.
///
/// Sealed: the only implementations are the production mapping resolver and
/// the test block resolver. A resolver is obtained only through an `unsafe`
/// constructor whose contract states that every slot reachable through views
/// of this resolver is valid for `'g`: NULL or pointing at a live, frozen,
/// properly aligned `T` inside the graph, non-null where typed [`RelRef`].
/// Under that contract resolution is safe and does no runtime checks
/// (debug assertions only).
pub trait Resolver<'g>: Copy + sealed::Resolver {
    /// Pointer with root provenance for `slot_addr + offset`.
    #[doc(hidden)]
    fn target<T>(self, slot_addr: usize, offset: isize) -> *const T;

    /// Resolves a nullable slot.
    #[inline(always)]
    fn resolve<T>(self, slot: &'g RelPtr<T>) -> Option<&'g T> {
        if slot.offset == 0 {
            return None;
        }
        let ptr = self.target::<T>((slot as *const RelPtr<T>).addr(), slot.offset);
        debug_assert!(ptr.is_aligned(), "misaligned relative target");
        // SAFETY: the resolver contract makes a non-null slot point at a
        // live, frozen, aligned `T` for 'g, and `target` gives the pointer
        // provenance over it. Stating non-nullness lets the optimiser drop
        // the null test it would otherwise emit for the returned option.
        unsafe {
            core::hint::assert_unchecked(!ptr.is_null());
            Some(&*ptr)
        }
    }

    /// Resolves a slot that the validated graph guarantees non-null.
    #[inline(always)]
    fn resolve_ref<T>(self, slot: &'g RelRef<T>) -> &'g T {
        debug_assert!(slot.0.offset != 0, "non-null relative pointer is NULL");
        let ptr = self.target::<T>((slot as *const RelRef<T>).addr(), slot.0.offset);
        debug_assert!(ptr.is_aligned(), "misaligned relative target");
        // SAFETY: as for `resolve`; the graph guarantees the slot non-null.
        unsafe {
            core::hint::assert_unchecked(!ptr.is_null());
            &*ptr
        }
    }
}

/// Resolves a slot pointing at the first of `len` consecutive elements.
///
/// # Safety
///
/// The graph must hold `len` live, frozen elements at the target for 'g.
pub(crate) unsafe fn resolve_slice<'g, R: Resolver<'g>, T>(res: R, slot: &'g RelPtr<T>, len: usize) -> Option<&'g [T]> {
    if slot.offset == 0 {
        return None;
    }
    let ptr = res.target::<T>((slot as *const RelPtr<T>).addr(), slot.offset);
    debug_assert!(ptr.is_aligned(), "misaligned relative target");
    // SAFETY: guaranteed by the caller.
    Some(unsafe { core::slice::from_raw_parts(ptr, len) })
}

/// View over a C aggregate inside a resolver's graph.
pub trait FrozenView<'g, R: Resolver<'g>>: Sized {
    /// The bindgen aggregate the view projects into.
    type C;

    /// Builds the view.
    ///
    /// # Safety
    ///
    /// `raw` must point at a live `Self::C` inside the graph of `res`, with
    /// every declared non-opaque field frozen for 'g.
    unsafe fn from_raw(res: R, raw: NonNull<Self::C>) -> Self;
}

/// Production resolver: one raw root pointer whose provenance covers the
/// whole shared-memory mapping.
///
/// Resolution is `root.with_addr(slot + offset)`, the same arithmetic as
/// `ADDR_OF`; `with_addr` compiles to nothing.
#[derive(Clone, Copy)]
pub struct MapResolver<'g> {
    root: NonNull<u8>,
    _graph: PhantomData<&'g [u8]>,
}

impl MapResolver<'_> {
    /// Creates a resolver from a raw pointer received across FFI.
    ///
    /// # Safety
    ///
    /// `root` must be a raw pointer obtained from C (or, in tests, from the
    /// allocation holding the whole image), never derived from a Rust
    /// reference, and its provenance must cover every byte any view of this
    /// resolver reaches. Every graph reachable through such views must stay
    /// valid and frozen for the resolver lifetime: the published-config
    /// contract that nobody writes published memory except through atomics.
    pub unsafe fn new(root: NonNull<u8>) -> Self {
        Self { root, _graph: PhantomData }
    }

    /// Pointer with root provenance at an absolute address of the mapping.
    pub fn at<T>(self, addr: usize) -> NonNull<T> {
        debug_assert!(addr != 0, "absolute address is NULL");
        // SAFETY: a non-zero address keeps the pointer non-null.
        unsafe { NonNull::new_unchecked(self.root.as_ptr().with_addr(addr).cast()) }
    }
}

impl sealed::Resolver for MapResolver<'_> {}

impl<'g> Resolver<'g> for MapResolver<'g> {
    #[inline(always)]
    fn target<T>(self, slot_addr: usize, offset: isize) -> *const T {
        self.root
            .as_ptr()
            .with_addr(slot_addr.wrapping_add_signed(offset))
            .cast_const()
            .cast()
    }
}
