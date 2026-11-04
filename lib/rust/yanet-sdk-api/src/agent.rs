//! The agent owner of the configuration builder.

use core::{
    ffi::{CStr, c_char, c_int},
    fmt,
    marker::PhantomData,
    mem::offset_of,
    ptr::{self, NonNull},
    slice,
};

use yanet_shm::{resolve::Resolve, root::ShmRoot};
use yanet_sys::{
    bindings::{
        self, COUNTER_NAME_LEN, CP_DEVICE_NAME_LEN, CP_MODULE_NAME_LEN, CP_MODULE_TYPE_LEN, CP_OBJECT_NAME_LEN,
        CP_OBJECT_TYPE_LEN,
    },
    counters::COUNTER_INVALID,
    cp::{BlockAddr, Built, BuiltObject, Context, Header, Owner},
    dp::{Module, Object},
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

/// A configuration or an object kept by a live generation, handed back with
/// the reason: a [`Built`] or a [`BuiltObject`].
///
/// Release it again while its agent is the live one; once a newer agent
/// supersedes that agent, it is abandoned to C's reclaim of it and is not
/// released.
pub struct Retained<B> {
    /// The configuration or the object, intact.
    pub built: B,
    /// Why it was kept; busy while a generation holds it.
    pub error: ApiError,
}

impl<B: fmt::Debug> fmt::Debug for Retained<B> {
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
    pub fn release<M: Module>(&self, built: Built<M, Agent>) -> Result<(), Retained<Built<M, Agent>>> {
        // SAFETY: the header is one this crate built in the mapping, with the
        // mapping's provenance from the agent.
        unsafe { release_at(NonNull::new_unchecked(raw_at::<Built<M, Agent>>(self, built.header()))) }
    }

    /// Releases an object as a C object's owner does; the object is of the
    /// one mapping every agent of the process has.
    ///
    /// While a live generation holds the object it is kept: it comes back
    /// intact with a busy error, to be released again once no generation
    /// holds it, as after a same-name upsert or a delete of it, retried only
    /// while its agent is the live one. Otherwise the object and its header
    /// are returned to the agent that built them.
    pub fn release_object<O: Object>(
        &self,
        built: BuiltObject<O, Agent>,
    ) -> Result<(), Retained<BuiltObject<O, Agent>>> {
        // SAFETY: the header is one this crate built in the mapping, with the
        // mapping's provenance from the agent.
        unsafe {
            release_at(NonNull::new_unchecked(raw_at::<BuiltObject<O, Agent>>(
                self,
                built.header(),
            )))
        }
    }

    /// Runs `f` over the body of an object, published or not, read as the
    /// dataplane reads it: its state changes only through the types that
    /// allow a shared change, as atomics. The body's edges resolve through
    /// the context `f` is given.
    ///
    /// Each call brands the context and the body with a lifetime of its own,
    /// so the context resolves only what this read reached, and neither
    /// leaves `f`. A reference from another read is refused:
    ///
    /// ```compile_fail
    /// # use yanet_sdk_api::Agent;
    /// # use yanet_shm::rel::ShmSlice;
    /// # use yanet_sys::{cp::BuiltObject, dp::Object};
    /// # struct Mine;
    /// # impl Object for Mine {
    /// #     const TYPE: &'static str = "mine";
    /// #     type Config = ShmSlice<u64>;
    /// # }
    /// fn mixed(agent: &Agent, first: &BuiltObject<Mine, Agent>, second: &BuiltObject<Mine, Agent>) -> usize {
    ///     agent.read(first, |_, outer| agent.read(second, |cx, _| outer.as_slice(&cx).len()))
    /// }
    /// ```
    ///
    /// The body does not leave the closure:
    ///
    /// ```compile_fail
    /// # use yanet_sdk_api::Agent;
    /// # use yanet_shm::rel::ShmSlice;
    /// # use yanet_sys::{cp::BuiltObject, dp::Object};
    /// # struct Mine;
    /// # impl Object for Mine {
    /// #     const TYPE: &'static str = "mine";
    /// #     type Config = ShmSlice<u64>;
    /// # }
    /// fn escape<'a>(agent: &'a Agent, built: &'a BuiltObject<Mine, Agent>) -> &'a ShmSlice<u64> {
    ///     agent.read(built, |_, config| config)
    /// }
    /// ```
    ///
    /// Nor does the context:
    ///
    /// ```compile_fail
    /// # use yanet_sdk_api::{Agent, ObjectCx};
    /// # use yanet_shm::rel::ShmSlice;
    /// # use yanet_sys::{cp::BuiltObject, dp::Object};
    /// # struct Mine;
    /// # impl Object for Mine {
    /// #     const TYPE: &'static str = "mine";
    /// #     type Config = ShmSlice<u64>;
    /// # }
    /// fn escape<'a>(agent: &'a Agent, built: &'a BuiltObject<Mine, Agent>) -> ObjectCx<'a> {
    ///     agent.read(built, |cx, _| cx)
    /// }
    /// ```
    ///
    /// The body's own edges resolve through the context of its read:
    ///
    /// ```no_run
    /// # use yanet_sdk_api::Agent;
    /// # use yanet_shm::rel::ShmSlice;
    /// # use yanet_sys::{cp::BuiltObject, dp::Object};
    /// # struct Mine;
    /// # impl Object for Mine {
    /// #     const TYPE: &'static str = "mine";
    /// #     type Config = ShmSlice<u64>;
    /// # }
    /// fn sum(agent: &Agent, built: &BuiltObject<Mine, Agent>) -> u64 {
    ///     agent.read(built, |cx, config| config.as_slice(&cx).iter().sum())
    /// }
    /// ```
    pub fn read<O: Object, R>(
        &self,
        built: &BuiltObject<O, Agent>,
        f: impl for<'g> FnOnce(ObjectCx<'g>, &'g O::Config) -> R,
    ) -> R {
        // SAFETY: every agent a process makes handles for is of one mapping
        // (constructor contract), so the memory the object was built in is
        // this agent's.
        let config = unsafe { built.config(self) };
        f(
            ObjectCx {
                root: self.root(),
                _brand: PhantomData,
            },
            config,
        )
    }
}

/// The context a read of an object resolves the edges of its body through,
/// made by [`Agent::read`] for that read alone.
#[derive(Clone, Copy)]
pub struct ObjectCx<'g> {
    root: ShmRoot,
    // Invariant brand: the contexts of two reads never unify, so a reference
    // one read reached is not resolved through another's context.
    _brand: PhantomData<fn(&'g ()) -> &'g ()>,
}

impl fmt::Debug for ObjectCx<'_> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> Result<(), fmt::Error> {
        f.debug_struct("ObjectCx").finish_non_exhaustive()
    }
}

