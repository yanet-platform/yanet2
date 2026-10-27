//! Typed construction of module configurations in shared memory.
//!
//! The author's closure builds the owned blocks and registrations of a
//! configuration through a [`Scope`] and returns the parts of its body, which
//! [`Builder::build`] writes in place: every relative pointer is formed at its
//! destination, so no offset is ever moved. A block or registration is bound to
//! the build that made it by an invariant lifetime, fresh for every build, so
//! it cannot be used in another build or outlive its own.
//!
//! A block is exactly its payload, as in C, which frees a structure by walking
//! its owning edges. Releasing does the same: [`Build::free`] returns every
//! block a value owns, reading only values that were written whole. Until a
//! block is written into its parent it is owned by an [`Owned`] or an
//! [`OwnedSlice`], which frees it on drop, so a build that stopped anywhere
//! releases what it took.

use core::{
    fmt,
    marker::PhantomData,
    mem::{ManuallyDrop, align_of, forget, size_of},
    sync::atomic::{AtomicI8, AtomicI16, AtomicI32, AtomicI64, AtomicU8, AtomicU16, AtomicU32, AtomicU64},
};

use yanet_shm::{
    rel::{ShmBox, ShmSlice},
    root::ShmRoot,
    shm::{MAX_ALIGN, ShmLayout},
};

use super::{
    addr::{BlockAddr, Context, Header},
    error::{BuildError, Refusal},
    owner::Owner,
};
use crate::{
    bindings,
    common::lpm::{Lpm, LpmRange},
    dp::{Counter, Device, Module, ModuleConfig},
};

/// Invariant lifetime brand: values of two builds never unify.
type Brand<'b> = PhantomData<fn(&'b ()) -> &'b ()>;

/// A shared-memory type and the parts a build writes it from.
///
/// # Safety
///
/// [`Build::write`] either writes the whole value at the address given and
/// returns `Ok`, or returns `Err` with nothing at the address owning anything:
/// it has returned what it allocated for the value and released its parts and
/// the blocks in them. It writes nothing else but the links of the C memory
/// context tree under the build's header, as an embedded C object does when it
/// opens its context, and does not panic. [`Build::free`] returns exactly the
/// blocks `write` handed to the value, each once, and reads only inside the
/// value, those blocks and the links of that tree, and writes those links as an
/// embedded C object does when it closes its context, which may reach a
/// sibling's context in another value. The SDK and its derive implement it, so
/// that a value of every type is written and released by code that knows its
/// layout. Implementing it is gated by `unsafe trait`, which module crates
/// cannot write directly under `#![forbid(unsafe_code)]`; `write` and `free`
/// are `unsafe fn`, so calling them is gated too.
pub unsafe trait Build: ShmLayout {
    /// What the author supplies for a value of this type inside build `'b` of
    /// owner `W`.
    type Parts<'b, W: Owner>;

    /// Writes `parts` as a `Self` at `addr`, with the blocks of `memory`.
    ///
    /// # Errors
    ///
    /// The memory is exhausted. Nothing at `addr` owns anything then, and the
    /// parts are released.
    ///
    /// # Safety
    ///
    /// `addr` is aligned for `Self`, and the `size_of::<Self>()` bytes there
    /// lie in a block of the build that made every block and registration in
    /// `parts`, which nobody else accesses; `memory` is that build's.
    #[doc(hidden)]
    unsafe fn write<W: Owner>(
        parts: Self::Parts<'_, W>,
        memory: &MemoryContext<'_, W>,
        addr: usize,
    ) -> Result<(), Refusal>;

    /// Returns to `memory` every block the `Self` at `addr` owns, as the
    /// `*_free` functions of C do; the storage of the value itself belongs to
    /// its parent.
    ///
    /// # Safety
    ///
    /// `addr` holds a `Self` written whole by [`Build::write`], every block it
    /// owns was allocated from `memory` with the exact size of its payload and
    /// is still live, and nothing references them.
    #[doc(hidden)]
    unsafe fn free<W: Owner>(addr: usize, memory: &MemoryContext<'_, W>);
}

/// The memory context of a module header, which the blocks of one
/// configuration come from and release returns them to.
#[doc(hidden)]
pub struct MemoryContext<'o, W: Owner> {
    owner: &'o W,
    context: Context,
    /// Keeps the context, and every scope and block that refers to it, on
    /// one thread.
    ///
    /// What it protects is the per-header C state, the memory context and
    /// the registries, which `W: Sync` does not promise to take concurrent
    /// calls.
    _local: PhantomData<*const ()>,
}

