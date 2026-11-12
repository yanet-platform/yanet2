//! Typed construction of module configurations in shared memory.
//!
//! The author's closure builds the owned blocks and registrations of a
//! configuration through a [`Scope`] and returns the parts of its body, which
//! [`Builder::build`] writes in place: every relative pointer is formed at its
//! destination, so no offset is ever moved. A block or registration is bound to
//! the build that made it by an invariant lifetime, fresh for every build, so
//! it cannot be used in another build or outlive its own.
//!
//! A block is its payload, as in C, which frees a structure by walking its
//! owning edges; a per-worker block also holds the slack that puts its first
//! slot or value on a cache line, sized again from its counts. Releasing does
//! the same: [`Build::free`] returns every block a value owns, reading only
//! values that were written whole. Until a block is written into its parent it
//! is owned by an [`Owned`], an [`OwnedSlice`], an [`OwnedSlots`] or an
//! [`OwnedRegions`], which frees it on drop, so a build that stopped anywhere
//! releases what it took.

use core::{
    ffi::c_void,
    fmt,
    marker::PhantomData,
    mem::{ManuallyDrop, align_of, forget, size_of},
    net::{Ipv4Addr, Ipv6Addr},
    ptr,
    sync::atomic::{AtomicI8, AtomicI16, AtomicI32, AtomicI64, AtomicU8, AtomicU16, AtomicU32, AtomicU64},
};

use yanet_shm::{
    index::{HashIndexKey, Slot, Unplaced, place_iter, slots_for},
    mutex::Mutex,
    rel::{ShmBox, ShmSlice},
    root::ShmRoot,
    shm::{MAX_ALIGN, ShmLayout},
};

use super::{
    addr::{BlockAddr, Context, Header},
    error::{BuildError, Refusal},
    names,
    owner::Owner,
};
use crate::{
    bindings,
    common::{
        index::HashIndex,
        lpm::{Lpm, LpmRange},
    },
    dp::{
        Counter, CounterLayout, Device, Module, ModuleConfig, Object, ObjectConfig, ObjectCounter, ObjectLink,
        PerWorker, PerWorkerSlice, TaggedCounter, TaggedCounters,
    },
};

mod classify;

pub use classify::Branch;

/// Invariant lifetime brand: values of two builds never unify.
type Brand<'b> = PhantomData<fn(&'b ()) -> &'b ()>;

/// What a build makes, which sets the C header it starts from and what its
/// scope registers.
///
/// Sealed: the kinds are [`ModuleKind`] and [`ObjectKind`].
pub trait Kind: kind::Sealed + 'static {
    /// Initialises the header of a build of the type and name.
    ///
    /// # Safety
    ///
    /// As [`Owner::init_header`].
    #[doc(hidden)]
    unsafe fn init<W: Owner>(owner: &W, header: Header, ty: &str, name: &str) -> Result<(), W::Error>;

    /// Releases what [`Kind::init`] set up.
    ///
    /// # Safety
    ///
    /// As [`Owner::fini_header`].
    #[doc(hidden)]
    unsafe fn fini<W: Owner>(owner: &W, header: Header);
}

/// The kind of a module configuration: its header carries the module's
/// counter registry, device list and object links, which its scope registers
/// into.
pub enum ModuleKind {}

/// The kind of an object: its header carries the object's own counter
/// registry, which its scope registers counters into, and no module
/// registries, so its scope registers no tagged counter and links no device or
/// object.
pub enum ObjectKind {}

impl Kind for ModuleKind {
    unsafe fn init<W: Owner>(owner: &W, header: Header, ty: &str, name: &str) -> Result<(), W::Error> {
        // SAFETY: the caller's contract.
        unsafe { owner.init_header(header, ty, name) }
    }

    unsafe fn fini<W: Owner>(owner: &W, header: Header) {
        // SAFETY: the caller's contract.
        unsafe { owner.fini_header(header) }
    }
}

impl Kind for ObjectKind {
    unsafe fn init<W: Owner>(owner: &W, header: Header, ty: &str, name: &str) -> Result<(), W::Error> {
        // SAFETY: the caller's contract.
        unsafe { owner.init_object_header(header, ty, name) }
    }

    unsafe fn fini<W: Owner>(owner: &W, header: Header) {
        // SAFETY: the caller's contract.
        unsafe { owner.fini_object_header(header) }
    }
}

mod kind {
    pub trait Sealed {}

    impl Sealed for super::ModuleKind {}

    impl Sealed for super::ObjectKind {}
}

/// The owned-memory layout of a shared-memory value.
///
/// The default source keeps named parts concise; construction may choose a
/// different source without changing the published layout or its release.
///
/// # Safety
///
/// Freeing returns exactly the blocks a successful write handed to the value,
/// each once. It reads only the value, its owned blocks and memory-context
/// links, and writes only those links. A whole value has every field
/// initialized; padding may remain uninitialized and is never read as data.
pub unsafe trait Build: ShmLayout + for<'b> BuildFrom<<Self as Build>::DefaultSource<'b>> {
    /// The source used when the parts omit their source parameters.
    type DefaultSource<'b>;

    /// Returns to a memory context every block the value at an address owns,
    /// as the `*_free` functions of C do; the storage of the value itself
    /// belongs to its parent.
    ///
    /// # Safety
    ///
    /// The address holds a value written whole by [`BuildFrom::write`], every
    /// block it owns was allocated from the memory context with the exact size
    /// of its payload and is still live, and nothing references them.
    #[doc(hidden)]
    unsafe fn free<W: Owner>(addr: usize, memory: &MemoryContext<'_, W>);
}

/// Writes a shared-memory value from one family of input parts.
///
/// The source identifies the input representation. The build lifetime and
/// owner remain separate so allocations cannot cross between builds.
///
/// # Safety
///
/// Writing initializes the whole value at its final address, or leaves
/// nothing there owning anything on refusal or panic. The write releases its
/// parts and their allocations on failure. It writes only the value and the
/// links of memory contexts beneath the build's header. Every successful
/// write must be releasable by the value's source-independent release.
pub unsafe trait BuildFrom<S>: ShmLayout {
    /// What the author supplies for a value of this type inside a build of an
    /// owner.
    type Parts<'b, W: Owner>;

    /// Writes the parts as a value at an address, with the blocks of a memory
    /// context.
    ///
    /// # Errors
    ///
    /// The input is refused or an allocation fails. Nothing at the address
    /// owns anything then, and the parts are released.
    ///
    /// # Safety
    ///
    /// The address is aligned for the value, and the value's bytes there lie
    /// in a block of the build that made every block and registration in the
    /// parts, which nobody else accesses; the memory context is that build's.
    #[doc(hidden)]
    unsafe fn write<W: Owner>(
        parts: Self::Parts<'_, W>,
        memory: &MemoryContext<'_, W>,
        addr: usize,
    ) -> Result<(), Refusal>;
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
    /// the registries, which the owner's `Sync` does not promise to take
    /// concurrent calls.
    _local: PhantomData<*const ()>,
}

impl<'o, W: Owner> MemoryContext<'o, W> {
    /// The context the module header embeds, which the owner has initialised.
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

    /// Returns a block of the given payload size.
    ///
    /// # Safety
    ///
    /// The block was allocated from this context with that size and is not
    /// referenced any longer.
    pub unsafe fn free(&self, addr: usize, size: usize) {
        // SAFETY: the context belongs to an initialised header and the block
        // is its own (caller contract).
        unsafe { self.owner.free_in(self.context, BlockAddr::new(addr), size) }
    }

