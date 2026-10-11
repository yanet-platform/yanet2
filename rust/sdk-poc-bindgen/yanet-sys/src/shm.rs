//! Generic shared-memory layout of module configurations.
//!
//! A module declares its configuration body as a `#[repr(C)]` struct and
//! derives [`ShmLayout`] with the audited SDK derive. The trait is `unsafe`
//! and implemented only by that derive and by this crate for primitives,
//! relative pointers and C library types, so every implementor accepts any
//! bit pattern and describes all of its relative edges. The sys crate knows
//! no module body: it only wraps one in [`ModuleConfig`], behind the opaque
//! C module header; the [`Module`] trait binds the body type to a module
//! name and a layout identity the dataplane checks at configuration
//! creation.

use core::{
    cell::{RefCell, UnsafeCell},
    fmt::{self, Display, Formatter},
    marker::PhantomPinned,
    mem::{MaybeUninit, align_of, size_of},
    ptr::NonNull,
};
use std::collections::BTreeSet;

use crate::{
    bindings,
    rel::{RelPtr, RelRef, Resolver},
};

/// Bytes C may write while Rust holds references to the enclosing struct.
///
/// The `UnsafeCell` tells the compiler a shared reference does not freeze
/// these bytes, so a reference over a struct embedding a C-mutable header
/// stays sound while C rewrites it. Rust never reads through it.
#[repr(transparent)]
pub struct Opaque<T> {
    _value: UnsafeCell<MaybeUninit<T>>,
    _pinned: PhantomPinned,
}

/// Layout of a type that may live in a published module configuration.
///
/// # Safety
///
/// Implementors must be `repr(C)` (or a primitive) with no invalid bit
/// pattern, so any bytes in shared memory form a valid value; every
/// relative edge inside must be checked by [`ShmLayout::validate`] and
/// every C library object reported by [`ShmLayout::visit_c_objects`];
/// [`ShmLayout::FINGERPRINT`] must change with any change of field types,
/// offsets or size. Only the SDK derive and this crate implement it.
pub unsafe trait ShmLayout: Sized + 'static {
    /// Structural fingerprint of the layout.
    const FINGERPRINT: u64;

    /// Validates a value stored at `addr` and everything it points to.
    fn validate(v: &Validator<'_>, addr: usize) -> Result<(), ValidationError>;

    /// Reports the C library objects embedded in a value at `addr`.
    fn visit_c_objects(addr: usize, out: &mut dyn FnMut(CObject)) {
        let _ = (addr, out);
    }
}

/// C library object embedded in a configuration body.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum CObject {
    /// An LPM header with the given key size.
    Lpm { addr: usize, key_size: usize },
}

/// Mixes one value into a structural fingerprint.
pub const fn fingerprint_mix(hash: u64, value: u64) -> u64 {
    let mut x = hash ^ value;
    x = x.wrapping_mul(0x0000_0100_0000_01b3);
    x ^= x >> 29;
    x.wrapping_mul(0xbf58_476d_1ce4_e5b9)
}

/// Starts the fingerprint of a struct of the given size and alignment.
pub const fn fingerprint_struct(size: usize, align: usize) -> u64 {
    fingerprint_mix(fingerprint_mix(0x5348_4d53_5452_5543, size as u64), align as u64)
}

/// Adds one field at `offset` to a struct fingerprint.
pub const fn fingerprint_field(hash: u64, offset: usize, field: u64) -> u64 {
    fingerprint_mix(fingerprint_mix(hash, offset as u64), field)
}

macro_rules! primitive {
    ($($ty:ty => $tag:expr),* $(,)?) => {$(
        // SAFETY: a primitive integer accepts every bit pattern and holds
        // no edge.
        unsafe impl ShmLayout for $ty {
            const FINGERPRINT: u64 = fingerprint_mix(0x494e_5400, $tag);

            fn validate(_: &Validator<'_>, _: usize) -> Result<(), ValidationError> {
                Ok(())
            }
        }
    )*};
}

primitive! {
    u8 => 1, u16 => 2, u32 => 4, u64 => 8, usize => 9,
    i8 => 0x81, i16 => 0x82, i32 => 0x84, i64 => 0x88, isize => 0x89,
}

// SAFETY: an array of layout types is a layout type; elements are
// validated and visited one by one.
unsafe impl<T: ShmLayout, const N: usize> ShmLayout for [T; N] {
    const FINGERPRINT: u64 = fingerprint_mix(fingerprint_mix(0x4152_5241_5900, N as u64), T::FINGERPRINT);

    fn validate(v: &Validator<'_>, addr: usize) -> Result<(), ValidationError> {
        for idx in 0..N {
            T::validate(v, addr + idx * size_of::<T>())?;
        }
        Ok(())
    }

