//! A configuration owner over the block arena, for the builder tests under
//! Miri and natively alike.
//!
//! It sets up no C memory context and calls no C. Builds that need an embedded
//! C object are refused. Read built configurations through [`TestOwner::read`]
//! or [`TestOwner::read_module`] and release them through
//! [`TestOwner::release`].

use alloc::{
    string::{String, ToString},
    vec::Vec,
};
use core::{
    cell::{Cell, RefCell},
    error::Error,
    fmt,
};

use yanet_shm::{
    resolve::{Graph, Resolve},
    root::ShmRoot,
    shm::ShmLayout,
};
use yanet_sys::{
    cp::{
        BlockAddr, Build, Built, BuiltObject, Context, Freed, Fresh, Header, Init, Live, LiveContext, ModuleKind,
        ObjectKind, Owner, Place, Record, RecordField, RecordPlace, RecordTaken, Refusal, Retiring,
    },
    dp::{Module, ModuleConfig, Object},
};

use crate::BlockArena;

/// A value whose build invokes a fault-injection callback after initialization.
#[derive(Debug)]
#[repr(transparent)]
pub struct Interruptible<T>(T);

// SAFETY: the wrapper has exactly its field's shared-memory representation.
unsafe impl<T: ShmLayout> ShmLayout for Interruptible<T> {}

impl<T: Build> Interruptible<T> {
    const FIELD: RecordField<T> = RecordField::at(0);
}

// SAFETY: the sole field is the whole wrapper, at offset zero.
unsafe impl<T: Build> Record for Interruptible<T> {
    type RecordParts<'b, W: Owner> = T::Parts<'b, W>;

    fn write_fields<'s, 'b, W: Owner>(
        parts: Self::RecordParts<'b, W>,
        place: RecordPlace<'s, 'b, Self, W>,
    ) -> Result<Init<'s, Self>, Refusal>
    where
        Self: Build,
    {
        place.write(Self::FIELD, parts)
    }

    fn free_fields<W: Owner>(value: RecordTaken<'_, Self, W>) {
        value.free(Self::FIELD);
    }
}

impl<T: Build> Build for Interruptible<T> {
    type Parts<'b, W: Owner> = (T::Parts<'b, W>, fn() -> Result<(), Refusal>);

    fn write<'s, 'b, W: Owner>(
        (parts, interrupt): Self::Parts<'b, W>,
        place: Place<'s, 'b, Self, W>,
    ) -> Result<Init<'s, Self>, Refusal> {
        let receipt = place.write_record(parts)?;
        if let Err(err) = interrupt() {
            let _forgotten = core::mem::ManuallyDrop::new(receipt);
            return Err(err);
        }
        Ok(receipt)
    }
}

/// A call of the owner a test can make fail.
#[derive(Clone, Copy)]
pub enum Call {
    /// Allocating a block, the header's included.
    Alloc,
    /// Creating a registry, registering a counter or linking a device or
    /// object.
    Registration,
    /// Initialising the header of a configuration or an object.
    Header,
}

/// The failure of a routine of the owner.
#[derive(Debug, PartialEq, Eq)]
pub enum TestError {
    /// A registration or a link refused by a planned failure.
    Registration,
    /// A header initialisation refused by a planned failure.
    Header,
}

impl fmt::Display for TestError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> Result<(), fmt::Error> {
        fmt::Debug::fmt(self, f)
    }
}

impl Error for TestError {}

/// Headers an owner initialised and has not finalised, by kind.
#[derive(Debug, PartialEq, Eq)]
pub struct LiveHeaders {
    /// Module configuration headers.
    pub modules: usize,
    /// Object headers.
    pub objects: usize,
}

/// Whether a header is a module configuration's or an object's.
#[derive(Clone, Copy, PartialEq, Eq)]
enum HeaderKind {
    Module,
    Object,
}