    /// Allocates a block of the given payload size.
    fn alloc(&self, size: usize) -> Option<usize> {
        // SAFETY: the header is initialised, so its context is.
        unsafe { self.owner.alloc_in(self.context, size) }.map(BlockAddr::get)
    }
}

macro_rules! plain_build {
    ($($ty:ty),* $(,)?) => {$(
        // SAFETY: the value is written whole.
        unsafe impl Build for $ty {
            type DefaultSource<'b> = ();

            unsafe fn free<W: Owner>(_: usize, _: &MemoryContext<'_, W>) {}
        }

        // SAFETY: the value is written whole.
        unsafe impl BuildFrom<()> for $ty {
            type Parts<'b, W: Owner> = Self;

            unsafe fn write<W: Owner>(
                parts: Self,
                memory: &MemoryContext<'_, W>,
                addr: usize,
            ) -> Result<(), Refusal> {
                // SAFETY: aligned and exclusive address (caller contract).
                unsafe { memory.root().ptr::<Self>(addr).write(parts) };
                Ok(())
            }
        }
    )*};
}

plain_build! {
    u8, u16, u32, u64, i8, i16, i32, i64,
    AtomicU8, AtomicU16, AtomicU32, AtomicU64,
    AtomicI8, AtomicI16, AtomicI32, AtomicI64,
    Ipv4Addr, Ipv6Addr,
    (),
}

// SAFETY: a copyable payload owns no blocks or address-relative edges. The
// mutex is written whole with its lock free and needs no release work.
unsafe impl<T> Build for Mutex<T>
where
    T: Copy + ShmLayout,
{
    type DefaultSource<'b> = ();

    unsafe fn free<W: Owner>(_: usize, _: &MemoryContext<'_, W>) {}
}

// SAFETY: a copyable payload owns no blocks or address-relative edges. The
// mutex is written whole with its lock free and needs no release work.
unsafe impl<T> BuildFrom<()> for Mutex<T>
where
    T: Copy + ShmLayout,
{
    type Parts<'b, W: Owner> = T;

    unsafe fn write<W: Owner>(parts: T, memory: &MemoryContext<'_, W>, addr: usize) -> Result<(), Refusal> {
        // SAFETY: the destination is aligned and exclusive (caller contract).
        unsafe { memory.root().ptr::<Self>(addr).write(Self::new(parts)) };
        Ok(())
    }
}

// SAFETY: each element is written at its own offset, covering the array.
unsafe impl<T: Build, const N: usize> Build for [T; N] {
    type DefaultSource<'b> = T::DefaultSource<'b>;

    unsafe fn free<W: Owner>(addr: usize, memory: &MemoryContext<'_, W>) {
        // SAFETY: the elements were written whole (caller contract).
        unsafe { free_elements::<T, W>(addr, N, memory) }
    }
}

// SAFETY: each element is written at its own offset, covering the array.
unsafe impl<T: Build + BuildFrom<S>, S, const N: usize> BuildFrom<S> for [T; N] {
    type Parts<'b, W: Owner> = [<T as BuildFrom<S>>::Parts<'b, W>; N];

    unsafe fn write<W: Owner>(
        parts: <Self as BuildFrom<S>>::Parts<'_, W>,
        memory: &MemoryContext<'_, W>,
        addr: usize,
    ) -> Result<(), Refusal> {
        // SAFETY: no element is written yet.
        let mut partial = unsafe { Partial::<T, W>::new(memory, addr, 0) };
        for part in parts {
            // SAFETY: the element being written lies in the array (caller
            // contract).
            unsafe { <T as BuildFrom<S>>::write::<W>(part, memory, addr + partial.written * size_of::<T>())? };
            partial.written += 1;
        }
        forget(partial);
        Ok(())
    }
}

// SAFETY: the offset word is the whole value.
unsafe impl<T: Build> Build for ShmBox<T> {
    type DefaultSource<'b> = ();

    unsafe fn free<W: Owner>(addr: usize, memory: &MemoryContext<'_, W>) {
        // SAFETY: the offset word was written whole (caller contract).
        let offset = unsafe { memory.root().ptr::<isize>(addr).read() };
        // SAFETY: the offset leads to the live block of one value the box
        // owns.
        unsafe { free_block::<T, W>(target_of(offset, addr), 1, memory) }
    }
}

// SAFETY: the offset word is the whole value.
unsafe impl<T: Build> BuildFrom<()> for ShmBox<T> {
    type Parts<'b, W: Owner> = Owned<'b, T, W>;

    unsafe fn write<W: Owner>(
        parts: <Self as BuildFrom<()>>::Parts<'_, W>,
        memory: &MemoryContext<'_, W>,
        addr: usize,
    ) -> Result<(), Refusal> {
        let target = parts.into_raw();
        debug_assert_ne!(0, target, "a box points at a block");
        // SAFETY: the offset word is aligned and exclusive (caller contract).
        unsafe { memory.root().ptr::<isize>(addr).write(offset_to(target, addr)) };
        Ok(())
    }
}

// SAFETY: the offset and the count words are the whole value; an empty
// slice is NULL with a zero count.
unsafe impl<T: Build> Build for ShmSlice<T> {
    type DefaultSource<'b> = ();

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
            // SAFETY: a non-empty slice owns the live block of as many
            // elements as its count, which the offset leads to.
            unsafe { free_block::<T, W>(target_of(offset, addr), len as usize, memory) }
        }
    }
}

// SAFETY: the offset and the count words are the whole value; an empty
// slice is NULL with a zero count.
unsafe impl<T: Build> BuildFrom<()> for ShmSlice<T> {
    type Parts<'b, W: Owner> = OwnedSlice<'b, T, W>;

    unsafe fn write<W: Owner>(
        parts: <Self as BuildFrom<()>>::Parts<'_, W>,
        memory: &MemoryContext<'_, W>,
        addr: usize,
    ) -> Result<(), Refusal> {
        let root = memory.root();
        let (target, len) = parts.into_raw();
        // SAFETY: the offset and count words are aligned and exclusive (caller
        // contract).
        unsafe {
            root.ptr::<isize>(addr).write(offset_to(target, addr));
            root.ptr::<u64>(addr + ShmSlice::<T>::LEN_OFFSET).write(len as u64);
        }
        Ok(())
    }
}

// SAFETY: the registry index is the whole value.
unsafe impl<T: CounterLayout> Build for Counter<T> {
    type DefaultSource<'b> = ();

    unsafe fn free<W: Owner>(_: usize, _: &MemoryContext<'_, W>) {}
}

// SAFETY: the registry index is the whole value.
unsafe impl<T: CounterLayout> BuildFrom<()> for Counter<T> {
    type Parts<'b, W: Owner> = Registered<'b, Counter<T>>;