    fn visit_c_objects(addr: usize, out: &mut dyn FnMut(CObject)) {
        for idx in 0..N {
            T::visit_c_objects(addr + idx * size_of::<T>(), out);
        }
    }
}

// SAFETY: a relative pointer is a plain offset; the edge is checked before
// its target is validated.
unsafe impl<T: ShmLayout> ShmLayout for RelPtr<T> {
    const FINGERPRINT: u64 = fingerprint_mix(0x5245_4c50_5452, T::FINGERPRINT);

    fn validate(v: &Validator<'_>, addr: usize) -> Result<(), ValidationError> {
        match v.edge(addr)? {
            None => Ok(()),
            Some(target) => v.target::<T>(target, 1),
        }
    }
}

// SAFETY: as for the nullable pointer, with NULL rejected.
unsafe impl<T: ShmLayout> ShmLayout for RelRef<T> {
    const FINGERPRINT: u64 = fingerprint_mix(0x5245_4c52_4546, T::FINGERPRINT);

    fn validate(v: &Validator<'_>, addr: usize) -> Result<(), ValidationError> {
        match v.edge(addr)? {
            None => Err(ValidationError::new(addr, "a non-null relative pointer is NULL")),
            Some(target) => v.target::<T>(target, 1),
        }
    }
}

/// Relative pointer to `len` consecutive elements.
#[repr(C)]
pub struct RelSlice<T> {
    ptr: RelPtr<T>,
    len: u64,
}

impl<T> RelSlice<T> {
    /// Element count.
    pub fn len(&self) -> usize {
        self.len as usize
    }

    /// Reports whether the slice is empty.
    pub fn is_empty(&self) -> bool {
        self.len == 0
    }
}

// SAFETY: the edge and the whole element range are checked before every
// element is validated.
unsafe impl<T: ShmLayout> ShmLayout for RelSlice<T> {
    const FINGERPRINT: u64 = fingerprint_mix(0x5245_4c53_4c49, T::FINGERPRINT);

    fn validate(v: &Validator<'_>, addr: usize) -> Result<(), ValidationError> {
        let len = v.read_u64(addr + core::mem::offset_of!(Self, len))? as usize;
        match v.edge(addr)? {
            None if len == 0 => Ok(()),
            None => Err(ValidationError::new(addr, "a NULL slice has a non-zero length")),
            Some(target) => v.target::<T>(target, len),
        }
    }
}

/// Memory a validator may read: bounds-checked, aligned 8-byte words.
pub trait ShmRead {
    /// Checks that `[addr, addr + len)` is readable and `addr` aligned.
    fn check(&self, addr: usize, len: usize, align: usize) -> bool;

    /// Reads the word at `addr` after the same check.
    fn read_u64(&self, addr: usize) -> Option<u64>;
}

/// Walks a configuration graph through a bounds-checked reader.
///
/// No reference to unvalidated memory is formed: every read goes through
/// the reader, which refuses addresses outside the owner's memory, so a
/// corrupt graph yields an error, never an access outside it.
pub struct Validator<'a> {
    mem: &'a dyn ShmRead,
    visited: RefCell<BTreeSet<(usize, u64, usize)>>,
}

impl<'a> Validator<'a> {
    /// Validator over the given memory.
    pub fn new(mem: &'a dyn ShmRead) -> Self {
        Self {
            mem,
            visited: RefCell::new(BTreeSet::new()),
        }
    }

    /// Checks a byte range.
    pub fn check(&self, addr: usize, len: usize, align: usize) -> Result<(), ValidationError> {
        if self.mem.check(addr, len, align) {
            Ok(())
        } else {
            Err(ValidationError::new(
                addr,
                "range is outside the owner's memory or misaligned",
            ))
        }
    }

    /// Reads an aligned word.
    pub fn read_u64(&self, addr: usize) -> Result<u64, ValidationError> {
        self.mem
            .read_u64(addr)
            .ok_or_else(|| ValidationError::new(addr, "word is outside the owner's memory or misaligned"))
    }

    /// Target of the relative slot at `addr`, `None` for NULL.
    pub fn edge(&self, addr: usize) -> Result<Option<usize>, ValidationError> {
        let offset = self.read_u64(addr)? as i64 as isize;
        Ok((offset != 0).then(|| addr.wrapping_add_signed(offset)))
    }

    /// Checks and validates `count` elements of `T` at `addr`, once per
    /// address and type so shared or cyclic edges terminate.
    pub fn target<T: ShmLayout>(&self, addr: usize, count: usize) -> Result<(), ValidationError> {
        let len = size_of::<T>()
            .checked_mul(count)
            .ok_or_else(|| ValidationError::new(addr, "element count overflows"))?;
        self.check(addr, len, align_of::<T>())?;
        if !self.visited.borrow_mut().insert((addr, T::FINGERPRINT, count)) {
            return Ok(());
        }
        for idx in 0..count {
            T::validate(self, addr + idx * size_of::<T>())?;
        }
        Ok(())
    }
}