impl<'o, W: Owner> MemoryContext<'o, W> {
    /// The context the module header at `header` embeds, which `owner` has
    /// initialised.
    fn new(owner: &'o W, header: Header) -> Self {
        Self {
            owner,
            context: header.context(),
            _local: PhantomData,
        }
    }

    /// Provenance root of the context's memory.
    pub fn root(&self) -> ShmRoot {
        self.owner.root()
    }

    /// Returns a block of `size` payload bytes.
    ///
    /// # Safety
    ///
    /// `addr` was allocated from this context with `size` and is not
    /// referenced any longer.
    pub unsafe fn free(&self, addr: usize, size: usize) {
        // SAFETY: the context belongs to an initialised header and the block
        // is its own (caller contract).
        unsafe { self.owner.free_in(self.context, BlockAddr::new(addr), size) }
    }

    /// Allocates a block of `size` payload bytes.
    fn alloc(&self, size: usize) -> Option<usize> {
        // SAFETY: the header is initialised, so its context is.
        unsafe { self.owner.alloc_in(self.context, size) }.map(BlockAddr::get)
    }
}

macro_rules! plain_build {
    ($($ty:ty),* $(,)?) => {$(
        // SAFETY: the value is written whole.
        unsafe impl Build for $ty {
            type Parts<'b, W: Owner> = Self;

            unsafe fn write<W: Owner>(
                parts: Self,
                memory: &MemoryContext<'_, W>,
                addr: usize,
            ) -> Result<(), Refusal> {
                // SAFETY: aligned and exclusive (caller contract).
                unsafe { memory.root().ptr::<Self>(addr).write(parts) };
                Ok(())
            }

            unsafe fn free<W: Owner>(_: usize, _: &MemoryContext<'_, W>) {}
        }
    )*};
}

plain_build! {
    u8, u16, u32, u64, i8, i16, i32, i64,
    AtomicU8, AtomicU16, AtomicU32, AtomicU64,
    AtomicI8, AtomicI16, AtomicI32, AtomicI64,
    (),
}

// SAFETY: each element is written at its own offset, covering the array.
unsafe impl<T: Build, const N: usize> Build for [T; N] {
    type Parts<'b, W: Owner> = [T::Parts<'b, W>; N];

    unsafe fn write<W: Owner>(
        parts: Self::Parts<'_, W>,
        memory: &MemoryContext<'_, W>,
        addr: usize,
    ) -> Result<(), Refusal> {
        // SAFETY: no element is written yet.
        let mut partial = unsafe { Partial::<T, W>::new(memory, addr, 0) };
        for part in parts {
            // SAFETY: element `written` lies in the array (caller contract).
            unsafe { T::write::<W>(part, memory, addr + partial.written * size_of::<T>())? };
            partial.written += 1;
        }
        forget(partial);
        Ok(())
    }

    unsafe fn free<W: Owner>(addr: usize, memory: &MemoryContext<'_, W>) {
        // SAFETY: the elements were written whole (caller contract).
        unsafe { free_elements::<T, W>(addr, N, memory) }
    }
}

// SAFETY: the offset word is the whole value.
unsafe impl<T: Build> Build for ShmBox<T> {
    type Parts<'b, W: Owner> = Owned<'b, T, W>;

    unsafe fn write<W: Owner>(
        parts: Self::Parts<'_, W>,
        memory: &MemoryContext<'_, W>,
        addr: usize,
    ) -> Result<(), Refusal> {
        let target = parts.into_raw();
        debug_assert_ne!(0, target, "a box points at a block");
        // SAFETY: the word is the box's, aligned and exclusive (caller
        // contract).
        unsafe { memory.root().ptr::<isize>(addr).write(offset_to(target, addr)) };
        Ok(())
    }

