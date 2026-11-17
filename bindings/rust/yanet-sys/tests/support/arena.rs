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

use yanet_shm::{
    resolve::{Graph, Resolve},
    root::ShmRoot,
};
use yanet_sys::{
    bindings,
    cp::{BlockAddr, Built, Context, Freed, Fresh, Live, LiveContext, ModuleKind, ObjectKind, Owner, Retiring},
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
            root: ShmRoot::from_mapping(NonNull::new(base).expect("an arena has a region")),
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

impl<'g> Resolve<'g> for ArenaOwner {
    fn graph(&self) -> Graph<'g> {
        // SAFETY: the root retains raw provenance over the whole arena.
        // Tests resolve only completed, frozen configurations and release
        // none while their references are in use.
        unsafe { Graph::new(self.root) }
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

// SAFETY: the root covers every aligned block in the C arena.
//
// Operations use the capability's owner and recorded allocation or header.
// Initialized headers carry live C memory contexts over the arena. Freeing
// does not panic, and finalization has no work for this leaked arena.
unsafe impl Owner for ArenaOwner {
    const C_MEMORY: bool = true;

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
        (!block.is_null()).then(|| BlockAddr::new(block.addr()))
    }

    fn free(block: Freed<'_, Self>) {
        let owner = block.owner();
        // SAFETY: the capability returns this owner's unreferenced root
        // block once with its original allocation size.
        unsafe {
            bindings::yanet_sys_common_memory_bfree(
                yanet_sys_test_arena_context(owner.arena),
                owner.root.ptr(block.addr().get()),
                block.size(),
            );
        }
    }

    fn alloc_in(context: &LiveContext<'_, Self>, size: usize) -> Option<BlockAddr> {
        let owner = context.owner();
        // SAFETY: the capability keeps this owner's header context live.
        let block = unsafe { bindings::yanet_sys_common_memory_balloc(owner.context(context.context()), size) };
        (!block.is_null()).then(|| BlockAddr::new(block.addr()))
    }

    fn free_in(block: Freed<'_, Self, Context>) {
        let owner = block.owner();
        // SAFETY: the capability returns this unreferenced block once with
        // its original size to the owner's live allocation context.
        unsafe {
            bindings::yanet_sys_common_memory_bfree(
                owner.context(block.context()),
                owner.root.ptr(block.addr().get()),
                block.size(),
            )
        };
    }

    fn init_header(block: Fresh<'_, Self, ModuleKind>, _module: &str, name: &str) -> Result<(), Never> {
        let owner = block.owner();
        let name = CString::new(name).unwrap_or_else(|_| CString::from(c"classify test"));
        // SAFETY: the capability names this owner's fresh block large
        // enough for a module header. The name is NUL terminated.
        unsafe {
            yanet_sys_test_arena_init_context(owner.arena, owner.context(block.header().context()), name.as_ptr())
        };
        Ok(())
    }

    fn fini_header(_: Retiring<'_, Self, ModuleKind>) {}

    fn init_object_header(block: Fresh<'_, Self, ObjectKind>, _ty: &str, name: &str) -> Result<(), Never> {
        let owner = block.owner();
        let name = CString::new(name).unwrap_or_else(|_| CString::from(c"classify test"));
        // SAFETY: the capability names this owner's fresh block large
        // enough for an object header. The name is NUL terminated.
        unsafe {
            yanet_sys_test_arena_init_context(owner.arena, owner.context(block.header().context()), name.as_ptr())
        };
        Ok(())
    }

    fn fini_object_header(_: Retiring<'_, Self, ObjectKind>) {}

    fn register_counter(_: &Live<'_, Self, ModuleKind>, _: &str, _: u64) -> Result<u64, Never> {
        Ok(0)
    }

    fn ensure_counter_registry(_: &Live<'_, Self, ModuleKind>, _: &str) -> Result<u64, Never> {
        Ok(0)
    }

    fn register_counter_in(_: &Live<'_, Self, ModuleKind>, _: &str, _: &str, _: u64) -> Result<(u64, u64), Never> {
        Ok((0, 0))
    }

    fn register_object_counter(_: &Live<'_, Self, ObjectKind>, _: &str, _: u64) -> Result<u64, Never> {
        Ok(0)
    }

    fn link_device(_: &Live<'_, Self, ModuleKind>, _: &str) -> Result<u64, Never> {
        Ok(0)
    }

    fn link_object(_: &Live<'_, Self, ModuleKind>, _: &str, _: &str) -> Result<u64, Never> {
        Ok(0)
    }
}
