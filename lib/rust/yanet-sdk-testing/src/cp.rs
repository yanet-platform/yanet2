//! A configuration owner over the block arena, for the builder tests.
//!
//! [`TestOwner`] hands out blocks of the arena and records what the builder
//! does with them; it emulates no behaviour of C. Registrations get
//! sequential ids, and a fresh header holds nothing but, natively, the C
//! memory context described below. Checks that a C allocator would not make
//! are recorded as violations, which [`TestOwner::assert_released`] reports.
//!
//! The SDK's blocks are blocks of the arena with those checks. The C objects a
//! configuration embeds use the real C: natively, the header holds a real C
//! memory context over a real C block allocator, so a C routine allocates and
//! frees as it does in a product, and the allocator's free size is checked
//! when the owner is released. Miri runs no C, so under Miri the owner has no
//! C memory and a configuration with a C object cannot be built.

use alloc::{string::String, vec::Vec};
use core::{
    cell::{Cell, RefCell},
    error::Error,
    fmt,
};

use yanet_shm::root::ShmRoot;
#[cfg(not(miri))]
use yanet_sys::bindings::{
    block_allocator, memory_context, yanet_sys_common_block_allocator_free_size, yanet_sys_common_block_allocator_init,
    yanet_sys_common_block_allocator_put_arena, yanet_sys_common_memory_context_init,
};
use yanet_sys::cp::{BlockAddr, Context, Header, Owner};

use crate::BlockArena;

/// Byte every block is filled with when handed out, so no test passes by
/// relying on zeroed memory.
const FILL: u8 = 0xA5;

/// Bytes of C memory an owner has by default, enough for the tests.
#[cfg(not(miri))]
const C_CAPACITY: usize = 512 << 10;

/// The real C block allocator of an owner and the bytes it started with.
#[cfg(not(miri))]
struct CMemory {
    /// Address of the allocator in the arena.
    allocator: usize,
    /// Bytes free when the owner was made.
    free: usize,
}

/// A call of the owner a test can make fail.
#[derive(Clone, Copy)]
pub enum Call {
    /// Allocating a block, the header's included.
    Alloc,
    /// Registering a counter or a device.
    Registration,
    /// Initialising the header.
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
        f.write_str(match self {
            Self::Registration => "the registration failed",
            Self::Header => "the header initialisation failed",
        })
    }
}

impl Error for TestError {}

/// A block handed out and not yet returned.
struct Block {
    addr: BlockAddr,
    size: usize,
    /// Memory context it came from: `None` for `alloc`.
    context: Option<Context>,
}

/// Owner whose every block is a block of the arena.
pub struct TestOwner {
    arena: &'static BlockArena,
    /// Absent under Miri, which runs no C.
    #[cfg(not(miri))]
    c_memory: CMemory,
    blocks: RefCell<Vec<Block>>,
    /// Addresses of the initialised headers.
    headers: RefCell<Vec<Header>>,
    /// Ids handed out, counters and devices alike.
    registered: Cell<u64>,
    /// Calls left before each kind fails, for good; `None` never fails.
    countdowns: [Cell<Option<usize>>; 3],
    /// What the owner caught the builder doing wrong. The routines that run
    /// from `Drop` record instead of panicking.
    violations: RefCell<Vec<String>>,
}

impl Default for TestOwner {
    fn default() -> Self {
        Self::new()
    }
}

impl TestOwner {
    /// Owner of an empty arena whose calls all succeed, with C memory enough
    /// for the tests natively.
    pub fn new() -> Self {
        #[cfg(not(miri))]
        return Self::with_c_memory(C_CAPACITY);
        #[cfg(miri)]
        Self::over(BlockArena::new())
    }

    /// As [`TestOwner::new`], with `capacity` bytes of C memory.
    #[cfg(not(miri))]
    pub fn with_c_memory(capacity: usize) -> Self {
        let arena = BlockArena::new();
        let memory = arena.alloc(capacity);
        let allocator = arena.alloc(size_of::<block_allocator>()).cast::<block_allocator>();
        // SAFETY: the allocator's block is fresh and exclusive, and the arena
        // block is handed to it whole, to be used by it only.
        let free = unsafe {
            yanet_sys_common_block_allocator_init(allocator.as_ptr());
            yanet_sys_common_block_allocator_put_arena(allocator.as_ptr(), memory.as_ptr().cast(), capacity);
            yanet_sys_common_block_allocator_free_size(allocator.as_ptr())
        };
        Self::over(
            arena,
            CMemory {
                allocator: allocator.as_ptr().addr(),
                free,
            },
        )
    }

    fn over(arena: &'static BlockArena, #[cfg(not(miri))] c_memory: CMemory) -> Self {
        Self {
            arena,
            #[cfg(not(miri))]
            c_memory,
            blocks: RefCell::new(Vec::new()),
            headers: RefCell::new(Vec::new()),
            registered: Cell::new(0),
            countdowns: Default::default(),
            violations: RefCell::new(Vec::new()),
        }
    }

    /// Makes every call of the kind fail after `count` more succeed.
    pub fn fail_after(&self, call: Call, count: usize) {
        self.countdowns[call as usize].set(Some(count));
    }

    /// Number of blocks handed out and not yet returned.
    pub fn live_blocks(&self) -> usize {
        self.blocks.borrow().len()
    }

    /// Asserts that nothing the owner handed out is left and that the builder
    /// broke no rule of the owner.
    pub fn assert_released(&self) {
        let (started, free) = self.c_free_size();
        assert_eq!(
            (0, 0, started, Vec::<String>::new()),
            (
                self.live_blocks(),
                self.headers.borrow().len(),
                free,
                self.violations.borrow().clone()
            ),
            "blocks or headers left, C free size against its start, and violations"
        );
    }