/// Owner whose every block is a block of the arena.
pub struct TestOwner {
    arena: &'static BlockArena,
    /// Blocks handed out and not yet returned, with their size and context.
    blocks: RefCell<Vec<(BlockAddr, usize, Option<Context>)>>,
    child_returns: Option<RefCell<Vec<usize>>>,
    /// Headers initialised and not yet finalised, with the kind of each.
    headers: RefCell<Vec<(Header, HeaderKind)>>,
    registered: Cell<u64>,
    /// Workers of the dataplane the owner stands for.
    workers: u64,
    /// Size of each counter registered, in the order of the registrations.
    counter_sizes: RefCell<Vec<u64>>,
    /// Header and tag of each tagged counter registry made, in creation
    /// order. A header's registries are numbered in its own order, as C
    /// numbers them in the module's list.
    counter_registries: RefCell<Vec<(Header, String)>>,
    /// Header, registry, name and size of each counter of a tagged registry,
    /// in the order the names first came; a registry's counters are numbered
    /// in its own order, as C numbers them.
    tagged_counters: RefCell<Vec<(Header, u64, String, u64)>>,
    /// Header, name and size of each counter of an object's own registry, in
    /// the order the names first came; an object's counters are numbered in
    /// its own order, as C numbers them.
    object_counters: RefCell<Vec<(Header, String, u64)>>,
    /// Header, type and name of each object link made, in the order of the
    /// links; a header's links are numbered in its own order, as C numbers
    /// them in the module's object list. The arena never reuses a header's
    /// address, so the links of a finalised header stay recorded.
    object_links: RefCell<Vec<(Header, String, String)>>,
    /// Calls left before each kind fails, for good; `None` never fails.
    countdowns: [Cell<Option<usize>>; 3],
    /// What the builder did wrong; recorded, as the returns run from
    /// destructors.
    violations: RefCell<Vec<&'static str>>,
}

impl TestOwner {
    /// An owner of an empty arena, for a dataplane of one worker.
    pub fn new() -> Self {
        Self {
            arena: BlockArena::new(),
            blocks: RefCell::default(),
            child_returns: None,
            headers: RefCell::default(),
            registered: Cell::new(0),
            workers: 1,
            counter_sizes: RefCell::default(),
            counter_registries: RefCell::default(),
            tagged_counters: RefCell::default(),
            object_counters: RefCell::default(),
            object_links: RefCell::default(),
            countdowns: Default::default(),
            violations: RefCell::default(),
        }
    }

    /// The owner for a dataplane of the given number of workers.
    pub fn with_workers(self, count: u64) -> Self {
        Self { workers: count, ..self }
    }

    /// Records the size of each returned child block, in release order.
    pub fn with_child_return_log(self) -> Self {
        Self {
            child_returns: Some(RefCell::default()),
            ..self
        }
    }

    /// Takes the recorded return sizes. Without logging the list is empty.
    pub fn take_child_returns(&self) -> Vec<usize> {
        self.child_returns.as_ref().map(RefCell::take).unwrap_or_default()
    }

    /// Runs `f` over a live object built by this owner.
    ///
    /// Each call brands the body and resolving context together, as
    /// `Agent::read` does. Neither may escape the callback or be mixed with
    /// another read.
    ///
    /// ```compile_fail
    /// use yanet_sdk_testing::cp::TestOwner;
    /// use yanet_shm::rel::ShmSlice;
    /// use yanet_sys::{cp::BuiltObject, dp::Object};
    ///
    /// fn mixed<O: Object<Config = ShmSlice<u64>>>(
    ///     first_owner: &TestOwner, first: &BuiltObject<O, TestOwner>,
    ///     second_owner: &TestOwner, second: &BuiltObject<O, TestOwner>,
    /// ) -> usize {
    ///     first_owner.read(first, |_, outer| {
    ///         second_owner.read(second, |cx, _| outer.as_slice(&cx).len())
    ///     })
    /// }
    /// ```
    ///
    /// The body cannot escape:
    ///
    /// ```compile_fail
    /// use yanet_sdk_testing::cp::TestOwner;
    /// use yanet_sys::{cp::BuiltObject, dp::Object};
    ///
    /// fn escape<'a, O: Object>(owner: &'a TestOwner, built: &'a BuiltObject<O, TestOwner>)
    ///     -> &'a O::Config
    /// {
    ///     owner.read(built, |_, config| config)
    /// }
    /// ```
    ///
    /// Nor can its context:
    ///
    /// ```compile_fail
    /// use yanet_sdk_testing::cp::{TestCx, TestOwner};
    /// use yanet_sys::{cp::BuiltObject, dp::Object};
    ///
    /// fn escape<'a, O: Object>(owner: &'a TestOwner, built: &'a BuiltObject<O, TestOwner>)
    ///     -> TestCx<'a>
    /// {
    ///     owner.read(built, |cx, _| cx)
    /// }
    /// ```
    ///
    /// # Panics
    ///
    /// The object is not live in this owner.
    pub fn read<O: Object, R>(
        &self,
        built: &BuiltObject<O, TestOwner>,
        f: impl for<'g> FnOnce(TestCx<'g>, &'g O::Config) -> R,
    ) -> R {
        self.assert_live(built.header(), HeaderKind::Object);
        // SAFETY: the live object belongs to this arena, and borrowing its
        // handle prevents release while the callback reads its built body.
        let config = unsafe { built.config(self) };
        // SAFETY: the arena's root retains allocation-wide provenance. The
        // callback's fresh invariant brand confines resolution to this live
        // built graph, frozen except for its shared-state types.
        let graph = unsafe { Graph::new(self.root()) };
        f(TestCx { graph }, config)
    }