// SAFETY: the root covers the whole mapping (constructor contract of the
// agent). A context is made only by `Agent::read`, under a fresh invariant
// brand, together with the one `'g` reference it hands out: the body of a
// live object, which its build wrote whole. The `Sync` bound does not by
// itself rule out a shared change; what does is that the body is a `Build`
// type, an unsafe trait whose impls are the SDK's (scalars, atomics, `Opaque`
// bytes, the edge types, the C objects it binds, and the derive over those),
// of which only atomics and opaque bytes change through a shared reference.
// The body lives for the read, as the object's handle is borrowed for it and
// a release takes the handle by value.
unsafe impl<'g> Resolve<'g> for ObjectCx<'g> {
    fn shm_root(&self) -> ShmRoot {
        self.root
    }
}

/// The C header of a configuration or an object in the mapping of the agent.
pub fn raw_at<B: Releasable>(agent: &Agent, header: Header) -> *mut B::Raw {
    header_at(agent, header)
}

/// A C header of type `T` in the mapping of the agent.
fn header_at<T>(agent: &Agent, header: Header) -> *mut T {
    agent.root().ptr(header.get())
}

/// The agent whose pointer, an offset from the slot itself, is at `slot` of
/// a live header.
///
/// # Safety
///
/// `slot` is the agent slot of a live module or object header of the mapping
/// of `root`, and the agent it names is live.
unsafe fn owner_at(root: ShmRoot, slot: usize) -> Agent {
    // SAFETY: the slot is inside the live header. It holds the offset of the
    // owning agent from the slot itself, which the shared memory stores in a
    // pointer-typed field.
    let offset = unsafe { root.ptr::<isize>(slot).read() };
    let owner = root.rel::<bindings::agent>(slot, offset);
    // SAFETY: the header names its agent, live (caller contract), of the one
    // mapping.
    unsafe { Agent::from_raw(NonNull::new_unchecked(owner)) }
}

/// A configuration or an object an agent built, with what its C header kind
/// has for a release: the header, its routines and the place of its fields.
pub trait Releasable: Sized {
    /// The C header.
    type Raw;
    /// Offset of the owning agent's slot in the header.
    const AGENT_SLOT: usize;
    /// Offset of the type name field in the header.
    const TYPE_FIELD: usize;
    /// Size of the type name field, the terminator included.
    const TYPE_LEN: usize;
    /// The type name the header of such a handle holds.
    const TYPE_NAME: &'static str;