    unsafe fn free<W: Owner>(addr: usize, memory: &MemoryContext<'_, W>) {
        // SAFETY: the offset word was written whole (caller contract).
        let offset = unsafe { memory.root().ptr::<isize>(addr).read() };
        // SAFETY: the offset leads to the live block of one `T` the box owns.
        unsafe { free_block::<T, W>(target_of(offset, addr), 1, memory) }
    }
}

// SAFETY: the offset and the count words are the whole value; an empty
// slice is NULL with a zero count.
unsafe impl<T: Build> Build for ShmSlice<T> {
    type Parts<'b, W: Owner> = OwnedSlice<'b, T, W>;

    unsafe fn write<W: Owner>(
        parts: Self::Parts<'_, W>,
        memory: &MemoryContext<'_, W>,
        addr: usize,
    ) -> Result<(), Refusal> {
        let root = memory.root();
        let (target, len) = parts.into_raw();
        // SAFETY: the words are the slice's, aligned and exclusive (caller
        // contract).
        unsafe {
            root.ptr::<isize>(addr).write(offset_to(target, addr));
            root.ptr::<u64>(addr + ShmSlice::<T>::LEN_OFFSET).write(len as u64);
        }
        Ok(())
    }

    unsafe fn free<W: Owner>(addr: usize, memory: &MemoryContext<'_, W>) {
        let root = memory.root();
        // SAFETY: the words were written whole (caller contract).
        let (offset, len) = unsafe {
            (
                root.ptr::<isize>(addr).read(),
                root.ptr::<u64>(addr + ShmSlice::<T>::LEN_OFFSET).read(),
            )
        };
        if offset != 0 {
            // SAFETY: a non-empty slice owns the live block of `len`
            // elements the offset leads to.
            unsafe { free_block::<T, W>(target_of(offset, addr), len as usize, memory) }
        }
    }
}

// SAFETY: the registry index is the whole value.
unsafe impl Build for Counter {
    type Parts<'b, W: Owner> = Registered<'b, Counter>;

    unsafe fn write<W: Owner>(
        parts: Self::Parts<'_, W>,
        memory: &MemoryContext<'_, W>,
        addr: usize,
    ) -> Result<(), Refusal> {
        // SAFETY: the id is the counter's only word, aligned and exclusive
        // (caller contract).
        unsafe { memory.root().ptr::<u64>(addr).write(parts.index) };
        Ok(())
    }

    unsafe fn free<W: Owner>(_: usize, _: &MemoryContext<'_, W>) {}
}

// SAFETY: the device index is the whole value.
unsafe impl Build for Device {
    type Parts<'b, W: Owner> = Registered<'b, Device>;

    unsafe fn write<W: Owner>(
        parts: Self::Parts<'_, W>,
        memory: &MemoryContext<'_, W>,
        addr: usize,
    ) -> Result<(), Refusal> {
        // SAFETY: the index is the device's only word, aligned and exclusive
        // (caller contract).
        unsafe { memory.root().ptr::<u64>(addr).write(parts.index) };
        Ok(())
    }

    unsafe fn free<W: Owner>(_: usize, _: &MemoryContext<'_, W>) {}
}

// SAFETY: the C table is the whole value. Its routines write inside it, in the
// blocks of its own memory context, which `free` returns, and in the links of
// the context tree under the header, which opening and closing a context
// update (`common/memory.h`).
unsafe impl<const K: usize> Build for Lpm<K> {
    /// The ranges, inserted in order: on an overlap the later one wins.
    type Parts<'b, W: Owner> = &'b [LpmRange<K>];

