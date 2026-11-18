//! Exclusive loans of final-address storage during a build.

use core::{fmt, marker::PhantomData, mem::forget};

use super::{Brand, Build, BuildError, Kind, MemoryContext, OwnedSlice, Owner, Refusal, Scope, free_elements, refused};

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
    /// exact build. The value alone owns the resources [`Build::free`] will
    /// release in this memory context, once each at the original size.
    pub unsafe fn assume_init(self) -> Init<'slot, T> {
        *self.initialized = true;
        Init {
            _slot: PhantomData,
            _value: PhantomData,
        }
    }

    /// Writes one field at its final offset, transferring it to the caller.
    ///
    /// # Safety
    ///
    /// The offset names an unwritten, aligned field of the given type within
    /// this destination. On success the caller must release that field if the
    /// enclosing write fails or unwinds before the whole value takes it over.
    pub unsafe fn write_field<U: Build>(&mut self, offset: usize, parts: U::Parts<'b, W>) -> Result<(), Refusal> {
        // SAFETY: the caller supplies the exclusive field of this place.
        unsafe { self.memory.write_at::<U>(self.addr + offset, parts) }
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
            unsafe { T::free(self.addr, self.memory) };
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
        unsafe {
            free_elements::<T, W>(self.addr, self.written, self.memory);
            self.memory.free(self.addr, self.len * size_of::<T>());
        }
    }
}
