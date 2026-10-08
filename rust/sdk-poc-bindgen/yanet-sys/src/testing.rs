//! Test support: C-built images, relocatable copies and the block resolver.
//!
//! Compiled only with the `testing` feature and never part of the module
//! API. Everything here exists so tests can build shared-memory graphs with
//! the C code, move them, and let Miri check that resolution stays inside
//! the right allocation.

use core::{alloc::Layout, cell::Cell, ptr::NonNull};
use std::{
    alloc::{alloc_zeroed, dealloc},
    vec::Vec,
};

use crate::{
    bindings,
    builder::{ConfigBuilder, CpError, Owner},
    lpm::Lpm,
    rel::{MapResolver, Resolver, sealed},
    shm::{Module, Shm, ShmRead},
};

/// C test arena: block allocator plus root memory context inside one
/// allocation.
pub struct TestArena {
    raw: NonNull<bindings::yanet_sys_test_arena>,
}

impl TestArena {
    /// Allocates an arena; `size` must be a multiple of 2 MiB.
    pub fn new(size: usize) -> Self {
        // SAFETY: plain C constructor.
        let raw = unsafe { bindings::yanet_sys_test_arena_new(size) };
        Self {
            raw: NonNull::new(raw).expect("test arena allocation"),
        }
    }

    /// First byte of the arena.
    pub fn base(&self) -> *mut u8 {
        // SAFETY: the arena is live.
        unsafe { bindings::yanet_sys_test_arena_base(self.raw.as_ptr()) }
    }

    /// Arena size in bytes.
    pub fn size(&self) -> usize {
        // SAFETY: the arena is live.
        unsafe { bindings::yanet_sys_test_arena_size(self.raw.as_ptr()) }
    }

    /// Allocates a zeroed block.
    pub fn alloc(&self, size: usize) -> *mut u8 {
        // SAFETY: the arena is live.
        let block = unsafe { bindings::yanet_sys_test_arena_alloc(self.raw.as_ptr(), size) };
        assert!(!block.is_null(), "test arena exhausted");
        block.cast()
    }

    /// Allocates and initialises an empty LPM.
    pub fn new_lpm(&self) -> CLpm<'_> {
        // SAFETY: the arena is live.
        let lpm = unsafe { bindings::yanet_sys_test_lpm_new(self.raw.as_ptr()) };
        CLpm {
            raw: NonNull::new(lpm).expect("test LPM allocation"),
            arena: self,
        }
    }

    /// Initialises an LPM embedded in a block of this arena.
    ///
    /// # Safety
    ///
    /// `lpm` must point at an unused `struct lpm` inside a block of this
    /// arena.
    pub unsafe fn init_lpm(&self, lpm: *mut bindings::lpm) -> CLpm<'_> {
        // SAFETY: guaranteed by the caller.
        let rc = unsafe { bindings::yanet_sys_test_lpm_init(self.raw.as_ptr(), lpm) };
        assert_eq!(0, rc, "test LPM init");
        CLpm {
            raw: NonNull::new(lpm).expect("non-null LPM"),
            arena: self,
        }
    }

    /// Byte offset of an arena address from the arena base.
    pub fn offset_of(&self, ptr: *const u8) -> usize {
        ptr.addr() - self.base().addr()
    }

    /// Copy of the whole arena into a fresh allocation.
    pub fn copy_image(&self) -> MappedImage {
        // SAFETY: the arena spans `size` initialised bytes.
        let bytes = unsafe { core::slice::from_raw_parts(self.base(), self.size()) };
        MappedImage::from_bytes(bytes)
    }
}

impl TestArena {
    /// Starts a configuration of module `M` in the arena.
    ///
    /// The C module header stays zeroed: a test arena is not an agent, so
    /// there is no dataplane module whose layout could be checked.
    pub fn build<M: Module>(&self, name: &str) -> Result<ConfigBuilder<'_, M, Self>, CpError> {
        ConfigBuilder::new(self, name)
    }
}

/// Whole-arena reader for validation in tests.
struct ArenaRange {
    root: NonNull<u8>,
    size: usize,
}

impl ShmRead for ArenaRange {
    fn check(&self, addr: usize, len: usize, align: usize) -> bool {
        let start = self.root.as_ptr().addr();
        addr.is_multiple_of(align) && addr >= start && addr.checked_add(len).is_some_and(|end| end <= start + self.size)
    }