    unsafe fn write<W: Owner>(
        parts: Self::Parts<'_, W>,
        memory: &MemoryContext<'_, W>,
        addr: usize,
    ) -> Result<(), Refusal> {
        const { assert!(K >= 1 && K <= 16, "LPM keys are 1 to 16 bytes") };
        if parts.iter().any(|range| range.from > range.to) {
            return Err(Refusal::InvalidRange);
        }
        let root = memory.root();
        let lpm = root.ptr::<bindings::lpm>(addr);
        // SAFETY: the table lies in a block of the build (caller contract),
        // and the context is the header's, which the owner set up for C.
        let initialised = unsafe {
            let context = root.ptr::<bindings::memory_context>(memory.context.get());
            bindings::yanet_sys_common_lpm_init(lpm, context, c"lpm".as_ptr()) == 0
        };
        // SAFETY: an initialised table takes ranges of `K` bytes.
        let built = initialised
            && parts.iter().all(|range| unsafe {
                bindings::yanet_sys_common_lpm_insert(lpm, K as u8, range.from.as_ptr(), range.to.as_ptr(), range.value)
                    == 0
            });
        if built {
            return Ok(());
        }
        // SAFETY: freeing is sound after a failed init or insert: init clears
        // the directory before it allocates the first page, and the context
        // stays linked under the header until the free unlinks it
        // (`common/lpm.h`). Memory is C's only failure here.
        unsafe { bindings::yanet_sys_common_lpm_free(lpm) };
        Err(Refusal::OutOfMemory)
    }

    unsafe fn free<W: Owner>(addr: usize, memory: &MemoryContext<'_, W>) {
        // SAFETY: the table was written whole (caller contract), so its
        // pages and context are live.
        unsafe { bindings::yanet_sys_common_lpm_free(memory.root().ptr(addr)) };
    }
}

/// The block of one `T` allocated in build `'b`, the parts of a [`ShmBox`].
///
/// Created by a [`Scope`] only. It owns the block until it is written into
/// the parts of an owning field, which takes it over; dropped before that,
/// it returns the block and what the value owns.
#[must_use]
pub struct Owned<'b, T: Build, W: Owner> {
    addr: usize,
    memory: &'b MemoryContext<'b, W>,
    _brand: Brand<'b>,
    _value: PhantomData<fn() -> T>,
}

/// The block of the elements of a slice allocated in build `'b`, the parts of
/// a [`ShmSlice`]; empty when nothing was allocated.
///
/// Created by a [`Scope`] only. It owns the block until it is written into
/// the parts of an owning field, which takes it over; dropped before that,
/// it returns the block and what the elements own.
#[must_use]
pub struct OwnedSlice<'b, T: Build, W: Owner> {
    addr: usize,
    len: usize,
    memory: &'b MemoryContext<'b, W>,
    _brand: Brand<'b>,
    _element: PhantomData<fn() -> T>,
}

/// A counter or device registered in build `'b`, by its registry index.
///
/// Created by a [`Scope`] only. It is copied rather than registered again: C
/// finds a registration by name with a linear scan of the registry, so a
/// device registered once is copied into every entry that goes through it.
#[must_use]
pub struct Registered<'b, T> {
    index: u64,
    _brand: Brand<'b>,
    _kind: PhantomData<fn() -> T>,
}

impl<T: Build, W: Owner> fmt::Debug for Owned<'_, T, W> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> Result<(), fmt::Error> {
        f.debug_struct("Owned").field("addr", &self.addr).finish()
    }
}

impl<T: Build, W: Owner> fmt::Debug for OwnedSlice<'_, T, W> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> Result<(), fmt::Error> {
        f.debug_struct("OwnedSlice")
            .field("addr", &self.addr)
            .field("len", &self.len)
            .finish()
    }
}

impl<T> Clone for Registered<'_, T> {
    fn clone(&self) -> Self {
        *self
    }
}

impl<T> Copy for Registered<'_, T> {}

impl<T> fmt::Debug for Registered<'_, T> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> Result<(), fmt::Error> {
        f.debug_struct("Registered").field("index", &self.index).finish()
    }
}

impl<W: Owner> fmt::Debug for MemoryContext<'_, W> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> Result<(), fmt::Error> {
        f.debug_struct("MemoryContext").field("context", &self.context).finish()
    }
}

/// Self-relative offset from the word at `slot` to `target`, 0 for none.
fn offset_to(target: usize, slot: usize) -> isize {
    if target == 0 {
        0
    } else {
        target.wrapping_sub(slot) as isize
    }
}

