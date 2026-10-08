//! A heap-backed stand-in for an agent's shared memory, for tests of api
//! crates that forbid unsafe code.
//!
//! One allocation holds an agent header, its arena table and one arena;
//! device blocks are carved from the arena with a common header whose
//! relative slots point where C would put them. Offsets follow
//! [`FakeShm::layout`], not the real C layout.

use core::{alloc::Layout as AllocLayout, cell::Cell, marker::PhantomData, ptr::NonNull};

use yanet_shm::{ShmError, ShmLayout};

use super::{Agent, DeviceBlock, Layout, raw};

const SIZE: usize = 1 << 16;
const TABLE_OFFSET: usize = 256;
const DATA_OFFSET: usize = 4096;

/// A fake agent with one arena spanning the upper part of the fixture.
pub struct FakeShm {
    base: NonNull<u8>,
    next: Cell<usize>,
}

impl FakeShm {
    fn alloc_layout() -> AllocLayout {
        AllocLayout::from_size_align(SIZE, 4096).expect("valid fixture layout")
    }

    /// The layout the fixture lays its structures out by.
    pub fn layout() -> Layout {
        Layout(raw::Layout {
            cp_device_size: 64,
            cp_device_align: 8,
            cp_device_agent: 0,
            cp_device_input: 8,
            cp_device_output: 16,
            agent_arena_count: 0,
            agent_arenas: 8,
            arena_size: 16,
            arena_data: 0,
            arena_len: 8,
            device_name_len: 80,
            pipeline_name_len: 80,
        })
    }

    pub fn new() -> Self {
        // SAFETY: the layout has a nonzero size.
        let base = unsafe { std::alloc::alloc_zeroed(Self::alloc_layout()) };
        let shm = Self {
            base: NonNull::new(base).expect("fixture allocation"),
            next: Cell::new(DATA_OFFSET),
        };
        shm.write_u64(0, 1);
        shm.write_u64(8, (TABLE_OFFSET - 8) as u64);
        shm.write_u64(TABLE_OFFSET, (DATA_OFFSET - TABLE_OFFSET) as u64);
        shm.write_u64(TABLE_OFFSET + 8, (SIZE - DATA_OFFSET) as u64);
        shm
    }

    /// Address of the byte at the given offset.
    pub fn addr(&self, offset: usize) -> usize {
        self.base.addr().get() + offset
    }

    /// Writes a word at an offset of the fixture.
    pub fn write_u64(&self, offset: usize, value: u64) {
        assert!(offset + 8 <= SIZE && offset % 8 == 0);
        // SAFETY: in bounds and aligned.
        unsafe { self.base.add(offset).cast::<u64>().write(value) };
    }

    /// The fixture agent, borrowed from the fixture.
    pub fn agent(&self) -> Borrowed<'_, Agent> {
        // The borrow keeps the fixture, which the base pointer covers,
        // alive; the agent carries the fixture layout.
        Borrowed {
            value: Agent {
                root: self.base,
                layout: Self::layout(),
                shim: false,
            },
            _shm: PhantomData,
        }
    }

    /// Carves a zeroed block of `size` bytes from the arena with a common
    /// header owned by the agent and input and output entries in the arena.
    ///
    /// The block is borrowed from the fixture and cannot be freed: it never
    /// came from the C allocator.
    pub fn device(&self, size: usize) -> (usize, Borrowed<'_, DeviceBlock<'_>>) {
        assert!(size <= SIZE - DATA_OFFSET, "the device does not fit the fixture");
        let offset = self.next.get();
        let entries = offset + size.next_multiple_of(64);
        self.next.set(entries + 128);
        assert!(entries + 128 <= SIZE);
        self.write_u64(offset, (0u64).wrapping_sub(offset as u64));
        self.write_u64(offset + 8, (entries - offset - 8) as u64);
        self.write_u64(offset + 16, (entries + 64 - offset - 16) as u64);
        // SAFETY: an in-bounds region of the fixture.
        let root = unsafe { self.base.add(offset) };
        let block = DeviceBlock {
            root,
            size,
            body_offset: Self::layout().cp_device_size(),
            _agent: PhantomData,
        };
        (offset, Borrowed { value: block, _shm: PhantomData })
    }
}

impl Default for FakeShm {
    fn default() -> Self {
        Self::new()
    }
}

impl Drop for FakeShm {
    fn drop(&mut self) {
        // SAFETY: allocated in the constructor with the same layout.
        unsafe { std::alloc::dealloc(self.base.as_ptr(), Self::alloc_layout()) };
    }
}

/// A value pointing into a [`FakeShm`], usable only while the fixture lives.
pub struct Borrowed<'a, T> {
    value: T,
    _shm: PhantomData<&'a FakeShm>,
}

impl<T> core::ops::Deref for Borrowed<'_, T> {
    type Target = T;

    fn deref(&self) -> &T {
        &self.value
    }
}

impl Borrowed<'_, DeviceBlock<'_>> {
    /// Writes a value inside the body of the borrowed block.
    pub fn write<T: ShmLayout>(&mut self, offset: usize, value: T) -> Result<(), ShmError> {
        self.value.write(offset, value)
    }
}