    /// Runs `f` over a live module configuration built by this owner.
    ///
    /// As with [`Self::read`], the body and context are confined to the read.
    ///
    /// ```compile_fail
    /// use yanet_sdk_testing::cp::TestOwner;
    /// use yanet_shm::rel::ShmSlice;
    /// use yanet_sys::{cp::Built, dp::Module};
    ///
    /// fn mixed<M: Module<Config = ShmSlice<u64>>>(
    ///     first_owner: &TestOwner, first: &Built<M, TestOwner>,
    ///     second_owner: &TestOwner, second: &Built<M, TestOwner>,
    /// ) -> usize {
    ///     first_owner.read_module(first, |_, outer| {
    ///         second_owner.read_module(second, |cx, _| outer.as_slice(&cx).len())
    ///     })
    /// }
    /// ```
    ///
    /// # Panics
    ///
    /// The configuration is not live in this owner.
    pub fn read_module<M: Module, R>(
        &self,
        built: &Built<M, TestOwner>,
        f: impl for<'g> FnOnce(TestCx<'g>, &'g M::Config) -> R,
    ) -> R {
        self.assert_live(built.header(), HeaderKind::Module);
        let body = built.header().get() + ModuleConfig::<M::Config>::BODY_OFFSET;
        // SAFETY: the live header belongs to this arena and the builder wrote
        // its body whole. The borrowed handle prevents release during `f`.
        let config = unsafe { &*self.root().ptr::<M::Config>(body) };
        // SAFETY: as in read, the arena root covers the live built graph,
        // whose references and context cannot escape the fresh callback brand.
        let graph = unsafe { Graph::new(self.root()) };
        f(TestCx { graph }, config)
    }

    /// Releases a live module configuration or object built by this owner.
    ///
    /// A read borrows the handle, so it cannot release it:
    ///
    /// ```compile_fail,E0505
    /// use yanet_sdk_testing::cp::TestOwner;
    /// use yanet_sys::{cp::Built, dp::Module};
    ///
    /// fn release_while_reading<M: Module>(owner: &TestOwner, built: Built<M, TestOwner>) {
    ///     owner.read_module(&built, |_, _| owner.release(built));
    /// }
    /// ```
    ///
    /// # Panics
    ///
    /// The configuration or object is not live in this owner.
    pub fn release(&self, built: impl TestBuilt) {
        built.release(self);
    }

    fn assert_live(&self, header: Header, kind: HeaderKind) {
        assert!(
            self.headers.borrow().contains(&(header, kind)),
            "the configuration or object is not live in this test owner"
        );
    }

    /// Makes every call of the kind fail after the given number more
    /// succeed.
    pub fn fail_after(&self, call: Call, count: usize) {
        self.countdowns[call as usize].set(Some(count));
    }

    /// Size of each counter registered, in the order of the registrations,
    /// those refused included.
    pub fn counter_sizes(&self) -> Vec<u64> {
        self.counter_sizes.borrow().clone()
    }

    /// Tag of each tagged counter registry made, in creation order.
    /// A registry made for a refused counter remains recorded, as in C.
    pub fn counter_registries(&self) -> Vec<String> {
        self.counter_registries
            .borrow()
            .iter()
            .map(|(_, tag)| tag.clone())
            .collect()
    }

    fn counter_registry(&self, header: Header, tag: &str) -> u64 {
        let mut registries = self.counter_registries.borrow_mut();
        let own = registries.iter().filter(|registry| registry.0 == header);
        match own.clone().position(|registry| registry.1 == tag) {
            Some(index) => index as u64,
            None => {
                let index = own.count() as u64;
                registries.push((header, tag.to_string()));
                index
            }
        }
    }

    /// Type and name of each object link made, in the order of the links;
    /// a pair linked again to the same header is listed once, and a refused
    /// link not at all.
    pub fn object_links(&self) -> Vec<(String, String)> {
        self.object_links
            .borrow()
            .iter()
            .map(|(_, ty, name)| (ty.clone(), name.clone()))
            .collect()
    }

