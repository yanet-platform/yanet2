//! Shared-memory layouts the Rust dataplane and control plane agree on.
//!
//! A type may live in YANET shared memory only if it implements the unsafe
//! [`ShmLayout`] trait, and outside this crate only `#[derive(ShmLayout)]`
//! provides it. The trait carries a structural fingerprint, which binds a
//! dataplane item type to the control plane allowed to create its items, and
//! a validator that walks every relative pointer, so the control plane can
//! check a configuration before it publishes it.
//!
//! Relative pointers resolve with strict provenance: the slot contributes
//! only its address, the target takes the provenance of a [`Root`] built
//! from the raw pointer C handed over. Bytes C may change after publish sit
//! in [`Opaque`], which no Rust reference can read through.

#![cfg_attr(not(test), no_std)]

extern crate self as yanet_shm;

use core::{
    cell::UnsafeCell,
    fmt::{self, Display, Formatter},
    marker::PhantomData,
    mem::{MaybeUninit, align_of, size_of},
    num::NonZeroUsize,
    ops::Range,
    ptr::NonNull,
};

pub use yanet_shm_derive::ShmLayout;

/// A type whose values may be placed in YANET shared memory.
///
/// # Safety
///
/// The type must be `#[repr(C)]` (or a primitive), every bit pattern must
/// be a valid value, interior mutability is allowed only inside [`Opaque`],
/// and it must hold no reference or absolute pointer. The fingerprint must
/// change whenever the layout does, and the validator must check every
/// relative pointer the type holds. Implement it only through the derive.
///
/// The derive works in a crate that forbids unsafe code:
///
/// ```
/// #![forbid(unsafe_code)]
/// use yanet_shm::ShmLayout;
///
/// #[derive(ShmLayout)]
/// #[repr(C)]
/// struct Body {
///     vni: u32,
///     mac: [u8; 6],
/// }
/// ```
///
/// A hand-written implementation does not build there:
///
/// ```compile_fail
/// #![forbid(unsafe_code)]
/// struct Body(u32);
///
/// unsafe impl yanet_shm::ShmLayout for Body {
///     const FINGERPRINT: u64 = 0;
/// }
/// ```
///
/// Nor does a field with invalid bit patterns:
///
/// ```compile_fail
/// #[derive(yanet_shm::ShmLayout)]
/// #[repr(C)]
/// struct Body {
///     enabled: bool,
/// }
/// ```
///
/// A reference:
///
/// ```compile_fail
/// #[derive(yanet_shm::ShmLayout)]
/// #[repr(C)]
/// struct Body {
///     value: &'static u32,
/// }
/// ```
///
/// An absolute pointer:
///
/// ```compile_fail
/// #[derive(yanet_shm::ShmLayout)]
/// #[repr(C)]
/// struct Body {
///     value: *const u32,
/// }
/// ```
///
/// Or a struct without a C representation:
///
/// ```compile_fail
/// #[derive(yanet_shm::ShmLayout)]
/// struct Body {
///     value: u32,
/// }
/// ```
pub unsafe trait ShmLayout: Sized + 'static {
    /// Structural fingerprint of the layout.
    const FINGERPRINT: u64;

    /// Whether any part of the value holds a relative pointer.
    const HAS_REL: bool = false;

    /// Checks a value placed at the given address.
    ///
    /// Leaf types check only that their range is readable and aligned;
    /// types holding relative pointers also follow them.
    fn validate(validator: &mut Validator<'_>, addr: usize) -> Result<(), ShmError> {
        validator.check_range(addr, size_of::<Self>(), align_of::<Self>())
    }
}

/// Deterministic folding used by fingerprints.
pub mod fingerprint {
    const OFFSET_BASIS: u64 = 0xcbf2_9ce4_8422_2325;
    const PRIME: u64 = 0x0000_0100_0000_01b3;

