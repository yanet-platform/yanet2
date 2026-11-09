//! An owner whose memory is a C block arena, so a test builds classifiers
//! through the builder as an agent does: the C compiles, joins and decodes
//! allocate in the memory context of the module header, over the arena.
//!
//! The arena is leaked and never checked for leaks; its registries hand out
//! index zero.

use alloc::ffi::CString;
use core::{
    convert::Infallible,
    ffi::{c_char, c_void},
    ptr::NonNull,
};

use yanet_shm::{resolve::Resolve, root::ShmRoot};
use yanet_sys::{
    bindings,
    cp::{BlockAddr, Built, Context, Header, Owner},
    dp::{Module, ModuleConfig},
};

// The C memory of the tests, with the classification library's compiler,
// which the joins and the decode of the shim call.
#[link(name = "yanet_sys_classify_tests", kind = "static", modifiers = "+whole-archive")]
unsafe extern "C" {
    fn yanet_sys_test_arena_new(size: usize) -> *mut c_void;
    fn yanet_sys_test_arena_base(arena: *mut c_void) -> *mut u8;
    fn yanet_sys_test_arena_context(arena: *mut c_void) -> *mut bindings::memory_context;
    fn yanet_sys_test_arena_free_bytes(arena: *mut c_void) -> usize;
    fn yanet_sys_test_arena_init_context(
        arena: *mut c_void,
        context: *mut bindings::memory_context,
        name: *const c_char,
    );
}

/// Bytes of the C arena of a test.
const ARENA_SIZE: usize = 256 << 20;

/// An owner over a leaked C arena.
pub struct ArenaOwner {
    arena: *mut c_void,
    root: ShmRoot,
}

impl ArenaOwner {
    pub fn new() -> Self {
        // SAFETY: a C allocation with no precondition.
        let arena = unsafe { yanet_sys_test_arena_new(ARENA_SIZE) };
        assert!(!arena.is_null(), "no heap for the C arena");
        // SAFETY: a live arena, never freed.
        let base = unsafe { yanet_sys_test_arena_base(arena) };
        Self {
            arena,
            // SAFETY: a raw pointer from C to the one region every block of
            // the arena lies in.
            root: unsafe { ShmRoot::from_mapping(NonNull::new(base).expect("an arena has a region")) },
        }
    }

    /// The body of a configuration this owner built, as the dataplane reads
    /// it.
    pub fn body<M: Module>(&self, built: &Built<M, Self>) -> &M::Config {
        let addr = built.header().get() + ModuleConfig::<M::Config>::BODY_OFFSET;
        // SAFETY: the body of a configuration built in the arena, written
        // whole and never released.
        unsafe { &*self.root.ptr::<M::Config>(addr) }
    }

    /// The bytes of the arena no block holds, which a release brings back to
    /// what they were before the build.
    pub fn free_bytes(&self) -> usize {
        // SAFETY: a live arena, never freed.
        unsafe { yanet_sys_test_arena_free_bytes(self.arena) }
    }

    fn context(&self, context: Context) -> *mut bindings::memory_context {
        self.root.ptr(context.get())
    }
}

// SAFETY: the root covers the arena's region, which holds every block of a
// configuration, frozen once built: a test reads a configuration only after
// its build, and releases none it reads.
unsafe impl<'g> Resolve<'g> for ArenaOwner {
    fn shm_root(&self) -> ShmRoot {
        self.root
    }
}

/// The arena's refusal: memory only, which the builder reports itself.
#[derive(Debug)]
pub struct Never(Infallible);

impl core::fmt::Display for Never {
    fn fmt(&self, _: &mut core::fmt::Formatter<'_>) -> Result<(), core::fmt::Error> {
        match self.0 {}
    }
}

impl core::error::Error for Never {}

// SAFETY: the root covers the arena, which every block comes from, aligned by
// the C block allocator; a header gets a live memory context over the arena,
// and the routines that free do not panic.
unsafe impl Owner for ArenaOwner {
    type Error = Never;

    fn root(&self) -> ShmRoot {
        self.root
    }

    fn worker_count(&self) -> u64 {
        1
    }

    fn alloc(&self, size: usize) -> Option<BlockAddr> {
        // SAFETY: the arena's context is live.
        let block = unsafe { bindings::yanet_sys_common_memory_balloc(yanet_sys_test_arena_context(self.arena), size) };
        // SAFETY: a block of the arena.
        (!block.is_null()).then(|| unsafe { BlockAddr::new(block.addr()) })
    }

    unsafe fn free(&self, addr: BlockAddr, size: usize) {
        // SAFETY: a block of the arena's context of that size (caller
        // contract).
        unsafe {
            bindings::yanet_sys_common_memory_bfree(
                yanet_sys_test_arena_context(self.arena),
                self.root.ptr(addr.get()),
                size,
            );
        }
    }

    unsafe fn alloc_in(&self, context: Context, size: usize) -> Option<BlockAddr> {
        // SAFETY: the context of a header this owner initialised (caller
        // contract).
        let block = unsafe { bindings::yanet_sys_common_memory_balloc(self.context(context), size) };
        // SAFETY: a block of the arena.
        (!block.is_null()).then(|| unsafe { BlockAddr::new(block.addr()) })
    }

    unsafe fn free_in(&self, context: Context, addr: BlockAddr, size: usize) {
        // SAFETY: a block of that context and size (caller contract).
        unsafe { bindings::yanet_sys_common_memory_bfree(self.context(context), self.root.ptr(addr.get()), size) };
    }

    unsafe fn init_header(&self, header: Header, _module: &str, name: &str) -> Result<(), Never> {
        let name = CString::new(name).unwrap_or_else(|_| CString::from(c"classify test"));
        // SAFETY: the header lies at the start of a block of the arena large
        // enough for it (caller contract).
        unsafe { yanet_sys_test_arena_init_context(self.arena, self.context(header.context()), name.as_ptr()) };
        Ok(())
    }

    unsafe fn fini_header(&self, _: Header) {}

    unsafe fn init_object_header(&self, header: Header, ty: &str, name: &str) -> Result<(), Never> {
        // SAFETY: as for a module header.
        unsafe { self.init_header(header, ty, name) }
    }

    unsafe fn fini_object_header(&self, _: Header) {}

    unsafe fn register_counter(&self, _: Header, _: &str, _: u64) -> Result<u64, Never> {
        Ok(0)
    }

    unsafe fn register_counter_in(&self, _: Header, _: &str, _: &str, _: u64) -> Result<(u64, u64), Never> {
        Ok((0, 0))
    }

    unsafe fn register_object_counter(&self, _: Header, _: &str, _: u64) -> Result<u64, Never> {
        Ok(0)
    }

    unsafe fn link_device(&self, _: Header, _: &str) -> Result<u64, Never> {
        Ok(0)
    }

    unsafe fn link_object(&self, _: Header, _: &str, _: &str) -> Result<u64, Never> {
        Ok(0)
    }
}