    /// Number of the blocks handed out and not yet returned.
    pub fn live_blocks(&self) -> usize {
        self.blocks.borrow().len()
    }

    /// Sizes of the blocks handed out and not yet returned, in the order of
    /// their allocation but for those returned since.
    pub fn live_block_sizes(&self) -> Vec<usize> {
        self.blocks.borrow().iter().map(|block| block.1).collect()
    }

    /// Headers initialised and not yet finalised, by kind.
    pub fn live_headers(&self) -> LiveHeaders {
        let headers = self.headers.borrow();
        let objects = headers.iter().filter(|(_, kind)| *kind == HeaderKind::Object).count();
        LiveHeaders {
            modules: headers.len() - objects,
            objects,
        }
    }

    /// What the builder did wrong so far, in the order found.
    pub fn violations(&self) -> Vec<&'static str> {
        self.violations.borrow().clone()
    }

    /// Asserts that no block or header is left and the builder broke no rule.
    ///
    /// # Panics
    ///
    /// When a block or a header is left, or the builder broke a rule.
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
        let addr = BlockAddr::new(block.as_ptr().addr());
        self.blocks.borrow_mut().push((addr, size, context));
        Some(addr)
    }

    fn give_back(&self, context: Option<Context>, addr: BlockAddr, size: usize) {
        if context.is_some()
            && let Some(log) = &self.child_returns
        {
            log.borrow_mut().push(size);
        }
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

    fn init(&self, header: Header, kind: HeaderKind) -> Result<(), TestError> {
        (!self.fails(Call::Header))
            .then(|| self.headers.borrow_mut().push((header, kind)))
            .ok_or(TestError::Header)
    }

    /// Finalises a header of the kind, recording a header unknown or of the
    /// other kind, or one whose context has live blocks.
    fn fini(&self, header: Header, kind: HeaderKind) {
        let mut headers = self.headers.borrow_mut();
        let known = headers
            .iter()
            .position(|&known| known == (header, kind))
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
                .push("a header finalised that is unknown, of the other kind or has live blocks");
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

// SAFETY: the arena aligns every block to 64 or 128 as required and keeps it
// for good. Its root covers every block. No dataplane runs over the owner,
// so its worker count indexes nothing. Destructor routines record violations
// rather than panicking. C_MEMORY is false, so embedded C objects are refused
// before opening a context in a header.
unsafe impl Owner for TestOwner {
    const C_MEMORY: bool = false;

    type Error = TestError;

    fn root(&self) -> ShmRoot {
        self.arena.root()
    }

    fn worker_count(&self) -> u64 {
        self.workers
    }

    fn alloc(&self, size: usize) -> Option<BlockAddr> {
        self.take(None, size)
    }

    fn free(block: Freed<'_, Self>) {
        block.owner().give_back(None, block.addr(), block.size());
    }

    fn alloc_in(context: &LiveContext<'_, Self>, size: usize) -> Option<BlockAddr> {
        context.owner().take(Some(context.context()), size)
    }

    fn free_in(block: Freed<'_, Self, Context>) {
        block
            .owner()
            .give_back(Some(block.context()), block.addr(), block.size());
    }

    fn init_header(block: Fresh<'_, Self, ModuleKind>, _module: &str, _name: &str) -> Result<(), TestError> {
        block.owner().init(block.header(), HeaderKind::Module)
    }

    fn fini_header(header: Retiring<'_, Self, ModuleKind>) {
        header.owner().fini(header.header(), HeaderKind::Module);
    }

    fn init_object_header(block: Fresh<'_, Self, ObjectKind>, _ty: &str, _name: &str) -> Result<(), TestError> {
        block.owner().init(block.header(), HeaderKind::Object)
    }

    fn fini_object_header(header: Retiring<'_, Self, ObjectKind>) {
        header.owner().fini(header.header(), HeaderKind::Object);
    }

    fn register_counter(header: &Live<'_, Self, ModuleKind>, _name: &str, size: u64) -> Result<u64, TestError> {
        let owner = header.owner();
        owner.counter_sizes.borrow_mut().push(size);
        owner.register()
    }

    fn ensure_counter_registry(header: &Live<'_, Self, ModuleKind>, tag: &str) -> Result<u64, TestError> {
        let owner = header.owner();
        if owner.fails(Call::Registration) {
            return Err(TestError::Registration);
        }
        Ok(owner.counter_registry(header.header(), tag))
    }

    fn register_counter_in(
        header: &Live<'_, Self, ModuleKind>,
        tag: &str,
        name: &str,
        size: u64,
    ) -> Result<(u64, u64), TestError> {
        let owner = header.owner();
        let header = header.header();
        let registry = owner.counter_registry(header, tag);
        owner.counter_sizes.borrow_mut().push(size);
        if owner.fails(Call::Registration) {
            return Err(TestError::Registration);
        }
        // The counters of a registry are numbered from zero, and a name met
        // before gets its id back, refused under another size.
        let mut counters = owner.tagged_counters.borrow_mut();
        let mut own = counters
            .iter()
            .filter(|counter| counter.0 == header && counter.1 == registry);
        match own.clone().position(|counter| counter.2 == name) {
            Some(id) if own.nth(id).is_some_and(|counter| counter.3 == size) => Ok((registry, id as u64)),
            Some(..) => Err(TestError::Registration),
            None => {
                let id = own.count() as u64;
                counters.push((header, registry, name.to_string(), size));
                Ok((registry, id))
            }
        }
    }

    fn register_object_counter(header: &Live<'_, Self, ObjectKind>, name: &str, size: u64) -> Result<u64, TestError> {
        let owner = header.owner();
        let header = header.header();
        owner.counter_sizes.borrow_mut().push(size);
        if owner.fails(Call::Registration) {
            return Err(TestError::Registration);
        }
        // The counters of an object are numbered from zero, apart from the
        // modules' counters, and a name met before gets its id back, refused
        // under another size.
        let mut counters = owner.object_counters.borrow_mut();
        let mut own = counters.iter().filter(|counter| counter.0 == header);
        match own.clone().position(|counter| counter.1 == name) {
            Some(id) if own.nth(id).is_some_and(|counter| counter.2 == size) => Ok(id as u64),
            Some(..) => Err(TestError::Registration),
            None => {
                let id = own.count() as u64;
                counters.push((header, name.to_string(), size));
                Ok(id)
            }
        }
    }

    fn link_device(header: &Live<'_, Self, ModuleKind>, _name: &str) -> Result<u64, TestError> {
        header.owner().register()
    }

    fn link_object(header: &Live<'_, Self, ModuleKind>, ty: &str, name: &str) -> Result<u64, TestError> {
        let owner = header.owner();
        let header = header.header();
        // The links of a header are numbered from zero, apart from its
        // counters and devices, and a pair linked before gets its index again
        // with no call that may fail, as `cp_module_link_object` does.
        let mut links = owner.object_links.borrow_mut();
        let own = links.iter().filter(|link| link.0 == header);
        if let Some(index) = own
            .clone()
            .position(|link| (link.1.as_str(), link.2.as_str()) == (ty, name))
        {
            return Ok(index as u64);
        }
        let index = own.count() as u64;
        if owner.fails(Call::Registration) {
            return Err(TestError::Registration);
        }
        links.push((header, ty.to_string(), name.to_string()));
        Ok(index)
    }
}

impl Default for TestOwner {
    fn default() -> Self {
        Self::new()
    }
}

/// The resolving context of a test read, branded with its built body.
#[derive(Clone, Copy)]
pub struct TestCx<'g> {
    graph: Graph<'g>,
}

impl<'g> Resolve<'g> for TestCx<'g> {
    fn graph(&self) -> Graph<'g> {
        self.graph
    }
}

/// A module configuration or object built over a [`TestOwner`].
///
/// Sealed to the two builder handle types.
pub trait TestBuilt: sealed::Sealed {}

impl<M: Module> TestBuilt for Built<M, TestOwner> {}
impl<O: Object> TestBuilt for BuiltObject<O, TestOwner> {}

mod sealed {
    use super::{Built, BuiltObject, HeaderKind, Module, Object, TestOwner};

    pub trait Sealed {
        fn release(self, owner: &TestOwner);
    }

    impl<M: Module> Sealed for Built<M, TestOwner> {
        fn release(self, owner: &TestOwner) {
            owner.assert_live(self.header(), HeaderKind::Module);
            // SAFETY: this live module belongs to the owner, has never been
            // published, and consuming the handle excludes outstanding reads.
            unsafe { self.destroy(owner) };
        }
    }

    impl<O: Object> Sealed for BuiltObject<O, TestOwner> {
        fn release(self, owner: &TestOwner) {
            owner.assert_live(self.header(), HeaderKind::Object);
            // SAFETY: this live object belongs to the owner, has never been
            // published, and consuming the handle excludes outstanding reads.
            unsafe { self.destroy(owner) };
        }
    }
}
