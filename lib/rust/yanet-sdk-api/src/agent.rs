//! The agent owner of the configuration builder.

use core::{
    ffi::c_char,
    fmt,
    mem::offset_of,
    ptr::{self, NonNull},
};

use yanet_shm::root::ShmRoot;
use yanet_sys::{
    bindings::{self, COUNTER_NAME_LEN, CP_DEVICE_NAME_LEN, CP_MODULE_NAME_LEN, CP_MODULE_TYPE_LEN},
    counters::COUNTER_INVALID,
    cp::{BlockAddr, Built, Context, Header, Owner},
    dp::Module,
};

use crate::error::{ApiError, ErrorKind, from_c_chain, tagged};

/// A handle on an agent attached to the shared memory, the owner of the
/// configurations built there.
///
/// It does not own the agent and does nothing when dropped: the attach is
/// the control plane's, which detaches it.
///
/// The C routines behind it are those a C module's API calls: the root block
/// comes from the agent's memory context and the blocks of a configuration
/// from the memory context of its header. A name reaches C whole and
/// terminated or not at all: C truncates a long module or device name
/// silently, and a truncated device name would link another device.
pub struct Agent {
    agent: NonNull<bindings::agent>,
}

impl fmt::Debug for Agent {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> Result<(), fmt::Error> {
        f.debug_struct("Agent").field("agent", &self.agent).finish()
    }
}

/// A configuration kept by a live generation, handed back with the reason.
///
/// Release it again while its agent is the live one; once a newer agent
/// supersedes that agent, the configuration is abandoned to C's reclaim of it
/// and is not released.
pub struct Retained<M> {
    /// The configuration, intact.
    pub built: Built<M, Agent>,
    /// Why it was kept; busy while a generation holds it.
    pub error: ApiError,
}

impl<M> fmt::Debug for Retained<M> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> Result<(), fmt::Error> {
        f.debug_struct("Retained")
            .field("built", &self.built)
            .field("error", &self.error)
            .finish()
    }
}

impl Agent {
    /// A handle on the attached agent.
    ///
    /// # Safety
    ///
    /// `agent` is live, whose pointer has the provenance of the whole
    /// mapping, as the pointer the attach returns. It is attached, or held
    /// alive as C holds the owner of a module being destroyed. All agents a
    /// process makes handles for are of one mapping (yanet attaches one),
    /// which stays mapped, and the agent live, while the value, or any
    /// configuration built through any of them, is used. While such a
    /// configuration is released, its agent is also the current one for its
    /// name, not superseded: C reads the item unlocked behind the gate.
    pub unsafe fn from_raw(agent: NonNull<bindings::agent>) -> Self {
        Self { agent }
    }

    /// The raw handle, live as the constructor requires.
    pub fn as_raw(&self) -> NonNull<bindings::agent> {
        self.agent
    }

    /// The agent's own memory context, which its root blocks come from.
    fn context(&self) -> *mut bindings::memory_context {
        // SAFETY: the agent is live, and a field address is computed without
        // a read (constructor contract).
        unsafe { &raw mut (*self.agent.as_ptr()).memory_context }
    }

    /// The block whose payload C returned, none for null.
    fn block(&self, payload: *mut core::ffi::c_void) -> Option<BlockAddr> {
        // SAFETY: a non-null payload C returned from a memory context is the
        // payload of a block of the mapping.
        (!payload.is_null()).then(|| unsafe { BlockAddr::new(payload.addr()) })
    }

    /// Releases a configuration as a C module's free routine does; the
    /// configuration is of the one mapping every agent of the process has.
    ///
    /// While a live generation holds the configuration it is kept: it comes
    /// back intact with a busy error, to be released again once the
    /// generation is gone, retried only while its agent is the live one.
    /// Otherwise the configuration and its header are returned to the agent
    /// that built them.
    pub fn release<M: Module>(&self, built: Built<M, Agent>) -> Result<(), Retained<M>> {
        // SAFETY: the header is one this crate built in the mapping, with the
        // mapping's provenance from the agent.
        unsafe { release_at(NonNull::new_unchecked(cp_module_at(self, built.header()))) }
    }
}

