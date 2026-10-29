//! Closed construction of module configurations at their final addresses.
//!
//! Encoders recursively consume input records and write directly into storage.
//! The writer owns every allocation until its completed owning edge is stored.
//! Cleanup guards cover completed fields and prefixes through errors and
//! unwinding; release follows only complete owning edges, with payload-only
//! allocations and no auxiliary graph.

use core::{
    fmt,
    marker::PhantomData,
    mem::{align_of, forget, size_of},
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

/// Releases the owning edges of a complete shared-memory value.
///
/// # Safety
///
/// Release must return each owned descendant block exactly once, using its
/// original context and payload size, without panicking. It may read only the
/// complete value and its descendants. Writes are limited to allocator state
/// and C context-tree links under the supplied header; unlinking a context may
/// update a sibling in another value. The value's own storage remains owned by
/// its parent. Derive this contract through the SDK storage derive.
pub unsafe trait Stored: ShmLayout {
    /// Releases descendants while retaining this value's storage.
    ///
    /// # Safety
    ///
    /// The address must contain a complete value whose descendants came from
    /// this context. All descendants must remain live and unreferenced.
    #[doc(hidden)]
    unsafe fn free<W: Owner>(addr: usize, memory: &MemoryContext<'_, W>);
}

/// Trusted encoding of one input record into its matching storage type.
///
/// # Safety
///
/// Success must initialise the entire destination with a valid storage graph
/// owned by this build. Errors and unwinding must release all resources
/// acquired by this call, leaving no owning edges at the destination. Only
/// destination bytes, freshly acquired descendants and the header's C state may
/// be written. No storage address or reference may escape. Implementations must
/// not move a completed relative-pointer value. Module and API crates derive
/// this contract and can forbid unsafe code; a manual implementation requires
/// unsafe code.
/// A safe implementation cannot bypass the construction contract.
///
/// ```compile_fail,E0200
/// use yanet_sys::cp::{BuildError, Encode, Owner, Writer};
/// struct Forged;
/// impl<E> Encode<E> for Forged {
///     type Output = u64;
///     unsafe fn encode<W>(self, _: &mut Writer<'_, W>, _: *mut u64) -> Result<(), E>
///     where W: Owner, E: From<BuildError<W::Error>> { Ok(()) }
/// }
/// ```
#[doc(hidden)]
pub unsafe trait Encode<E>: Sized {
    type Output: Stored;

    /// Writes the input directly into its final destination.
    ///
    /// # Safety
    ///
    /// The destination must be exclusive, aligned and large enough for the
    /// output, inside this build's live storage. It must own no resources yet.
    unsafe fn encode<W>(self, writer: &mut Writer<'_, W>, destination: *mut Self::Output) -> Result<(), E>
    where
        W: Owner,
        E: From<BuildError<W::Error>>;
}

/// Header context used only by trusted storage release implementations.
#[doc(hidden)]
pub struct MemoryContext<'o, W: Owner> {
    owner: &'o W,
    context: Context,
    _local: PhantomData<*const ()>,
}

impl<W: Owner> Copy for MemoryContext<'_, W> {}
impl<W: Owner> Clone for MemoryContext<'_, W> {
    fn clone(&self) -> Self {
        *self
    }
}

impl<'o, W: Owner> MemoryContext<'o, W> {
    fn new(owner: &'o W, header: Header) -> Self {
        Self {
            owner,
            context: header.context(),
            _local: PhantomData,
        }
    }

    pub fn root(&self) -> ShmRoot {
        self.owner.root()
    }

    /// Returns an unreferenced block with its original payload size.
    ///
    /// # Safety
    ///
    /// The block must have been allocated from this context with the same
    /// payload size, and no references to it may remain.
    pub unsafe fn free(&self, addr: usize, size: usize) {
        // SAFETY: the context is initialised and owns this block (caller
        // contract).
        unsafe { self.owner.free_in(self.context, BlockAddr::new(addr), size) }
    }
}

macro_rules! leaves {
    ($($ty:ty),* $(,)?) => {$(
        // SAFETY: these values own no descendant storage.
        unsafe impl Stored for $ty {
            unsafe fn free<W: Owner>(_: usize, _: &MemoryContext<'_, W>) {}
        }
    )*};
}
leaves!(
    u8,
    u16,
    u32,
    u64,
    i8,
    i16,
    i32,
    i64,
    (),
    Counter,
    Device,
    AtomicU8,
    AtomicU16,
    AtomicU32,
    AtomicU64,
    AtomicI8,
    AtomicI16,
    AtomicI32,
    AtomicI64
);

macro_rules! plain {
    ($($ty:ty),* $(,)?) => {$(
        // SAFETY: writing the complete scalar cannot acquire resources.
        unsafe impl<E> Encode<E> for $ty {
            type Output = Self;
            unsafe fn encode<W: Owner>(self, _: &mut Writer<'_, W>, destination: *mut Self)
                -> Result<(), E> where E: From<BuildError<W::Error>> {
                // SAFETY: the caller supplies aligned, exclusive storage.
                unsafe { destination.write(self) };
                Ok(())
            }
        }
    )*};
}
plain!(
    u8,
    u16,
    u32,
    u64,
    i8,
    i16,
    i32,
    i64,
    (),
    AtomicU8,
    AtomicU16,
    AtomicU32,
    AtomicU64,
    AtomicI8,
    AtomicI16,
    AtomicI32,
    AtomicI64
);

// SAFETY: every element is complete and exclusively owns its descendants.
unsafe impl<T: Stored, const N: usize> Stored for [T; N] {
    unsafe fn free<W: Owner>(addr: usize, memory: &MemoryContext<'_, W>) {
        // SAFETY: the caller supplies the entire complete array.
        unsafe { free_elements::<T, W>(addr, N, memory) }
    }
}

// SAFETY: the guard owns the completed prefix until the entire array succeeds.
unsafe impl<E, I: Encode<E>, const N: usize> Encode<E> for [I; N] {
    type Output = [I::Output; N];
    unsafe fn encode<W: Owner>(self, writer: &mut Writer<'_, W>, destination: *mut Self::Output) -> Result<(), E>
    where
        E: From<BuildError<W::Error>>,
    {
        let destination = destination.cast::<I::Output>();
        let mut partial = Partial::<I::Output, W>::new(writer.memory, destination.addr(), 0);
        for (index, input) in self.into_iter().enumerate() {
            // SAFETY: each element is aligned and inside the caller's array.
            unsafe { input.encode(writer, destination.add(index))? };
            partial.written += 1;
        }
        forget(partial);
        Ok(())
    }
}

// SAFETY: the complete edge owns exactly one complete payload block.
unsafe impl<T: Stored> Stored for ShmBox<T> {
    unsafe fn free<W: Owner>(addr: usize, memory: &MemoryContext<'_, W>) {
        // SAFETY: the offset word is complete (caller contract).
        let offset = unsafe { memory.root().ptr::<isize>(addr).read() };
        // SAFETY: the complete owning edge identifies its live payload.
        unsafe { free_block::<T, W>(addr.wrapping_add_signed(offset), 1, memory) }
    }
}

// SAFETY: the complete edge owns its recorded run, or is empty and NULL.
unsafe impl<T: Stored> Stored for ShmSlice<T> {
    unsafe fn free<W: Owner>(addr: usize, memory: &MemoryContext<'_, W>) {
        let root = memory.root();
        // SAFETY: both words are complete (caller contract).
        let (offset, len) = unsafe {
            (
                root.ptr::<isize>(addr).read(),
                root.ptr::<u64>(addr + ShmSlice::<T>::LEN_OFFSET).read(),
            )
        };
        if offset != 0 {
            // SAFETY: the edge exclusively owns this complete run.
            unsafe { free_block::<T, W>(addr.wrapping_add_signed(offset), len as usize, memory) }
        }
    }
}

// SAFETY: C releases the table's pages and unlinks its embedded context.
unsafe impl<const K: usize> Stored for Lpm<K> {
    unsafe fn free<W: Owner>(addr: usize, memory: &MemoryContext<'_, W>) {
        // SAFETY: the complete table and its context remain live and exclusive.
        unsafe { bindings::yanet_sys_common_lpm_free(memory.root().ptr(addr)) }
    }
}

/// Private-state writer used by trusted encoders and derive expansions.
///
/// Construction and allocation state never reaches source callbacks. Every
/// method that accepts a storage destination requires an unsafe caller;
/// ordinary input helpers return only values and borrowed descriptors.
#[doc(hidden)]
pub struct Writer<'o, W: Owner> {
    memory: MemoryContext<'o, W>,
    header: Header,
}

impl<'o, W: Owner> Writer<'o, W> {
    /// Encodes one final field and guards it until its containing record
    /// succeeds.
    ///
    /// # Safety
    ///
    /// As for [`Encode::encode`]. The returned guard must be forgotten only
    /// after the containing value takes ownership of this complete field.
    pub unsafe fn field<E, I: Encode<E>>(
        &mut self,
        input: I,
        destination: *mut I::Output,
    ) -> Result<FieldGuard<'o, I::Output, W>, E>
    where
        E: From<BuildError<W::Error>>,
    {
        // SAFETY: the encoder receives the caller's exclusive final field.
        unsafe { input.encode(self, destination)? };
        let mut partial = Partial::new(self.memory, destination.addr(), 0);
        partial.written = 1;
        Ok(FieldGuard { _partial: partial })
    }

    /// Writes an already valid scalar into exclusive final storage.
    ///
    /// # Safety
    ///
    /// The destination must be aligned and exclusive, and the value must
    /// contain no owning edges or relative pointers.
    pub unsafe fn scalar<T: Stored>(&mut self, value: T, destination: *mut T) {
        // SAFETY: the caller supplies the complete scalar and its final place.
        unsafe { destination.write(value) }
    }

    /// Recursively writes a boxed value before storing its final owning edge.
    ///
    /// # Safety
    ///
    /// As for [`Encode::encode`], for the box's destination.
    pub unsafe fn boxed<E, I: Encode<E>>(&mut self, input: I, destination: *mut ShmBox<I::Output>) -> Result<(), E>
    where
        E: From<BuildError<W::Error>>,
    {
        const { assert!(size_of::<I::Output>() != 0, "a block of a zero-sized type") };
        let addr = self.alloc(size_of::<I::Output>())?;
        let mut partial = Partial::<I::Output, W>::new(self.memory, addr, size_of::<I::Output>());
        // SAFETY: the fresh block is aligned and holds the whole output.
        unsafe { input.encode(self, self.memory.root().ptr(addr))? };
        partial.written = 1;
        // SAFETY: the box's only word is aligned and exclusive.
        unsafe { destination.cast::<isize>().write(relative(addr, destination.addr())) };
        forget(partial);
        Ok(())
    }

    /// Streams one record at a time into the slice's final payload block.
    ///
    /// # Safety
    ///
    /// As for [`Encode::encode`], for the slice's destination. The callback
    /// supplies values only; it may panic without retaining construction state.
    pub unsafe fn slice<E, I: Encode<E>>(
        &mut self,
        len: usize,
        mut read: impl FnMut(usize) -> Result<I, E>,
        destination: *mut ShmSlice<I::Output>,
    ) -> Result<(), E>
    where
        E: From<BuildError<W::Error>>,
    {
        const { assert!(size_of::<I::Output>() != 0, "a block of a zero-sized type") };
        if len == 0 {
            drop(read);
            // SAFETY: both words lie in the caller's exclusive destination.
            unsafe { write_slice(destination, 0, 0) };
            return Ok(());
        }
        let size = len
            .checked_mul(size_of::<I::Output>())
            .ok_or(BuildError::Refused(Refusal::BlockTooLarge))?;
        let addr = self.alloc(size)?;
        let mut partial = Partial::<I::Output, W>::new(self.memory, addr, size);
        for index in 0..len {
            let input = read(index)?;
            // SAFETY: the unwritten slot lies inside the fresh payload.
            unsafe { input.encode(self, self.memory.root().ptr(addr + index * size_of::<I::Output>()))? };
            partial.written += 1;
        }
        drop(read);
        // SAFETY: both words lie in the caller's exclusive destination.
        unsafe { write_slice(destination, addr, len) };
        forget(partial);
        Ok(())
    }

    /// Registers and stores a counter directly in its final field.
    ///
    /// # Safety
    ///
    /// As for [`Encode::encode`], for a counter's destination.
    pub unsafe fn counter(&mut self, name: &str, destination: *mut Counter) -> Result<(), BuildError<W::Error>> {
        // SAFETY: construction serialises the initialised, unpublished header.
        let id = unsafe { self.memory.owner.register_counter(self.header, name) }.map_err(BuildError::Owner)?;
        // SAFETY: the registry index is the field's entire representation.
        unsafe { destination.cast::<u64>().write(id) };
        Ok(())
    }

    /// Links and stores a device directly in its final field.
    ///
    /// # Safety
    ///
    /// As for [`Encode::encode`], for a device's destination.
    pub unsafe fn device(&mut self, name: &str, destination: *mut Device) -> Result<(), BuildError<W::Error>> {
        // SAFETY: construction serialises the initialised, unpublished header.
        let id = unsafe { self.memory.owner.link_device(self.header, name) }.map_err(BuildError::Owner)?;
        // SAFETY: the registry index is the field's entire representation.
        unsafe { destination.cast::<u64>().write(id) };
        Ok(())
    }

    /// Consumes and validates each returned range once, immediately before C.
    ///
    /// # Safety
    ///
    /// As for [`Encode::encode`], for the table's destination. C context-tree
    /// links may change while the unpublished table is constructed or released.
    pub unsafe fn lpm<E, const K: usize>(
        &mut self,
        len: usize,
        mut read: impl FnMut(usize) -> Result<LpmRange<K>, E>,
        destination: *mut Lpm<K>,
    ) -> Result<(), E>
    where
        E: From<BuildError<W::Error>>,
    {
        const { assert!(K >= 1 && K <= 16, "LPM keys are 1 to 16 bytes") };
        let lpm = destination.cast::<bindings::lpm>();
        let context = self
            .memory
            .root()
            .ptr::<bindings::memory_context>(self.memory.context.get());
        // SAFETY: the complete destination and parent context are exclusive.
        let status = unsafe { bindings::yanet_sys_common_lpm_init(lpm, context, c"lpm".as_ptr()) };
        let guard = LpmGuard(lpm);
        if status != 0 {
            return Err(BuildError::Refused(Refusal::OutOfMemory).into());
        }
        for index in 0..len {
            let range = read(index)?;
            if range.from > range.to {
                return Err(BuildError::Refused(Refusal::InvalidRange).into());
            }
            // SAFETY: the table is initialised; the validated endpoints have
            // the supplied key width and are consumed synchronously.
            let status = unsafe {
                bindings::yanet_sys_common_lpm_insert(lpm, K as u8, range.from.as_ptr(), range.to.as_ptr(), range.value)
            };
            if status != 0 {
                return Err(BuildError::Refused(Refusal::OutOfMemory).into());
            }
        }
        drop(read);
        forget(guard);
        Ok(())
    }

    fn alloc(&self, size: usize) -> Result<usize, BuildError<W::Error>> {
        if size > bindings::MEMORY_BLOCK_ALLOCATOR_MAX_SIZE as usize {
            return Err(Refusal::BlockTooLarge.into());
        }
        // SAFETY: the private writer retains an initialised header context.
        unsafe { self.memory.owner.alloc_in(self.memory.context, size) }
            .map(BlockAddr::get)
            .ok_or(Refusal::OutOfMemory.into())
    }
}

/// Completed-field rollback used only by trusted derive expansions.
#[doc(hidden)]
#[must_use]
pub struct FieldGuard<'o, T: Stored, W: Owner> {
    _partial: Partial<'o, T, W>,
}

/// Owns a completed prefix and, optionally, its payload allocation.
struct Partial<'o, T: Stored, W: Owner> {
    memory: MemoryContext<'o, W>,
    addr: usize,
    written: usize,
    block: usize,
    _element: PhantomData<fn() -> T>,
}

impl<'o, T: Stored, W: Owner> Partial<'o, T, W> {
    fn new(memory: MemoryContext<'o, W>, addr: usize, block: usize) -> Self {
        Self {
            memory,
            addr,
            written: 0,
            block,
            _element: PhantomData,
        }
    }
}

impl<T: Stored, W: Owner> Drop for Partial<'_, T, W> {
    fn drop(&mut self) {
        // SAFETY: only complete elements advance the prefix; the private guard
        // retains the original context and allocation size.
        unsafe {
            free_elements::<T, W>(self.addr, self.written, &self.memory);
            if self.block != 0 {
                self.memory.free(self.addr, self.block);
            }
        }
    }
}

