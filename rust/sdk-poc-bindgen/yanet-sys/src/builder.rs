//! Building a module configuration in shared memory on the control plane.
//!
//! The builder allocates `ModuleConfig<M::Config>` with the C allocator,
//! initialises the C module header for the module's layout (refused by C
//! when the loaded dataplane module declares another), initialises every C
//! library object the body declares, runs C inserts on request and
//! validates the whole graph before hand-over.
//! Pointers handed to C are always derived from the owner's raw root, never
//! from a Rust reference.

use core::{
    ffi::{CStr, c_int},
    marker::PhantomData,
    mem::{align_of, size_of},
    ptr::NonNull,
};
use std::{string::String, vec::Vec};

use crate::{
    bindings,
    lpm::Lpm,
    shm::{CObject, Module, ModuleConfig, ShmLayout, ShmRead, ValidationError, Validator, config_layout},
};

/// Control-plane failure with a human-readable message.
#[derive(Debug, PartialEq, Eq)]
pub struct CpError(pub String);

impl core::fmt::Display for CpError {
    fn fmt(&self, f: &mut core::fmt::Formatter<'_>) -> Result<(), core::fmt::Error> {
        f.write_str(&self.0)
    }
}

impl From<ValidationError> for CpError {
    fn from(err: ValidationError) -> Self {
        Self(format!("validation failed: {err}"))
    }
}

/// Memory owner a configuration is built in: an attached agent, or a test
/// arena.
///
/// # Safety
///
/// The safe builder trusts every method: [`Owner::root`] must carry
/// provenance over every block [`Owner::alloc`] returns; `alloc` must return
/// NULL or a zeroed block of at least `size` bytes, aligned to 64 and owned
/// by the caller until freed; [`Owner::reader`] must refuse every address
/// outside memory the owner keeps mapped; the C-facing methods must run the
/// C routines they name on the pointers given. Only the agent and the test
/// arena implement it.
pub unsafe trait Owner {
    /// Raw root whose provenance covers the owner's memory.
    #[doc(hidden)]
    fn root(&self) -> NonNull<u8>;

    /// Zeroed block, NULL when exhausted.
    #[doc(hidden)]
    fn alloc(&self, size: usize) -> *mut u8;

    /// Returns a block.
    ///
    /// # Safety
    ///
    /// `block` must come from [`Owner::alloc`] with the same size.
    #[doc(hidden)]
    unsafe fn free(&self, block: *mut u8, size: usize);

    /// Initialises the C module header for a configuration of the given
    /// layout, refused when the dataplane module declares another one.
    ///
    /// # Safety
    ///
    /// `header` must be a zeroed header inside a block of this owner.
    #[doc(hidden)]
    unsafe fn init_header(
        &self,
        header: *mut bindings::cp_module,
        module: &CStr,
        name: &CStr,
        layout: u64,
    ) -> Result<(), CpError>;

    /// Releases what [`Owner::init_header`] set up.
    ///
    /// # Safety
    ///
    /// `header` must have been initialised by this owner.
    #[doc(hidden)]
    unsafe fn fini_header(&self, header: *mut bindings::cp_module);

    /// Initialises an LPM below the module's memory context.
    ///
    /// # Safety
    ///
    /// `lpm` must be a zeroed LPM inside the block of `header`.
    #[doc(hidden)]
    unsafe fn lpm_init(&self, lpm: *mut bindings::lpm, header: *mut bindings::cp_module) -> c_int;

    /// Inserts a range into an initialised LPM.
    ///
    /// # Safety
    ///
    /// `lpm` must be initialised; keys must be `key_size` bytes.
    #[doc(hidden)]
    unsafe fn lpm_insert(
        &self,
        lpm: *mut bindings::lpm,
        key_size: u8,
        from: *const u8,
        to: *const u8,
        value: u32,
    ) -> c_int;

    /// Frees an initialised LPM.
    ///
    /// # Safety
    ///
    /// `lpm` must be initialised and not used afterwards.
    #[doc(hidden)]
    unsafe fn lpm_free(&self, lpm: *mut bindings::lpm);

    /// Memory validation may read for a configuration with this header.
    ///
    /// # Safety
    ///
    /// `header` must have been initialised by this owner.
    #[doc(hidden)]
    unsafe fn reader(&self, header: *mut bindings::cp_module) -> Result<Box<dyn ShmRead + '_>, CpError>;
}

/// Configuration under construction, owned by the builder until
/// [`ConfigBuilder::finish`] hands it over.
pub struct ConfigBuilder<'a, M: Module, O: Owner> {
    owner: &'a O,
    raw: NonNull<ModuleConfig<M::Config>>,
    header_ready: bool,
    lpms: Vec<CObject>,
    _module: PhantomData<M>,
}

impl<'a, M: Module, O: Owner> ConfigBuilder<'a, M, O> {
    /// Allocates the configuration and initialises its headers and C objects.
    pub fn new(owner: &'a O, name: &str) -> Result<Self, CpError> {
        let name = std::ffi::CString::new(name).map_err(|_| CpError("module name contains NUL".into()))?;
        let module = std::ffi::CString::new(M::NAME).map_err(|_| CpError("module type contains NUL".into()))?;
        let block = owner
            .alloc(size_of::<ModuleConfig<M::Config>>())
            .cast::<ModuleConfig<M::Config>>();
        let raw = NonNull::new(block).ok_or_else(|| CpError("failed to allocate the module config".into()))?;
        let mut builder = Self {
            owner,
            raw,
            header_ready: false,
            lpms: Vec::new(),
            _module: PhantomData,
        };
        let header = builder.header();
        // SAFETY: the zeroed block is ours; the header sits at its start.
        unsafe { owner.init_header(header, &module, &name, config_layout::<M>())? };
        builder.header_ready = true;
        let body = builder.body_addr();
        let mut objects = Vec::new();
        M::Config::visit_c_objects(body, &mut |object| objects.push(object));
        for object in objects {
            match object {
                CObject::Lpm { addr, .. } => {
                    builder.check_inside(addr, size_of::<bindings::lpm>())?;
                    let lpm = owner.root().as_ptr().with_addr(addr).cast();
                    // SAFETY: a zeroed LPM inside our block, reported by the
                    // body's own layout.
                    if unsafe { owner.lpm_init(lpm, header) } != 0 {
                        return Err(CpError("failed to allocate an LPM".into()));
                    }
                    builder.lpms.push(object);
                }
            }
        }
        Ok(builder)
    }