/// Inverse of [`offset_to`].
fn target_of(offset: isize, slot: usize) -> usize {
    if offset == 0 {
        0
    } else {
        slot.wrapping_add_signed(offset)
    }
}

/// Returns what the `len` elements at `addr` own.
///
/// # Safety
///
/// As for [`Build::free`], for each element.
unsafe fn free_elements<T: Build, W: Owner>(addr: usize, len: usize, memory: &MemoryContext<'_, W>) {
    // Newest first: C prepends child memory contexts and unlinks from the
    // head, so this order is linear.
    for idx in (0..len).rev() {
        // SAFETY: element `idx` of the block (caller contract).
        unsafe { T::free(addr + idx * size_of::<T>(), memory) };
    }
}

/// Returns the block of `len` elements at `addr` and what they own.
///
/// # Safety
///
/// The block was allocated from `memory` with `len * size_of::<T>()` bytes,
/// is live and unreferenced, and its elements were written whole.
unsafe fn free_block<T: Build, W: Owner>(addr: usize, len: usize, memory: &MemoryContext<'_, W>) {
    // SAFETY: the elements were written whole (caller contract).
    unsafe { free_elements::<T, W>(addr, len, memory) };
    // SAFETY: the block is the one allocated, and its edges are returned.
    unsafe { memory.free(addr, len * size_of::<T>()) };
}

fn refused<W: Owner, E: From<BuildError<W::Error>>>(refusal: Refusal) -> E {
    BuildError::<W::Error>::Refused(refusal).into()
}

fn failed<W: Owner, E: From<BuildError<W::Error>>>(err: W::Error) -> E {
    BuildError::Owner(err).into()
}

/// A run of elements of which the first `written` are written, with the block
/// they lie in, if any; returns what they own and the block when dropped, also
/// while unwinding.
///
/// It guards the elements of a slice and their block, the elements of an
/// array, and each written field of a derived struct as a run of one. Forget
/// it once the run is written whole.
#[doc(hidden)]
pub struct Partial<'a, T: Build, W: Owner> {
    memory: &'a MemoryContext<'a, W>,
    addr: usize,
    written: usize,
    block: usize,
    _element: PhantomData<fn() -> T>,
}

impl<'a, T: Build, W: Owner> Partial<'a, T, W> {
    /// Guards the `written` elements at `addr`, which lie in a larger value.
    ///
    /// # Safety
    ///
    /// The first `written` elements at `addr` were written whole, and they
    /// are owned by nothing else; `memory` is the context the blocks they own
    /// came from.
    pub unsafe fn new(memory: &'a MemoryContext<'a, W>, addr: usize, written: usize) -> Self {
        Self {
            memory,
            addr,
            written,
            block: 0,
            _element: PhantomData,
        }
    }
}

impl<T: Build, W: Owner> Drop for Partial<'_, T, W> {
    fn drop(&mut self) {
        // SAFETY: the first `written` elements were written whole, and the
        // block, if any, was allocated with `block` bytes and is ours alone.
        unsafe {
            free_elements::<T, W>(self.addr, self.written, self.memory);
            if self.block != 0 {
                self.memory.free(self.addr, self.block);
            }
        }
    }
}

/// Construction services of one build: allocates blocks and registers
/// counters and devices.
///
/// A copyable handle shaped like a `&std::thread::Scope`. `'b` is the build
/// the scope belongs to, fresh for every [`Builder::build`] and invariant, so
/// nothing made by one build is accepted by another or outlives its own;
/// `'env` is what the build's closure borrows from outside it, which the
/// parts of a value may borrow for the build, such as the ranges of a table;
/// `E` is the error type of the author's closure, which the methods return in
/// place of the SDK's own.
pub struct Scope<'b, 'env: 'b, W: Owner, E = BuildError<<W as Owner>::Error>> {
    memory: &'b MemoryContext<'b, W>,
    header: Header,
    _brand: Brand<'b>,
    _env: PhantomData<&'env mut &'env ()>,
    _error: PhantomData<fn() -> E>,
}

