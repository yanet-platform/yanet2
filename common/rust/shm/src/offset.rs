//! Self-relative pointers as stored in YANET shared memory.

use core::marker::PhantomData;

/// A shared-memory offset pointer, mirroring the `ADDR_OF` family in
/// `common/memory_address.h`.
///
/// The field stores `target - field_address` (zero means NULL), so a
/// structure full of them stays valid under any mapping base of the same
/// segment. Resolving re-adds the field's own address, which makes the
/// mapping position-independent; the NULL special case reproduces the C
/// macro exactly, including the asymmetric `SET_OFFSET_OF` write.
#[repr(C)]
pub struct OffsetPtr<T> {
    offset: isize,
    target: PhantomData<fn() -> T>,
}

// SAFETY: the pointer is a plain offset value; sending or sharing it
// across threads carries no borrow.
unsafe impl<T> Send for OffsetPtr<T> {}
unsafe impl<T> Sync for OffsetPtr<T> {}

impl<T> Copy for OffsetPtr<T> {}

impl<T> Clone for OffsetPtr<T> {
    fn clone(&self) -> Self {
        *self
    }
}

impl<T> Default for OffsetPtr<T> {
    fn default() -> Self {
        Self::null()
    }
}

impl<T> core::fmt::Debug for OffsetPtr<T> {
    fn fmt(&self, f: &mut core::fmt::Formatter<'_>) -> core::fmt::Result {
        f.debug_struct("OffsetPtr").field("offset", &self.offset).finish()
    }
}

impl<T> OffsetPtr<T> {
    /// The NULL offset pointer.
    pub const fn null() -> Self {
        Self { offset: 0, target: PhantomData }
    }

    /// Whether the pointer stores NULL.
    pub const fn is_null(&self) -> bool {
        self.offset == 0
    }

    /// The raw stored offset.
    pub const fn raw(&self) -> isize {
        self.offset
    }

    /// Resolve to an absolute address, mapping NULL to NULL.
    ///
    /// Mirrors `ADDR_OF`: the stored offset plus the field's own address,
    /// except a zero offset resolves to NULL.
    pub fn resolve(&self) -> *mut T {
        if self.offset == 0 {
            return core::ptr::null_mut();
        }
        (self as *const Self as *mut u8).wrapping_offset(self.offset) as *mut T
    }

    /// Resolve a structurally non-NULL pointer.
    ///
    /// Mirrors `ADDR_OF_NONNULL`: same arithmetic as [`OffsetPtr::resolve`]
    /// without the NULL test, for hot pointer chases whose target is never
    /// NULL by construction (an intermediate trie node, a compiled table).
    /// A zero offset here yields the field's own address, a wrong value.
    pub fn resolve_non_null(&self) -> *mut T {
        (self as *const Self as *mut u8).wrapping_offset(self.offset) as *mut T
    }

    /// Store a target address, mirroring `SET_OFFSET_OF`.
    ///
    /// Storing NULL zeroes the offset rather than recording the distance
    /// to the field, so a NULL write reads back NULL through
    /// [`OffsetPtr::resolve`].
    pub fn store(&mut self, target: *mut T) {
        self.offset = match target as isize {
            0 => 0,
            addr => addr - (self as *mut Self as isize),
        };
    }
}

#[cfg(test)]
mod tests {
    use super::OffsetPtr;

    #[test]
    fn null_round_trip() {
        let ptr: OffsetPtr<u8> = OffsetPtr::null();
        assert!(ptr.is_null());
        assert!(ptr.resolve().is_null());
    }

    #[test]
    fn store_resolve_round_trip() {
        let mut storage = [13u8, 17];
        let mut field: OffsetPtr<u8> = OffsetPtr::null();
        field.store(storage.as_mut_ptr());
        assert!(!field.is_null());
        // SAFETY: the field was just aimed at a live two-byte array.
        assert_eq!(unsafe { *field.resolve() }, 13);

        // Offsets are position-independent: copying the stored field into
        // another location resolves to the same target from either spot.
        let copy = field;
        assert_eq!(copy.resolve(), field.resolve());
    }

    #[test]
    fn store_null_writes_zero() {
        let mut field: OffsetPtr<u8> = OffsetPtr::null();
        field.store(core::ptr::null_mut());
        assert!(field.is_null());
    }

    #[test]
    fn non_null_resolve_skips_null_test() {
        let mut storage = 5u32;
        let mut field: OffsetPtr<u32> = OffsetPtr::null();
        field.store(&mut storage);
        // SAFETY: aimed at a live u32 above.
        assert_eq!(unsafe { *field.resolve_non_null() }, 5);
    }
}
