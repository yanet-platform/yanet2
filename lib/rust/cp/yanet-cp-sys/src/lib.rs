//! The unsafe boundary of the Rust control plane: the agent's shared memory
//! and the C ABI the Go control plane calls.
//!
//! The crate holds every `unsafe` block the control-plane api crates need
//! and no device logic. Shared memory is reached through raw pointers C
//! hands over and read only at C-reported offsets of the agent, inside a
//! device block this control plane allocated, or inside the agent's arenas
//! through a [`Validator`]; relative slots resolve with strict provenance.
//! The C-ABI helpers in [`abi`] turn caller pointers into Rust values,
//! write results only into caller buffers, and catch every panic.

use core::{
    ffi::{CStr, c_char, c_int, c_void},
    marker::PhantomData,
    mem::size_of,
    num::NonZeroUsize,
    ops::Range,
    ptr::NonNull,
};

use yanet_shm::{ItemLayout, ShmError, ShmLayout, Validator, item_layout};

/// Hand-written declarations of the C shim, which meson builds and links
/// next to the archive.
pub mod raw {
    use core::ffi::{c_char, c_int, c_void};

    /// Sizes and field offsets of the C structures Rust walks.
    #[repr(C)]
    #[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
    pub struct Layout {
        pub cp_device_size: u64,
        pub cp_device_align: u64,
        pub cp_device_agent: u64,
        pub cp_device_input: u64,
        pub cp_device_output: u64,
        pub agent_arena_count: u64,
        pub agent_arenas: u64,
        pub arena_size: u64,
        pub arena_data: u64,
        pub arena_len: u64,
        pub device_name_len: u64,
        pub pipeline_name_len: u64,
    }

    #[repr(C)]
    pub struct Pipeline {
        pub name: *const c_char,
        pub weight: u64,
    }

    #[repr(C)]
    pub struct DeviceRequest {
        pub type_name: *const c_char,
        pub name: *const c_char,
        pub size: u64,
        pub config_layout: u64,
        pub input: *const Pipeline,
        pub input_count: u64,
        pub output: *const Pipeline,
        pub output_count: u64,
    }

    unsafe extern "C" {
        pub fn yanet_cp_shim_layout(layout: *mut Layout);
        pub fn yanet_cp_shim_device_new(
            agent: *mut c_void,
            request: *const DeviceRequest,
            precondition: *mut i32,
            err: *mut c_char,
            err_len: u64,
        ) -> *mut c_void;
        pub fn yanet_cp_shim_device_free(device: *mut c_void, size: u64, err: *mut c_char, err_len: u64) -> c_int;
        pub fn yanet_cp_shim_device_read(
            agent: *mut c_void,
            type_name: *const c_char,
            name: *const c_char,
            config_layout: u64,
            offset: u64,
            out: *mut c_void,
            len: u64,
        ) -> c_int;
    }
}

/// C layout facts, obtainable only from the C shim.
///
/// The fields stay private, so the offsets the unsafe code reads at come
/// from the C compiler and not from safe callers.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Layout(raw::Layout);

impl Layout {
    /// Size of the common device header.
    pub fn cp_device_size(&self) -> usize {
        self.0.cp_device_size as usize
    }

    /// Alignment of the common device header.
    pub fn cp_device_align(&self) -> usize {
        self.0.cp_device_align as usize
    }

    /// Offset of the owner agent's relative slot in the device header.
    pub fn cp_device_agent(&self) -> usize {
        self.0.cp_device_agent as usize
    }

    /// Offset of the input entry's relative slot in the device header.
    pub fn cp_device_input(&self) -> usize {
        self.0.cp_device_input as usize
    }

    /// Offset of the output entry's relative slot in the device header.
    pub fn cp_device_output(&self) -> usize {
        self.0.cp_device_output as usize
    }

    /// Capacity of a device name, the terminating zero included.
    pub fn device_name_len(&self) -> usize {
        self.0.device_name_len as usize
    }

    /// Capacity of a pipeline name, the terminating zero included.
    pub fn pipeline_name_len(&self) -> usize {
        self.0.pipeline_name_len as usize
    }
}