    /// The C allocator's free size at the start and now.
    fn c_free_size(&self) -> (usize, usize) {
        #[cfg(not(miri))]
        {
            let allocator = self.arena.root().ptr::<block_allocator>(self.c_memory.allocator);
            // SAFETY: the allocator was set up with the owner and lives as
            // long as the arena.
            let free = unsafe { yanet_sys_common_block_allocator_free_size(allocator) };
            (self.c_memory.free, free)
        }
        #[cfg(miri)]
        (0, 0)
    }

    /// Whether the call of the kind fails now.
    fn fails(&self, call: Call) -> bool {
        let countdown = &self.countdowns[call as usize];
        match countdown.get() {
            Some(0) => true,
            Some(left) => {
                countdown.set(Some(left - 1));
                false
            }
            None => false,
        }
    }

    fn violation(&self, what: &str) {
        self.violations.borrow_mut().push(what.into());
    }

    /// A block of `size` bytes filled with [`FILL`], `None` when the call
    /// fails.
    fn take_block(&self, context: Option<Context>, size: usize) -> Option<BlockAddr> {
        if self.fails(Call::Alloc) {
            return None;
        }
        let block = self.arena.alloc(size);
        // SAFETY: the arena returned a block of at least `size.max(1)` bytes.
        unsafe { block.as_ptr().write_bytes(FILL, size.max(1)) };
        // SAFETY: the arena block is the payload this owner hands out.
        let addr = unsafe { BlockAddr::new(block.as_ptr().addr()) };
        self.blocks.borrow_mut().push(Block { addr, size, context });
        Some(addr)
    }

    /// Takes the block off the live ones, recording a violation when it is
    /// none, or came from another context, or has another size.
    fn return_block(&self, context: Option<Context>, addr: BlockAddr, size: usize) {
        let mut blocks = self.blocks.borrow_mut();
        let Some(idx) = blocks.iter().position(|block| block.addr == addr) else {
            drop(blocks);
            return self.violation("a block returned that is not live");
        };
        let block = blocks.swap_remove(idx);
        drop(blocks);
        if block.context != context {
            self.violation("a block returned to a context other than its own");
        }
        if block.size != size {
            self.violation("a block returned with a size other than its own");
        }
    }

    fn register(&self) -> Result<u64, TestError> {
        if self.fails(Call::Registration) {
            return Err(TestError::Registration);
        }
        let id = self.registered.get();
        self.registered.set(id + 1);
        Ok(id)
    }
}

// SAFETY: the arena aligns every block to 64, which covers any size, and keeps
// it until the arena is dropped, which is never; `root` is the arena's, which
// covers every block. The routines called from `Drop` do not panic: they
// record what they find wrong. Natively, header initialisation puts a real C
// memory context over a real C block allocator on the header, so the C routines
// an embedded object calls work on memory they own. Miri runs no C, so there a
// header has no C memory context and a C call is an unsupported operation that
// stops the run, not undefined behaviour.
unsafe impl Owner for TestOwner {
    type Error = TestError;

    fn root(&self) -> ShmRoot {
        self.arena.root()
    }

    fn alloc(&self, size: usize) -> Option<BlockAddr> {
        self.take_block(None, size)
    }

    unsafe fn free(&self, addr: BlockAddr, size: usize) {
        self.return_block(None, addr, size);
    }

    unsafe fn alloc_in(&self, context: Context, size: usize) -> Option<BlockAddr> {
        self.take_block(Some(context), size)
    }

    unsafe fn free_in(&self, context: Context, addr: BlockAddr, size: usize) {
        self.return_block(Some(context), addr, size);
    }

    unsafe fn init_header(&self, header: Header, _module: &str, _name: &str) -> Result<(), TestError> {
        if self.fails(Call::Header) {
            return Err(TestError::Header);
        }
        #[cfg(not(miri))]
        {
            let context = self.arena.root().ptr::<memory_context>(header.context().get());
            let allocator = self.arena.root().ptr::<block_allocator>(self.c_memory.allocator);
            // SAFETY: the context lies in the header (caller contract), and
            // the allocator lives as long as the arena.
            unsafe { yanet_sys_common_memory_context_init(context, c"header".as_ptr(), allocator) };
        }
        self.headers.borrow_mut().push(header);
        Ok(())
    }

    unsafe fn fini_header(&self, header: Header) {
        let context = header.context();
        if self.blocks.borrow().iter().any(|block| block.context == Some(context)) {
            self.violation("a header finalised while blocks of its context are live");
        }
        #[cfg(not(miri))]
        {
            let context = self.arena.root().ptr::<memory_context>(context.get());
            // SAFETY: the header holds the context the owner set up for it. The
            // link's bits are compared with zero and never followed: the field
            // is pointer-typed, not an offset word.
            let first_child = unsafe { (&raw const (*context).first_child).read() };
            if first_child.addr() != 0 {
                self.violation("a header finalised while a C context is linked under it");
            }
        }
        let mut headers = self.headers.borrow_mut();
        match headers.iter().position(|&known| known == header) {
            Some(idx) => {
                headers.swap_remove(idx);
            }
            None => {
                drop(headers);
                self.violation("a header finalised that is not initialised");
            }
        }
    }

    unsafe fn register_counter(&self, _header: Header, _name: &str) -> Result<u64, TestError> {
        self.register()
    }

    unsafe fn link_device(&self, _header: Header, _name: &str) -> Result<u64, TestError> {
        self.register()
    }
}