impl<W: Owner, E> Clone for Scope<'_, '_, W, E> {
    fn clone(&self) -> Self {
        *self
    }
}

impl<W: Owner, E> Copy for Scope<'_, '_, W, E> {}

impl<W: Owner, E> fmt::Debug for Scope<'_, '_, W, E> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> Result<(), fmt::Error> {
        f.debug_struct("Scope").field("header", &self.header).finish()
    }
}

impl<'b, W: Owner, E: From<BuildError<W::Error>>> Scope<'b, '_, W, E> {
    /// Allocates a block holding one `T`, written from `parts`.
    ///
    /// A zero-sized `T` does not compile: C has no block of no bytes.
    ///
    /// # Errors
    ///
    /// The memory is exhausted, or the write of the value is refused; the
    /// blocks in `parts` and the block of the value are returned.
    pub fn boxed<T: Build>(self, parts: T::Parts<'b, W>) -> Result<Owned<'b, T, W>, E> {
        const { assert!(size_of::<T>() != 0, "a block of a zero-sized type") };
        let addr = self.alloc(size_of::<T>())?;
        // SAFETY: the fresh block holds one `T`, aligned (see `alloc`), and
        // every block and registration in the parts belongs to this build by
        // the brand.
        if let Err(refusal) = unsafe { T::write::<W>(parts, self.memory, addr) } {
            // SAFETY: the block is ours, and the refused write left nothing
            // in it.
            unsafe { self.memory.free(addr, size_of::<T>()) };
            return Err(refused::<W, E>(refusal));
        }
        Ok(Owned {
            addr,
            memory: self.memory,
            _brand: PhantomData,
            _value: PhantomData,
        })
    }

    /// Allocates one block of `len` elements, each written in place.
    ///
    /// `fill` is called once per index, in order, and returns the parts of
    /// that element. An empty slice allocates nothing. A zero-sized `T` does
    /// not compile.
    ///
    /// # Errors
    ///
    /// The size overflows, the memory is exhausted, `fill` fails, which is
    /// returned as it is, or the write of an element is refused. The
    /// elements written so far and the block are returned.
    pub fn slice<T: Build>(
        self,
        len: usize,
        mut fill: impl FnMut(usize) -> Result<T::Parts<'b, W>, E>,
    ) -> Result<OwnedSlice<'b, T, W>, E> {
        const { assert!(size_of::<T>() != 0, "a block of a zero-sized type") };
        if len == 0 {
            return Ok(self.owned_slice(0, 0));
        }
        let size = len
            .checked_mul(size_of::<T>())
            .ok_or_else(|| refused::<W, E>(Refusal::BlockTooLarge))?;
        let addr = self.alloc(size)?;
        let mut partial = Partial::<T, W> {
            memory: self.memory,
            addr,
            written: 0,
            block: size,
            _element: PhantomData,
        };
        for idx in 0..len {
            let parts = fill(idx)?;
            // SAFETY: element `idx < len` of the fresh block, aligned (see
            // `alloc`), and every block and registration in the parts belongs
            // to this build by the brand.
            unsafe { T::write::<W>(parts, self.memory, addr + idx * size_of::<T>()) }.map_err(refused::<W, E>)?;
            partial.written += 1;
        }
        forget(partial);
        Ok(self.owned_slice(addr, len))
    }

    /// Registers a single-value counter named `name` with the module.
    ///
    /// # Errors
    ///
    /// The owner refuses the registration.
    pub fn counter(self, name: &str) -> Result<Registered<'b, Counter>, E> {
        // SAFETY: the header is initialised and not yet published.
        let id = unsafe { self.memory.owner.register_counter(self.header, name) }.map_err(failed::<W, E>)?;
        Ok(Registered::new(id))
    }

    /// Links the device named `name` to the module.
    ///
    /// # Errors
    ///
    /// The owner refuses the device.
    pub fn device(self, name: &str) -> Result<Registered<'b, Device>, E> {
        // SAFETY: the header is initialised and not yet published.
        let index = unsafe { self.memory.owner.link_device(self.header, name) }.map_err(failed::<W, E>)?;
        Ok(Registered::new(index))
    }

    /// Allocates a block of `size` payload bytes.
    ///
    /// The size is a multiple of the alignment of the type it holds, so the
    /// allocator's alignment, the smaller of 64 and the size rounded up to a
    /// power of two, is at least that of the type, which `MAX_ALIGN` bounds.
    /// A size beyond the C block allocator's largest block is refused before
    /// the owner is asked, as no free memory could serve it.
    fn alloc(self, size: usize) -> Result<usize, E> {
        if size > bindings::MEMORY_BLOCK_ALLOCATOR_MAX_SIZE as usize {
            return Err(refused::<W, E>(Refusal::BlockTooLarge));
        }
        self.memory
            .alloc(size)
            .ok_or_else(|| refused::<W, E>(Refusal::OutOfMemory))
    }

    fn owned_slice<T: Build>(self, addr: usize, len: usize) -> OwnedSlice<'b, T, W> {
        OwnedSlice {
            addr,
            len,
            memory: self.memory,
            _brand: PhantomData,
            _element: PhantomData,
        }
    }
}