    fn read_u64(&self, addr: usize) -> Option<u64> {
        if !self.check(addr, 8, 8) {
            return None;
        }
        // SAFETY: an aligned word inside the live arena.
        let word = unsafe { core::sync::atomic::AtomicU64::from_ptr(self.root.as_ptr().with_addr(addr).cast()) };
        Some(word.load(core::sync::atomic::Ordering::Relaxed))
    }
}

// SAFETY: the root is the arena C allocated, covering every block the
// arena's C allocator hands out (zeroed by the shim, power-of-two blocks);
// the reader refuses addresses outside the arena.
unsafe impl Owner for TestArena {
    fn root(&self) -> NonNull<u8> {
        NonNull::new(self.base()).unwrap()
    }

    fn alloc(&self, size: usize) -> *mut u8 {
        // SAFETY: the arena is live.
        unsafe { bindings::yanet_sys_test_arena_alloc(self.raw.as_ptr(), size) }.cast()
    }

    unsafe fn free(&self, _block: *mut u8, _size: usize) {}

    unsafe fn init_header(
        &self,
        _: *mut bindings::cp_module,
        _: &core::ffi::CStr,
        _: &core::ffi::CStr,
        _: u64,
    ) -> Result<(), CpError> {
        Ok(())
    }

    unsafe fn fini_header(&self, _: *mut bindings::cp_module) {}

    unsafe fn lpm_init(&self, lpm: *mut bindings::lpm, _: *mut bindings::cp_module) -> core::ffi::c_int {
        // SAFETY: guaranteed by the caller.
        unsafe { bindings::yanet_sys_test_lpm_init(self.raw.as_ptr(), lpm) }
    }

    unsafe fn lpm_insert(
        &self,
        lpm: *mut bindings::lpm,
        key_size: u8,
        from: *const u8,
        to: *const u8,
        value: u32,
    ) -> core::ffi::c_int {
        // SAFETY: guaranteed by the caller.
        unsafe { bindings::yanet_sys_test_lpm_insert(lpm, key_size, from, to, value) }
    }

    unsafe fn lpm_free(&self, lpm: *mut bindings::lpm) {
        // SAFETY: guaranteed by the caller.
        unsafe { bindings::yanet_sys_test_lpm_free(lpm) }
    }

    unsafe fn reader(&self, _: *mut bindings::cp_module) -> Result<Box<dyn ShmRead + '_>, CpError> {
        Ok(Box::new(ArenaRange { root: self.root(), size: self.size() }))
    }
}

impl Drop for TestArena {
    fn drop(&mut self) {
        // SAFETY: the arena was allocated by the C constructor.
        unsafe { bindings::yanet_sys_test_arena_free(self.raw.as_ptr()) }
    }
}

/// C-built LPM inside a live [`TestArena`].
///
/// One handle per LPM: C writes need `&mut`, so they cannot run while a
/// view borrowed from the handle is alive.
pub struct CLpm<'a> {
    raw: NonNull<bindings::lpm>,
    arena: &'a TestArena,
}