    fn header(&self) -> *mut bindings::cp_module {
        self.raw.as_ptr().cast()
    }

    fn at<T>(&self, offset: usize) -> NonNull<T> {
        let addr = self.raw.as_ptr().addr() + offset;
        // SAFETY: the address is inside our non-null block.
        unsafe { NonNull::new_unchecked(self.owner.root().as_ptr().with_addr(addr).cast()) }
    }

    fn body_addr(&self) -> usize {
        self.raw.as_ptr().addr() + ModuleConfig::<M::Config>::BODY_OFFSET
    }

    fn check_inside(&self, addr: usize, len: usize) -> Result<(), CpError> {
        let start = self.body_addr();
        let end = self.raw.as_ptr().addr() + size_of::<ModuleConfig<M::Config>>();
        if addr >= start && addr + len <= end {
            Ok(())
        } else {
            Err(CpError("the body names a C object outside the configuration".into()))
        }
    }

    /// Address of the configuration block.
    pub fn addr(&self) -> usize {
        self.raw.as_ptr().addr()
    }

    /// Maps the inclusive big-endian range `[from, to]` to `value` in the LPM
    /// field `field` selects, with the C LPM insert.
    pub fn insert<const K: usize>(
        &mut self,
        field: impl FnOnce(&M::Config) -> &Lpm<K>,
        from: &[u8; K],
        to: &[u8; K],
        value: u32,
    ) -> Result<(), CpError> {
        let addr = {
            // SAFETY: every body byte is valid for any pattern and C-mutable
            // ones are opaque; the reference ends before C runs.
            let body = unsafe { self.at::<M::Config>(ModuleConfig::<M::Config>::BODY_OFFSET).as_ref() };
            core::ptr::from_ref(field(body)).addr()
        };
        if !self.lpms.contains(&CObject::Lpm { addr, key_size: K }) {
            return Err(CpError("the selected LPM is not a field of this configuration".into()));
        }
        let lpm = self.owner.root().as_ptr().with_addr(addr).cast();
        // SAFETY: an initialised LPM of our block; keys are K bytes.
        match unsafe { self.owner.lpm_insert(lpm, K as u8, from.as_ptr(), to.as_ptr(), value) } {
            0 => Ok(()),
            _ => Err(CpError("failed to insert an LPM range: out of memory".into())),
        }
    }

    /// Validates the configuration as the dataplane will read it.
    pub fn validate(&self) -> Result<(), CpError> {
        // SAFETY: the header was initialised by this owner.
        let reader = unsafe { self.owner.reader(self.header())? };
        let validator = Validator::new(&*reader);
        validator.check(
            self.addr(),
            size_of::<ModuleConfig<M::Config>>(),
            align_of::<ModuleConfig<M::Config>>(),
        )?;
        M::Config::validate(&validator, self.body_addr())?;
        Ok(())
    }

    /// Validates the configuration and hands it over as a module header.
    pub fn finish(self) -> Result<NonNull<core::ffi::c_void>, CpError> {
        self.validate()?;
        // The configuration now belongs to the caller: skip the unwinding
        // drop, but release the builder's own bookkeeping.
        let mut builder = core::mem::ManuallyDrop::new(self);
        drop(core::mem::take(&mut builder.lpms));
        Ok(builder.raw.cast())
    }
}

impl<M: Module, O: Owner> Drop for ConfigBuilder<'_, M, O> {
    fn drop(&mut self) {
        // SAFETY: a configuration that was never handed over is ours alone;
        // the steps undo the construction in reverse order.
        unsafe {
            for object in self.lpms.iter().rev() {
                let CObject::Lpm { addr, .. } = *object;
                self.owner.lpm_free(self.owner.root().as_ptr().with_addr(addr).cast());
            }
            if self.header_ready {
                self.owner.fini_header(self.header());
            }
            self.owner
                .free(self.raw.as_ptr().cast(), size_of::<ModuleConfig<M::Config>>());
        }
    }
}

/// Releases the C objects and the block of a handed-over configuration.
///
/// # Safety
///
/// `config` must be a configuration of module `M` built by this owner and
/// handed over, with no generation referencing it any more.
pub unsafe fn destroy<M: Module, O: Owner>(owner: &O, config: NonNull<core::ffi::c_void>) {
    let base = config.as_ptr().addr();
    let mut objects = Vec::new();
    M::Config::visit_c_objects(base + ModuleConfig::<M::Config>::BODY_OFFSET, &mut |object| {
        objects.push(object)
    });
    // SAFETY: guaranteed by the caller.
    unsafe {
        for CObject::Lpm { addr, .. } in objects.into_iter().rev() {
            owner.lpm_free(owner.root().as_ptr().with_addr(addr).cast());
        }
        owner.fini_header(owner.root().as_ptr().with_addr(base).cast());
        owner.free(
            owner.root().as_ptr().with_addr(base).cast(),
            size_of::<ModuleConfig<M::Config>>(),
        );
    }
}
