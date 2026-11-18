//! Exclusive loans of final-address storage during a build.

use core::{fmt, marker::PhantomData, mem::forget};

use super::{
    Brand, Build, BuildError, Free, Kind, MemoryContext, OwnedSlice, Owner, Refusal, Scope, ShmLayout, Taken, refused,
};

type Invariant<T> = PhantomData<fn(T) -> T>;

/// One exclusive, uninitialized destination, issued for a single write.
///
/// The build lifetime binds the parts to their allocator. The independent,
/// invariant slot lifetime prevents substituting a concurrent write's receipt.
#[doc(hidden)]
pub struct Place<'slot, 'b, T: Build, W: Owner> {
    addr: usize,
    memory: &'b MemoryContext<'b, W>,
    initialized: &'slot mut bool,
    _slot: Brand<'slot>,
    _build: Brand<'b>,
    _value: Invariant<(T, W)>,
}

impl<'slot, 'b, T: Build, W: Owner> Place<'slot, 'b, T, W> {
    /// Final address of the value, including for relative offset computations.
    pub fn addr(&self) -> usize {
        self.addr
    }

    /// The issuing build's memory and provenance root.
    pub fn memory(&self) -> &'b MemoryContext<'b, W> {
        self.memory
    }

    /// Writes a whole value whose representation is valid at this address.
    ///
    /// # Safety
    ///
    /// Moving the value to this final address must preserve its complete
    /// shared-memory layout, including every relative edge. Once written, it
    /// must satisfy all the read and ownership requirements of
    /// [`Self::assume_init`].
    pub unsafe fn put(self, value: T) -> Init<'slot, T> {
        // SAFETY: the place covers one exclusive, aligned value. The caller
        // establishes the value's release contract at this final address.
        unsafe {
            self.memory.root().ptr::<T>(self.addr).write(value);
            self.assume_init()
        }
    }

    /// Transfers a fully initialized value to the active transaction.
    ///
    /// # Safety
    ///
    /// All fields are initialized and valid to read as `T` at this final
    /// address. Relative links resolve to live targets of the correct type
    /// and extent, and registrations are live and correctly typed for this
    /// exact build. The value alone owns the resources [`Free::free`] will
    /// release in this memory context, once each at the original size.
    pub unsafe fn assume_init(self) -> Init<'slot, T> {
        *self.initialized = true;
        Init {
            _slot: PhantomData,
            _value: PhantomData,
        }
    }

    /// Initializes the certified fields of this record in declaration order.
    pub fn write_record(self, parts: T::RecordParts<'b, W>) -> Result<Init<'slot, T>, Refusal>
    where
        T: Record,
    {
        T::write_fields(parts, RecordPlace(self))
    }
}