impl CLpm<'_> {
    /// The C LPM header.
    pub fn raw(&self) -> *mut bindings::lpm {
        self.raw.as_ptr()
    }

    /// Inserts `[from, to] -> value` with the C LPM insert.
    pub fn insert(&mut self, from: &[u8], to: &[u8], value: u32) {
        assert_eq!(from.len(), to.len());
        // SAFETY: the LPM lives in a live arena; keys are key-size long.
        let rc = unsafe {
            bindings::yanet_sys_test_lpm_insert(self.raw(), from.len() as u8, from.as_ptr(), to.as_ptr(), value)
        };
        assert_eq!(0, rc, "C LPM insert");
    }

    /// Looks a key up with the C LPM lookup.
    pub fn lookup(&self, key: &[u8]) -> u32 {
        // SAFETY: the LPM lives in a live arena; the key is key-size long.
        unsafe { bindings::yanet_sys_test_lpm_lookup(self.raw(), key.len() as u8, key.as_ptr()) }
    }

    /// Looks consecutive keys up with the inline C lookup in a C loop.
    pub fn lookup_many(&self, key_size: usize, keys: &[u8], results: &mut [u32]) {
        assert_eq!(keys.len(), key_size * results.len());
        // SAFETY: the LPM lives in a live arena; buffers sized above.
        unsafe {
            bindings::yanet_sys_test_lpm_lookup_many(
                self.raw(),
                key_size as u8,
                keys.as_ptr(),
                results.as_mut_ptr(),
                results.len(),
            )
        }
    }

    /// View of the LPM, with `K` its key size, through a resolver rooted at
    /// the arena pointer C returned.
    pub fn view<const K: usize>(&self) -> Shm<'_, Lpm<K>, MapResolver<'_>> {
        // SAFETY: the root comes straight from C and covers the whole arena,
        // which outlives the handle; the LPM is C-built, and C writes to it
        // need `&mut self`, which the returned borrow excludes.
        unsafe {
            let res = MapResolver::new(NonNull::new(self.arena.base()).unwrap());
            Shm::from_raw(res, res.at(self.raw().addr()))
        }
    }

    /// Blocks of the LPM: (address, size) of the header, the chunk
    /// directory and every chunk, as the C code allocated them.
    pub fn blocks(&self) -> Vec<(usize, usize)> {
        let lpm = self.raw();
        let chunk = core::mem::size_of::<bindings::lpm_page>() * bindings::LPM_CHUNK_SIZE as usize;
        let mut blocks = vec![(lpm.addr(), core::mem::size_of::<bindings::lpm>())];
        // SAFETY: a C-built LPM: the directory slot is relative to itself and
        // each chunk slot to its own address, as ADDR_OF computes.
        unsafe {
            let pages = c_addr_of((&raw const (*lpm).pages).cast());
            let count = (*lpm).page_count.div_ceil(bindings::LPM_CHUNK_SIZE as usize);
            blocks.push((pages.addr(), count * core::mem::size_of::<usize>()));
            for idx in 0..count {
                let slot = pages.cast::<isize>().add(idx);
                blocks.push((c_addr_of(slot).addr(), chunk));
            }
        }
        blocks
    }
}

/// The C `ADDR_OF` on a raw slot, for test inspection of C-built images.
///
/// # Safety
///
/// `slot` must be readable.
unsafe fn c_addr_of(slot: *const isize) -> *const u8 {
    // SAFETY: guaranteed by the caller.
    let offset = unsafe { slot.read() };
    slot.cast::<u8>().wrapping_offset(offset)
}

/// Relocatable copy of an image in one heap allocation.
///
/// The allocation is held as a raw pointer, never as a Rust reference, so
/// the resolver root keeps permissions over the whole image.
pub struct MappedImage {
    ptr: NonNull<u8>,
    layout: Layout,
}

impl MappedImage {
    /// Alignment of image copies: the largest block alignment a fixture
    /// relies on for its own data.
    const ALIGN: usize = 64;

    /// Copies bytes into a fresh, suitably aligned allocation.
    pub fn from_bytes(bytes: &[u8]) -> Self {
        let image = Self::zeroed(bytes.len());
        // SAFETY: the fresh allocation spans `bytes.len()` bytes.
        unsafe { core::ptr::copy_nonoverlapping(bytes.as_ptr(), image.ptr.as_ptr(), bytes.len()) };
        image
    }

    /// Allocates a zeroed image.
    pub fn zeroed(len: usize) -> Self {
        let layout = Layout::from_size_align(len.max(1), Self::ALIGN).unwrap();
        // SAFETY: the layout has a non-zero size.
        let ptr = NonNull::new(unsafe { alloc_zeroed(layout) }).expect("image allocation");
        Self { ptr, layout }
    }

    /// Copies a byte range into the image at an offset.
    pub fn write(&mut self, offset: usize, bytes: &[u8]) {
        assert!(offset + bytes.len() <= self.layout.size());
        // SAFETY: bounds checked above.
        unsafe { core::ptr::copy_nonoverlapping(bytes.as_ptr(), self.ptr.as_ptr().add(offset), bytes.len()) };
    }

    /// Image size in bytes.
    pub fn len(&self) -> usize {
        self.layout.size()
    }

    /// Reports whether the image is empty.
    pub fn is_empty(&self) -> bool {
        self.layout.size() == 0
    }

    /// Copy of the image at a new address.
    pub fn relocate(&self) -> Self {
        let image = Self::zeroed(self.len());
        // SAFETY: both allocations span `len` bytes and are distinct.
        unsafe { core::ptr::copy_nonoverlapping(self.ptr.as_ptr(), image.ptr.as_ptr(), self.len()) };
        image
    }