/// A graph invariant the dataplane relies on does not hold.
#[derive(Debug, PartialEq, Eq)]
pub struct ValidationError {
    pub addr: usize,
    pub reason: std::string::String,
}

impl ValidationError {
    /// Error at an address.
    pub fn new(addr: usize, reason: impl Into<std::string::String>) -> Self {
        Self { addr, reason: reason.into() }
    }
}

impl Display for ValidationError {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        write!(f, "{} at {:#x}", self.reason, self.addr)
    }
}

/// A dataplane module that reads its configuration through a typed layout.
///
/// One impl binds the module name, the body type the control-plane api
/// builds and the body type the dataplane handler reads, so both sides use
/// one type by construction.
pub trait Module: 'static {
    /// Module type name, as the dataplane registers the module.
    const NAME: &'static str;

    /// Configuration body.
    type Config: ShmLayout;
}

/// Layout identity of a module's configuration, as declared to the
/// dataplane and checked by the C module init; never zero, the value of
/// unchecked C modules.
pub const fn config_layout<M: Module>() -> u64 {
    let layout = fingerprint_field(
        fingerprint_struct(
            size_of::<ModuleConfig<M::Config>>(),
            align_of::<ModuleConfig<M::Config>>(),
        ),
        ModuleConfig::<M::Config>::BODY_OFFSET,
        M::Config::FINGERPRINT,
    );
    if layout == 0 { 1 } else { layout }
}

/// Module configuration block: the C module header, then the module body.
///
/// The C header stays [`Opaque`]: the control plane updates its registry
/// reference count under its lock while workers run the configuration.
#[repr(C)]
pub struct ModuleConfig<B> {
    header: Opaque<bindings::cp_module>,
    body: B,
}

impl<B> ModuleConfig<B> {
    /// Module body.
    pub fn body(&self) -> &B {
        &self.body
    }

    /// Offset of the body.
    pub const BODY_OFFSET: usize = core::mem::offset_of!(Self, body);
}

const _: () = assert!(core::mem::offset_of!(ModuleConfig<u64>, header) == 0);
const _: () = assert!(size_of::<Opaque<bindings::cp_module>>() == size_of::<bindings::cp_module>());

/// Reference into a published graph together with the resolver of its
/// relative edges.
pub struct Shm<'g, T, R> {
    value: &'g T,
    res: R,
}

impl<T, R: Copy> Clone for Shm<'_, T, R> {
    fn clone(&self) -> Self {
        *self
    }
}

impl<T, R: Copy> Copy for Shm<'_, T, R> {}

impl<'g, T: 'g, R: Resolver<'g>> Shm<'g, T, R> {
    /// Wraps a value of a validated graph.
    ///
    /// # Safety
    ///
    /// `ptr` must point at a live `T` of a graph that passed validation,
    /// frozen for 'g except for [`Opaque`] bytes, inside the memory `res`
    /// resolves.
    pub unsafe fn from_raw(res: R, ptr: NonNull<T>) -> Self {
        Self {
            // SAFETY: guaranteed by the caller.
            value: unsafe { ptr.as_ref() },
            res,
        }
    }

    /// The referenced value.
    pub fn get(&self) -> &'g T {
        self.value
    }

    /// Resolver of the graph.
    pub fn resolver(&self) -> R {
        self.res
    }

    /// Projects into a part of the value.
    pub fn map<U: 'g>(self, project: impl FnOnce(&'g T) -> &'g U) -> Shm<'g, U, R> {
        Shm {
            value: project(self.value),
            res: self.res,
        }
    }

    /// Elements of a relative slice of the value.
    pub fn slice<U: 'g>(self, edge: impl FnOnce(&'g T) -> &'g RelSlice<U>) -> impl Iterator<Item = Shm<'g, U, R>> {
        let slice = edge(self.value);
        // SAFETY: the validated graph holds `len` elements at the target.
        let elements = unsafe { crate::rel::resolve_slice(self.res, &slice.ptr, slice.len()) }.unwrap_or(&[]);
        let res = self.res;
        elements.iter().map(move |value| Shm { value, res })
    }

    /// Follows a relative edge of the value.
    pub fn follow<U: 'g>(self, edge: impl FnOnce(&'g T) -> &'g RelPtr<U>) -> Option<Shm<'g, U, R>> {
        let value = self.res.resolve(edge(self.value))?;
        Some(Shm { value, res: self.res })
    }
}

/// Compile-time string equality, for the export macro's module-name check.
pub const fn same_name(a: &str, b: &str) -> bool {
    let (a, b) = (a.as_bytes(), b.as_bytes());
    if a.len() != b.len() {
        return false;
    }
    let mut idx = 0;
    while idx < a.len() {
        if a[idx] != b[idx] {
            return false;
        }
        idx += 1;
    }
    true
}
