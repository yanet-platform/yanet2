//! Exclusive loans of final-address storage during a build.

use core::{fmt, marker::PhantomData, mem::forget};

use super::{
    Brand, Build, BuildError, Free, Kind, MemoryContext, OwnedSlice, Owner, Refusal, Scope, ShmLayout, ShmRoot, refused,
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
        let mut prefix = Run::<T, W> {
            memory: self.memory,
            addr: self.addr,
            written: 0,
            _element: PhantomData,
        };
        // The iterator owns the unused parts and drops them before the prefix.
        for part in parts {
            let addr = self.addr + prefix.written * size_of::<T>();
            // Each part is paired with one still unwritten array element.
            Slot::<T, W> {
                addr,
                memory: self.memory,
                initialized: false,
                _value: PhantomData,
            }
            .write(part)?;
            prefix.written += 1;
        }
        forget(prefix);
        // SAFETY: each array element has supplied its initialization receipt.
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
        fields::write_record(fields, self, parts)
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
        fields::release_record(fields, self);
    }
}

mod fields {
    use core::{marker::PhantomData, mem::forget};

    use super::{
        Build, Init, MemoryContext, Owner, RecordField, RecordPlace, RecordTaken, Refusal, Run, ShmLayout, Slot, Taken,
    };

    pub struct Written<'b, T: Build, W: Owner> {
        _guard: Run<'b, T, W>,
    }

    /// Exclusive write destinations for one certified subtree.
    pub struct Write<'b, F, W: Owner> {
        fields: F,
        addr: usize,
        memory: &'b MemoryContext<'b, W>,
    }

    #[inline]
    pub(super) fn write_record<'slot, 'b, F: List, T: Build, W: Owner>(
        fields: F,
        RecordPlace(place): RecordPlace<'slot, 'b, T, W>,
        parts: F::Parts<'b, W>,
    ) -> Result<Init<'slot, T>, Refusal> {
        let (written, _) = F::write(
            Write {
                fields,
                addr: place.addr,
                memory: place.memory,
            },
            parts,
            ((), ()),
        )?;
        forget(written);
        // SAFETY: the certified traversal initialized the complete record.
        Ok(unsafe { place.assume_init() })
    }

    /// Exclusive release rights for one certified subtree.
    pub struct Release<'a, F, W: Owner> {
        fields: F,
        addr: usize,
        memory: &'a MemoryContext<'a, W>,
    }

    #[inline]
    pub(super) fn release_record<F: List, T: ShmLayout, W: Owner>(
        fields: F,
        RecordTaken(value): RecordTaken<'_, T, W>,
    ) {
        F::release(Release {
            fields,
            addr: value.addr,
            memory: value.memory,
        });
    }

    type Staged<G, P, L> = Result<(G, (P, L)), Refusal>;

    pub trait List {
        type Parts<'b, W: Owner>;
        type Written<'b, W: Owner>;

        /// Stages initialized fields in reverse teardown order. `state`
        /// owns prior writes followed by unsubmitted parts after this group.
        fn write<'b, W: Owner, P, L>(
            destination: Write<'b, Self, W>,
            parts: Self::Parts<'b, W>,
            state: (P, L),
        ) -> Staged<Self::Written<'b, W>, P, L>
        where
            Self: Sized;

        /// Releases the listed fields in declaration order.
        fn release<W: Owner>(release: Release<'_, Self, W>)
        where
            Self: Sized;
    }

    impl<T: Build> List for RecordField<T> {
        type Parts<'b, W: Owner> = T::Parts<'b, W>;
        type Written<'b, W: Owner> = Written<'b, T, W>;

        fn write<'b, W: Owner, P, L>(
            Write { fields, addr, memory }: Write<'b, Self, W>,
            parts: Self::Parts<'b, W>,
            state: (P, L),
        ) -> Staged<Self::Written<'b, W>, P, L> {
            let addr = addr + fields.offset;
            // The current slot rolls back before prior writes and unused parts.
            Slot::<T, W> {
                addr,
                memory,
                initialized: false,
                _value: PhantomData,
            }
            .write(parts)?;
            let guard = Run {
                memory,
                addr,
                written: 1,
                _element: PhantomData,
            };
            Ok((Written { _guard: guard }, state))
        }

        fn release<W: Owner>(Release { fields, addr, memory }: Release<'_, Self, W>) {
            T::free(Taken {
                addr: addr + fields.offset,
                memory,
                _value: PhantomData,
            });
        }
    }

    impl<A: List, B: List> List for (A, B) {
        // Each tuple drops later fields first within its group.
        type Parts<'b, W: Owner> = (B::Parts<'b, W>, A::Parts<'b, W>);
        type Written<'b, W: Owner> = (B::Written<'b, W>, A::Written<'b, W>);

        fn write<'b, W: Owner, P, L>(
            Write { fields: (left, right), addr, memory }: Write<'b, Self, W>,
            (right_parts, left_parts): Self::Parts<'b, W>,
            (prior, later): (P, L),
        ) -> Staged<Self::Written<'b, W>, P, L> {
            // Hand off the rest of the rollback state before starting a group.
            let (left, (prior, (later, right_parts))) = A::write(
                Write { fields: left, addr, memory },
                left_parts,
                (prior, (later, right_parts)),
            )?;
            let (right, ((left, prior), later)) = B::write(
                Write { fields: right, addr, memory },
                right_parts,
                ((left, prior), later),
            )?;
            Ok(((right, left), (prior, later)))
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

