//! Fixture of the runtime reach test: only what the runtime reads, laid out
//! by hand, as the control plane would, in the block arena.
//!
//! It follows no packet rule of C and no rule of the dataplane.

use alloc::alloc::{alloc_zeroed, dealloc, handle_alloc_error};
use core::{
    alloc::Layout,
    ffi::c_void,
    marker::PhantomData,
    mem::{align_of, size_of, size_of_val},
    ptr::NonNull,
};

use yanet_sdk_testing::BlockArena;
use yanet_sys::{
    bindings,
    dp::{
        __private::{new_module, new_object},
        Module, ModuleConfig, Object,
    },
};

unsafe extern "C" {
    fn free(ptr: *mut c_void);
}

/// Writes `value` at `addr` of a block just allocated in `arena`.
///
/// # Safety
///
/// `addr` must lie in a block of `arena` that holds a `T` there, aligned and
/// referenced by nothing.
pub unsafe fn write<T>(arena: &'static BlockArena, addr: usize, value: T) {
    // SAFETY: the caller states that the block holds a `T` at the address.
    unsafe { arena.root().ptr::<T>(addr).write(value) }
}

/// The handler and commit hook a module's plugin export registers, read from
/// its descriptor as the dataplane's loader reads them.
fn module_hooks<M: Module>() -> (bindings::module_handler, bindings::module_commit_ectx_handler) {
    let module = new_module::<M>();
    assert!(!module.is_null(), "C malloc returned no descriptor");
    // SAFETY: the descriptor was just allocated with C malloc and
    // initialised; it is read here and released with C free, as the loader
    // does.
    unsafe {
        let hooks = ((*module).handler, (*module).commit_ectx_handler);
        free(module.cast());
        hooks
    }
}

/// Runs the commit hook an object type's plugin export registers over the
/// object's context, as the dataplane does before it commits a module
/// context linking the object; a type without a prepared value registers
/// none, and nothing runs.
///
/// # Safety
///
/// The context and the configuration must lie in `arena`, laid out for `O`,
/// the context's prepared buffer allocated when `O` has a prepared value.
pub unsafe fn commit_object<O: Object>(arena: &'static BlockArena, object_ectx: usize, object: usize) {
    let descriptor = new_object::<O>();
    assert!(!descriptor.is_null(), "C malloc returned no descriptor");
    // SAFETY: the descriptor was just allocated with C malloc and
    // initialised; it is read here and released with C free, as the loader
    // does.
    let commit = unsafe {
        let commit = (*descriptor).commit_ectx_handler;
        free(descriptor.cast());
        commit
    };
    if let Some(commit) = commit {
        let root = arena.root();
        // SAFETY: the context lies in the arena, whose root it is, laid out
        // for the type with its buffer (caller contract).
        unsafe { commit(root.ptr(object_ectx), root.ptr(object)) };
    }
}

/// Fake dataplane state for calling a module's handler.
pub struct Ectx<M> {
    pub arena: &'static BlockArena,
    /// Address of the execution context, which the entries take as the root
    /// of the mapping.
    ectx: usize,
    worker: bindings::dp_worker,
    /// The handler the module's descriptor registers.
    handler: bindings::module_handler,
    _module: PhantomData<fn() -> M>,
}

