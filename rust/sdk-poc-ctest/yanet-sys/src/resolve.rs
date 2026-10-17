//! Strict-provenance resolution of self-relative pointers.

use core::{marker::PhantomData, ptr, ptr::NonNull};

use crate::ffi::RelPtr;

/// Provenance root of one attached shared-memory mapping.
///
/// A resolved target is `root.with_addr(slot address + stored offset)`: the
/// slot contributes only its address, never its provenance, so resolving
/// through a shared reference to a small slot never narrows or invalidates
/// access to the target. This is the integer arithmetic of the C `ADDR_OF`
/// macro with the provenance of `root` attached.
#[derive(Clone, Copy)]
pub struct Root<'g> {
    base: NonNull<u8>,
    _mapping: PhantomData<&'g [u8]>,
}

impl<'g> Root<'g> {
    /// Wraps a raw pointer handed over by C as the provenance root.
    ///
    /// # Safety
    ///
    /// `base` must be a raw pointer received from C (or, in tests, derived
    /// from a raw allocation pointer), never from a Rust reference. Its
    /// provenance must cover every byte reached through relative pointers
    /// resolved with this root for `'g`; those bytes must form a graph
    /// validated before publication and must not be written for `'g`.
    pub unsafe fn from_raw(base: NonNull<u8>) -> Self {
        Self { base, _mapping: PhantomData }
    }

    /// The root pointer itself, for raw field projection inside this crate.
    pub fn as_ptr(self) -> *mut u8 {
        self.base.as_ptr()
    }

    /// Target of `slot`, or `None` for a zero offset.
    #[inline(always)]
    pub fn resolve<T>(self, slot: &'g RelPtr<T>) -> Option<NonNull<T>> {
        let offset = slot.offset();
        if offset == 0 {
            return None;
        }
        NonNull::new(self.resolve_nonnull(slot).cast_mut())
    }

    /// Target of a slot known to be non-NULL, without the zero test.
    ///
    /// Mirrors `ADDR_OF_NONNULL`: a zero offset yields the slot address.
    #[inline(always)]
    pub fn resolve_nonnull<T>(self, slot: &'g RelPtr<T>) -> *const T {
        let target = ptr::from_ref(slot).addr().wrapping_add_signed(slot.offset());
        self.base.as_ptr().with_addr(target).cast::<T>().cast_const()
    }
}

#[cfg(test)]
mod tests {
    use core::ptr::NonNull;

    use yanet_testkit::RawBuf;

    use super::Root;
    use crate::ffi::RelPtr;

    /// Buffer of eight words: an offset at word `slot` pointing at word
    /// `target`, which holds `value`.
    fn buffer_with_edge(slot: usize, target: usize, value: u64) -> RawBuf {
        let buf = RawBuf::zeroed(64);
        let words = buf.as_ptr().cast::<u64>();
        // SAFETY: both words lie inside the 64-byte allocation.
        unsafe {
            words.add(target).write(value);
            words
                .add(slot)
                .cast::<isize>()
                .write((target as isize - slot as isize) * 8);
        }
        buf
    }

    /// Shared reference to the relative pointer at word `slot`.
    ///
    /// # Safety
    ///
    /// The word must stay unwritten while the reference lives.
    unsafe fn slot_ref(buf: &RawBuf, slot: usize) -> &RelPtr<u64> {
        // SAFETY: the word is inside the allocation, aligned and initialised.
        unsafe { &*buf.as_ptr().cast::<RelPtr<u64>>().add(slot) }
    }

    #[test]
    fn test_resolve_with_interior_raw_root() {
        let buf = buffer_with_edge(0, 3, 42);
        // SAFETY: the root is an interior raw pointer of the allocation and
        // nothing writes the buffer while it is used.
        let root = unsafe { Root::from_raw(buf.ptr_at(8)) };
        // SAFETY: the slot word is not written below.
        let slot = unsafe { slot_ref(&buf, 0) };
        let target = root.resolve(slot).expect("non-zero offset resolves");
        // SAFETY: the target word is inside the allocation and initialised.
        assert_eq!(42, unsafe { target.read() });
    }

    #[test]
    fn test_resolve_negative_offset() {
        let buf = buffer_with_edge(6, 1, 7);
        // SAFETY: raw allocation pointer; the buffer is not written.
        let root = unsafe { Root::from_raw(buf.ptr_at(0)) };
        // SAFETY: the slot word is not written below.
        let slot = unsafe { slot_ref(&buf, 6) };
        // SAFETY: the target word is inside the allocation and initialised.
        assert_eq!(7, unsafe { root.resolve_nonnull(slot).read() });
    }

    #[test]
    fn test_resolve_zero_offset_is_none() {
        let buf = RawBuf::zeroed(64);
        // SAFETY: raw allocation pointer; the buffer is not written.
        let root = unsafe { Root::from_raw(buf.ptr_at(0)) };
        // SAFETY: the slot word is not written below.
        let slot = unsafe { slot_ref(&buf, 2) };
        assert!(root.resolve(slot).is_none());
    }

    #[test]
    fn test_resolve_after_remap_uses_new_mapping() {
        let old = buffer_with_edge(1, 5, 99);
        let new = old.duplicate();
        drop(old);
        // SAFETY: raw allocation pointer of the copy; it is not written.
        let root = unsafe { Root::from_raw(new.ptr_at(0)) };
        // SAFETY: the slot word is not written below.
        let slot = unsafe { slot_ref(&new, 1) };
        let target = root.resolve(slot).expect("non-zero offset resolves");
        assert_eq!(new.as_ptr().addr() + 40, target.addr().get());
        // SAFETY: the target word is inside the live copy.
        assert_eq!(99, unsafe { target.read() });
    }

    #[test]
    fn test_resolve_matches_c_address_arithmetic() {
        let buf = buffer_with_edge(2, 7, 1);
        let base = NonNull::new(buf.as_ptr()).expect("allocation is non-null");
        // SAFETY: raw allocation pointer; the buffer is not written.
        let root = unsafe { Root::from_raw(base) };
        // SAFETY: the slot word is not written below.
        let slot = unsafe { slot_ref(&buf, 2) };
        let expected = core::ptr::from_ref(slot).addr() as isize + slot.offset();
        assert_eq!(expected as usize, root.resolve_nonnull(slot).addr());
    }
}