/// The header of a configuration in the mapping of the agent.
pub fn cp_module_at(agent: &Agent, header: Header) -> *mut bindings::cp_module {
    agent.root().ptr(header.get())
}

/// Releases the configuration of module `M` whose header is at `module`, as
/// [`Agent::release`] does, finding the agent that built it in the header.
///
/// # Safety
///
/// `module` is the header of a configuration of module `M` built over an
/// [`Agent`] and not released since, a pointer that has the provenance of the
/// whole mapping, as the header pointer of an agent's root. The other
/// requirements are those of [`Agent::from_raw`] and [`Built::destroy`].
pub unsafe fn release_at<M: Module>(module: NonNull<bindings::cp_module>) -> Result<(), Retained<M>> {
    // SAFETY: the pointer has the provenance of the whole mapping (caller
    // contract).
    let root = unsafe { ShmRoot::from_mapping(module.cast()) };
    // SAFETY: the address is a header of this mapping (caller contract).
    let header = unsafe { Header::at(BlockAddr::new(module.addr().get())) };
    // SAFETY: the header is the one of such a configuration (caller contract).
    let built = unsafe { Built::<M, Agent>::from_header(header) };
    let mut chain = ptr::null_mut();
    // SAFETY: the header is an initialised one of this mapping, built by
    // this crate; the out-parameter points at a null chain.
    if unsafe { bindings::cp_module_try_destroy(module.as_ptr(), &mut chain) } != 0 {
        // SAFETY: the chain, if any, is ours alone.
        let error = unsafe { from_c_chain(chain) };
        return Err(Retained { built, error });
    }
    // The agent is read before the header is finalised, as C does.
    let slot = header.get() + offset_of!(bindings::cp_module, entity.agent);
    // SAFETY: the slot is inside the live header. It holds the offset of the
    // owning agent from the slot itself, which the shared memory stores in a
    // pointer-typed field.
    let offset = unsafe { root.ptr::<isize>(slot).read() };
    let owner = root.rel::<bindings::agent>(slot, offset);
    // SAFETY: the header names its agent, which the destruction hold that the
    // successful try-destroy took keeps live, though a newer agent may have
    // superseded it; it is of the one mapping.
    let owner_agent = unsafe { Agent::from_raw(NonNull::new_unchecked(owner)) };
    // SAFETY: the successful try-destroy gave the exclusive right to destroy:
    // no generation references the configuration. The owner is the agent the
    // header names, whose memory the configuration was built in.
    unsafe { built.destroy(&owner_agent) };
    // SAFETY: the hold try-destroy took on this agent is returned once, after
    // the last access to its storage.
    unsafe { bindings::agent_destroy_released(owner) };
    Ok(())
}

/// A name as the NUL terminated bytes of a C field of `N` bytes.
fn c_name<const N: usize>(name: &str) -> Result<[u8; N], ApiError> {
    if name.len() >= N || name.bytes().any(|byte| byte == 0) {
        return Err(tagged(
            ErrorKind::InvalidArgument,
            c"a name holds a NUL or does not fit its C field whole",
        ));
    }
    let mut bytes = [0; N];
    bytes[..name.len()].copy_from_slice(name.as_bytes());
    Ok(bytes)
}

// SAFETY: the root is the mapping's, which covers every block (constructor
// contract); the blocks come from C's memory contexts, which align them as
// the contract asks and own them until returned. The routines called from
// `Drop` do not panic. `cp_module_init` leaves a live context in the header
// over the agent's memory.
unsafe impl Owner for Agent {
    type Error = ApiError;

    fn root(&self) -> ShmRoot {
        // SAFETY: the pointer has the provenance of the whole mapping
        // (constructor contract).
        unsafe { ShmRoot::from_mapping(self.agent.cast()) }
    }