/// Keeps C table cleanup active across failures and source-callback panics.
struct LpmGuard(*mut bindings::lpm);
impl Drop for LpmGuard {
    fn drop(&mut self) {
        // SAFETY: C initialisation links the context and clears the directory
        // before its first allocation; release also accepts failed setup.
        unsafe { bindings::yanet_sys_common_lpm_free(self.0) }
    }
}

fn relative(target: usize, slot: usize) -> isize {
    if target == 0 {
        0
    } else {
        target.wrapping_sub(slot) as isize
    }
}

/// Writes both words only after every payload element is complete.
///
/// # Safety
///
/// The destination must be aligned and exclusive, and own no resources. A
/// nonempty payload must contain exactly the recorded complete elements.
unsafe fn write_slice<T>(destination: *mut ShmSlice<T>, addr: usize, len: usize) {
    // SAFETY: the caller supplies both aligned, exclusive words.
    unsafe {
        destination.cast::<isize>().write(relative(addr, destination.addr()));
        destination
            .cast::<u8>()
            .add(ShmSlice::<T>::LEN_OFFSET)
            .cast::<u64>()
            .write(len as u64);
    }
}

/// Releases a complete run's descendants in reverse construction order.
///
/// # Safety
///
/// Each element must satisfy the complete-value release contract.
unsafe fn free_elements<T: Stored, W: Owner>(addr: usize, len: usize, memory: &MemoryContext<'_, W>) {
    for index in (0..len).rev() {
        // SAFETY: the caller supplies a complete, exclusive run.
        unsafe { T::free(addr + index * size_of::<T>(), memory) }
    }
}