    /// Folds one word into a running hash.
    pub const fn mix(hash: u64, word: u64) -> u64 {
        let mut hash = hash;
        let mut idx = 0;
        while idx < 8 {
            hash ^= (word >> (idx * 8)) & 0xff;
            hash = hash.wrapping_mul(PRIME);
            idx += 1;
        }
        hash
    }

    /// Starts the fingerprint of a type with the given kind tag.
    pub const fn leaf(tag: u64, size: usize, align: usize) -> u64 {
        mix(mix(mix(OFFSET_BASIS, tag), size as u64), align as u64)
    }

    /// Starts the fingerprint of a struct.
    pub const fn start(size: usize, align: usize) -> u64 {
        leaf(TAG_STRUCT, size, align)
    }

    /// Adds one struct field at its offset.
    pub const fn field(hash: u64, offset: usize, field: u64) -> u64 {
        mix(mix(hash, offset as u64), field)
    }

    pub const TAG_STRUCT: u64 = 1;
    pub const TAG_UNSIGNED: u64 = 2;
    pub const TAG_SIGNED: u64 = 3;
    pub const TAG_ARRAY: u64 = 4;
    pub const TAG_REL_PTR: u64 = 5;
    pub const TAG_REL_SLICE: u64 = 6;
    pub const TAG_OPAQUE: u64 = 7;
}

macro_rules! leaf_impl {
    ($tag:expr, $($ty:ty),*) => {
        $(
            // SAFETY: integers are valid for every bit pattern and hold no
            // pointer.
            unsafe impl ShmLayout for $ty {
                const FINGERPRINT: u64 = fingerprint::leaf($tag, size_of::<$ty>(), align_of::<$ty>());
            }
        )*
    };
}

leaf_impl!(fingerprint::TAG_UNSIGNED, u8, u16, u32, u64);
leaf_impl!(fingerprint::TAG_SIGNED, i8, i16, i32, i64);

// SAFETY: an array of valid layouts is a valid layout; the validator
// visits every element when the element holds relative pointers.
unsafe impl<T: ShmLayout, const N: usize> ShmLayout for [T; N] {
    const FINGERPRINT: u64 = fingerprint::mix(fingerprint::mix(fingerprint::TAG_ARRAY, T::FINGERPRINT), N as u64);
    const HAS_REL: bool = T::HAS_REL;

    fn validate(validator: &mut Validator<'_>, addr: usize) -> Result<(), ShmError> {
        validator.check_range(addr, size_of::<Self>(), align_of::<Self>())?;
        if T::HAS_REL {
            for idx in 0..N {
                T::validate(validator, addr + idx * size_of::<T>())?;
            }
        }
        Ok(())
    }
}

/// Bytes owned and mutated by C, never read through a Rust reference.
///
/// The cell makes a shared reference to an enclosing struct sound while C
/// writes these bytes.
#[repr(transparent)]
pub struct Opaque<T>(UnsafeCell<MaybeUninit<T>>);

// SAFETY: the bytes are never read by Rust, so any content is acceptable,
// and the cell is the permitted interior mutability.
unsafe impl<T: 'static> ShmLayout for Opaque<T> {
    const FINGERPRINT: u64 = fingerprint::leaf(fingerprint::TAG_OPAQUE, size_of::<T>(), align_of::<T>());
}

/// A relative pointer: the offset from its own address to the target, zero
/// for null. The layout C's relative pointers use.
///
/// Values exist only in shared memory, reached by reference: the type has
/// no constructor and is neither `Clone` nor `Copy`, and the sys APIs that
/// copy bytes out refuse types holding relative pointers. A reference to a
/// relative pointer therefore always sits inside a validated item.
#[repr(transparent)]
pub struct RelPtr<T> {
    offset: u64,
    _target: PhantomData<fn() -> T>,
}