impl<T: Build, W: Owner> Owned<'_, T, W> {
    /// Hands the block over to the field it is written into: its address,
    /// without returning it.
    fn into_raw(self) -> usize {
        ManuallyDrop::new(self).addr
    }
}

impl<T: Build, W: Owner> Drop for Owned<'_, T, W> {
    fn drop(&mut self) {
        // SAFETY: the block holds one `T` written whole and was allocated
        // from `memory` with its exact size; nothing but this value owns it.
        unsafe { free_block::<T, W>(self.addr, 1, self.memory) }
    }
}

impl<T: Build, W: Owner> OwnedSlice<'_, T, W> {
    /// Hands the block over to the field it is written into: its address and
    /// element count, without returning it.
    fn into_raw(self) -> (usize, usize) {
        let this = ManuallyDrop::new(self);
        (this.addr, this.len)
    }
}

impl<T: Build, W: Owner> Drop for OwnedSlice<'_, T, W> {
    fn drop(&mut self) {
        if self.addr != 0 {
            // SAFETY: the block holds `len` elements written whole and was
            // allocated from `memory` with its exact size; nothing but this
            // value owns it.
            unsafe { free_block::<T, W>(self.addr, self.len, self.memory) }
        }
    }
}

impl<T> Registered<'_, T> {
    fn new(index: u64) -> Self {
        Self {
            index,
            _brand: PhantomData,
            _kind: PhantomData,
        }
    }
}

/// Entry point of the construction of configurations of module `M`; never
/// constructed.
pub struct Builder<M>(PhantomData<fn() -> M>);

impl<M: Module> Builder<M> {
    /// Builds a configuration named `name`; the caller owns it and publishes
    /// it.
    ///
    /// The closure builds blocks and registrations through its scope and
    /// returns the parts of the body, which may borrow what the closure
    /// captures. The scope's lifetime is fresh for the call, so a block of
    /// this build is accepted by no other build and cannot leave the closure.
    /// When the closure fails or panics, or the owner reports an error,
    /// everything allocated is released, except a block the closure leaks
    /// (`mem::forget`, for one), which stays leaked.
    ///
    /// # Errors
    ///
    /// The owner's memory is exhausted, its header routine refuses the module
    /// type or the name, the closure fails, or the write of the body is
    /// refused.
    pub fn build<'env, E, W>(
        owner: &W,
        name: &str,
        f: impl for<'b> FnOnce(Scope<'b, 'env, W, E>) -> Result<<M::Config as Build>::Parts<'b, W>, E>,
    ) -> Result<Built<M, W>, E>
    where
        W: Owner,
        E: From<BuildError<W::Error>>,
    {
        const {
            assert!(
                size_of::<ModuleConfig<M::Config>>() <= bindings::MEMORY_BLOCK_ALLOCATOR_MAX_SIZE as usize,
                "a configuration larger than the largest block of the allocator"
            )
        };
        let size = size_of::<ModuleConfig<M::Config>>();
        let block = owner.alloc(size).ok_or_else(|| refused::<W, E>(Refusal::OutOfMemory))?;
        // SAFETY: the root block holds the header and the body, so it is large
        // enough for the header.
        let header = unsafe { Header::at(block) };
        // SAFETY: the block is ours and the header sits at its start.
        if let Err(err) = unsafe { owner.init_header(header, M::NAME, name) } {
            // SAFETY: the block came from `alloc` with this size, and a
            // refused header leaves nothing to finalise.
            unsafe { owner.free(block, size) };
            return Err(failed::<W, E>(err));
        }
        // The blocks of the closure's values are returned, or leaked, before
        // this runs.
        let release = Release { owner, header, size };
        let memory = MemoryContext::new(owner, header);
        let parts = f(Scope {
            memory: &memory,
            header,
            _brand: PhantomData,
            _env: PhantomData,
            _error: PhantomData,
        })?;

        let body = header.get() + ModuleConfig::<M::Config>::BODY_OFFSET;
        // SAFETY: the body lies in the root block, aligned, and the blocks
        // and registrations in the parts are this build's by the brand.
        unsafe { M::Config::write::<W>(parts, &memory, body) }.map_err(refused::<W, E>)?;
        forget(release);
        Ok(Built { header, _kind: PhantomData })
    }
}