impl<'slot, 'b, T: Build, W: Owner, const N: usize> Place<'slot, 'b, [T; N], W> {
    pub(super) fn write_array(self, parts: [T::Parts<'b, W>; N]) -> Result<Init<'slot, [T; N]>, Refusal> {
        // SAFETY: the empty prefix owns no elements yet.
        let mut prefix = unsafe { Run::<T, W>::new(self.addr, 0, self.memory) };
        // The iterator owns the unused parts and drops them before the prefix.
        for part in parts {
            let addr = self.addr + prefix.written * size_of::<T>();
            // SAFETY: the iterator submits exactly one part per array element.
            unsafe { Slot::<T, W>::write(addr, self.memory, part)? };
            prefix.written += 1;
        }
        forget(prefix);
        // SAFETY: the array has taken ownership of all its initialized fields.
        Ok(unsafe { self.assume_init() })
    }
}

/// One entry in a derived record's layout, in declaration order.
#[derive(Debug)]
pub struct RecordField<T> {
    offset: usize,
    _value: Invariant<T>,
}

impl<T> RecordField<T> {
    /// Describes a field of type `T` at this byte offset from its parent.
    pub const fn at(offset: usize) -> Self {
        Self { offset, _value: PhantomData }
    }
}

/// The compile-time layout used for both construction and release.
///
/// # Safety
///
/// Both callbacks must use their capability with the same tree. The tree
/// lists every actual field of `Self` once in declaration order, at its
/// real offset and with its real type. Every entry must be aligned at any
/// valid address of `Self`, even when zero-sized. Initializing the listed
/// fields must satisfy all of [`Place::assume_init`] for the whole value.
/// The release callback must uphold the [`Free`] contract.
pub unsafe trait Record: ShmLayout {
    /// The record's parts in the author-facing form.
    type RecordParts<'b, W: Owner>;

    /// Submits the field tree and its parts to the exclusive write.
    fn write_fields<'slot, 'b, W: Owner>(
        parts: Self::RecordParts<'b, W>,
        place: RecordPlace<'slot, 'b, Self, W>,
    ) -> Result<Init<'slot, Self>, Refusal>
    where
        Self: Build;

    /// Submits the same field tree to the exclusive release.
    fn free_fields<W: Owner>(value: RecordTaken<'_, Self, W>);
}

// SAFETY: the record certifies that its tree describes exactly the fields of
// the initialized value, including all the resources it owns.
unsafe impl<T: Record> Free for T {
    fn free<W: Owner>(value: Taken<'_, Self, W>) {
        T::free_fields(RecordTaken(value));
    }
}

/// A write destination lent only to the certified record implementation.
///
/// Safe code cannot turn a general destination into a certified one:
///
/// ```compile_fail,E0423
/// use yanet_sys::cp::{Owner, Place, RecordPlace};
/// fn certify<'s, 'b, W: Owner>(place: Place<'s, 'b, u32, W>) {
///     let _ = RecordPlace(place);
/// }
/// ```
///
/// The certificate is also bound to the parent type:
///
/// ```compile_fail,E0308
/// use yanet_sys::cp::{Owner, RecordPlace};
/// fn substitute<'s, 'b, W: Owner>(place: RecordPlace<'s, 'b, u32, W>) -> RecordPlace<'s, 'b, u64, W> {
///     place
/// }
/// ```
#[doc(hidden)]
#[derive(Debug)]
pub struct RecordPlace<'slot, 'b, T: Build, W: Owner>(Place<'slot, 'b, T, W>);

impl<'slot, 'b, T: Build, W: Owner> RecordPlace<'slot, 'b, T, W> {
    /// Writes the fields certified by the record implementation.
    #[inline]
    pub fn write<F: fields::List>(self, fields: F, parts: F::Parts<'b, W>) -> Result<Init<'slot, T>, Refusal> {
        let Self(place) = self;
        // SAFETY: the record certifies every field of this exclusive place.
        let (written, _) = unsafe { fields.write_at(place.addr, place.memory, parts, ((), ()))? };
        forget(written);
        // SAFETY: all fields are initialized and owned by the record.
        Ok(unsafe { place.assume_init() })
    }
}

/// A release capability lent only to the certified record implementation.
#[doc(hidden)]
#[derive(Debug)]
pub struct RecordTaken<'a, T: ShmLayout, W: Owner>(Taken<'a, T, W>);

impl<T: ShmLayout, W: Owner> RecordTaken<'_, T, W> {
    /// Releases the certified fields in declaration order.
    #[inline]
    pub fn free<F: fields::List>(self, fields: F) {
        let Self(value) = self;
        // SAFETY: the record certifies every field of this exclusive value.
        unsafe { fields::release_record(fields, value) };
    }
}

mod fields {
    use super::{Build, MemoryContext, Owner, RecordField, Refusal, Run, ShmLayout, Taken};

    pub struct Written<'b, T: Build, W: Owner> {
        _guard: Run<'b, T, W>,
    }

    /// Exclusive release rights for one certified subtree.
    pub struct Release<'a, F, W: Owner> {
        fields: F,
        addr: usize,
        memory: &'a MemoryContext<'a, W>,
    }

    /// # Safety
    ///
    /// The tree describes distinct, initialized fields of the owned value,
    /// with their exact types and offsets, in declaration order.
    pub(super) unsafe fn release_record<F: List, T: ShmLayout, W: Owner>(fields: F, value: Taken<'_, T, W>) {
        F::release(Release {
            fields,
            addr: value.addr(),
            memory: value.memory(),
        });
    }

    type Staged<G, P, L> = Result<(G, (P, L)), Refusal>;

    pub trait List {
        type Parts<'b, W: Owner>;
        type Written<'b, W: Owner>;

        /// Stages initialized fields in reverse teardown order. `state`
        /// owns prior writes followed by unsubmitted parts after this group.
        ///
        /// # Safety
        ///
        /// The entries describe distinct unwritten fields of the parent at
        /// `addr` from `memory`.
        unsafe fn write_at<'b, W: Owner, P, L>(
            self,
            addr: usize,
            memory: &'b MemoryContext<'b, W>,
            parts: Self::Parts<'b, W>,
            state: (P, L),
        ) -> Staged<Self::Written<'b, W>, P, L>;

        /// Releases the listed fields in declaration order.
        fn release<W: Owner>(release: Release<'_, Self, W>)
        where
            Self: Sized;
    }

    impl<T: Build> List for RecordField<T> {
        type Parts<'b, W: Owner> = T::Parts<'b, W>;
        type Written<'b, W: Owner> = Written<'b, T, W>;