// SAFETY: the offset is an integer; the validator follows it to a valid T.
unsafe impl<T: ShmLayout> ShmLayout for RelPtr<T> {
    const FINGERPRINT: u64 = fingerprint::mix(fingerprint::TAG_REL_PTR, T::FINGERPRINT);
    const HAS_REL: bool = true;

    fn validate(validator: &mut Validator<'_>, addr: usize) -> Result<(), ShmError> {
        validator.check_range(addr, size_of::<Self>(), align_of::<Self>())?;
        let offset = validator.read_u64(addr)?;
        if offset == 0 {
            return Ok(());
        }
        validator.enter()?;
        T::validate(validator, addr.wrapping_add(offset as usize))
    }
}

impl<T: ShmLayout> RelPtr<T> {
    /// Resolves the pointer inside the mapping of the root.
    ///
    /// Returns `None` for null. The target is valid when the configuration
    /// passed validation before it was published.
    pub fn get<'r>(&self, root: Root<'r>) -> Option<&'r T> {
        if self.offset == 0 {
            return None;
        }
        let slot = core::ptr::from_ref(self).addr();
        root.reference(slot.wrapping_add(self.offset as usize))
    }
}

/// A relative pointer to `len` consecutive values.
#[repr(C)]
pub struct RelSlice<T> {
    offset: u64,
    len: u64,
    _target: PhantomData<fn() -> T>,
}

// SAFETY: two integers; the validator follows the offset to len valid
// values.
unsafe impl<T: ShmLayout> ShmLayout for RelSlice<T> {
    const FINGERPRINT: u64 = fingerprint::mix(fingerprint::TAG_REL_SLICE, T::FINGERPRINT);
    const HAS_REL: bool = true;

    fn validate(validator: &mut Validator<'_>, addr: usize) -> Result<(), ShmError> {
        validator.check_range(addr, size_of::<Self>(), align_of::<Self>())?;
        let offset = validator.read_u64(addr)?;
        let len = validator.read_u64(addr + size_of::<u64>())? as usize;
        if len == 0 {
            return Ok(());
        }
        validator.enter()?;
        let start = addr.wrapping_add(offset as usize);
        let bytes = len
            .checked_mul(size_of::<T>())
            .ok_or(ShmError::OutOfBounds { addr: start })?;
        validator.check_range(start, bytes, align_of::<T>())?;
        if T::HAS_REL {
            for idx in 0..len {
                T::validate(validator, start + idx * size_of::<T>())?;
            }
        }
        Ok(())
    }
}

impl<T: ShmLayout> RelSlice<T> {
    /// Resolves the slice inside the mapping of the root; empty for null.
    pub fn get<'r>(&self, root: Root<'r>) -> &'r [T] {
        if self.len == 0 {
            return &[];
        }
        let slot = core::ptr::from_ref(self).addr();
        root.slice(slot.wrapping_add(self.offset as usize), self.len as usize)
    }
}

/// The provenance of a shared-memory mapping, for one borrow lifetime.
#[derive(Clone, Copy)]
pub struct Root<'r> {
    base: NonNull<u8>,
    _mapping: PhantomData<&'r [u8]>,
}

impl<'r> Root<'r> {
    /// Wraps the raw pointer C handed over.
    ///
    /// # Safety
    ///
    /// The pointer must carry provenance for the whole mapping, every
    /// relative pointer reached through this root must have been validated
    /// before publish, and the targets must stay frozen for `'r`.
    pub unsafe fn new(base: NonNull<u8>) -> Self {
        Self { base, _mapping: PhantomData }
    }

    /// Returns the root pointer moved to the given address.
    pub fn at(self, addr: NonZeroUsize) -> NonNull<u8> {
        self.base.with_addr(addr)
    }