    unsafe fn write<W: Owner>(
        parts: <Self as BuildFrom<()>>::Parts<'_, W>,
        memory: &MemoryContext<'_, W>,
        addr: usize,
    ) -> Result<(), Refusal> {
        // SAFETY: the registry index is the counter's only word, aligned and
        // exclusive (caller contract).
        unsafe { memory.root().ptr::<u64>(addr).write(parts.index) };
        Ok(())
    }
}

// SAFETY: the registry index is the whole value.
unsafe impl<T> Build for ObjectCounter<T>
where
    T: CounterLayout,
{
    type DefaultSource<'b> = ();

    unsafe fn free<W: Owner>(_: usize, _: &MemoryContext<'_, W>) {}
}

// SAFETY: the registry index is the whole value.
unsafe impl<T> BuildFrom<()> for ObjectCounter<T>
where
    T: CounterLayout,
{
    type Parts<'b, W: Owner> = Registered<'b, ObjectCounter<T>>;

    unsafe fn write<W: Owner>(
        parts: <Self as BuildFrom<()>>::Parts<'_, W>,
        memory: &MemoryContext<'_, W>,
        addr: usize,
    ) -> Result<(), Refusal> {
        // SAFETY: the registry index is the counter's only word, aligned and
        // exclusive (caller contract).
        unsafe { memory.root().ptr::<u64>(addr).write(parts.index) };
        Ok(())
    }
}

// SAFETY: the registry index and the counter index are the whole value.
unsafe impl<T> Build for TaggedCounter<T>
where
    T: CounterLayout,
{
    type DefaultSource<'b> = ();

    unsafe fn free<W: Owner>(_: usize, _: &MemoryContext<'_, W>) {}
}

// SAFETY: the registry index and the counter index are the whole value.
unsafe impl<T> BuildFrom<()> for TaggedCounter<T>
where
    T: CounterLayout,
{
    type Parts<'b, W: Owner> = Registered<'b, TaggedCounter<T>>;

    unsafe fn write<W: Owner>(
        parts: <Self as BuildFrom<()>>::Parts<'_, W>,
        memory: &MemoryContext<'_, W>,
        addr: usize,
    ) -> Result<(), Refusal> {
        let root = memory.root();
        // SAFETY: the registry index and the counter index are the counter's
        // two words in the order of its fields, aligned and exclusive (caller
        // contract).
        unsafe {
            root.ptr::<u64>(addr).write(parts.registry);
            root.ptr::<u64>(addr + size_of::<u64>()).write(parts.index);
        }
        Ok(())
    }
}

// SAFETY: the registry index and the number of counters are the whole value.
unsafe impl<T> Build for TaggedCounters<T>
where
    T: CounterLayout,
{
    type DefaultSource<'b> = ();

    unsafe fn free<W: Owner>(_: usize, _: &MemoryContext<'_, W>) {}
}

// SAFETY: the registry index and the number of counters are the whole value.
unsafe impl<T> BuildFrom<()> for TaggedCounters<T>
where
    T: CounterLayout,
{
    type Parts<'b, W: Owner> = CounterRegistrar<'b, T, W>;

    unsafe fn write<W: Owner>(
        parts: <Self as BuildFrom<()>>::Parts<'_, W>,
        memory: &MemoryContext<'_, W>,
        addr: usize,
    ) -> Result<(), Refusal> {
        let root = memory.root();
        // SAFETY: the registry index and the number of counters are the
        // value's two words in the order of its fields, aligned and exclusive
        // (caller contract).
        unsafe {
            root.ptr::<u64>(addr).write(parts.registry);
            root.ptr::<u64>(addr + size_of::<u64>()).write(parts.len);
        }
        Ok(())
    }
}

// SAFETY: the device index is the whole value.
unsafe impl Build for Device {
    type DefaultSource<'b> = ();

    unsafe fn free<W: Owner>(_: usize, _: &MemoryContext<'_, W>) {}
}

// SAFETY: the device index is the whole value.
unsafe impl BuildFrom<()> for Device {
    type Parts<'b, W: Owner> = Registered<'b, Device>;

    unsafe fn write<W: Owner>(
        parts: <Self as BuildFrom<()>>::Parts<'_, W>,
        memory: &MemoryContext<'_, W>,
        addr: usize,
    ) -> Result<(), Refusal> {
        // SAFETY: the device index is the device's only word, aligned and
        // exclusive (caller contract).
        unsafe { memory.root().ptr::<u64>(addr).write(parts.index) };
        Ok(())
    }
}

// SAFETY: the link index is the whole value.
unsafe impl<O: Object> Build for ObjectLink<O> {
    type DefaultSource<'b> = ();

    unsafe fn free<W: Owner>(_: usize, _: &MemoryContext<'_, W>) {}
}

// SAFETY: the link index is the whole value.
unsafe impl<O: Object> BuildFrom<()> for ObjectLink<O> {
    type Parts<'b, W: Owner> = Registered<'b, ObjectLink<O>>;

    unsafe fn write<W: Owner>(
        parts: <Self as BuildFrom<()>>::Parts<'_, W>,
        memory: &MemoryContext<'_, W>,
        addr: usize,
    ) -> Result<(), Refusal> {
        // SAFETY: the link index is the link's only word, aligned and
        // exclusive (caller contract).
        unsafe { memory.root().ptr::<u64>(addr).write(parts.index) };
        Ok(())
    }
}

// SAFETY: the offset and the count words are the whole value; an owner without
// workers leaves the field NULL with a zero count. The slots are plain values,
// so the block owns nothing.
unsafe impl<T: Copy + ShmLayout> Build for PerWorker<T> {
    type DefaultSource<'b> = ();

    unsafe fn free<W: Owner>(addr: usize, memory: &MemoryContext<'_, W>) {
        let root = memory.root();
        // SAFETY: the words were written whole (caller contract).
        let (offset, workers) = unsafe {
            (
                root.ptr::<isize>(addr).read(),
                root.ptr::<u64>(addr + size_of::<isize>()).read(),
            )
        };
        if offset != 0 {
            // SAFETY: the offset leads to the live slot block of as many
            // workers as the count, which the value owns, allocated with the
            // size of that count; the slots own nothing.
            unsafe { memory.free(target_of(offset, addr), slot_block::<T>(workers)) }
        }
    }
}

// SAFETY: the offset and the count words are the whole value; an owner without
// workers leaves the field NULL with a zero count. The slots are plain values,
// so the block owns nothing.
unsafe impl<T: Copy + ShmLayout> BuildFrom<()> for PerWorker<T> {
    type Parts<'b, W: Owner> = OwnedSlots<'b, T, W>;

    unsafe fn write<W: Owner>(
        parts: <Self as BuildFrom<()>>::Parts<'_, W>,
        memory: &MemoryContext<'_, W>,
        addr: usize,
    ) -> Result<(), Refusal> {
        let root = memory.root();
        let (target, workers) = parts.into_raw();
        // SAFETY: the offset and count words are aligned and exclusive (caller
        // contract).
        unsafe {
            root.ptr::<isize>(addr).write(offset_to(target, addr));
            root.ptr::<u64>(addr + size_of::<isize>()).write(workers);
        }
        Ok(())
    }
}

// SAFETY: the table offset and the two count words are the whole value; a
// field of no region bytes, or of an owner without workers, leaves the table
// NULL. The regions are plain values, so their blocks own nothing.
unsafe impl<T: Copy + ShmLayout> Build for PerWorkerSlice<T> {
    type DefaultSource<'b> = ();

    unsafe fn free<W: Owner>(addr: usize, memory: &MemoryContext<'_, W>) {
        let root = memory.root();
        // SAFETY: the words were written whole (caller contract).
        let (offset, workers, len) = unsafe {
            (
                root.ptr::<isize>(addr).read(),
                root.ptr::<u64>(addr + size_of::<isize>()).read(),
                root.ptr::<u64>(addr + 2 * size_of::<isize>()).read(),
            )
        };
        if offset != 0 {
            // SAFETY: the offset leads to the live table of as many workers as
            // the count, which the value owns with the regions it names, each
            // allocated with the size of the length.
            unsafe { free_regions::<T, W>(target_of(offset, addr), workers, len as usize, memory) }
        }
    }
}

// SAFETY: the table offset and the two count words are the whole value; a
// field of no region bytes, or of an owner without workers, leaves the table
// NULL. The regions are plain values, so their blocks own nothing.
unsafe impl<T: Copy + ShmLayout> BuildFrom<()> for PerWorkerSlice<T> {
    type Parts<'b, W: Owner> = OwnedRegions<'b, T, W>;

    unsafe fn write<W: Owner>(
        parts: <Self as BuildFrom<()>>::Parts<'_, W>,
        memory: &MemoryContext<'_, W>,
        addr: usize,
    ) -> Result<(), Refusal> {
        let root = memory.root();
        let (table, workers, len) = parts.into_raw();
        // SAFETY: the offset and count words are aligned and exclusive (caller
        // contract).
        unsafe {
            root.ptr::<isize>(addr).write(offset_to(table, addr));
            root.ptr::<u64>(addr + size_of::<isize>()).write(workers);
            root.ptr::<u64>(addr + 2 * size_of::<isize>()).write(len as u64);
        }
        Ok(())
    }
}

// SAFETY: the C table is the whole value. Its routines write inside it, in the
// blocks of its own memory context, which freeing returns, and in the links of
// the context tree under the header, which opening and closing a context
// update (`common/memory.h`).
unsafe impl<const K: usize> Build for Lpm<K> {
    type DefaultSource<'b> = ();

    unsafe fn free<W: Owner>(addr: usize, memory: &MemoryContext<'_, W>) {
        // SAFETY: the table was written whole (caller contract).
        let lpm = unsafe { &*memory.root().ptr::<Lpm<K>>(addr) };
        if lpm.is_empty() {
            // An empty table opened nothing in C.
            return;
        }
        // SAFETY: a table that is not empty was initialised, so its pages and
        // context are live.
        unsafe { bindings::yanet_sys_common_lpm_free(memory.root().ptr(addr)) };
    }
}

// SAFETY: the C table is the whole value. Its routines write inside it, in the
// blocks of its own memory context, which freeing returns, and in the links of
// the context tree under the header, which opening and closing a context
// update (`common/memory.h`).
unsafe impl<const K: usize> BuildFrom<()> for Lpm<K> {
    /// The ranges, inserted in order: on an overlap the later one wins. No
    /// ranges leave the table empty, allocating nothing.
    type Parts<'b, W: Owner> = &'b [LpmRange<K>];

    unsafe fn write<W: Owner>(
        parts: <Self as BuildFrom<()>>::Parts<'_, W>,
        memory: &MemoryContext<'_, W>,
        addr: usize,
    ) -> Result<(), Refusal> {
        const { assert!(K >= 1 && K <= 16, "LPM keys are 1 to 16 bytes") };
        if parts.iter().any(|range| range.from > range.to) {
            return Err(Refusal::InvalidRange);
        }
        let root = memory.root();
        if parts.is_empty() {
            // SAFETY: the table's bytes lie in a block of the build, exclusive
            // (caller contract), and a zeroed table is the empty one: a null
            // directory and no C memory context to close.
            unsafe { root.ptr::<u8>(addr).write_bytes(0, size_of::<Lpm<K>>()) };
            return Ok(());
        }
        let lpm = root.ptr::<bindings::lpm>(addr);
        // SAFETY: the table lies in a block of the build (caller contract),
        // and the context is the header's, which the owner set up for C.
        let initialised = unsafe {
            let context = root.ptr::<bindings::memory_context>(memory.context.get());
            bindings::yanet_sys_common_lpm_init(lpm, context, c"lpm".as_ptr()) == 0
        };
        // SAFETY: an initialised table takes ranges of the key size.
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
}

/// Input records for constructing an exact-match index.
///
/// The iterator is consumed while the index is written at its final address.
/// Keeping it by value permits inline projections without temporary storage.
pub struct HashIndexParts<I> {
    records: I,
}

impl<K: HashIndexKey> HashIndex<K> {
    /// Consumes records without an intermediate array.
    ///
    /// A write reads the declared count once, then checks that exactly that
    /// many records precede the first end of input, including for an empty
    /// index. A refusal stops consumption.
    pub fn builder<I>(records: I) -> HashIndexParts<I>
    where
        I: ExactSizeIterator<Item = (K, u32)>,
    {
        HashIndexParts { records }
    }
}

impl<K, I> HashIndexParts<I>
where
    K: HashIndexKey,
    I: ExactSizeIterator<Item = (K, u32)>,
{
    fn len(&self) -> usize {
        self.records.len()
    }

    fn place(self, slots: &mut [Slot<K>], len: usize) -> Result<(), Refusal> {
        place_iter(slots, len, self.records).map_err(|unplaced| match unplaced {
            Unplaced::DuplicateKey { first, second } => Refusal::DuplicateKey { first, second },
            Unplaced::InvalidPosition => Refusal::InvalidPosition,
            Unplaced::LengthMismatch => Refusal::IndexLengthMismatch,
        })
    }
}

impl<I: ExactSizeIterator> fmt::Debug for HashIndexParts<I> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> Result<(), fmt::Error> {
        f.debug_struct("HashIndexParts")
            .field("len", &self.records.len())
            .finish_non_exhaustive()
    }
}

impl<'a, K: Copy> From<&'a [(K, u32)]> for HashIndexParts<core::iter::Copied<core::slice::Iter<'a, (K, u32)>>> {
    fn from(records: &'a [(K, u32)]) -> Self {
        Self { records: records.iter().copied() }
    }
}

impl<'a, K: Copy, const N: usize> From<&'a [(K, u32); N]>
    for HashIndexParts<core::iter::Copied<core::slice::Iter<'a, (K, u32)>>>
{
    fn from(records: &'a [(K, u32); N]) -> Self {
        Self::from(records.as_slice())
    }
}

// SAFETY: release needs only the stored context and the slice of slots.
//
// A populated index owns one block under its context. Returning that block
// and closing the context releases all it owns. An empty index has no block
// or open context.
unsafe impl<K: HashIndexKey> Build for HashIndex<K> {
    type DefaultSource<'b> = core::iter::Copied<core::slice::Iter<'b, (K, u32)>>;

    unsafe fn free<W: Owner>(addr: usize, memory: &MemoryContext<'_, W>) {
        let root = memory.root();
        let slice = addr + HashIndex::<K>::SLOTS_OFFSET;
        // SAFETY: the words were written whole (caller contract).
        let (offset, len) = unsafe {
            (
                root.ptr::<isize>(slice).read(),
                root.ptr::<u64>(slice + ShmSlice::<Slot<K>>::LEN_OFFSET).read(),
            )
        };
        if offset == 0 {
            // An empty index opened nothing.
            return;
        }
        let unlinked = Unlinked {
            context: root.ptr(addr),
            block: root.ptr(target_of(offset, slice)),
            size: len as usize * size_of::<Slot<K>>(),
        };
        // Dropping returns the block of the slots and closes their context.
        drop(unlinked);
    }
}

// SAFETY: construction initializes the context and slice at their final place.
//
// Until the slice owns the slots, a guard returns their block and closes the
// context on refusal or panic. The input iterator is consumed and dropped
// while that guard is live, including its captured values. An empty index
// consumes and drops its input before zeroing the value without a context.
unsafe impl<K: HashIndexKey, I: ExactSizeIterator<Item = (K, u32)>> BuildFrom<I> for HashIndex<K> {
    /// The records, each a key and the position it names, every key at most
    /// once. No records leave the index empty, allocating nothing.
    type Parts<'b, W: Owner> = HashIndexParts<I>;

    unsafe fn write<W: Owner>(
        parts: <Self as BuildFrom<I>>::Parts<'_, W>,
        memory: &MemoryContext<'_, W>,
        addr: usize,
    ) -> Result<(), Refusal> {
        let root = memory.root();
        let records = parts.len();
        if records == 0 {
            parts.place(&mut [], 0)?;
            // SAFETY: the index's bytes lie in a block of the build,
            // exclusive (caller contract), and a zeroed index is the empty
            // one: no context to close and a NULL slice.
            unsafe { root.ptr::<u8>(addr).write_bytes(0, size_of::<Self>()) };
            return Ok(());
        }
        let len = slots_for(records).ok_or(Refusal::BlockTooLarge)?;
        let size = len
            .checked_mul(size_of::<Slot<K>>())
            .filter(|&size| size <= bindings::MEMORY_BLOCK_ALLOCATOR_MAX_SIZE as usize)
            .ok_or(Refusal::BlockTooLarge)?;
        let context = root.ptr::<bindings::memory_context>(addr);
        // SAFETY: the context lies at the start of the index, in a block of
        // the build (caller contract), and the parent is the header's, which
        // the owner set up for C.
        unsafe {
            let parent = root.ptr::<bindings::memory_context>(memory.context.get());
            bindings::yanet_sys_common_memory_context_init_from(context, parent, c"hash_index".as_ptr());
        }
        let mut unlinked = Unlinked {
            context,
            block: ptr::null_mut(),
            size,
        };
        // SAFETY: the context is open.
        unlinked.block = unsafe { bindings::yanet_sys_common_memory_balloc(context, size) };
        if unlinked.block.is_null() {
            return Err(Refusal::OutOfMemory);
        }
        // SAFETY: the fresh block holds the slots, aligned by the C allocator
        // as its size is a multiple of a slot's, and nothing else accesses
        // it. Every slot is written before the slice is made.
        let slots = unsafe {
            let first = root.ptr::<Slot<K>>(unlinked.block.addr());
            for idx in 0..len {
                first.add(idx).write(Slot::FREE);
            }
            core::slice::from_raw_parts_mut(first, len)
        };
        parts.place(slots, records)?;
        let slice = addr + HashIndex::<K>::SLOTS_OFFSET;
        // SAFETY: the offset and count words are aligned and exclusive
        // (caller contract).
        unsafe {
            root.ptr::<isize>(slice).write(offset_to(unlinked.block.addr(), slice));
            root.ptr::<u64>(slice + ShmSlice::<Slot<K>>::LEN_OFFSET)
                .write(len as u64);
        }
        forget(unlinked);
        Ok(())
    }
}

/// The memory context an index opened and the block of its slots, until the
/// index links them: the block is returned and the context closed when
/// dropped, also while unwinding.
///
/// Forget it once the index owns them.
struct Unlinked {
    context: *mut bindings::memory_context,
    /// The block of the slots, NULL before it is allocated.
    block: *mut c_void,
    size: usize,
}

impl Drop for Unlinked {
    fn drop(&mut self) {
        // SAFETY: the context is open under a header and only the index uses
        // it, and the block, unless NULL, which C skips, came from it with
        // this size and nothing references it.
        unsafe {
            bindings::yanet_sys_common_memory_bfree(self.context, self.block, self.size);
            bindings::yanet_sys_common_memory_context_fini(self.context);
        }
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

/// A counter, device or object link registered in build `'b`, by its
/// registry index; a counter of a tagged registry also by the registry's
/// index.
///
/// Created by a [`Scope`] only. It is copied rather than registered again: C
/// finds a registration by name with a linear scan of the registry, so a
/// device registered once is copied into every entry that goes through it.
#[must_use]
pub struct Registered<'b, T> {
    // The tagged registry of a counter registered in one, zero for the rest.
    registry: u64,
    index: u64,
    _brand: Brand<'b>,
    _kind: PhantomData<fn() -> T>,
}

/// The counters of a module's tagged registry that build `'b` registers by
/// name, each of layout `T`, for a [`TaggedCounters`] field, its parts.
///
/// Created by [`Scope::counters_in`] only. Ids run from zero in the order the
/// names first come, a name given again keeping its id, as C numbers a
/// registry; the field takes the registry and the number of counters
/// registered. A counter registered in the registry amid them other than
/// through the registrar would fall below that bound with a size of its own, so
/// the registrar refuses to go on past one.
#[must_use]
pub struct CounterRegistrar<'b, T, W: Owner> {
    memory: &'b MemoryContext<'b, W>,
    header: Header,
    tag: &'b str,
    // The registry the first counter made or found, zero before it.
    registry: u64,
    len: u64,
    _brand: Brand<'b>,
    _layout: PhantomData<fn() -> T>,
}

/// The slot block of a [`PerWorker`] allocated in build `'b`, one slot per
/// worker of the owner, its parts; empty when the owner has no worker.
///
/// Created by a [`Scope`] only. It owns the block until it is written into
/// the parts of a per-worker field, which takes it over; dropped before that,
/// it returns the block.
#[must_use]
pub struct OwnedSlots<'b, T: Copy + ShmLayout, W: Owner> {
    addr: usize,
    workers: u64,
    memory: &'b MemoryContext<'b, W>,
    _brand: Brand<'b>,
    _slot: PhantomData<fn() -> T>,
}

/// The region table of a [`PerWorkerSlice`] allocated in build `'b` with the
/// region of each worker of the owner it names, its parts; empty when the
/// regions take no bytes or the owner has no worker.
///
/// Created by a [`Scope`] only. It owns the blocks until it is written into
/// the parts of a per-worker field, which takes them over; dropped before
/// that, it returns them.
#[must_use]
pub struct OwnedRegions<'b, T: Copy + ShmLayout, W: Owner> {
    table: usize,
    workers: u64,
    len: usize,
    memory: &'b MemoryContext<'b, W>,
    _brand: Brand<'b>,
    _element: PhantomData<fn() -> T>,
}

impl<T: Copy + ShmLayout, W: Owner> fmt::Debug for OwnedRegions<'_, T, W> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> Result<(), fmt::Error> {
        f.debug_struct("OwnedRegions")
            .field("table", &self.table)
            .field("workers", &self.workers)
            .field("len", &self.len)
            .finish()
    }
}

impl<T: Copy + ShmLayout, W: Owner> fmt::Debug for OwnedSlots<'_, T, W> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> Result<(), fmt::Error> {
        f.debug_struct("OwnedSlots")
            .field("addr", &self.addr)
            .field("workers", &self.workers)
            .finish()
    }
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