/// Returns the C layout facts.
pub fn c_layout() -> Layout {
    let mut layout = raw::Layout::default();
    // SAFETY: the shim fills the struct and has no other effect.
    unsafe { raw::yanet_cp_shim_layout(&mut layout) };
    Layout(layout)
}

/// Bytes of C error messages the shim may write.
const ERR_LEN: usize = 512;

fn message(buf: &[u8]) -> String {
    let end = buf.iter().position(|&byte| byte == 0).unwrap_or(buf.len());
    String::from_utf8_lossy(&buf[..end]).into_owned()
}

/// A word-aligned read of a 64-bit field at an address of the mapping.
///
/// # Safety
///
/// The address must be readable and aligned inside the root's mapping.
unsafe fn read_u64(root: NonNull<u8>, addr: usize) -> u64 {
    let addr = NonZeroUsize::new(addr).expect("non-null shared memory address");
    // SAFETY: per the caller contract.
    unsafe { root.with_addr(addr).cast::<u64>().read() }
}

/// The agent this control plane attached as, in shared memory, with the C
/// layout its structures follow.
pub struct Agent {
    root: NonNull<u8>,
    layout: Layout,
    // False for a fixture agent, which has no C agent behind it.
    shim: bool,
}

impl Agent {
    /// Wraps the agent pointer the Go control plane passes in.
    ///
    /// # Safety
    ///
    /// The pointer must be a live attached agent whose provenance covers the
    /// whole shared mapping, for as long as the returned value lives; the
    /// device blocks it creates borrow it.
    pub unsafe fn from_raw(ptr: *mut c_void) -> Option<Self> {
        Some(Self {
            root: NonNull::new(ptr.cast())?,
            layout: c_layout(),
            shim: true,
        })
    }

    /// The C layout of the agent's structures.
    pub fn layout(&self) -> &Layout {
        &self.layout
    }

    /// Address of the agent.
    pub fn addr(&self) -> usize {
        self.root.addr().get()
    }

    /// Address ranges of the agent's arenas: the memory every block the
    /// agent allocates lies in.
    pub fn arenas(&self) -> Vec<Range<usize>> {
        let layout = &self.layout.0;
        let base = self.addr();
        // SAFETY: fields of the live agent at C-reported offsets.
        let count = unsafe { read_u64(self.root, base + layout.agent_arena_count as usize) };
        let slot = base + layout.agent_arenas as usize;
        // SAFETY: as above.
        let table = slot.wrapping_add(unsafe { read_u64(self.root, slot) } as usize);
        (0..count as usize)
            .map(|idx| {
                let entry = table + idx * layout.arena_size as usize;
                let data_slot = entry + layout.arena_data as usize;
                // SAFETY: the arena table is an array of count entries the
                // agent owns; the data field is relative to its own slot.
                let (data, len) = unsafe {
                    (
                        data_slot.wrapping_add(read_u64(self.root, data_slot) as usize),
                        read_u64(self.root, entry + layout.arena_len as usize) as usize,
                    )
                };
                data..data.saturating_add(len)
            })
            .collect()
    }