impl<M: Module> Ectx<M> {
    /// Committed context of a module whose configuration starts with the
    /// words, on worker 0; `setup` fills the context before the commit, with
    /// the counter storage and the device targets the module reads, and may
    /// write blocks the body, at the address it is given, points to.
    ///
    /// # Safety
    ///
    /// The words, laid out from the start of the configuration body, must
    /// form a valid `M::Config`, with zeros valid for the rest of it, and the
    /// counter ids in it must be below the number of slots `setup` provides.
    /// The handler must not redirect a packet: the targets have no routing
    /// state.
    pub unsafe fn new(
        config: &[u64],
        setup: impl FnOnce(&'static BlockArena, usize, &mut bindings::module_ectx),
    ) -> Self {
        const {
            assert!(
                align_of::<ModuleConfig<M::Config>>() <= 64,
                "the configuration is aligned beyond a block of the arena"
            )
        };
        let (handler, commit) = module_hooks::<M>();
        assert!(
            size_of_val(config) <= size_of::<M::Config>(),
            "the words exceed the body"
        );
        let arena = BlockArena::new();
        let root = arena.root();
        let alloc = |size: usize| arena.alloc(size).as_ptr().addr();
        let cp_module = alloc(size_of::<ModuleConfig<M::Config>>());
        // SAFETY: inside the configuration block just allocated, whose body
        // holds the words (size checked above).
        unsafe {
            root.ptr::<u64>(cp_module + ModuleConfig::<M::Config>::BODY_OFFSET)
                .copy_from_nonoverlapping(config.as_ptr(), config.len())
        };
        let mut context = bindings::module_ectx {
            abs_cp_module: root.ptr(cp_module),
            ..Default::default()
        };
        if size_of::<M::Prepared<'static>>() != 0 {
            context.abs_module_prepared = root.ptr(alloc(size_of::<M::Prepared<'static>>()));
        }
        setup(arena, cp_module + ModuleConfig::<M::Config>::BODY_OFFSET, &mut context);
        let ectx = alloc(size_of::<bindings::module_ectx>());
        // SAFETY: the context block was just allocated.
        unsafe { write(arena, ectx, context) };
        let commit = commit.expect("a module registers its commit hook");
        // SAFETY: the context lies in the arena, whose root it is, and its
        // configuration is a published one of the module (caller contract).
        unsafe { commit(root.ptr(ectx), root.ptr(cp_module)) };
        Self {
            arena,
            ectx,
            worker: Default::default(),
            handler,
            _module: PhantomData,
        }
    }

    /// The prepared value the commit entry wrote into the context's buffer.
    pub fn prepared(&self) -> &M::Prepared<'static> {
        assert_ne!(0, size_of::<M::Prepared<'static>>(), "a zero-sized value has no buffer");
        let root = self.arena.root();
        // SAFETY: the context and its buffer lie in the arena, committed at
        // construction and written by nothing while the borrow lives.
        unsafe {
            let context = &*root.ptr::<bindings::module_ectx>(self.ectx);
            &*root.ptr::<M::Prepared<'static>>(context.abs_module_prepared.addr())
        }
    }

    /// Runs the following calls on the worker of the given index.
    pub fn set_worker(&mut self, idx: u64) {
        self.worker.idx = idx;
    }

    /// Calls the handler on the packets as the input list of a front.
    ///
    /// The packets are borrowed mutably because the call rewrites their
    /// `next` links.
    pub fn run(&mut self, packets: &mut [Packet]) {
        let mut front = bindings::packet_front::default();
        if let (Some(first), Some(last)) = (packets.first(), packets.last()) {
            for pair in packets.windows(2) {
                // SAFETY: live buffers of the caller, read by nothing else now.
                unsafe { (*pair[0].packet()).next = pair[1].packet() };
            }
            // SAFETY: as above.
            let end = unsafe { &raw mut (*last.packet()).next };
            front.input = bindings::packet_list { first: first.packet(), last: end };
            front.input_count = packets.len() as u64;
            front.input_bytes = u64::from(FRAME) * packets.len() as u64;
        }
        let handler = self.handler.expect("a module registers its handler");
        // SAFETY: the context lies in the arena, whose root it is, committed
        // at construction; the worker, front and packets are live and ours.
        unsafe { handler(&raw mut self.worker, self.arena.root().ptr(self.ectx), &raw mut front) };
    }
}

/// Size of the mbuf at the start of a packet allocation.
const MBUF: usize = size_of::<bindings::rte_mbuf>();

/// Bytes of the frame of every packet.
const FRAME: u16 = 60;

/// Offset of the frame in the buffer: the descriptor shares the buffer, so
/// the packet has no headroom past it.
const DATA_OFF: u16 = size_of::<bindings::packet>() as u16;

/// Layout of a packet allocation.
const LAYOUT: Layout =
    match Layout::from_size_align(MBUF + (DATA_OFF + FRAME) as usize, align_of::<bindings::rte_mbuf>()) {
        Ok(layout) => layout,
        Err(_) => panic!("the packet exceeds the address space"),
    };

/// A non-IP packet over one allocation: the mbuf, then the descriptor and
/// the frame, as the dataplane lays them out.
///
/// The allocation ends with the frame, so an SDK write past it leaves the
/// allocation and Miri reports it.
pub struct Packet {
    base: NonNull<u8>,
}

impl Packet {
    /// Packet whose frame is `first` repeated.
    fn new(first: u8) -> Self {
        // SAFETY: the layout has a non-zero size.
        let base = NonNull::new(unsafe { alloc_zeroed(LAYOUT) }).unwrap_or_else(|| handle_alloc_error(LAYOUT));
        // SAFETY: the mbuf and the descriptor are aligned and the frame lies
        // in the buffer, all inside the zeroed allocation.
        unsafe {
            let buffer = base.add(MBUF);
            let mbuf = bindings::rte_mbuf {
                buf_addr: buffer.as_ptr().cast(),
                buf_len: DATA_OFF + FRAME,
                data_off: DATA_OFF,
                data_len: FRAME,
                pkt_len: FRAME.into(),
                ..Default::default()
            };
            base.cast().write(mbuf);
            let packet = bindings::packet {
                mbuf: base.as_ptr().cast(),
                data_len: FRAME,
                ..Default::default()
            };
            buffer.cast().write(packet);
            buffer.add(DATA_OFF.into()).write_bytes(first, FRAME.into());
        }
        Self { base }
    }

    /// The descriptor, at the start of the buffer.
    fn packet(&self) -> *mut bindings::packet {
        // SAFETY: inside the allocation, where the descriptor was written.
        unsafe { self.base.add(MBUF) }.as_ptr().cast()
    }
}

impl Drop for Packet {
    fn drop(&mut self) {
        // SAFETY: allocated in `new` with this layout; no handler is running.
        unsafe { dealloc(self.base.as_ptr(), LAYOUT) }
    }
}

/// Packets of 60 bytes, the first byte of packet `n` being `n`.
pub fn numbered(count: usize) -> Vec<Packet> {
    (0..count).map(|idx| Packet::new(idx as u8)).collect()
}