/// Exclusive release right for one initialized value in the issuing context.
///
/// The parent retains the storage of the value itself. Only its resources
/// are handed over to the release implementation.
#[must_use]
pub struct Taken<'a, T: ShmLayout, W: Owner> {
    addr: usize,
    memory: &'a MemoryContext<'a, W>,
    _value: PhantomData<fn(T) -> T>,
}

impl<'a, T: ShmLayout, W: Owner> Taken<'a, T, W> {
    /// # Safety
    ///
    /// The address holds one live, aligned and initialized value satisfying
    /// the release contract of `T` in this context. No other code can access
    /// the value or release its resources while this right is held.
    pub(crate) unsafe fn new(addr: usize, memory: &'a MemoryContext<'_, W>) -> Self {
        Self { addr, memory, _value: PhantomData }
    }

    /// Final address of the value.
    pub fn addr(&self) -> usize {
        self.addr
    }

    /// The issuing context and its provenance root.
    pub fn memory(&self) -> &'a MemoryContext<'a, W> {
        self.memory
    }

    /// Gives a reborrowed release right for one of this value's fields.
    ///
    /// # Safety
    ///
    /// The offset names an aligned field of exactly this type, written
    /// whole. Its resources must not already have been released or handed
    /// over. Even fields sharing an address because they are zero-sized
    /// are distinct fields and must each be released exactly once.
    pub unsafe fn field<U: ShmLayout>(&mut self, offset: usize) -> Taken<'_, U, W> {
        Taken {
            addr: self.addr + offset,
            memory: self.memory,
            _value: PhantomData,
        }
    }

    pub(crate) fn get(&self) -> &T {
        let (value, _) = self.view();
        value
    }

    #[inline]
    pub(super) fn view(&self) -> (&T, ShmRoot) {
        let root = self.memory.root();
        // SAFETY: the capability holds an initialized, aligned value, and
        // the shared borrow prevents use of its exclusive release right.
        (unsafe { &*root.ptr(self.addr) }, root)
    }
}

impl<T: ShmLayout, W: Owner> fmt::Debug for Taken<'_, T, W> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Taken")
            .field("addr", &self.addr)
            .finish_non_exhaustive()
    }
}

/// Exclusive destination and rollback owner of an in-flight write.
///
/// The slot is aligned and unwritten when issued; only an accepted receipt
/// hands the initialized value over to the enclosing guard.
struct Slot<'b, T: Build, W: Owner> {
    addr: usize,
    memory: &'b MemoryContext<'b, W>,
    initialized: bool,
    _value: Invariant<T>,
}

impl<'b, T: Build, W: Owner> Slot<'b, T, W> {
    /// On success the caller takes over the initialized value.
    #[inline]
    fn write(mut self, parts: T::Parts<'b, W>) -> Result<(), Refusal> {
        let _receipt = T::write(
            parts,
            Place {
                addr: self.addr,
                memory: self.memory,
                initialized: &mut self.initialized,
                _slot: PhantomData,
                _build: PhantomData,
                _value: PhantomData,
            },
        )?;
        forget(self);
        Ok(())
    }
}

impl<T: Build, W: Owner> Drop for Slot<'_, T, W> {
    fn drop(&mut self) {
        if self.initialized {
            T::free(Taken {
                addr: self.addr,
                memory: self.memory,
                _value: PhantomData,
            });
        }
    }
}

impl<'b, W: Owner> MemoryContext<'b, W> {
    /// # Safety
    ///
    /// The caller lends an aligned, exclusive destination for the value in
    /// this build, and takes over its teardown on success.
    pub(crate) unsafe fn write_at<T: Build>(&'b self, addr: usize, parts: T::Parts<'b, W>) -> Result<(), Refusal> {
        Slot::<T, W> {
            addr,
            memory: self,
            initialized: false,
            _value: PhantomData,
        }
        .write(parts)
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
    /// Releases a prefix and returns its backing allocation.
    ///
    /// # Safety
    ///
    /// The caller owns an unreferenced block allocated for `len` elements
    /// in this context. `written` is the initialized prefix length, at most
    /// `len`. Only the prefix owns resources.
    #[inline]
    pub(super) unsafe fn release_block(addr: usize, written: usize, len: usize, memory: &'a MemoryContext<'_, W>) {
        drop(Self {
            memory,
            addr,
            written,
            _element: PhantomData,
        });
        // SAFETY: the caller owns the original allocation and the prefix
        // has returned its resources.
        unsafe { memory.free(addr, len * size_of::<T>()) };
    }
}

impl<T: Free, W: Owner> Drop for Run<'_, T, W> {
    fn drop(&mut self) {
        // Newest first: C prepends child contexts and unlinks from the head,
        // so this order is linear.
        for idx in (0..self.written).rev() {
            let addr = self.addr + idx * size_of::<T>();
            T::free(Taken {
                addr,
                memory: self.memory,
                _value: PhantomData,
            });
        }
    }
}

impl<T: Free, W: Owner, const N: usize> Taken<'_, [T; N], W> {
    pub(super) fn release_array(self) {
        let Self { addr, memory, .. } = self;
        drop(Run::<T, W> {
            memory,
            addr,
            written: N,
            _element: PhantomData,
        });
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
        // The reservation covers the next element; the prefix only owns it
        // once the write succeeds.
        Slot::<T, W> {
            addr,
            memory: self.memory,
            initialized: false,
            _value: PhantomData,
        }
        .write(parts)?;
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