impl<T, W: Owner> fmt::Debug for CounterRegistrar<'_, T, W> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> Result<(), fmt::Error> {
        f.debug_struct("CounterRegistrar")
            .field("tag", &self.tag)
            .field("registry", &self.registry)
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
        f.debug_struct("Registered")
            .field("registry", &self.registry)
            .field("index", &self.index)
            .finish()
    }
}

impl<W: Owner> fmt::Debug for MemoryContext<'_, W> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> Result<(), fmt::Error> {
        f.debug_struct("MemoryContext").field("context", &self.context).finish()
    }
}

/// Self-relative offset from a word to its target, 0 for none.
fn offset_to(target: usize, slot: usize) -> isize {
    if target == 0 {
        0
    } else {
        target.wrapping_sub(slot) as isize
    }
}

/// The target address a self-relative offset leads to from its word; the
/// inverse of the offset computation, 0 for none.
fn target_of(offset: isize, slot: usize) -> usize {
    if offset == 0 {
        0
    } else {
        slot.wrapping_add_signed(offset)
    }
}

/// Returns what a run of consecutive elements owns.
///
/// # Safety
///
/// As for [`Build::free`], for each element of the run.
unsafe fn free_elements<T: Build, W: Owner>(addr: usize, len: usize, memory: &MemoryContext<'_, W>) {
    // Newest first: C prepends child memory contexts and unlinks from the
    // head, so this order is linear.
    for idx in (0..len).rev() {
        // SAFETY: element `idx` of the block (caller contract).
        unsafe { T::free(addr + idx * size_of::<T>(), memory) };
    }
}