    /// Resolver rooted at the image allocation.
    fn resolver(&self) -> MapResolver<'_> {
        // SAFETY: the root is the raw allocation pointer, covering the whole
        // image, which nobody mutates while it is borrowed.
        unsafe { MapResolver::new(self.ptr) }
    }

    /// LPM view of an LPM header at a byte offset of the image.
    ///
    /// # Safety
    ///
    /// The image must hold a valid LPM graph at `offset`, as a C-built
    /// image does: the views do no validation of their own.
    pub unsafe fn lpm<const K: usize>(&self, offset: usize) -> Shm<'_, Lpm<K>, MapResolver<'_>> {
        assert!(offset + core::mem::size_of::<bindings::lpm>() <= self.len());
        let res = self.resolver();
        // SAFETY: in bounds; the graph is valid by the caller's contract.
        unsafe { Shm::from_raw(res, res.at(self.ptr.as_ptr().addr() + offset)) }
    }

    /// Address of a byte offset of the image.
    pub fn addr_of(&self, offset: usize) -> usize {
        self.ptr.as_ptr().addr() + offset
    }

    /// Looks a key up with the C LPM lookup on an LPM inside the image.
    ///
    /// # Safety
    ///
    /// `offset` must be the header of a C-built LPM inside the image.
    pub unsafe fn c_lpm_lookup(&self, offset: usize, key: &[u8]) -> u32 {
        // SAFETY: guaranteed by the caller.
        unsafe { bindings::yanet_sys_test_lpm_lookup(self.ptr_at(offset).cast(), key.len() as u8, key.as_ptr()) }
    }

    /// Raw pointer at a byte offset, with image provenance.
    pub fn ptr_at(&self, offset: usize) -> *mut u8 {
        self.ptr.as_ptr().wrapping_add(offset)
    }
}

impl ShmRead for MappedImage {
    fn check(&self, addr: usize, len: usize, align: usize) -> bool {
        let start = self.ptr.as_ptr().addr();
        addr.is_multiple_of(align)
            && addr >= start
            && addr.checked_add(len).is_some_and(|end| end <= start + self.len())
    }

    fn read_u64(&self, addr: usize) -> Option<u64> {
        if !self.check(addr, 8, 8) {
            return None;
        }
        // SAFETY: an aligned word inside the image allocation.
        Some(unsafe { self.ptr.as_ptr().with_addr(addr).cast::<u64>().read() })
    }
}

impl Drop for MappedImage {
    fn drop(&mut self) {
        // SAFETY: allocated with this layout.
        unsafe { dealloc(self.ptr.as_ptr(), self.layout) }
    }
}

struct Block {
    logical: usize,
    ptr: NonNull<u8>,
    layout: Layout,
    live: Cell<bool>,
}

/// Image whose every allocator block is a separate Rust allocation.
///
/// Addresses stored in the image are logical (the addresses the blocks had
/// when C built them); resolution translates the slot's real address to its
/// logical one, adds the offset, finds the block holding the logical target
/// and returns a pointer derived from that block's own allocation. Miri then
/// reports any access past a block end or into a freed block natively.
#[derive(Default)]
pub struct BlockImage {
    blocks: Vec<Block>,
}

impl BlockImage {
    /// Adds a block at a logical address.
    pub fn add_block(&mut self, logical: usize, bytes: &[u8]) {
        let layout = Layout::from_size_align(bytes.len().max(1), 64).unwrap();
        // SAFETY: non-zero layout; the copy stays inside the new block.
        let ptr = unsafe {
            let ptr = NonNull::new(alloc_zeroed(layout)).expect("block allocation");
            core::ptr::copy_nonoverlapping(bytes.as_ptr(), ptr.as_ptr(), bytes.len());
            ptr
        };
        self.blocks.push(Block {
            logical,
            ptr,
            layout,
            live: Cell::new(true),
        });
    }

    fn by_logical(&self, logical: usize) -> &Block {
        self.blocks
            .iter()
            .find(|b| logical >= b.logical && logical < b.logical + b.layout.size())
            .unwrap_or_else(|| panic!("logical address {logical:#x} is outside every block"))
    }

