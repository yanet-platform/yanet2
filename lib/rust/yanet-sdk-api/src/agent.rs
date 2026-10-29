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

/// A borrowed handle to the agent that owns shared-memory configurations.
///
/// The control plane retains attachment and detachment authority. Root blocks
/// use the agent memory context; descendants use the configuration header
/// context. Names must fit their complete NUL terminated C fields so device
/// identity survives conversion.
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
    /// Creates a handle to a live attached or destruction-held agent.
    ///
    /// # Safety
    ///
    /// The pointer must carry provenance for the entire shared-memory mapping.
    /// All handles in the process must share one mapping. The agent and mapping
    /// must remain live while a handle or any configuration built through it is
    /// used. Before the destruction gate succeeds, release requires the owning
    /// agent to remain current for its name. After success, the destruction
    /// hold keeps the original owner live even if a newer agent replaces
    /// it.
    pub unsafe fn from_raw(agent: NonNull<bindings::agent>) -> Self {
        Self { agent }
    }

    /// The raw handle, live as the constructor requires.
    pub fn as_raw(&self) -> NonNull<bindings::agent> {
        self.agent
    }

    /// Builds a complete configuration while preserving fallible input errors.
    pub fn build_config<M: Module>(
        &self,
        name: &str,
        input: impl yanet_sys::cp::Encode<ApiError, Output = M::Config>,
    ) -> Result<Built<M, Agent>, ApiError> {
        yanet_sys::cp::build_config(self, name, input)
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

/// Releases a configuration through the original owner recorded in its header.
///
/// # Safety
///
/// The pointer must identify an unreleased configuration of the requested
/// module built through this SDK, and carry provenance for the whole mapping.
/// The lifetime and exclusive-release requirements of [`Agent::from_raw`] and
/// [`Built::destroy`] also apply.
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
    // SAFETY: the live header stores its owning agent as a relative offset.
    //
    // The pointer-typed storage contains the displacement from its own address.
    let offset = unsafe { root.ptr::<isize>(slot).read() };
    let owner = root.rel::<bindings::agent>(slot, offset);
    // SAFETY: the destruction hold keeps the original owning agent live.
    //
    // It belongs to the same mapping even after a newer agent supersedes it.
    let owner_agent = unsafe { Agent::from_raw(NonNull::new_unchecked(owner)) };
    // SAFETY: the destruction gate grants exclusive release authority.
    //
    // No generation retains the configuration, and the header identifies its
    // original allocation owner.
    unsafe { built.destroy(&owner_agent) };
    // SAFETY: the hold try-destroy took on this agent is returned once, after
    // the last access to its storage.
    unsafe { bindings::agent_destroy_released(owner) };
    Ok(())
}

/// Encodes a complete name and its NUL terminator in a fixed-size C field.
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

// SAFETY: the mapping covers every correctly aligned, live allocation.
//
// Native memory contexts retain each block until its matching release. Header
// initialization establishes a live descendant context, and native cleanup
// routines cannot unwind.
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
        // SAFETY: the original agent context owns this block with the exact
        // recorded allocation size (caller contract).
        unsafe { bindings::yanet_sys_common_memory_bfree(self.context(), self.root().ptr(addr.get()), size) };
    }

    unsafe fn alloc_in(&self, context: Context, size: usize) -> Option<BlockAddr> {
        // SAFETY: the context is that of an initialised header (caller
        // contract).
        self.block(unsafe { bindings::yanet_sys_common_memory_balloc(self.root().ptr(context.get()), size) })
    }

    unsafe fn free_in(&self, context: Context, addr: BlockAddr, size: usize) {
        // SAFETY: this initialized descendant context owns the block with its
        // exact original allocation size (caller contract).
        unsafe {
            bindings::yanet_sys_common_memory_bfree(self.root().ptr(context.get()), self.root().ptr(addr.get()), size)
        };
    }

    unsafe fn init_header(&self, header: Header, module: &str, name: &str) -> Result<(), ApiError> {
        let module = c_name::<{ CP_MODULE_TYPE_LEN as usize }>(module)?;
        let name = c_name::<{ CP_MODULE_NAME_LEN as usize }>(name)?;
        let mut chain = ptr::null_mut();
        // SAFETY: the agent owns sufficient exclusive storage for the header.
        //
        // Both names are NUL terminated and the writable error output starts
        // null.
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
        // SAFETY: the initialized header has no remaining references (caller
        // contract).
        unsafe { bindings::cp_module_fini(cp_module_at(self, header)) };
    }

    unsafe fn register_counter(&self, header: Header, name: &str) -> Result<u64, ApiError> {
        let name = c_name::<{ COUNTER_NAME_LEN as usize }>(name)?;
        let mut chain = ptr::null_mut();
        // SAFETY: the initialized header owns a live counter registry.
        //
        // The name is NUL terminated and the writable error output starts null.
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

    unsafe fn register_counter_pair(&self, header: Header, registry: &str, name: &str) -> Result<(u64, u64), ApiError> {
        let registry = c_name::<{ COUNTER_NAME_LEN as usize }>(registry)?;
        let name = c_name::<{ COUNTER_NAME_LEN as usize }>(name)?;
        let mut chain = ptr::null_mut();
        let mut registry_id = 0;
        // SAFETY: this unpublished header owns its registries.
        //
        // Names are complete C strings; output locations are exclusive and
        // initialized.
        let storage = unsafe {
            bindings::cp_module_counter_registry(
                cp_module_at(self, header),
                registry.as_ptr().cast::<c_char>(),
                &mut registry_id,
                &mut chain,
            )
        };
        if storage.is_null() {
            // SAFETY: the failed native call transferred this chain
            // exclusively.
            return Err(unsafe { from_c_chain(chain) });
        }
        // SAFETY: the live registry allocates exactly two adjacent values per
        // name.
        let id = unsafe { bindings::counter_registry_register(storage, name.as_ptr().cast::<c_char>(), 2, &mut chain) };
        if id == COUNTER_INVALID {
            // SAFETY: the failed native call transferred this chain
            // exclusively.
            return Err(unsafe { from_c_chain(chain) });
        }
        Ok((registry_id, id))
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