/// Returns a block of consecutive elements and what they own.
///
/// # Safety
///
/// The block was allocated from the memory context with exactly the bytes of
/// its elements, is live and unreferenced, and its elements were written
/// whole.
unsafe fn free_block<T: Build, W: Owner>(addr: usize, len: usize, memory: &MemoryContext<'_, W>) {
    // SAFETY: the elements were written whole (caller contract).
    unsafe { free_elements::<T, W>(addr, len, memory) };
    // SAFETY: the block is the one allocated, and its edges are returned.
    unsafe { memory.free(addr, len * size_of::<T>()) };
}

/// Bytes of the slot block of a per-worker field of the given workers, which
/// its build allocated, so the size does not overflow.
fn slot_block<T: Copy + ShmLayout>(workers: u64) -> usize {
    PerWorker::<T>::block_size(workers as usize).unwrap_or(usize::MAX)
}

/// Returns the region table of a per-worker slice field and every region it
/// names.
///
/// # Safety
///
/// The table was allocated from the memory context with a word per worker,
/// each zero or the offset, from the word, of a live region block allocated
/// with the size of the length; nothing references the blocks.
unsafe fn free_regions<T: Copy + ShmLayout, W: Owner>(
    table: usize,
    workers: u64,
    len: usize,
    memory: &MemoryContext<'_, W>,
) {
    let root = memory.root();
    let region = PerWorkerSlice::<T>::region_block(len).unwrap_or(usize::MAX);
    let (count, table_size) = region_table(workers).unwrap_or((0, usize::MAX));
    for worker in (0..count).rev() {
        let entry = table + worker * size_of::<isize>();
        // SAFETY: the table's word of the worker (caller contract).
        let offset = unsafe { root.ptr::<isize>(entry).read() };
        if offset != 0 {
            // SAFETY: the region block the word leads to (caller contract).
            unsafe { memory.free(target_of(offset, entry), region) };
        }
    }
    // SAFETY: the table is the one allocated, and the regions it named are
    // returned.
    unsafe { memory.free(table, table_size) };
}

/// The number of words of the region table of a per-worker slice field of
/// the given workers, one per worker, and its bytes; `None` when either
/// overflows.
fn region_table(workers: u64) -> Option<(usize, usize)> {
    let count = usize::try_from(workers).ok()?;
    Some((count, count.checked_mul(size_of::<isize>())?))
}

fn refused<W: Owner, E: From<BuildError<W::Error>>>(refusal: Refusal) -> E {
    BuildError::<W::Error>::Refused(refusal).into()
}

fn failed<W: Owner, E: From<BuildError<W::Error>>>(err: W::Error) -> E {
    BuildError::Owner(err).into()
}

/// A run of elements of which a prefix is written, with the block they lie in,
/// if any; returns what they own and the block when dropped, also while
/// unwinding.
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
    /// Guards the written prefix of a run that lies in a larger value.
    ///
    /// # Safety
    ///
    /// The prefix was written whole, and it is owned by nothing else; the
    /// memory context is the one the blocks it owns came from.
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
        // SAFETY: the written prefix was written whole, and the block, if any,
        // was allocated with its recorded size and is ours alone.
        unsafe {
            free_elements::<T, W>(self.addr, self.written, self.memory);
            if self.block != 0 {
                self.memory.free(self.addr, self.block);
            }
        }
    }
}

/// Construction services of one build: allocates blocks, registers counters
/// and devices and links objects.
///
/// A copyable handle shaped like a `&std::thread::Scope`. The build lifetime
/// is fresh for every [`Builder::build`] and invariant, so nothing made by one
/// build is accepted by another or outlives its own; the environment lifetime
/// is what the build's closure borrows from outside it, which the parts of a
/// value may borrow for the build, such as the ranges of a table; the error
/// type is that of the author's closure, which the methods return in place of
/// the SDK's own.
pub struct Scope<'b, 'env: 'b, W: Owner, E = BuildError<<W as Owner>::Error>, K: Kind = ModuleKind> {
    memory: &'b MemoryContext<'b, W>,
    header: Header,
    _brand: Brand<'b>,
    _env: PhantomData<&'env mut &'env ()>,
    _error: PhantomData<fn() -> E>,
    _kind: PhantomData<fn() -> K>,
}

impl<W: Owner, E, K: Kind> Clone for Scope<'_, '_, W, E, K> {
    fn clone(&self) -> Self {
        *self
    }
}

impl<W: Owner, E, K: Kind> Copy for Scope<'_, '_, W, E, K> {}

impl<W: Owner, E, K: Kind> fmt::Debug for Scope<'_, '_, W, E, K> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> Result<(), fmt::Error> {
        f.debug_struct("Scope").field("header", &self.header).finish()
    }
}