        unsafe fn write_at<'b, W: Owner, P, L>(
            self,
            addr: usize,
            memory: &'b MemoryContext<'b, W>,
            parts: Self::Parts<'b, W>,
            state: (P, L),
        ) -> Staged<Self::Written<'b, W>, P, L> {
            let field = addr + self.offset;
            // SAFETY: the record certifies this field. Its slot rolls back
            // before prior writes and then unused parts on failure.
            unsafe {
                memory.write_at::<T>(field, parts)?;
                Ok((Written { _guard: Run::new(field, 1, memory) }, state))
            }
        }

        fn release<W: Owner>(Release { fields, addr, memory }: Release<'_, Self, W>) {
            // SAFETY: the certificate owns this exact initialized field.
            T::free(unsafe { Taken::new(addr + fields.offset, memory) });
        }
    }

    impl<A: List, B: List> List for (A, B) {
        // Each tuple drops later fields first within its group.
        type Parts<'b, W: Owner> = (B::Parts<'b, W>, A::Parts<'b, W>);
        type Written<'b, W: Owner> = (B::Written<'b, W>, A::Written<'b, W>);

        unsafe fn write_at<'b, W: Owner, P, L>(
            self,
            addr: usize,
            memory: &'b MemoryContext<'b, W>,
            (right_parts, left_parts): Self::Parts<'b, W>,
            (prior, later): (P, L),
        ) -> Staged<Self::Written<'b, W>, P, L> {
            let (left, right) = self;
            // SAFETY: each group owns distinct slots, in declaration order.
            // Hand off the rest of the rollback state before starting a group.
            unsafe {
                let (left, (prior, (later, right_parts))) =
                    left.write_at(addr, memory, left_parts, (prior, (later, right_parts)))?;
                let (right, ((left, prior), later)) =
                    right.write_at(addr, memory, right_parts, ((left, prior), later))?;
                Ok(((right, left), (prior, later)))
            }
        }

        #[inline(always)]
        fn release<W: Owner>(Release { fields: (left, right), addr, memory }: Release<'_, Self, W>) {
            A::release(Release { fields: left, addr, memory });
            B::release(Release { fields: right, addr, memory });
        }
    }
}

impl<T: Build, W: Owner> fmt::Debug for Place<'_, '_, T, W> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Place")
            .field("addr", &self.addr)
            .finish_non_exhaustive()
    }
}

/// Receipt for the exact destination the transaction lent out.
///
/// The transaction, not the receipt, owns rollback. Forgetting this token
/// cannot strand an initialized C context in storage about to be returned.
///
/// A write may return its own receipt:
///
/// ```
/// use yanet_sys::cp::Init;
/// fn complete<'slot, T>(receipt: Init<'slot, T>) -> Init<'slot, T> {
///     receipt
/// }
/// struct Payload;
/// fn debug<T: core::fmt::Debug>() {}
/// debug::<Init<'static, Payload>>();
/// ```
///
/// Even an overlapping, longer-lived slot cannot supply that receipt:
///
/// ```compile_fail
/// use yanet_sys::cp::Init;
/// fn substitute<'slot, 'other: 'slot, T>(receipt: Init<'other, T>) -> Init<'slot, T> {
///     receipt
/// }
/// ```
#[doc(hidden)]
#[must_use]
pub struct Init<'slot, T> {
    _slot: Brand<'slot>,
    _value: Invariant<T>,
}

impl<T> fmt::Debug for Init<'_, T> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Init").finish_non_exhaustive()
    }
}

/// Sole rollback owner between initialization and acceptance of its receipt.
struct Slot<'b, T: Build, W: Owner> {
    addr: usize,
    memory: &'b MemoryContext<'b, W>,
    initialized: bool,
    _value: Invariant<T>,
}

impl<'b, T: Build, W: Owner> Slot<'b, T, W> {
    /// # Safety
    ///
    /// The address is an aligned, exclusive, unwritten slot of this type in
    /// this build. On success the caller takes ownership of the whole value.
    unsafe fn write(addr: usize, memory: &'b MemoryContext<'b, W>, parts: T::Parts<'b, W>) -> Result<(), Refusal> {
        let mut slot = Self {
            addr,
            memory,
            initialized: false,
            _value: PhantomData,
        };
        let _receipt = T::write(
            parts,
            Place {
                addr,
                memory,
                initialized: &mut slot.initialized,
                _slot: PhantomData,
                _build: PhantomData,
                _value: PhantomData,
            },
        )?;
        forget(slot);
        Ok(())
    }
}

impl<T: Build, W: Owner> Drop for Slot<'_, T, W> {
    fn drop(&mut self) {
        if self.initialized {
            // SAFETY: initialization transferred the whole value to this guard.
            T::free(unsafe { Taken::new(self.addr, self.memory) });
        }
    }
}

