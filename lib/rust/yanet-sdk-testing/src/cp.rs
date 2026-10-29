//! A configuration owner over the block arena, for the builder tests under
//! Miri and natively alike.
//!
//! It sets up no C memory context and calls no C: a configuration that embeds a
//! C object, such as an LPM, is built over the real agent in the C harness,
//! from Go tests.

use alloc::vec::Vec;
use core::{
    cell::{Cell, RefCell},
    error::Error,
    fmt,
};

use yanet_shm::root::ShmRoot;
use yanet_sys::cp::{BlockAddr, Context, Header, Owner};

use crate::BlockArena;

/// A call of the owner a test can make fail.
#[derive(Clone, Copy)]
pub enum Call {
    /// Allocating a block, the header's included.
    Alloc,
    /// Registering a counter or linking a device.
    Registration,
    /// Initialising the header of a configuration.
    Header,
}

/// The failure of a routine of the owner.
#[derive(Debug, PartialEq, Eq)]
pub enum TestError {
    Registration,
    Header,
}

impl fmt::Display for TestError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> Result<(), fmt::Error> {
        fmt::Debug::fmt(self, f)
    }
}

impl Error for TestError {}

/// Owner whose every block is a block of the arena.
pub struct TestOwner {
    arena: &'static BlockArena,
    /// Blocks handed out and not yet returned, with their size and context.
    blocks: RefCell<Vec<(BlockAddr, usize, Option<Context>)>>,
    headers: RefCell<Vec<Header>>,
    registered: Cell<u64>,
    /// Calls left before each kind fails, for good; `None` never fails.
    countdowns: [Cell<Option<usize>>; 3],
    /// What the builder did wrong; recorded, as the returns run from
    /// destructors.
    violations: RefCell<Vec<&'static str>>,
}

impl TestOwner {
    /// An owner of an empty arena.
    ///
    /// # Safety
    ///
    /// No configuration that embeds a C object, such as an LPM, is built over
    /// the owner: it leaves no live C memory context in a header, which such an
    /// object opens its own context under.
    pub unsafe fn new() -> Self {
        Self {
            arena: BlockArena::new(),
            blocks: RefCell::default(),
            headers: RefCell::default(),
            registered: Cell::new(0),
            countdowns: Default::default(),
            violations: RefCell::default(),
        }
    }

    /// Makes every call of the kind fail after the given number more
    /// succeed.
    pub fn fail_after(&self, call: Call, count: usize) {
        self.countdowns[call as usize].set(Some(count));
    }

    pub fn live_blocks(&self) -> usize {
        self.blocks.borrow().len()
    }

    /// Asserts that no block or header is left and the builder broke no rule.
    pub fn assert_released(&self) {
        let left = (self.live_blocks(), self.headers.borrow().len());
        assert_eq!((0, 0, Vec::new()), (left.0, left.1, self.violations.borrow().clone()));
    }

    fn fails(&self, call: Call) -> bool {
        let countdown = &self.countdowns[call as usize];
        let left = countdown.get();
        countdown.set(left.map(|left| left.saturating_sub(1)));
        left == Some(0)
    }

    /// A block filled with a byte no test relies on, so none relies on zeroes.
    fn take(&self, context: Option<Context>, size: usize) -> Option<BlockAddr> {
        let block = (!self.fails(Call::Alloc)).then(|| self.arena.alloc(size))?;
        // SAFETY: the arena returned a block of at least the size, or one byte
        // for an empty request.
        unsafe { block.as_ptr().write_bytes(0xA5, size.max(1)) };
        // SAFETY: the arena's block is the payload of a block of this owner.
        let addr = unsafe { BlockAddr::new(block.as_ptr().addr()) };
        self.blocks.borrow_mut().push((addr, size, context));
        Some(addr)
    }

    fn give_back(&self, context: Option<Context>, addr: BlockAddr, size: usize) {
        let mut blocks = self.blocks.borrow_mut();
        let block = blocks
            .iter()
            .position(|block| block.0 == addr)
            .map(|idx| blocks.swap_remove(idx));
        drop(blocks);
        if block.is_none_or(|(_, have, from)| (have, from) != (size, context)) {
            self.violations
                .borrow_mut()
                .push("a block returned that is not live, or to another context or size");
        }
    }

    fn register(&self) -> Result<u64, TestError> {
        let id = self.registered.get();
        self.registered.set(id + 1);
        (!self.fails(Call::Registration))
            .then_some(id)
            .ok_or(TestError::Registration)
    }
}

// SAFETY: the arena aligns every block to 64, which covers any size, and keeps
// it for good; the root is the arena's, which covers every block. The routines
// called from destructors do not panic: they record what they find wrong. The
// owner sets up no C memory context in a header and calls no C, which breaks
// the contract for a configuration with an embedded C object; the constructor
// is unsafe, and its caller promises to build none.
unsafe impl Owner for TestOwner {
    type Error = TestError;

    fn root(&self) -> ShmRoot {
        self.arena.root()
    }

    fn alloc(&self, size: usize) -> Option<BlockAddr> {
        self.take(None, size)
    }

    unsafe fn free(&self, addr: BlockAddr, size: usize) {
        self.give_back(None, addr, size);
    }

    unsafe fn alloc_in(&self, context: Context, size: usize) -> Option<BlockAddr> {
        self.take(Some(context), size)
    }

    unsafe fn free_in(&self, context: Context, addr: BlockAddr, size: usize) {
        self.give_back(Some(context), addr, size);
    }

    unsafe fn init_header(&self, header: Header, _module: &str, _name: &str) -> Result<(), TestError> {
        (!self.fails(Call::Header))
            .then(|| self.headers.borrow_mut().push(header))
            .ok_or(TestError::Header)
    }

    unsafe fn fini_header(&self, header: Header) {
        let mut headers = self.headers.borrow_mut();
        let known = headers
            .iter()
            .position(|&known| known == header)
            .map(|idx| headers.swap_remove(idx));
        drop(headers);
        let live = self
            .blocks
            .borrow()
            .iter()
            .any(|block| block.2 == Some(header.context()));
        if known.is_none() || live {
            self.violations
                .borrow_mut()
                .push("a header finalised that is unknown or has live blocks");
        }
    }

    unsafe fn register_counter(&self, _header: Header, _name: &str, _size: u64) -> Result<u64, TestError> {
        self.register()
    }

    unsafe fn link_device(&self, _header: Header, _name: &str) -> Result<u64, TestError> {
        self.register()
    }
}