    /// The handle of the header.
    ///
    /// # Safety
    ///
    /// As [`Built::from_header`].
    unsafe fn from_header(header: Header) -> Self;

    /// Address of the header.
    fn header(&self) -> Header;

    /// C's gate of the destruction of the header kind.
    ///
    /// # Safety
    ///
    /// `raw` is an initialised header of the kind; `err` points at a null
    /// chain.
    unsafe fn try_destroy(raw: *mut Self::Raw, err: *mut *mut bindings::yanet_error) -> c_int;

    /// Releases the handle with everything it owns.
    ///
    /// # Safety
    ///
    /// As [`Built::destroy`].
    unsafe fn destroy(self, owner: &Agent);
}

impl<M: Module> Releasable for Built<M, Agent> {
    type Raw = bindings::cp_module;
    const AGENT_SLOT: usize = offset_of!(bindings::cp_module, entity.agent);
    const TYPE_FIELD: usize = offset_of!(bindings::cp_module, type_);
    const TYPE_LEN: usize = CP_MODULE_TYPE_LEN as usize;
    const TYPE_NAME: &'static str = M::NAME;

    unsafe fn from_header(header: Header) -> Self {
        // SAFETY: the caller's contract.
        unsafe { Built::from_header(header) }
    }

    fn header(&self) -> Header {
        Built::header(self)
    }

    unsafe fn try_destroy(raw: *mut bindings::cp_module, err: *mut *mut bindings::yanet_error) -> c_int {
        // SAFETY: the caller's contract.
        unsafe { bindings::cp_module_try_destroy(raw, err) }
    }

    unsafe fn destroy(self, owner: &Agent) {
        // SAFETY: the caller's contract.
        unsafe { Built::destroy(self, owner) }
    }
}

impl<O: Object> Releasable for BuiltObject<O, Agent> {
    type Raw = bindings::cp_object;
    const AGENT_SLOT: usize = offset_of!(bindings::cp_object, entity.agent);
    const TYPE_FIELD: usize = offset_of!(bindings::cp_object, type_);
    const TYPE_LEN: usize = CP_OBJECT_TYPE_LEN as usize;
    const TYPE_NAME: &'static str = O::TYPE;

    unsafe fn from_header(header: Header) -> Self {
        // SAFETY: the caller's contract.
        unsafe { BuiltObject::from_header(header) }
    }

    fn header(&self) -> Header {
        BuiltObject::header(self)
    }

    unsafe fn try_destroy(raw: *mut bindings::cp_object, err: *mut *mut bindings::yanet_error) -> c_int {
        // SAFETY: the caller's contract.
        unsafe { bindings::cp_object_try_destroy(raw, err) }
    }

    unsafe fn destroy(self, owner: &Agent) {
        // SAFETY: the caller's contract.
        unsafe { BuiltObject::destroy(self, owner) }
    }
}

/// The handle of the header at `raw`, refused as an invalid argument when the
/// header's type name is not the handle's: a header of another type of the
/// same kind, handed in by C.
///
/// # Safety
///
/// `raw` is an initialised header of the handle's kind (a module header for a
/// configuration, an object header for an object) an export of this crate
/// returned and nothing released, a pointer that has the provenance of the
/// whole mapping. Headers carry no kind, so a header of the other kind is not
/// detected. When its type name is the handle's, it is a header of such a
/// handle, built over an [`Agent`].
pub unsafe fn handle_at<B: Releasable>(raw: NonNull<B::Raw>) -> Result<B, ApiError> {
    // SAFETY: the pointer has the provenance of the whole mapping (caller
    // contract).
    let root = unsafe { ShmRoot::from_mapping(raw.cast()) };
    // SAFETY: the type name field lies in the initialised header (caller
    // contract); C wrote it whole.
    let field = unsafe { slice::from_raw_parts(root.ptr::<u8>(raw.addr().get() + B::TYPE_FIELD), B::TYPE_LEN) };
    if CStr::from_bytes_until_nul(field).map(CStr::to_bytes) != Ok(B::TYPE_NAME.as_bytes()) {
        return Err(tagged(
            ErrorKind::InvalidArgument,
            c"the handle is of another module or object type",
        ));
    }
    // SAFETY: the address is a header of this mapping of the handle's type
    // (caller contract).
    Ok(unsafe { B::from_header(Header::at(BlockAddr::new(raw.addr().get()))) })
}