impl<'b, W: Owner> MemoryContext<'b, W> {
    /// # Safety
    ///
    /// The caller lends an aligned, exclusive destination for the value in
    /// this build, and takes over its teardown on success.
    pub(crate) unsafe fn write_at<T: Build>(&'b self, addr: usize, parts: T::Parts<'b, W>) -> Result<(), Refusal> {
        // SAFETY: the caller owns the slot. Its write uses the issuing build.
        unsafe { Slot::<T, W>::write(addr, self, parts) }
    }
}

/// A run of initialized values owned until their parent takes them over.
pub(super) struct Run<'a, T: Free, W: Owner> {
    memory: &'a MemoryContext<'a, W>,
    addr: usize,
    written: usize,
    _element: Invariant<T>,
}

impl<'a, T: Free, W: Owner> Run<'a, T, W> {
    /// # Safety
    ///
    /// The prefix holds `written` initialized elements in this context and
    /// this guard takes exclusive ownership of every element's resources.
    pub(super) unsafe fn new(addr: usize, written: usize, memory: &'a MemoryContext<'_, W>) -> Self {
        Self {
            memory,
            addr,
            written,
            _element: PhantomData,
        }
    }

    /// Releases a prefix and returns its backing allocation.
    ///
    /// # Safety
    ///
    /// The caller owns an unreferenced block allocated for `len` elements
    /// in this context. `written` is the initialized prefix length, at most
    /// `len`. Only the prefix owns resources.
    #[inline]
    pub(super) unsafe fn release_block(addr: usize, written: usize, len: usize, memory: &MemoryContext<'_, W>) {
        // SAFETY: the prefix holds precisely the elements to release.
        unsafe { Self::release(addr, written, memory) };
        // SAFETY: the caller owns the original allocation and the prefix
        // has returned its resources.
        unsafe { memory.free(addr, len * size_of::<T>()) };
    }

    /// # Safety
    ///
    /// The caller owns the `written` initialized elements at this address
    /// and their resources in this context. No references remain to them.
    pub(super) unsafe fn release(addr: usize, written: usize, memory: &MemoryContext<'_, W>) {
        // Newest first: C prepends child contexts and unlinks from the head,
        // so this order is linear.
        for idx in (0..written).rev() {
            let element = addr + idx * size_of::<T>();
            // SAFETY: this call owns the initialized element at this index.
            T::free(unsafe { Taken::new(element, memory) });
        }
    }
}

impl<T: Free, W: Owner> Drop for Run<'_, T, W> {
    fn drop(&mut self) {
        // SAFETY: the guard owns exactly the initialized prefix.
        unsafe { Self::release(self.addr, self.written, self.memory) };
    }
}

/// Reservation of a whole block and sole owner of its committed prefix.
/// Empty slices never acquire a reservation.
pub(super) struct SliceWriter<'b, T: Build, W: Owner> {
    memory: &'b MemoryContext<'b, W>,
    addr: usize,
    len: usize,
    written: usize,
    _build: Brand<'b>,
    _element: Invariant<(T, W)>,
}

impl<'b, T: Build, W: Owner> SliceWriter<'b, T, W> {
    pub(super) fn reserve<E: From<BuildError<W::Error>>, K: Kind>(
        scope: Scope<'b, '_, W, E, K>,
        len: core::num::NonZeroUsize,
    ) -> Result<Self, E> {
        let len = len.get();
        let size = len
            .checked_mul(size_of::<T>())
            .ok_or_else(|| refused::<W, E>(Refusal::BlockTooLarge))?;
        let addr = scope.alloc(size)?;
        Ok(Self {
            memory: scope.memory,
            addr,
            len,
            written: 0,
            _build: PhantomData,
            _element: PhantomData,
        })
    }

    pub(super) fn write_next(&mut self, parts: T::Parts<'b, W>) -> Result<(), Refusal> {
        assert!(self.written < self.len);
        let addr = self.addr + self.written * size_of::<T>();
        // SAFETY: the reservation covers this next, unwritten element.
        // Only after the write succeeds does the prefix take ownership.
        unsafe { Slot::<T, W>::write(addr, self.memory, parts)? };
        self.written += 1;
        Ok(())
    }

    pub(super) fn finish(self) -> OwnedSlice<'b, T, W> {
        assert!(self.len == self.written, "not all slice elements were initialized");
        let owned = OwnedSlice {
            addr: self.addr,
            len: self.len,
            memory: self.memory,
            _brand: PhantomData,
            _element: PhantomData,
        };
        forget(self);
        owned
    }
}

impl<T: Build, W: Owner> Drop for SliceWriter<'_, T, W> {
    fn drop(&mut self) {
        // SAFETY: only the committed prefix owns values. The transaction
        // has ended and the unreferenced block retains its original size.
        unsafe { Run::<T, W>::release_block(self.addr, self.written, self.len, self.memory) };
    }
}