    fn by_real(&self, addr: usize) -> &Block {
        self.blocks
            .iter()
            .find(|b| b.live.get() && addr >= b.ptr.addr().get() && addr < b.ptr.addr().get() + b.layout.size())
            .unwrap_or_else(|| panic!("slot address {addr:#x} is outside every live block"))
    }

    fn pointer(block: &Block, logical: usize) -> NonNull<u8> {
        // Derived from the block's own allocation pointer: the provenance
        // of exactly this block.
        // SAFETY: `logical` lies inside the block, so the result is non-null.
        unsafe { NonNull::new_unchecked(block.ptr.as_ptr().wrapping_add(logical - block.logical)) }
    }

    /// Resolver over this image.
    pub fn resolver(&self) -> BlockResolver<'_> {
        BlockResolver { image: self }
    }

    /// LPM view of the LPM header at a logical address.
    ///
    /// # Safety
    ///
    /// The blocks must hold a valid LPM graph rooted at `logical`, as a
    /// C-built image does: the views do no validation of their own.
    pub unsafe fn lpm<const K: usize>(&self, logical: usize) -> Shm<'_, Lpm<K>, BlockResolver<'_>> {
        let block = self.by_logical(logical);
        assert!(logical + core::mem::size_of::<bindings::lpm>() <= block.logical + block.layout.size());
        // SAFETY: the header lies inside one live block; the graph is valid
        // by the caller's contract.
        unsafe { Shm::from_raw(self.resolver(), Self::pointer(block, logical).cast()) }
    }

    /// Frees the block at a logical address while keeping it resolvable.
    ///
    /// # Safety
    ///
    /// Deliberately breaks the resolver contract: only for tests expecting
    /// Miri to report a use after free.
    pub unsafe fn free_block(&self, logical: usize) {
        let block = self.by_logical(logical);
        assert!(block.live.replace(false), "block freed twice");
        // SAFETY: allocated with this layout; it is never freed again.
        unsafe { dealloc(block.ptr.as_ptr(), block.layout) }
    }

    /// Overwrites the 8-byte slot at a logical address.
    ///
    /// The write itself is in bounds; it may break the graph, which only
    /// matters to a later, `unsafe`, view construction.
    pub fn poke(&mut self, logical: usize, value: isize) {
        let block = self.by_logical(logical);
        assert!(logical + 8 <= block.logical + block.layout.size());
        // SAFETY: bounds checked above; the image is exclusively borrowed.
        unsafe { Self::pointer(block, logical).cast::<isize>().write_unaligned(value) }
    }
}

impl Drop for BlockImage {
    fn drop(&mut self) {
        for block in &self.blocks {
            if block.live.get() {
                // SAFETY: allocated with this layout and still live.
                unsafe { dealloc(block.ptr.as_ptr(), block.layout) }
            }
        }
    }
}

/// Resolver of a [`BlockImage`].
#[derive(Clone, Copy)]
pub struct BlockResolver<'g> {
    image: &'g BlockImage,
}

impl sealed::Resolver for BlockResolver<'_> {}

impl<'g> Resolver<'g> for BlockResolver<'g> {
    fn target<T>(self, slot_addr: usize, offset: isize) -> *const T {
        let slot_block = self.image.by_real(slot_addr);
        let slot_logical = slot_block.logical + (slot_addr - slot_block.ptr.addr().get());
        let target = slot_logical.wrapping_add_signed(offset);
        let block = self.image.by_logical(target);
        BlockImage::pointer(block, target).as_ptr().cast_const().cast()
    }
}

/// C-built IPv4 LPM fixture, produced at build time by the C generator in
/// `shim/fixture_gen.c`.
pub const LPM4_FIXTURE: &[u8] = include_bytes!(concat!(env!("OUT_DIR"), "/lpm4.bin"));

/// C-built IPv6 LPM fixture, produced like [`LPM4_FIXTURE`].
pub const LPM6_FIXTURE: &[u8] = include_bytes!(concat!(env!("OUT_DIR"), "/lpm6.bin"));

/// C-built LPM captured as blocks plus reference lookups.
pub struct Fixture {
    pub key_size: u8,
    /// Logical address of the LPM header.
    pub root: usize,
    pub blocks: Vec<(usize, Vec<u8>)>,
    /// Keys (first `key_size` bytes used) and the C lookup result.
    pub cases: Vec<([u8; 16], u32)>,
}

const FIXTURE_MAGIC: u64 = 0x5941_4e45_544c_504d;