/// Releases the configuration or object whose header is at `raw`, as
/// [`Agent::release`] and [`Agent::release_object`] do, finding the agent that
/// built it in the header.
///
/// # Safety
///
/// `raw` is the header of such a configuration or object built over an
/// [`Agent`] and not released since, a pointer that has the provenance of the
/// whole mapping, as the header pointer of an agent's root. The other
/// requirements are those of [`Agent::from_raw`] and of the handle's destroy.
pub unsafe fn release_at<B: Releasable>(raw: NonNull<B::Raw>) -> Result<(), Retained<B>> {
    // SAFETY: the pointer has the provenance of the whole mapping (caller
    // contract).
    let root = unsafe { ShmRoot::from_mapping(raw.cast()) };
    // SAFETY: the address is a header of this mapping (caller contract).
    let header = unsafe { Header::at(BlockAddr::new(raw.addr().get())) };
    // SAFETY: the header is the one of such a handle (caller contract).
    let built = unsafe { B::from_header(header) };
    let mut chain = ptr::null_mut();
    // SAFETY: the header is an initialised one of this mapping, built by
    // this crate; the out-parameter points at a null chain.
    if unsafe { B::try_destroy(raw.as_ptr(), &mut chain) } != 0 {
        // SAFETY: the chain, if any, is ours alone.
        let error = unsafe { from_c_chain(chain) };
        return Err(Retained { built, error });
    }
    // The agent is read before the header is finalised, as C does. The
    // destruction hold that the successful try-destroy took keeps it live,
    // though a newer agent may have superseded it.
    // SAFETY: the slot is the agent slot of the live header.
    let owner_agent = unsafe { owner_at(root, header.get() + B::AGENT_SLOT) };
    // SAFETY: the successful try-destroy gave the exclusive right to destroy:
    // no generation references the header. The owner is the agent the header
    // names, whose memory the handle was built in.
    unsafe { built.destroy(&owner_agent) };
    // SAFETY: the hold try-destroy took on this agent is returned once, after
    // the last access to its storage.
    unsafe { bindings::agent_destroy_released(owner_agent.as_raw().as_ptr()) };
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
// `Drop` do not panic. `cp_module_init` and `cp_object_init` leave a live
// context in the header over the agent's memory.
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
                header_at::<bindings::cp_module>(self, header),
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
        unsafe { bindings::cp_module_fini(header_at::<bindings::cp_module>(self, header)) };
    }

    unsafe fn init_object_header(&self, header: Header, ty: &str, name: &str) -> Result<(), ApiError> {
        let ty = c_name::<{ CP_OBJECT_TYPE_LEN as usize }>(ty)?;
        let name = c_name::<{ CP_OBJECT_NAME_LEN as usize }>(name)?;
        let mut chain = ptr::null_mut();
        // SAFETY: the header is a block of this agent large enough for it
        // (caller contract), the names are NUL terminated and the
        // out-parameter points at a null chain.
        if unsafe {
            bindings::cp_object_init(
                header_at::<bindings::cp_object>(self, header),
                self.agent.as_ptr(),
                ty.as_ptr().cast::<c_char>(),
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

    unsafe fn fini_object_header(&self, header: Header) {
        // SAFETY: the header was initialised by `init_object_header` and is
        // no longer referenced (caller contract).
        unsafe { bindings::cp_object_fini(header_at::<bindings::cp_object>(self, header)) };
    }

    unsafe fn register_counter(&self, header: Header, name: &str, size: u64) -> Result<u64, ApiError> {
        let name = c_name::<{ COUNTER_NAME_LEN as usize }>(name)?;
        let mut chain = ptr::null_mut();
        // SAFETY: the header is initialised (caller contract), so its
        // registry is; the name is NUL terminated and the out-parameter
        // points at a null chain.
        let id = unsafe {
            bindings::counter_registry_register(
                &raw mut (*header_at::<bindings::cp_module>(self, header)).counter_registry,
                name.as_ptr().cast::<c_char>(),
                size,
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
                header_at::<bindings::cp_module>(self, header),
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

    unsafe fn link_object(&self, header: Header, ty: &str, name: &str) -> Result<u64, ApiError> {
        let ty = c_name::<{ CP_OBJECT_TYPE_LEN as usize }>(ty)?;
        let name = c_name::<{ CP_OBJECT_NAME_LEN as usize }>(name)?;
        let (mut index, mut chain) = (0, ptr::null_mut());
        // SAFETY: the header is initialised (caller contract); the names are
        // NUL terminated and the out-parameters are valid.
        if unsafe {
            bindings::cp_module_link_object(
                header_at::<bindings::cp_module>(self, header),
                ty.as_ptr().cast::<c_char>(),
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