    fn alloc(&self, size: usize) -> Option<BlockAddr> {
        // SAFETY: the context is the agent's, live (constructor contract).
        self.block(unsafe { bindings::yanet_sys_common_memory_balloc(self.context(), size) })
    }

    unsafe fn free(&self, addr: BlockAddr, size: usize) {
        // SAFETY: the block came from `alloc` of this context with this size
        // (caller contract).
        unsafe { bindings::yanet_sys_common_memory_bfree(self.context(), self.root().ptr(addr.get()), size) };
    }

    unsafe fn alloc_in(&self, context: Context, size: usize) -> Option<BlockAddr> {
        // SAFETY: the context is that of an initialised header (caller
        // contract).
        self.block(unsafe { bindings::yanet_sys_common_memory_balloc(self.root().ptr(context.get()), size) })
    }

    unsafe fn free_in(&self, context: Context, addr: BlockAddr, size: usize) {
        // SAFETY: the block came from `alloc_in` of this context with this
        // size, and the context is initialised (caller contract).
        unsafe {
            bindings::yanet_sys_common_memory_bfree(self.root().ptr(context.get()), self.root().ptr(addr.get()), size)
        };
    }

    unsafe fn init_header(&self, header: Header, module: &str, name: &str) -> Result<(), ApiError> {
        let module = c_name::<{ CP_MODULE_TYPE_LEN as usize }>(module)?;
        let name = c_name::<{ CP_MODULE_NAME_LEN as usize }>(name)?;
        let mut chain = ptr::null_mut();
        // SAFETY: the header is a block of this agent large enough for it
        // (caller contract), the names are NUL terminated and the
        // out-parameter points at a null chain.
        if unsafe {
            bindings::cp_module_init(
                cp_module_at(self, header),
                self.agent.as_ptr(),
                module.as_ptr().cast::<c_char>(),
                name.as_ptr().cast::<c_char>(),
                &mut chain,
            )
        } != 0
        {
            // SAFETY: the chain C wrote, if any, is ours alone.
            return Err(unsafe { from_c_chain(chain) });
        }
        Ok(())
    }

    unsafe fn fini_header(&self, header: Header) {
        // SAFETY: the header was initialised by `init_header` and is no
        // longer referenced (caller contract).
        unsafe { bindings::cp_module_fini(cp_module_at(self, header)) };
    }

    unsafe fn register_counter(&self, header: Header, name: &str) -> Result<u64, ApiError> {
        let name = c_name::<{ COUNTER_NAME_LEN as usize }>(name)?;
        let mut chain = ptr::null_mut();
        // SAFETY: the header is initialised (caller contract), so its
        // registry is; the name is NUL terminated and the out-parameter
        // points at a null chain.
        let id = unsafe {
            bindings::counter_registry_register(
                &raw mut (*cp_module_at(self, header)).counter_registry,
                name.as_ptr().cast::<c_char>(),
                1,
                &mut chain,
            )
        };
        if id == COUNTER_INVALID {
            // SAFETY: the chain C wrote, if any, is ours alone.
            return Err(unsafe { from_c_chain(chain) });
        }
        Ok(id)
    }

    unsafe fn link_device(&self, header: Header, name: &str) -> Result<u64, ApiError> {
        let name = c_name::<{ CP_DEVICE_NAME_LEN as usize }>(name)?;
        let (mut index, mut chain) = (0, ptr::null_mut());
        // SAFETY: the header is initialised (caller contract); the name is
        // NUL terminated and the out-parameters are valid.
        if unsafe {
            bindings::cp_module_link_device(
                cp_module_at(self, header),
                name.as_ptr().cast::<c_char>(),
                &mut index,
                &mut chain,
            )
        } != 0
        {
            // SAFETY: the chain C wrote, if any, is ours alone.
            return Err(unsafe { from_c_chain(chain) });
        }
        Ok(index)
    }
}