impl Fixture {
    /// Captures a C-built LPM and the C answers for the given keys.
    ///
    /// Logical addresses are arena offsets plus a fixed base, so no block
    /// sits at a NULL-like address.
    pub fn capture(arena: &TestArena, lpm: &CLpm<'_>, key_size: u8, keys: &[[u8; 16]]) -> Self {
        const BASE: usize = 0x1000_0000;
        let blocks = lpm
            .blocks()
            .into_iter()
            .map(|(addr, len)| {
                // SAFETY: each block lies inside the live arena.
                let bytes = unsafe { core::slice::from_raw_parts(arena.base().with_addr(addr), len) };
                (BASE + addr - arena.base().addr(), bytes.to_vec())
            })
            .collect();
        let cases = keys
            .iter()
            .map(|key| (*key, lpm.lookup(&key[..usize::from(key_size)])))
            .collect();
        Self {
            key_size,
            root: BASE + arena.offset_of(lpm.raw().cast()),
            blocks,
            cases,
        }
    }

    /// Serialises as little-endian words with run-length encoded blocks.
    pub fn encode(&self) -> Vec<u8> {
        let mut words = vec![
            FIXTURE_MAGIC,
            u64::from(self.key_size),
            self.root as u64,
            self.blocks.len() as u64,
        ];
        for (logical, bytes) in &self.blocks {
            assert_eq!(0, bytes.len() % 8);
            words.push(*logical as u64);
            words.push(bytes.len() as u64);
            let mut runs: Vec<(u64, u64)> = Vec::new();
            for chunk in bytes.as_chunks::<8>().0 {
                let word = u64::from_le_bytes(*chunk);
                match runs.last_mut() {
                    Some((count, value)) if *value == word => *count += 1,
                    _ => runs.push((1, word)),
                }
            }
            words.push(runs.len() as u64);
            for (count, value) in runs {
                words.push(count);
                words.push(value);
            }
        }
        words.push(self.cases.len() as u64);
        for (key, value) in &self.cases {
            words.push(u64::from_le_bytes(key[..8].try_into().unwrap()));
            words.push(u64::from_le_bytes(key[8..].try_into().unwrap()));
            words.push(u64::from(*value));
        }
        words.iter().flat_map(|w| w.to_le_bytes()).collect()
    }

    /// Parses [`Fixture::encode`] output.
    pub fn decode(bytes: &[u8]) -> Self {
        let mut words = bytes.as_chunks::<8>().0.iter().map(|c| u64::from_le_bytes(*c));
        let mut next = move || words.next().expect("truncated fixture");
        assert_eq!(FIXTURE_MAGIC, next(), "not an LPM fixture");
        let key_size = next() as u8;
        let root = next() as usize;
        let block_count = next();
        let mut blocks = Vec::new();
        for _ in 0..block_count {
            let logical = next() as usize;
            let len = next() as usize;
            let mut bytes = Vec::with_capacity(len);
            for _ in 0..next() {
                let (count, value) = (next(), next());
                for _ in 0..count {
                    bytes.extend_from_slice(&value.to_le_bytes());
                }
            }
            assert_eq!(len, bytes.len());
            blocks.push((logical, bytes));
        }
        let mut cases = Vec::new();
        for _ in 0..next() {
            let mut key = [0; 16];
            key[..8].copy_from_slice(&next().to_le_bytes());
            key[8..].copy_from_slice(&next().to_le_bytes());
            cases.push((key, next() as u32));
        }
        Self { key_size, root, blocks, cases }
    }

    /// Lays the blocks out at their logical distances in one allocation;
    /// returns the image and the header offset.
    pub fn mapping(&self) -> (MappedImage, usize) {
        let low = self.blocks.iter().map(|(l, _)| *l).min().unwrap();
        let high = self.blocks.iter().map(|(l, b)| l + b.len()).max().unwrap();
        // Keep the logical alignment of every block.
        let low = low & !(MappedImage::ALIGN - 1);
        let mut image = MappedImage::zeroed(high - low);
        for (logical, bytes) in &self.blocks {
            image.write(logical - low, bytes);
        }
        (image, self.root - low)
    }

    /// One Rust allocation per block.
    pub fn block_image(&self) -> BlockImage {
        let mut image = BlockImage::default();
        for (logical, bytes) in &self.blocks {
            image.add_block(*logical, bytes);
        }
        image
    }
}
