//! Control-plane owner: module configurations in an attached agent's arenas.
//!
//! The provenance root is the agent pointer the caller passes across the C
//! ABI; arena addresses come from C as integers and become pointers through
//! that root only. Allocation, module header setup and LPM construction run
//! the C routines through the C shim.

use core::{
    ffi::{CStr, c_int},
    marker::PhantomData,
    ptr::NonNull,
    sync::atomic::{AtomicU64, Ordering},
};
use std::{boxed::Box, vec::Vec};

use crate::{
    bindings,
    builder::{ConfigBuilder, CpError, Owner, destroy},
    shm::{Module, ShmRead},
};

/// Consumes a C error chain into its message.
fn take_error(err: *mut bindings::yanet_error, context: &str) -> CpError {
    if err.is_null() {
        return CpError(context.into());
    }
    // SAFETY: a non-null chain produced by the C call that just failed; its
    // message is a NUL-terminated string owned by the chain.
    unsafe {
        let message = CStr::from_ptr(bindings::yanet_error_message(err))
            .to_string_lossy()
            .into_owned();
        bindings::yanet_error_free(err);
        CpError(format!("{context}: {message}"))
    }
}

/// Attached agent of the calling process.
pub struct Agent<'a> {
    raw: NonNull<bindings::agent>,
    _agent: PhantomData<&'a bindings::agent>,
}

impl Agent<'_> {
    /// Wraps an agent handle received across the C ABI.
    ///
    /// # Safety
    ///
    /// `raw` must be an attached agent valid for the lifetime, passed
    /// straight from C so that its provenance covers the shared mapping.
    pub unsafe fn from_raw(raw: NonNull<core::ffi::c_void>) -> Self {
        Self { raw: raw.cast(), _agent: PhantomData }
    }

    /// Starts a configuration of module `M` named `name`.
    pub fn build<M: Module>(&self, name: &str) -> Result<ConfigBuilder<'_, M, Self>, CpError> {
        ConfigBuilder::new(self, name)
    }
}

// SAFETY: the root is the attached agent, whose provenance covers the
// mapping its arenas live in; allocation goes through the agent's C block
// allocator (zeroed, power-of-two blocks, so aligned to at least 64 for
// configuration sizes); the reader refuses addresses outside the arenas.
unsafe impl Owner for Agent<'_> {
    fn root(&self) -> NonNull<u8> {
        self.raw.cast()
    }

    fn alloc(&self, size: usize) -> *mut u8 {
        // SAFETY: the agent is attached (construction contract).
        unsafe { bindings::yanet_sys_cp_balloc(self.raw.as_ptr(), size) }.cast()
    }

    unsafe fn free(&self, block: *mut u8, size: usize) {
        // SAFETY: guaranteed by the caller.
        unsafe { bindings::yanet_sys_cp_bfree(self.raw.as_ptr(), block.cast(), size) }
    }

    unsafe fn init_header(
        &self,
        header: *mut bindings::cp_module,
        module: &CStr,
        name: &CStr,
        layout: u64,
    ) -> Result<(), CpError> {
        let mut err = core::ptr::null_mut();
        // SAFETY: guaranteed by the caller.
        match unsafe {
            bindings::cp_module_init_layout(
                header,
                self.raw.as_ptr(),
                module.as_ptr(),
                name.as_ptr(),
                layout,
                &mut err,
            )
        } {
            0 => Ok(()),
            _ => Err(take_error(err, "failed to init the module header")),
        }
    }

    unsafe fn fini_header(&self, header: *mut bindings::cp_module) {
        // SAFETY: guaranteed by the caller.
        unsafe { bindings::cp_module_fini(header) }
    }

    unsafe fn lpm_init(&self, lpm: *mut bindings::lpm, header: *mut bindings::cp_module) -> c_int {
        // SAFETY: guaranteed by the caller.
        unsafe { bindings::yanet_sys_cp_lpm_init(lpm, header, c"lpm".as_ptr()) }
    }

    unsafe fn lpm_insert(
        &self,
        lpm: *mut bindings::lpm,
        key_size: u8,
        from: *const u8,
        to: *const u8,
        value: u32,
    ) -> c_int {
        // SAFETY: guaranteed by the caller.
        unsafe { bindings::yanet_sys_cp_lpm_insert(lpm, key_size, from, to, value) }
    }

    unsafe fn lpm_free(&self, lpm: *mut bindings::lpm) {
        // SAFETY: guaranteed by the caller.
        unsafe { bindings::yanet_sys_cp_lpm_free(lpm) }
    }

    unsafe fn reader(&self, header: *mut bindings::cp_module) -> Result<Box<dyn ShmRead + '_>, CpError> {
        // SAFETY: guaranteed by the caller.
        let owner = unsafe { bindings::yanet_sys_cp_module_agent(header) };
        if owner.addr() != self.raw.as_ptr().addr() {
            return Err(CpError("the module header names another agent as its owner".into()));
        }
        let mut starts = [0usize; 64];
        let mut sizes = [0u64; 64];
        // SAFETY: the agent is attached; the buffers hold `len` entries.
        let count = unsafe {
            bindings::yanet_sys_cp_agent_arenas(
                self.raw.as_ptr(),
                starts.as_mut_ptr(),
                sizes.as_mut_ptr(),
                starts.len(),
            )
        };
        if count > starts.len() {
            return Err(CpError("the agent has more arenas than the validator reads".into()));
        }
        let arenas = starts
            .iter()
            .zip(sizes)
            .take(count)
            .map(|(start, size)| (*start, size as usize))
            .collect();
        Ok(Box::new(ArenaReader {
            root: self.root(),
            arenas,
            _graph: PhantomData,
        }))
    }
}