/// A configuration of module `M` that [`Builder::build`] handed over in the
/// memory of owner type `W`: its header address, to publish, and the right to
/// release it.
///
/// Not freed on drop, as the configuration is shared once published; release
/// it with [`Built::destroy`], which takes an owner of type `W` only.
#[must_use]
pub struct Built<M, W> {
    header: Header,
    _kind: PhantomData<fn() -> (M, W)>,
}

impl<M, W> fmt::Debug for Built<M, W> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> Result<(), fmt::Error> {
        f.debug_struct("Built").field("header", &self.header).finish()
    }
}

impl<M, W> Built<M, W> {
    /// Address of the module header, which is what the control plane
    /// publishes.
    pub fn header(&self) -> Header {
        self.header
    }
}

impl<M: Module, W: Owner> Built<M, W> {
    /// Releases the configuration with everything it owns.
    ///
    /// # Safety
    ///
    /// `owner` is the one that built it, and nothing references the
    /// configuration any longer.
    pub unsafe fn destroy(self, owner: &W) {
        let memory = MemoryContext::new(owner, self.header);
        let body = self.header.get() + ModuleConfig::<M::Config>::BODY_OFFSET;
        // SAFETY: the body was written whole by the build, and every block it
        // owns came from the header's context (caller contract).
        unsafe { M::Config::free::<W>(body, &memory) };
        // SAFETY: every block the body owns is returned, and a block leaked
        // during the build is never referenced.
        unsafe { release_root(owner, self.header, size_of::<ModuleConfig<M::Config>>()) }
    }
}

/// Releases the header and root block of an unfinished build when dropped,
/// also while unwinding.
struct Release<'a, W: Owner> {
    owner: &'a W,
    header: Header,
    size: usize,
}

impl<W: Owner> Drop for Release<'_, W> {
    fn drop(&mut self) {
        // SAFETY: the blocks of the build's context are returned by the drops
        // of the values that owned them, or leaked if forgotten, which C's
        // finalisation allows; the root block is ours.
        unsafe { release_root(self.owner, self.header, self.size) }
    }
}

/// Finalises the header at `header` and returns its root block of `size`
/// bytes.
///
/// # Safety
///
/// `header` is a module header initialised by `owner` at the start of a root
/// block allocated with `size`, every block of its context is returned or
/// never referenced again, and nothing references the header.
unsafe fn release_root<W: Owner>(owner: &W, header: Header, size: usize) {
    // SAFETY: guaranteed by the caller; the header is the start of the block.
    unsafe {
        owner.fini_header(header);
        owner.free(BlockAddr::new(header.get()), size);
    }
}

// The root block holds the header and the body, so the header's alignment is
// the one it needs from the allocator.
const _: () = assert!(align_of::<ModuleConfig<u64>>() <= MAX_ALIGN);