impl<'b, W: Owner, E: From<BuildError<W::Error>>, K: Kind> Scope<'b, '_, W, E, K> {
    /// Allocates a block holding one value, written from its parts.
    ///
    /// A zero-sized type does not compile: C has no block of no bytes.
    ///
    /// # Errors
    ///
    /// The memory is exhausted, or the write of the value is refused; the
    /// blocks in the parts and the block of the value are returned.
    pub fn boxed<T: Build + BuildFrom<S>, S>(
        self,
        parts: <T as BuildFrom<S>>::Parts<'b, W>,
    ) -> Result<Owned<'b, T, W>, E> {
        const { assert!(size_of::<T>() != 0, "a block of a zero-sized type") };
        let addr = self.alloc(size_of::<T>())?;
        let partial = Partial::<T, W> {
            memory: self.memory,
            addr,
            written: 0,
            block: size_of::<T>(),
            _element: PhantomData,
        };
        // SAFETY: the fresh block holds one value, aligned by the allocation,
        // and every block and registration in the parts belongs to this build
        // by the brand.
        unsafe { <T as BuildFrom<S>>::write::<W>(parts, self.memory, addr) }.map_err(refused::<W, E>)?;
        forget(partial);
        Ok(Owned {
            addr,
            memory: self.memory,
            _brand: PhantomData,
            _value: PhantomData,
        })
    }

    /// Allocates one block of the given number of elements, each written in
    /// place.
    ///
    /// The callback is called once per index, in order, and returns the parts
    /// of that element. An empty slice allocates nothing. A zero-sized type
    /// does not compile.
    ///
    /// # Errors
    ///
    /// The size overflows, the memory is exhausted, the callback fails, which
    /// is returned as it is, or the write of an element is refused. The
    /// elements written so far and the block are returned.
    pub fn slice<T: Build + BuildFrom<S>, S>(
        self,
        len: usize,
        mut fill: impl FnMut(usize) -> Result<<T as BuildFrom<S>>::Parts<'b, W>, E>,
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
            // SAFETY: the element lies in the fresh block, aligned by the
            // allocation, and every block and registration in the parts
            // belongs to this build by the brand.
            unsafe { <T as BuildFrom<S>>::write::<W>(parts, self.memory, addr + idx * size_of::<T>()) }
                .map_err(refused::<W, E>)?;
            partial.written += 1;
        }
        forget(partial);
        Ok(self.owned_slice(addr, len))
    }

    /// Allocates the slots of a per-worker field, one per worker, each
    /// holding the initial value.
    ///
    /// The slots live as long as the configuration that holds them. An owner
    /// without workers allocates nothing.
    ///
    /// # Errors
    ///
    /// The block of the slots is larger than the allocator's largest, or the
    /// memory is exhausted.
    pub fn per_worker<T: Copy + ShmLayout>(self, init: T) -> Result<OwnedSlots<'b, T, W>, E> {
        let workers = self.memory.owner.worker_count();
        if workers == 0 {
            return Ok(self.owned_slots(0, 0));
        }
        let size = usize::try_from(workers)
            .ok()
            .and_then(PerWorker::<T>::block_size)
            .ok_or_else(|| refused::<W, E>(Refusal::BlockTooLarge))?;
        let addr = self.alloc(size)?;
        let root = self.memory.root();
        let first = PerWorker::<T>::first_slot(addr);
        for idx in 0..workers as usize {
            // SAFETY: the fresh block is at least 64 bytes, so aligned to 64
            // by the allocation, and the first slot lies at most 64 bytes past
            // its start, on a 128-byte line, which the size leaves room for;
            // each slot is a multiple of the stride further, of 128, and a
            // layout type is aligned to at most 64.
            unsafe { root.ptr::<T>(first + idx * PerWorker::<T>::STRIDE).write(init) };
        }
        Ok(self.owned_slots(addr, workers))
    }

    /// Allocates the regions of a per-worker slice field, one block per
    /// worker, each holding `len` copies of the initial value, and the table
    /// that names them.
    ///
    /// The regions live as long as the configuration that holds them. Regions
    /// of no bytes (no values, or values of a zero-sized type), or an owner
    /// without workers, allocate nothing, and the field holds no table and a
    /// length of zero, so its handles are empty.
    ///
    /// # Errors
    ///
    /// A region or the table is larger than the allocator's largest block,
    /// refused before anything is allocated, or the memory is exhausted; the
    /// blocks allocated so far are returned.
    pub fn per_worker_slice<T: Copy + ShmLayout>(self, len: usize, init: T) -> Result<OwnedRegions<'b, T, W>, E> {
        let workers = self.memory.owner.worker_count();
        let max = bindings::MEMORY_BLOCK_ALLOCATOR_MAX_SIZE as usize;
        let region = PerWorkerSlice::<T>::region_block(len)
            .filter(|&size| size <= max)
            .ok_or_else(|| refused::<W, E>(Refusal::BlockTooLarge))?;
        if region == 0 || workers == 0 {
            return Ok(self.owned_regions(0, workers, 0));
        }
        let (count, table_size) = region_table(workers)
            .filter(|&(_, size)| size <= max)
            .ok_or_else(|| refused::<W, E>(Refusal::BlockTooLarge))?;
        let table = self.alloc(table_size)?;
        let root = self.memory.root();
        for worker in 0..count {
            // SAFETY: the fresh table holds a word per worker, aligned to 8
            // by the allocation.
            unsafe { root.ptr::<isize>(table + worker * size_of::<isize>()).write(0) };
        }
        // Every word is zero or a region, so dropping it returns what the
        // loop allocated.
        let regions = self.owned_regions(table, workers, len);
        for worker in 0..count {
            let block = self.alloc(region)?;
            let entry = table + worker * size_of::<isize>();
            let first = PerWorkerSlice::<T>::first_element(block);
            // SAFETY: the word is the table's, as above. The fresh block is
            // at least 64 bytes, so aligned to 64 by the allocation, and the
            // first value lies at most 64 bytes past its start, on a 128-byte
            // line, which the size leaves room for after the values; a layout
            // type is aligned to at most 64.
            unsafe {
                root.ptr::<isize>(entry).write(offset_to(block, entry));
                for idx in 0..len {
                    root.ptr::<T>(first + idx * size_of::<T>()).write(init);
                }
            }
        }
        Ok(regions)
    }

    /// Allocates a block of the given payload size.
    ///
    /// The size is a multiple of the alignment of the type it holds, so the
    /// allocator's alignment, the smaller of 64 and the size rounded up to a
    /// power of two, is at least that of the type, which the maximum supported
    /// alignment bounds.
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

    fn owned_regions<T: Copy + ShmLayout>(self, table: usize, workers: u64, len: usize) -> OwnedRegions<'b, T, W> {
        OwnedRegions {
            table,
            workers,
            len,
            memory: self.memory,
            _brand: PhantomData,
            _element: PhantomData,
        }
    }

    fn owned_slots<T: Copy + ShmLayout>(self, addr: usize, workers: u64) -> OwnedSlots<'b, T, W> {
        OwnedSlots {
            addr,
            workers,
            memory: self.memory,
            _brand: PhantomData,
            _slot: PhantomData,
        }
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

impl<'b, W: Owner, E: From<BuildError<W::Error>>> Scope<'b, '_, W, E, ModuleKind> {
    /// Registers a counter of the given name with the module, of as many
    /// values as its layout holds; the layout usually comes from the field the
    /// counter is built into.
    ///
    /// # Errors
    ///
    /// The owner refuses the registration, as C does for a name registered
    /// before with another size.
    pub fn counter<T: CounterLayout>(self, name: &str) -> Result<Registered<'b, Counter<T>>, E> {
        let size = T::VALUES as u64;
        // SAFETY: the header is initialised and not yet published.
        let id = unsafe { self.memory.owner.register_counter(self.header, name, size) }.map_err(failed::<W, E>)?;
        Ok(Registered::new(id))
    }

    /// Registers a counter of the given name with the module's tagged registry
    /// of the given tag, of as many values as its layout holds.
    ///
    /// The first registration under a tag makes its registry, as C's
    /// `cp_module_counter_registry` does. Readers find the counters of the
    /// registry by the tag, apart from the module's own counters, and C carries
    /// their values by tag and name over to a configuration that replaces this
    /// one.
    ///
    /// # Errors
    ///
    /// The tag is empty, holds a NUL or does not fit a C counter tag value with
    /// the terminator, or the owner refuses the registration, as C does for a
    /// name registered in the registry before with another size.
    #[doc(alias = "cp_module_counter_registry")]
    pub fn counter_in<T>(self, tag: &str, name: &str) -> Result<Registered<'b, TaggedCounter<T>>, E>
    where
        T: CounterLayout,
    {
        if names::validate_name(tag, bindings::COUNTER_TAG_VALUE_LEN as usize).is_err() {
            return Err(refused::<W, E>(Refusal::InvalidName));
        }
        let size = T::VALUES as u64;
        // SAFETY: the header is initialised and not yet published.
        let (registry, id) =
            unsafe { self.memory.owner.register_counter_in(self.header, tag, name, size) }.map_err(failed::<W, E>)?;
        Ok(Registered::tagged(registry, id))
    }

    /// Starts registering counters of the module's tagged registry of the
    /// given tag, each of as many values as the layout holds, through the
    /// registrar it returns, which is the parts of a [`TaggedCounters`] field.
    ///
    /// The registry is made by the first counter registered, as C's
    /// `cp_module_counter_registry` makes it; a registrar that registers none
    /// leaves the module without it and the field without counters.
    ///
    /// # Errors
    ///
    /// The tag is empty, holds a NUL or does not fit a C counter tag value with
    /// the terminator.
    #[doc(alias = "cp_module_counter_registry")]
    pub fn counters_in<T>(self, tag: &'b str) -> Result<CounterRegistrar<'b, T, W>, E>
    where
        T: CounterLayout,
    {
        if names::validate_name(tag, bindings::COUNTER_TAG_VALUE_LEN as usize).is_err() {
            return Err(refused::<W, E>(Refusal::InvalidName));
        }
        Ok(CounterRegistrar {
            memory: self.memory,
            header: self.header,
            tag,
            registry: 0,
            len: 0,
            _brand: PhantomData,
            _layout: PhantomData,
        })
    }

    /// Links the device of the given name to the module.
    ///
    /// # Errors
    ///
    /// The owner refuses the device.
    pub fn device(self, name: &str) -> Result<Registered<'b, Device>, E> {
        // SAFETY: the header is initialised and not yet published.
        let index = unsafe { self.memory.owner.link_device(self.header, name) }.map_err(failed::<W, E>)?;
        Ok(Registered::new(index))
    }

    /// Links the object of type `O` and the given name to the module, as C's
    /// `cp_module_link_object` does: by type and name, linking a pair once.
    ///
    /// The object need not exist yet: C resolves the link when it installs the
    /// configuration and refuses a configuration whose object the generation
    /// does not hold.
    ///
    /// # Errors
    ///
    /// The type or the name is empty, holds a NUL or does not fit its C field
    /// with the terminator, or the owner refuses the link.
    #[doc(alias = "cp_module_link_object")]
    pub fn object<O: Object>(self, name: &str) -> Result<Registered<'b, ObjectLink<O>>, E> {
        if names::validate_name(O::TYPE, bindings::CP_OBJECT_TYPE_LEN as usize).is_err()
            || names::validate_name(name, bindings::CP_OBJECT_NAME_LEN as usize).is_err()
        {
            return Err(refused::<W, E>(Refusal::InvalidName));
        }
        // SAFETY: the header is initialised and not yet published.
        let index = unsafe { self.memory.owner.link_object(self.header, O::TYPE, name) }.map_err(failed::<W, E>)?;
        Ok(Registered::new(index))
    }
}