    fn reference<T: ShmLayout>(self, addr: usize) -> Option<&'r T> {
        let target = self.at(NonZeroUsize::new(addr)?).cast::<T>();
        debug_assert!(target.is_aligned());
        // SAFETY: validated before publish: in bounds, aligned and a valid
        // T for any bytes; frozen for 'r per the constructor contract.
        Some(unsafe { target.as_ref() })
    }

    fn slice<T: ShmLayout>(self, addr: usize, len: usize) -> &'r [T] {
        let Some(addr) = NonZeroUsize::new(addr) else {
            return &[];
        };
        let start = self.at(addr).cast::<T>();
        debug_assert!(start.is_aligned());
        // SAFETY: as for a single value, for len consecutive values.
        unsafe { core::slice::from_raw_parts(start.as_ptr(), len) }
    }
}

/// Why a shared-memory layout failed validation.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum ShmError {
    /// A value does not lie inside one readable region.
    OutOfBounds { addr: usize },
    /// A value is not aligned for its type.
    Misaligned { addr: usize },
    /// Relative pointers nest deeper than the validation budget.
    TooDeep,
}

impl Display for ShmError {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        match self {
            Self::OutOfBounds { addr } => write!(f, "value at {addr:#x} lies outside the owner's memory"),
            Self::Misaligned { addr } => write!(f, "value at {addr:#x} is misaligned"),
            Self::TooDeep => write!(f, "relative pointers nest too deep"),
        }
    }
}

/// Walks a layout inside a set of readable regions of one mapping.
pub struct Validator<'a> {
    root: NonNull<u8>,
    regions: &'a [Range<usize>],
    budget: u32,
}

impl<'a> Validator<'a> {
    /// Relative pointers a validation follows at most.
    pub const BUDGET: u32 = 1 << 16;

    /// Creates a validator over the given regions.
    ///
    /// # Safety
    ///
    /// The root must carry provenance for a mapping that contains every
    /// region, and the regions must be readable for the validator lifetime.
    pub unsafe fn new(root: NonNull<u8>, regions: &'a [Range<usize>]) -> Self {
        Self { root, regions, budget: Self::BUDGET }
    }

    /// Checks that `size` bytes at `addr` lie in one region and are aligned.
    pub fn check_range(&self, addr: usize, size: usize, align: usize) -> Result<(), ShmError> {
        if addr % align != 0 {
            return Err(ShmError::Misaligned { addr });
        }
        let end = addr.checked_add(size).ok_or(ShmError::OutOfBounds { addr })?;
        if self
            .regions
            .iter()
            .any(|region| region.start <= addr && end <= region.end)
        {
            Ok(())
        } else {
            Err(ShmError::OutOfBounds { addr })
        }
    }

    /// Reads a 64-bit word after checking its range.
    pub fn read_u64(&self, addr: usize) -> Result<u64, ShmError> {
        self.check_range(addr, size_of::<u64>(), align_of::<u64>())?;
        let addr = NonZeroUsize::new(addr).ok_or(ShmError::OutOfBounds { addr })?;
        // SAFETY: inside a readable region of the root's mapping and aligned.
        Ok(unsafe { self.root.with_addr(addr).cast::<u64>().read() })
    }

    fn enter(&mut self) -> Result<(), ShmError> {
        self.budget = self.budget.checked_sub(1).ok_or(ShmError::TooDeep)?;
        Ok(())
    }
}

/// Reads a value from the start of a byte buffer.
///
/// Returns `None` when the buffer is shorter than the value.
///
/// A value holding relative pointers is refused at compile time: a copy
/// outside shared memory would resolve against foreign addresses.
pub fn from_bytes<T: ShmLayout>(bytes: &[u8]) -> Option<T> {
    const { assert!(!T::HAS_REL, "values with relative pointers stay in shared memory") };
    if bytes.len() < size_of::<T>() {
        return None;
    }
    // SAFETY: the buffer holds enough bytes, every bit pattern is a valid T,
    // and the read does not assume alignment.
    Some(unsafe { bytes.as_ptr().cast::<T>().read_unaligned() })
}

pub mod item;
pub use item::{ItemLayout, ShmItem, item_layout};

#[cfg(test)]
mod tests;