/// Releases a complete payload and its exact allocation.
///
/// # Safety
///
/// The run must be complete and unreferenced, allocated from this context with
/// exactly the element count times the element size in payload bytes.
unsafe fn free_block<T: Stored, W: Owner>(addr: usize, len: usize, memory: &MemoryContext<'_, W>) {
    // SAFETY: the complete run and its allocation satisfy the caller's
    // contract.
    unsafe {
        free_elements::<T, W>(addr, len, memory);
        memory.free(addr, len * size_of::<T>());
    }
}

/// Runs the trusted encoder with no construction callback or escaping state.
pub fn build_config<M, W, E>(owner: &W, name: &str, input: impl Encode<E, Output = M::Config>) -> Result<Built<M, W>, E>
where
    M: Module,
    W: Owner,
    E: From<BuildError<W::Error>>,
{
    const {
        assert!(
            size_of::<ModuleConfig<M::Config>>() <= bindings::MEMORY_BLOCK_ALLOCATOR_MAX_SIZE as usize,
            "a configuration larger than the largest block of the allocator"
        );
    }
    let size = size_of::<ModuleConfig<M::Config>>();
    let block = owner.alloc(size).ok_or(BuildError::Refused(Refusal::OutOfMemory))?;
    // SAFETY: the root allocation holds the entire header and body.
    let header = unsafe { Header::at(block) };
    // SAFETY: the exclusive block begins with the complete header.
    if let Err(error) = unsafe { owner.init_header(header, M::NAME, name) } {
        // SAFETY: failed setup owns no header resources (owner contract).
        unsafe { owner.free(block, size) };
        return Err(BuildError::Owner(error).into());
    }
    let release = Release { owner, header, size };
    let mut writer = Writer {
        memory: MemoryContext::new(owner, header),
        header,
    };
    let destination = owner
        .root()
        .ptr::<M::Config>(header.get() + ModuleConfig::<M::Config>::BODY_OFFSET);
    // SAFETY: the aligned, exclusive body owns no resources before encoding.
    unsafe { input.encode(&mut writer, destination)? };
    forget(release);
    Ok(Built { header, _kind: PhantomData })
}