impl<'b, W, E> Scope<'b, '_, W, E, ObjectKind>
where
    W: Owner,
    E: From<BuildError<W::Error>>,
{
    /// Registers a counter of the given name with the object's own registry,
    /// of as many values as its layout holds; the layout usually comes from
    /// the field the counter is built into.
    ///
    /// Readers find the object's counters by its type and name, and C carries
    /// their values by name over to an object of the same type and name that
    /// replaces this one. The object's prepare step resolves the counter for
    /// the modules linking the object.
    ///
    /// # Errors
    ///
    /// The owner refuses the registration, as C does for a name registered
    /// before with another size.
    pub fn counter<T>(self, name: &str) -> Result<Registered<'b, ObjectCounter<T>>, E>
    where
        T: CounterLayout,
    {
        let size = T::VALUES as u64;
        // SAFETY: the header is an initialised object header, not yet
        // published.
        let id =
            unsafe { self.memory.owner.register_object_counter(self.header, name, size) }.map_err(failed::<W, E>)?;
        Ok(Registered::new(id))
    }
}

// A tag fits its registry's C field when it fits a counter tag value, the
// field readers find the registry by.
const _: () = assert!(bindings::COUNTER_TAG_VALUE_LEN <= bindings::COUNTER_NAME_LEN);

impl<T: Build, W: Owner> Owned<'_, T, W> {
    /// Hands the block over to the field it is written into: its address,
    /// without returning it.
    fn into_raw(self) -> usize {
        ManuallyDrop::new(self).addr
    }
}

impl<T: Build, W: Owner> Drop for Owned<'_, T, W> {
    fn drop(&mut self) {
        // SAFETY: the block holds one value written whole and was allocated
        // from the memory context with its exact size; nothing but this value
        // owns it.
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
            // SAFETY: the block holds its elements written whole and was
            // allocated from the memory context with its exact size; nothing
            // but this value owns it.
            unsafe { free_block::<T, W>(self.addr, self.len, self.memory) }
        }
    }
}

impl<T: Copy + ShmLayout, W: Owner> OwnedSlots<'_, T, W> {
    /// Hands the block over to the field it is written into: its address and
    /// slot count, without returning it.
    fn into_raw(self) -> (usize, u64) {
        let this = ManuallyDrop::new(self);
        (this.addr, this.workers)
    }
}

impl<T: Copy + ShmLayout, W: Owner> Drop for OwnedSlots<'_, T, W> {
    fn drop(&mut self) {
        if self.addr != 0 {
            // SAFETY: the block was allocated from the memory context with
            // the size of its slot count, and its slots own nothing; nothing
            // but this value owns it.
            unsafe { self.memory.free(self.addr, slot_block::<T>(self.workers)) }
        }
    }
}

impl<T: Copy + ShmLayout, W: Owner> OwnedRegions<'_, T, W> {
    /// Hands the blocks over to the field they are written into: the table's
    /// address, the worker count and the length, without returning them.
    fn into_raw(self) -> (usize, u64, usize) {
        let this = ManuallyDrop::new(self);
        (this.table, this.workers, this.len)
    }
}

impl<T: Copy + ShmLayout, W: Owner> Drop for OwnedRegions<'_, T, W> {
    fn drop(&mut self) {
        if self.table != 0 {
            // SAFETY: the table was allocated from the memory context with a
            // word per worker, each zero or a region allocated with the size
            // of the length; nothing but this value owns them.
            unsafe { free_regions::<T, W>(self.table, self.workers, self.len, self.memory) }
        }
    }
}

impl<T, W> CounterRegistrar<'_, T, W>
where
    T: CounterLayout,
    W: Owner,
{
    /// Registers a counter of the given name in the registry, of as many
    /// values as the layout holds, and returns its id: the next one for a new
    /// name, the one it took for a name given before.
    ///
    /// # Errors
    ///
    /// The owner refuses the registration, as C does for a name registered in
    /// the registry before with another size, or the registry gives the counter
    /// an id past the next one, having taken a counter amid the registrar's
    /// other than through it.
    pub fn counter(&mut self, name: &str) -> Result<u64, BuildError<W::Error>> {
        let size = T::VALUES as u64;
        // SAFETY: the header is initialised and not yet published: the
        // registrar is of the build, which publishes nothing before it ends.
        let (registry, id) = unsafe { self.memory.owner.register_counter_in(self.header, self.tag, name, size) }
            .map_err(BuildError::Owner)?;
        if id > self.len {
            return Err(BuildError::Refused(Refusal::ForeignCounter));
        }
        if id == self.len {
            self.len += 1;
        }
        self.registry = registry;
        Ok(id)
    }
}

impl<T> Registered<'_, T> {
    fn new(index: u64) -> Self {
        Self::tagged(0, index)
    }

    fn tagged(registry: u64, index: u64) -> Self {
        Self {
            registry,
            index,
            _brand: PhantomData,
            _kind: PhantomData,
        }
    }
}

/// Entry point of the construction of configurations of a module; never
/// constructed.
pub struct Builder<M>(PhantomData<fn() -> M>);

impl<M: Module> Builder<M> {
    /// Builds a configuration of the given name; the caller owns it and
    /// publishes it.
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
    /// An invalid type or name is refused before allocating the root block.
    /// Otherwise, the owner's memory is exhausted, its header routine refuses
    /// the request, the closure fails, or the write of the body is refused.
    pub fn build<'env, E, W, S>(
        owner: &W,
        name: &str,
        f: impl for<'b> FnOnce(Scope<'b, 'env, W, E>) -> Result<<M::Config as BuildFrom<S>>::Parts<'b, W>, E>,
    ) -> Result<Built<M, W>, E>
    where
        M::Config: BuildFrom<S>,
        W: Owner,
        E: From<BuildError<W::Error>>,
    {
        const {
            assert!(
                size_of::<ModuleConfig<M::Config>>() <= bindings::MEMORY_BLOCK_ALLOCATOR_MAX_SIZE as usize,
                "a configuration larger than the largest block of the allocator"
            )
        };
        if names::validate_name(M::NAME, bindings::CP_MODULE_TYPE_LEN as usize).is_err()
            || names::validate_name(name, bindings::CP_MODULE_NAME_LEN as usize).is_err()
        {
            return Err(refused::<W, E>(Refusal::InvalidName));
        }
        let header = build_root::<ModuleKind, M::Config, S, W, E>(
            owner,
            M::NAME,
            name,
            size_of::<ModuleConfig<M::Config>>(),
            ModuleConfig::<M::Config>::BODY_OFFSET,
            f,
        )?;
        Ok(Built { header, _kind: PhantomData })
    }
}

/// Entry point of the construction of objects of a type; never constructed.
pub struct ObjectBuilder<O>(PhantomData<fn() -> O>);