    /// Runs a check with a validator over the agent's arenas.
    pub fn validate<R>(&self, check: impl FnOnce(&mut Validator<'_>) -> R) -> R {
        let arenas = self.arenas();
        // SAFETY: the arenas come from the agent itself, are part of the
        // mapping the agent pointer covers, and stay mapped while the agent
        // is attached.
        let mut validator = unsafe { Validator::new(self.root, &arenas) };
        check(&mut validator)
    }

    /// The layout of a device with body `B` after this agent's C header.
    pub fn device_layout<B: ShmLayout>(&self) -> ItemLayout {
        item_layout::<B>(self.layout.cp_device_size(), self.layout.cp_device_align())
    }

    /// Allocates a device block for body `B` and initializes its common
    /// header.
    ///
    /// The block size, the body offset and the configuration layout all
    /// follow from `B`, so the C device init admits the block only for a
    /// device type loaded with `B`'s fingerprint.
    pub fn create_device<B: ShmLayout>(&self, request: &DeviceRequest<'_>) -> Result<DeviceBlock<'_>, CError> {
        const { assert!(B::FINGERPRINT != 0, "the zero layout is the one of C devices") };
        if !self.shim {
            return Err(CError {
                precondition: true,
                message: "a fixture agent cannot create devices".into(),
            });
        }
        let item = self.device_layout::<B>();
        let pipelines = |bindings: &[(&CStr, u64)]| -> Vec<raw::Pipeline> {
            bindings
                .iter()
                .map(|(name, weight)| raw::Pipeline { name: name.as_ptr(), weight: *weight })
                .collect()
        };
        let input = pipelines(request.input);
        let output = pipelines(request.output);
        let raw_request = raw::DeviceRequest {
            type_name: request.type_name.as_ptr(),
            name: request.name.as_ptr(),
            size: item.size as u64,
            config_layout: B::FINGERPRINT,
            input: input.as_ptr(),
            input_count: input.len() as u64,
            output: output.as_ptr(),
            output_count: output.len() as u64,
        };
        let mut err = [0u8; ERR_LEN];
        let mut precondition = 0i32;
        // SAFETY: the agent is live, the request and its strings outlive
        // the call, and the flag and error buffers are writable.
        let device = unsafe {
            raw::yanet_cp_shim_device_new(
                self.root.as_ptr().cast(),
                &raw_request,
                &mut precondition,
                err.as_mut_ptr().cast(),
                ERR_LEN as u64,
            )
        };
        match NonNull::new(device.cast::<u8>()) {
            Some(root) => Ok(DeviceBlock {
                root,
                size: item.size,
                body_offset: item.body_offset,
                _agent: PhantomData,
            }),
            None => Err(CError {
                precondition: precondition != 0,
                message: message(&err),
            }),
        }
    }

    /// Reads the body `B` of the live device with the given type and name.
    ///
    /// The shim refuses a device whose type was loaded for another layout
    /// than `B`'s, so the block holds a `B` at the offset read.
    pub fn read_live<B: ShmLayout>(&self, type_name: &CStr, name: &CStr) -> Result<B, ReadError> {
        const { assert!(!B::HAS_REL, "values with relative pointers stay in shared memory") };
        const { assert!(B::FINGERPRINT != 0, "the zero layout is the one of C devices") };
        if !self.shim {
            return Err(ReadError::NotFound);
        }
        let item = self.device_layout::<B>();
        let mut bytes = vec![0u8; size_of::<B>()];
        // SAFETY: the agent is live, the strings outlive the call, and the
        // shim copies exactly the buffer's length into it.
        let rc: c_int = unsafe {
            raw::yanet_cp_shim_device_read(
                self.root.as_ptr().cast(),
                type_name.as_ptr(),
                name.as_ptr(),
                B::FINGERPRINT,
                item.body_offset as u64,
                bytes.as_mut_ptr().cast(),
                bytes.len() as u64,
            )
        };
        match rc {
            0 => Ok(yanet_shm::from_bytes(&bytes).expect("the buffer holds one body")),
            1 => Err(ReadError::NotFound),
            _ => Err(ReadError::Layout),
        }
    }
}

/// Why a live device body was not read.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum ReadError {
    /// The active generation has no such device.
    NotFound,
    /// The device type was loaded for another configuration layout.
    Layout,
}

/// A refusal reported by the C side.
#[derive(Debug)]
pub struct CError {
    /// The system is not in the state the call needs, such as a dataplane
    /// without the device type or with another configuration layout.
    pub precondition: bool,
    pub message: String,
}

/// What a device block is created with.
pub struct DeviceRequest<'a> {
    pub type_name: &'a CStr,
    pub name: &'a CStr,
    pub input: &'a [(&'a CStr, u64)],
    pub output: &'a [(&'a CStr, u64)],
}

/// A device block in the agent's memory, created by this control plane and
/// not yet destroyed; it borrows the agent that maps it.
pub struct DeviceBlock<'a> {
    root: NonNull<u8>,
    size: usize,
    body_offset: usize,
    _agent: PhantomData<&'a Agent>,
}

/// Why a device block was not destroyed.
pub enum FreeError<'a> {
    /// A live generation still references the device; the block is intact.
    StillReferenced(DeviceBlock<'a>),
    /// The C side refused with a message.
    Failed(String),
}