/// Destroys a handed-over configuration once no generation holds it.
///
/// Returns -1 with errno EAGAIN and an error chain while a live generation
/// still references the module, exactly like a C module's free.
///
/// # Safety
///
/// `config` must be a configuration of module `M` built through an agent and
/// handed over, not destroyed yet, passed straight from C; `err` must be
/// null or a writable slot.
pub unsafe fn free<M: Module>(config: NonNull<core::ffi::c_void>, err: *mut *mut bindings::yanet_error) -> c_int {
    // SAFETY: guaranteed by the caller; the owner agent comes from the
    // header's own link, as a pointer C computed.
    unsafe {
        if bindings::cp_module_try_destroy(config.as_ptr().cast(), err) != 0 {
            return -1;
        }
        // The C module init always links the owner, so a missing link means
        // a corrupt header; the panic reaches the caller as an error code.
        let agent = NonNull::new(bindings::yanet_sys_cp_module_agent(config.as_ptr().cast()))
            .expect("module header has no owner agent");
        let agent = Agent::from_raw(agent.cast());
        destroy::<M, _>(&agent, agent.root().with_addr(config.addr()).cast());
    }
    0
}

/// Reads inside the owner agent's arenas only.
///
/// Every read is bounds- and alignment-checked against the arena table, so
/// a corrupt offset yields an error, never an access outside agent memory.
/// Reads are relaxed atomic loads: other control-plane threads may write
/// unrelated blocks of the same arenas concurrently. Bounds are the whole
/// arena table, not the configuration's own blocks; the graph checks of the
/// layout types narrow targets further.
pub struct ArenaReader<'a> {
    root: NonNull<u8>,
    arenas: Vec<(usize, usize)>,
    _graph: PhantomData<&'a ()>,
}

impl ShmRead for ArenaReader<'_> {
    fn check(&self, addr: usize, len: usize, align: usize) -> bool {
        addr.is_multiple_of(align)
            && self
                .arenas
                .iter()
                .any(|&(start, size)| addr >= start && addr.checked_add(len).is_some_and(|end| end <= start + size))
    }

    fn read_u64(&self, addr: usize) -> Option<u64> {
        if !self.check(addr, 8, 8) {
            return None;
        }
        // SAFETY: the word lies inside a mapped arena of the agent and is
        // aligned; the pointer takes the agent root's provenance.
        let word = unsafe { AtomicU64::from_ptr(self.root.as_ptr().with_addr(addr).cast()) };
        Some(word.load(Ordering::Relaxed))
    }
}