impl<O: Object> ObjectBuilder<O> {
    /// Builds an object of the given name; the caller owns it and publishes
    /// it.
    ///
    /// As [`Builder::build`], over an object scope, which registers counters in
    /// the object's own registry and links no device or object, as the
    /// object's header carries no module registries.
    ///
    /// # Errors
    ///
    /// An invalid type or name is refused before allocating the root block.
    /// Otherwise, the owner's memory is exhausted, its header routine refuses
    /// the request, the closure fails, or the write of the body is refused.
    pub fn build<'env, E, W, S>(
        owner: &W,
        name: &str,
        f: impl for<'b> FnOnce(Scope<'b, 'env, W, E, ObjectKind>) -> Result<<O::Config as BuildFrom<S>>::Parts<'b, W>, E>,
    ) -> Result<BuiltObject<O, W>, E>
    where
        O::Config: BuildFrom<S>,
        W: Owner,
        E: From<BuildError<W::Error>>,
    {
        const {
            assert!(
                size_of::<ObjectConfig<O::Config>>() <= bindings::MEMORY_BLOCK_ALLOCATOR_MAX_SIZE as usize,
                "an object larger than the largest block of the allocator"
            )
        };
        if names::validate_name(O::TYPE, bindings::CP_OBJECT_TYPE_LEN as usize).is_err()
            || names::validate_name(name, bindings::CP_OBJECT_NAME_LEN as usize).is_err()
        {
            return Err(refused::<W, E>(Refusal::InvalidName));
        }
        let header = build_root::<ObjectKind, O::Config, S, W, E>(
            owner,
            O::TYPE,
            name,
            size_of::<ObjectConfig<O::Config>>(),
            ObjectConfig::<O::Config>::BODY_OFFSET,
            f,
        )?;
        Ok(BuiltObject { header, _kind: PhantomData })
    }
}

/// Builds the root block of a module configuration or an object: the header
/// of the kind at its start, then the body written from the closure's parts at
/// the body offset; returns the header, or releases everything on failure.
fn build_root<'env, K: Kind, C: Build + BuildFrom<S>, S, W: Owner, E: From<BuildError<W::Error>>>(
    owner: &W,
    ty: &str,
    name: &str,
    size: usize,
    body_offset: usize,
    f: impl for<'b> FnOnce(Scope<'b, 'env, W, E, K>) -> Result<<C as BuildFrom<S>>::Parts<'b, W>, E>,
) -> Result<Header, E> {
    let block = owner.alloc(size).ok_or_else(|| refused::<W, E>(Refusal::OutOfMemory))?;
    let header = Header::at(block);
    // SAFETY: the block is ours and the header sits at its start; the block
    // holds the header and the body, so it is large enough for the header.
    if let Err(err) = unsafe { K::init(owner, header, ty, name) } {
        // SAFETY: the block came from the owner's allocation with this size,
        // and a refused header leaves nothing to finalise.
        unsafe { owner.free(block, size) };
        return Err(failed::<W, E>(err));
    }
    // The blocks of the closure's values are returned, or leaked, before this
    // runs.
    let release = Release::<W, K> {
        owner,
        header,
        size,
        _kind: PhantomData,
    };
    let memory = MemoryContext::new(owner, header);
    let parts = f(Scope {
        memory: &memory,
        header,
        _brand: PhantomData,
        _env: PhantomData,
        _error: PhantomData,
        _kind: PhantomData,
    })?;
    // SAFETY: the body lies in the root block, aligned, and the blocks and
    // registrations in the parts are this build's by the brand.
    unsafe { <C as BuildFrom<S>>::write::<W>(parts, &memory, header.get() + body_offset) }.map_err(refused::<W, E>)?;
    forget(release);
    Ok(header)
}

/// A configuration of a module that [`Builder::build`] handed over in the
/// memory of an owner type: its header address, to publish, and the right to
/// release it.
///
/// Not freed on drop, as the configuration is shared once published; release
/// it with [`Built::destroy`], which takes an owner of that type only.
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
    /// The configuration whose header is at `header`, for a C caller that
    /// kept only the header's address.
    ///
    /// # Safety
    ///
    /// `header` is the header of a configuration of module `M` that a
    /// [`Builder::build`] in the memory of an owner of type `W` handed over,
    /// not released since, and no other `Built` of it is used.
    pub unsafe fn from_header(header: Header) -> Self {
        Self { header, _kind: PhantomData }
    }

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
    /// The owner is the one that built it, and nothing references the
    /// configuration any longer.
    pub unsafe fn destroy(self, owner: &W) {
        let memory = MemoryContext::new(owner, self.header);
        let body = self.header.get() + ModuleConfig::<M::Config>::BODY_OFFSET;
        // SAFETY: the body was written whole by the build, and every block it
        // owns came from the header's context (caller contract).
        unsafe { M::Config::free::<W>(body, &memory) };
        // SAFETY: every block the body owns is returned, and a block leaked
        // during the build is never referenced.
        unsafe { release_root::<W, ModuleKind>(owner, self.header, size_of::<ModuleConfig<M::Config>>()) }
    }
}

/// An object that [`ObjectBuilder::build`] handed over in the memory of an
/// owner type: its header address, to publish, and the right to release it.
///
/// Not freed on drop, as the object is shared once published; release it with
/// [`BuiltObject::destroy`], which takes an owner of that type only.
#[must_use]
pub struct BuiltObject<O, W> {
    header: Header,
    _kind: PhantomData<fn() -> (O, W)>,
}

impl<O, W> fmt::Debug for BuiltObject<O, W> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> Result<(), fmt::Error> {
        f.debug_struct("BuiltObject").field("header", &self.header).finish()
    }
}

impl<O, W> BuiltObject<O, W> {
    /// The object whose header is at `header`, for a C caller that kept only
    /// the header's address.
    ///
    /// # Safety
    ///
    /// `header` is the header of an object of type `O` that an
    /// [`ObjectBuilder::build`] in the memory of an owner of type `W` handed
    /// over, not released since, and no other `BuiltObject` of it is used.
    pub unsafe fn from_header(header: Header) -> Self {
        Self { header, _kind: PhantomData }
    }

    /// Address of the object header, which is what the control plane
    /// publishes.
    pub fn header(&self) -> Header {
        self.header
    }
}

impl<O: Object, W: Owner> BuiltObject<O, W> {
    /// The object's body, published or not, read as the dataplane reads it:
    /// its state changes only through the types that allow a shared change,
    /// as atomics and mutexes.
    ///
    /// # Safety
    ///
    /// The owner's memory is the one the object was built in.
    pub unsafe fn config<'a>(&'a self, owner: &'a W) -> &'a O::Config {
        let body = self.header.get() + ObjectConfig::<O::Config>::BODY_OFFSET;
        // SAFETY: the body was written whole by the build and is not released
        // while `self` lives, as release takes it; it lies in the owner's
        // memory (caller contract).
        unsafe { &*owner.root().ptr::<O::Config>(body) }
    }

    /// Releases the object with everything it owns.
    ///
    /// # Safety
    ///
    /// The owner is the one that built it, and nothing references the object
    /// any longer.
    pub unsafe fn destroy(self, owner: &W) {
        let memory = MemoryContext::new(owner, self.header);
        let body = self.header.get() + ObjectConfig::<O::Config>::BODY_OFFSET;
        // SAFETY: the body was written whole by the build, and every block it
        // owns came from the header's context (caller contract).
        unsafe { O::Config::free::<W>(body, &memory) };
        // SAFETY: every block the body owns is returned, and a block leaked
        // during the build is never referenced.
        unsafe { release_root::<W, ObjectKind>(owner, self.header, size_of::<ObjectConfig<O::Config>>()) }
    }
}

/// Releases the header and root block of an unfinished build when dropped,
/// also while unwinding.
struct Release<'a, W: Owner, K: Kind> {
    owner: &'a W,
    header: Header,
    size: usize,
    _kind: PhantomData<fn() -> K>,
}

impl<W: Owner, K: Kind> Drop for Release<'_, W, K> {
    fn drop(&mut self) {
        // SAFETY: the blocks of the build's context are returned by the drops
        // of the values that owned them, or leaked if forgotten, which C's
        // finalisation allows; the root block is ours.
        unsafe { release_root::<W, K>(self.owner, self.header, self.size) }
    }
}

/// Finalises a header and returns its root block.
///
/// # Safety
///
/// The header is one of the kind, initialised by the owner at the start of a
/// root block allocated with the given size, every block of its context is
/// returned or never referenced again, and nothing references the header.
unsafe fn release_root<W: Owner, K: Kind>(owner: &W, header: Header, size: usize) {
    // SAFETY: guaranteed by the caller; the header is the start of the block.
    unsafe {
        K::fini(owner, header);
        owner.free(BlockAddr::new(header.get()), size);
    }
}

// The root block holds the header and the body, so the header's alignment is
// the one it needs from the allocator.
const _: () = assert!(align_of::<ModuleConfig<u64>>() <= MAX_ALIGN);
const _: () = assert!(align_of::<ObjectConfig<u64>>() <= MAX_ALIGN);