impl<'a> DeviceBlock<'a> {
    /// Wraps a device pointer handed back by the Go control plane.
    ///
    /// # Safety
    ///
    /// The pointer must come from [`DeviceBlock::into_raw`] of a block of
    /// `size` bytes that was not destroyed since, and must carry provenance
    /// for the shared mapping that stays mapped for `'a`. The block may be
    /// published, so the returned value gives no access to the body.
    pub unsafe fn from_raw(ptr: *mut c_void, size: usize) -> Option<Self> {
        Some(Self {
            root: NonNull::new(ptr.cast())?,
            size,
            body_offset: size,
            _agent: PhantomData,
        })
    }

    /// Hands the block over as the C device pointer.
    pub fn into_raw(self) -> *mut c_void {
        self.root.as_ptr().cast()
    }

    /// Address of the block.
    pub fn addr(&self) -> usize {
        self.root.addr().get()
    }

    /// Size of the block.
    pub fn size(&self) -> usize {
        self.size
    }

    fn check(&self, range: Range<usize>, offset: usize, size: usize, align: usize) -> Result<NonNull<u8>, ShmError> {
        let addr = self.addr() + offset;
        let end = offset.checked_add(size).ok_or(ShmError::OutOfBounds { addr })?;
        if offset < range.start || end > range.end {
            return Err(ShmError::OutOfBounds { addr });
        }
        if addr % align != 0 {
            return Err(ShmError::Misaligned { addr });
        }
        // SAFETY: inside the block, checked above.
        Ok(unsafe { self.root.add(offset) })
    }

    fn body(&self) -> Range<usize> {
        self.body_offset..self.size
    }

    /// Writes a value inside the body.
    ///
    /// The block is not published yet, so this control plane is its only
    /// user; the C header before the body is never written from Rust.
    pub fn write<T: ShmLayout>(&mut self, offset: usize, value: T) -> Result<(), ShmError> {
        let at = self.check(self.body(), offset, size_of::<T>(), align_of::<T>())?;
        // SAFETY: in bounds and aligned; nothing else accesses an
        // unpublished block.
        unsafe { at.cast::<T>().write(value) };
        Ok(())
    }

    /// Reads a value inside the body.
    ///
    /// A value holding relative pointers is refused at compile time: a copy
    /// outside shared memory would resolve against foreign addresses.
    pub fn read<T: ShmLayout>(&self, offset: usize) -> Result<T, ShmError> {
        const { assert!(!T::HAS_REL, "values with relative pointers stay in shared memory") };
        let at = self.check(self.body(), offset, size_of::<T>(), align_of::<T>())?;
        // SAFETY: in bounds and aligned; every bit pattern is a valid T.
        Ok(unsafe { at.cast::<T>().read() })
    }

    /// Reads the target address of the relative slot at the given offset,
    /// without dereferencing it. `None` for null.
    pub fn rel_target(&self, offset: usize) -> Result<Option<usize>, ShmError> {
        let at = self.check(0..self.size, offset, size_of::<u64>(), align_of::<u64>())?;
        // SAFETY: in bounds and aligned.
        let value = unsafe { at.cast::<u64>().read() };
        Ok((value != 0).then(|| at.addr().get().wrapping_add(value as usize)))
    }

    /// Destroys the block, unless a live generation still references it.
    pub fn free(self) -> Result<(), FreeError<'a>> {
        let mut err = [0u8; ERR_LEN];
        // SAFETY: the block is a device this control plane created; the
        // error buffer is writable for its length.
        let rc = unsafe {
            raw::yanet_cp_shim_device_free(
                self.root.as_ptr().cast(),
                self.size as u64,
                err.as_mut_ptr().cast(),
                ERR_LEN as u64,
            )
        };
        match rc {
            0 => Ok(()),
            1 => Err(FreeError::StillReferenced(self)),
            _ => Err(FreeError::Failed(message(&err))),
        }
    }
}

/// Helpers for `extern "C"` functions called from Go.
pub mod abi {
    use core::panic::AssertUnwindSafe;
    use std::panic::catch_unwind;

    use super::{CStr, c_char};