/// Complete configuration with publication address and release authority.
///
/// Drop retains the graph because publication may share it. Destruction
/// requires the original owner and exclusive access after all readers have
/// finished.
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
    pub fn header(&self) -> Header {
        self.header
    }
}

impl<M: Module, W: Owner> Built<M, W> {
    /// Releases the complete graph through its original owner.
    ///
    /// # Safety
    ///
    /// The supplied owner must have built this configuration. No references to
    /// the configuration or any descendant may remain.
    pub unsafe fn destroy(self, owner: &W) {
        let memory = MemoryContext::new(owner, self.header);
        let body = self.header.get() + ModuleConfig::<M::Config>::BODY_OFFSET;
        // SAFETY: construction produced a complete graph in this owner's
        // memory.
        unsafe {
            M::Config::free(body, &memory);
            release_root(owner, self.header, size_of::<ModuleConfig<M::Config>>());
        }
    }
}

/// Retains root cleanup until the entire encoder succeeds.
struct Release<'o, W: Owner> {
    owner: &'o W,
    header: Header,
    size: usize,
}
impl<W: Owner> Drop for Release<'_, W> {
    fn drop(&mut self) {
        // SAFETY: recursive guards have already released all descendants.
        unsafe { release_root(self.owner, self.header, self.size) }
    }
}

/// Finalises the header before returning its root block.
///
/// # Safety
///
/// The header must be initialised in this owner's root block of the recorded
/// size. Every descendant must have been released and no references may remain.
unsafe fn release_root<W: Owner>(owner: &W, header: Header, size: usize) {
    // SAFETY: the caller supplies the initialised header and exclusive block.
    unsafe {
        owner.fini_header(header);
        owner.free(BlockAddr::new(header.get()), size);
    }
}

const _: () = assert!(align_of::<ModuleConfig<u64>>() <= MAX_ALIGN);