    /// Status codes returned across the C ABI.
    pub const OK: i32 = 0;
    pub const INVALID_ARGUMENT: i32 = 1;
    pub const NOT_FOUND: i32 = 2;
    pub const STILL_REFERENCED: i32 = 3;
    pub const FAILED: i32 = 4;
    pub const PANIC: i32 = 5;
    pub const FAILED_PRECONDITION: i32 = 6;

    /// An error to report through a status code and a message.
    pub struct Failure {
        pub code: i32,
        pub message: String,
    }

    impl Failure {
        pub fn new(code: i32, message: impl Into<String>) -> Self {
            Self { code, message: message.into() }
        }
    }

    /// Runs an exported function body: catches panics, writes the message
    /// of a failure into the caller buffer and returns the status code.
    ///
    /// # Safety
    ///
    /// `err` must be null or writable for `err_len` bytes.
    pub unsafe fn guard(err: *mut c_char, err_len: usize, body: impl FnOnce() -> Result<(), Failure>) -> i32 {
        let failure = match catch_unwind(AssertUnwindSafe(body)) {
            Ok(Ok(())) => {
                // SAFETY: per the caller contract.
                unsafe { write_message(err, err_len, "") };
                return OK;
            }
            Ok(Err(failure)) => failure,
            Err(_) => Failure::new(PANIC, "panic in the Rust control plane"),
        };
        // SAFETY: per the caller contract.
        unsafe { write_message(err, err_len, &failure.message) };
        failure.code
    }

    /// Writes a zero-terminated message, truncated to the buffer.
    unsafe fn write_message(err: *mut c_char, err_len: usize, message: &str) {
        if err.is_null() || err_len == 0 {
            return;
        }
        let len = message.len().min(err_len - 1);
        // SAFETY: the buffer is writable for err_len bytes; len + 1 fits.
        unsafe {
            core::ptr::copy_nonoverlapping(message.as_ptr(), err.cast::<u8>(), len);
            err.add(len).write(0);
        }
    }

    /// Reads a zero-terminated string argument.
    ///
    /// # Safety
    ///
    /// `ptr` must be null or point to a zero-terminated string that stays
    /// unchanged during the call.
    pub unsafe fn cstr<'a>(ptr: *const c_char) -> Result<&'a CStr, Failure> {
        if ptr.is_null() {
            return Err(Failure::new(INVALID_ARGUMENT, "null string argument"));
        }
        // SAFETY: per the caller contract.
        Ok(unsafe { CStr::from_ptr(ptr) })
    }

    /// Reads a struct argument.
    ///
    /// # Safety
    ///
    /// `ptr` must be null or point to a valid, initialized `T`.
    pub unsafe fn read<T: Copy>(ptr: *const T) -> Result<T, Failure> {
        if ptr.is_null() {
            return Err(Failure::new(INVALID_ARGUMENT, "null struct argument"));
        }
        // SAFETY: per the caller contract.
        Ok(unsafe { ptr.read() })
    }

    /// Reads an array argument; a zero length accepts a null pointer.
    ///
    /// # Safety
    ///
    /// `ptr` must point to `len` valid values that stay unchanged during the
    /// call, unless `len` is zero.
    pub unsafe fn slice<'a, T>(ptr: *const T, len: usize) -> Result<&'a [T], Failure> {
        if len == 0 {
            return Ok(&[]);
        }
        if ptr.is_null() {
            return Err(Failure::new(INVALID_ARGUMENT, "null array argument"));
        }
        // SAFETY: per the caller contract.
        Ok(unsafe { core::slice::from_raw_parts(ptr, len) })
    }

    /// Writes a result into a caller-provided slot.
    ///
    /// # Safety
    ///
    /// `ptr` must be null or writable for one `T`.
    pub unsafe fn write<T>(ptr: *mut T, value: T) -> Result<(), Failure> {
        if ptr.is_null() {
            return Err(Failure::new(INVALID_ARGUMENT, "null result argument"));
        }
        // SAFETY: per the caller contract.
        unsafe { ptr.write(value) };
        Ok(())
    }
}

#[cfg(any(test, feature = "fixture"))]
pub mod fixture;

#[cfg(test)]
mod tests;
