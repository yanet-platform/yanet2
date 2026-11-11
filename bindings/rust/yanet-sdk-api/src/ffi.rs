//! The conversions and guards the exports of `#[yanet_sdk::api]` expand to.
//!
//! An export converts what C hands in, runs the method under a guard and
//! turns a failure or a panic into a `yanet_error` chain in the trailing
//! argument, as the exports of a C module do. Nothing here unwinds into C.

use core::{
    any::Any,
    ffi::{CStr, c_char, c_int},
    mem::size_of,
    panic::AssertUnwindSafe,
    ptr::{self, NonNull},
    slice,
};
use std::panic::catch_unwind;

use yanet_sys::{
    bindings::{self, yanet_error},
    cp::FfiRecord,
};

use crate::{
    agent::{Agent, Releasable, handle_at, raw_at, release_at},
    error::{Error, ErrorKind, into_c_chain, wrap_c_chain},
};

/// The `err` argument of an export: null, or the place for a chain.
type ErrOut = *mut *mut yanet_error;

fn invalid(argument: &str, what: &str) -> Error {
    Error::new(ErrorKind::InvalidArgument, &format!("argument `{argument}` {what}"))
}

/// A pointer C passed for a non-null argument: not null, aligned.
fn checked<T>(pointer: *mut T, argument: &str) -> Result<NonNull<T>, Error> {
    match NonNull::new(pointer) {
        Some(pointer) if pointer.is_aligned() => Ok(pointer),
        _ => Err(invalid(argument, "is null or misaligned")),
    }
}

/// The agent behind the pointer C passed.
///
/// # Safety
///
/// The pointer is null, misaligned or an attached agent, with the other
/// requirements of [`Agent::from_raw`].
unsafe fn attached(pointer: *mut bindings::agent, argument: &str) -> Result<Agent, Error> {
    // SAFETY: caller contract.
    Ok(unsafe { Agent::from_raw(checked(pointer, argument)?) })
}

/// A NUL terminated UTF-8 string.
///
/// # Safety
///
/// `pointer` is null or a NUL terminated string that lives, unchanged, as long
/// as `'a`.
pub unsafe fn c_str<'a>(pointer: *const c_char, argument: &str) -> Result<&'a str, Error> {
    let pointer = checked(pointer.cast_mut(), argument)?;
    // SAFETY: a non-null string is NUL terminated (caller contract).
    unsafe { CStr::from_ptr(pointer.as_ptr()) }
        .to_str()
        .map_err(|_| invalid(argument, "is not UTF-8"))
}

/// A record behind a pointer.
///
/// # Safety
///
/// `pointer` is null, misaligned or points at `size_of::<T>()` readable bytes
/// that live, unchanged, as long as `'a`.
pub unsafe fn record<'a, T: FfiRecord>(pointer: *const T, argument: &str) -> Result<&'a T, Error> {
    let pointer = checked(pointer.cast_mut(), argument)?;
    // SAFETY: aligned, and every bit pattern of the readable bytes is a value
    // of `T` (caller contract, `FfiRecord`).
    Ok(unsafe { pointer.as_ref() })
}

/// A slice of records; `(NULL, 0)` is empty.
///
/// # Safety
///
/// `pointer` is null, misaligned or points at `len` records readable and
/// unchanged as long as `'a`, as for [`record`].
pub unsafe fn slice_of<'a, T: FfiRecord>(pointer: *const T, len: usize, argument: &str) -> Result<&'a [T], Error> {
    if pointer.is_null() && len == 0 {
        return Ok(&[]);
    }
    let pointer = checked(pointer.cast_mut(), argument)?;
    if len
        .checked_mul(size_of::<T>())
        .is_none_or(|bytes| bytes > isize::MAX as usize)
    {
        return Err(invalid(argument, "is longer than a slice can be"));
    }
    // SAFETY: aligned, in bounds of one allocation and all bit patterns valid
    // (caller contract, `FfiRecord`).
    Ok(unsafe { slice::from_raw_parts(pointer.as_ptr(), len) })
}

/// A record a query writes, checked and not yet touched: [`zeroed`] makes it
/// the method's once every argument is accepted.
///
/// # Safety
///
/// `pointer` is null, misaligned or points at `size_of::<T>()` writable bytes
/// that nothing else reads or writes during the call, the other arguments
/// included.
pub unsafe fn out_record<T: FfiRecord>(pointer: *mut T, argument: &str) -> Result<NonNull<T>, Error> {
    checked(pointer, argument)
}

/// Records a query writes, checked and not yet touched: [`zeroed_slice`] makes
/// them the method's once every argument is accepted; `(NULL, 0)` is empty.
///
/// # Safety
///
/// `pointer` is null, misaligned or points at `len` records writable as for
/// [`out_record`].
pub unsafe fn out_slice<T: FfiRecord>(pointer: *mut T, len: usize, argument: &str) -> Result<NonNull<[T]>, Error> {
    if pointer.is_null() && len == 0 {
        return Ok(NonNull::slice_from_raw_parts(NonNull::dangling(), 0));
    }
    let pointer = checked(pointer, argument)?;
    if len
        .checked_mul(size_of::<T>())
        .is_none_or(|bytes| bytes > isize::MAX as usize)
    {
        return Err(invalid(argument, "is longer than a slice can be"));
    }
    Ok(NonNull::slice_from_raw_parts(pointer, len))
}

/// The output record, zeroed: a value of `T`, as every bit pattern is, which
/// C may have handed over uninitialised.
///
/// # Safety
///
/// `out` came from [`out_record`], whose contract holds while `'a` lives.
pub unsafe fn zeroed<'a, T: FfiRecord>(mut out: NonNull<T>) -> &'a mut T {
    // SAFETY: writable, aligned and reached by nothing else (caller
    // contract); all-zero bytes are a value of `T` (`FfiRecord`).
    unsafe {
        out.write_bytes(0, 1);
        out.as_mut()
    }
}

/// The output records, zeroed, as [`zeroed`] makes one.
///
/// # Safety
///
/// `out` came from [`out_slice`], whose contract holds while `'a` lives.
pub unsafe fn zeroed_slice<'a, T: FfiRecord>(mut out: NonNull<[T]>) -> &'a mut [T] {
    // SAFETY: as for `zeroed`, over the `len` records of a slice that fits
    // the address space (checked); an empty one is dangling and aligned.
    unsafe {
        out.cast::<T>().write_bytes(0, out.len());
        out.as_mut()
    }
}

/// Preserves a panic message while containing a panicking payload destructor.
fn panic_error(payload: Box<dyn Any + Send>) -> Error {
    let message = payload
        .downcast_ref::<&str>()
        .copied()
        .or_else(|| payload.downcast_ref::<String>().map(String::as_str))
        .unwrap_or("no message");
    let error = Error::new(ErrorKind::Untagged, &format!("panic: {message}"));
    if let Err(second) = catch_unwind(AssertUnwindSafe(|| drop(payload))) {
        if second.is::<&str>() || second.is::<String>() {
            drop(second);
        } else {
            // Retain unknown secondary payloads to contain another destructor
            // panic.
            core::mem::forget(second);
        }
    }
    error
}

/// Runs `body`, a panic of which is an error.
fn guarded<T>(body: impl FnOnce() -> Result<T, Error>) -> Result<T, Error> {
    catch_unwind(AssertUnwindSafe(body)).unwrap_or_else(|payload| Err(panic_error(payload)))
}

/// Hands `error` to C at `err` unless it is null or misaligned: as the chain
/// there when it holds none, and otherwise over the chain it holds, which stays
/// as the cause.
fn set_err(err: ErrOut, error: Error) {
    if err.is_null() || !err.is_aligned() {
        return;
    }
    // SAFETY: `err` is writable and holds null or a chain C owns (export
    // contract).
    let chain = unsafe { &mut *err };
    if chain.is_null() {
        *chain = into_c_chain(error);
    } else {
        // SAFETY: as above.
        unsafe { wrap_c_chain(error, chain) };
    }
}

fn set_errno(code: c_int) {
    // SAFETY: the location is that of the calling thread.
    unsafe { *bindings::__errno_location() = code };
}

/// The errno a failed free reports for a refused argument or a panic.
fn errno_of(kind: ErrorKind) -> c_int {
    (match kind {
        ErrorKind::InvalidArgument => bindings::EINVAL,
        _ => bindings::EIO,
    }) as c_int
}

/// The body of a builder export: runs `body` over the agent and returns the
/// header of the configuration or the object it built, null with `*err` set
/// on failure.
///
/// # Safety
///
/// The export contract: `agent` is null, misaligned or an attached agent
/// (see [`Agent::from_raw`]); `err` is null or writable and holds null or a
/// chain C owns.
pub unsafe fn build<B: Releasable>(
    agent_pointer: *mut bindings::agent,
    err: ErrOut,
    body: impl FnOnce(&Agent) -> Result<B, Error>,
) -> *mut B::Raw {
    let header = guarded(|| {
        // SAFETY: the export contract.
        let agent = unsafe { attached(agent_pointer, "agent") }?;
        Ok(raw_at::<B>(&agent, body(&agent)?.header()))
    });
    header.unwrap_or_else(|error| {
        set_err(err, error);
        ptr::null_mut()
    })
}

/// A configuration or an object an export of the crate built, behind the
/// pointer C passed; one of another type is refused.
///
/// # Safety
///
/// `pointer` is null, misaligned or a header of the handle's kind (module or
/// object) an export of this crate returned and nothing released, nor
/// releases during the call, a pointer of the mapping of the agent of the
/// call.
pub unsafe fn handle<B: Releasable>(pointer: *mut B::Raw, argument: &str) -> Result<B, Error> {
    let pointer = checked(pointer, argument)?;
    // SAFETY: the caller's contract; the handle is only lent to the method,
    // which cannot release it.
    unsafe { handle_at::<B>(pointer) }
}

/// The body of a query export: runs `body` over the agent and writes its
/// value at `out`, 0, or -1 with `*err` set on failure and `out` untouched.
///
/// The records a query writes are its arguments: `body` zeroes them once it
/// accepts every argument, so a refused one leaves them untouched, and a
/// failed method leaves what it wrote.
///
/// # Safety
///
/// As for [`build`]; `out` is null, misaligned or writable.
pub unsafe fn query(
    agent_pointer: *mut bindings::agent,
    out: *mut u64,
    err: ErrOut,
    body: impl FnOnce(&Agent) -> Result<u64, Error>,
) -> c_int {
    let value = guarded(|| {
        let out = checked(out, "out")?;
        // SAFETY: the export contract.
        let agent = unsafe { attached(agent_pointer, "agent") }?;
        Ok((out, body(&agent)?))
    });
    match value {
        Ok((out, value)) => {
            // SAFETY: writable and aligned (export contract, checked).
            unsafe { out.write(value) };
            0
        }
        Err(error) => {
            set_err(err, error);
            -1
        }
    }
}

/// What a free export returns: 0 for a release, otherwise -1 with `*err` set
/// and errno EAGAIN for a busy refusal, or the errno of the error's kind.
fn released_or_errno(released: Result<(), Error>, busy: bool, err: ErrOut) -> c_int {
    match released {
        Ok(()) => 0,
        Err(error) => {
            let errno = if busy {
                bindings::EAGAIN as c_int
            } else {
                errno_of(error.kind())
            };
            set_err(err, error);
            // Last, right before the return, so nothing above clobbers it.
            set_errno(errno);
            -1
        }
    }
}

/// The body of a free export: releases the configuration or the object at
/// `raw`, -1 with `*err` and errno set when it is not released; one of
/// another type is refused as an invalid argument and left as it is.
///
/// # Safety
///
/// The export contract: `raw` is null, misaligned or a header of the handle's
/// kind (module or object) an export of this crate returned and nothing
/// released; `err` is null or writable and holds null or a chain C owns.
pub unsafe fn free<B: Releasable>(raw: *mut B::Raw, err: ErrOut) -> c_int {
    // A release the live generation refused is busy whatever its chain says:
    // the chain has no kind when C could not allocate the frame.
    let mut busy = false;
    let released = guarded(|| {
        // SAFETY: the export contract.
        unsafe { handle::<B>(raw, "handle") }?;
        // SAFETY: the export contract, and the header is of the handle's type.
        unsafe { release_at::<B>(NonNull::new_unchecked(raw)) }.map_err(|retained| {
            busy = true;
            retained.error
        })
    });
    released_or_errno(released, busy, err)
}

#[cfg(test)]
mod tests {
    use core::{mem::MaybeUninit, ptr::NonNull};

    use super::{zeroed, zeroed_slice};

    /// verifies that records C left uninitialised reach the method as zeroed
    /// values, which Miri checks for the slice formed over them.
    #[test]
    fn test_zeroed_slice_uninitialised_reads_zero() {
        let mut places = [MaybeUninit::<u64>::uninit(); 4];
        let out = NonNull::slice_from_raw_parts(NonNull::from(&mut places).cast::<u64>(), places.len());

        // SAFETY: the places are writable and aligned, and nothing else
        // reaches them while the slice lives.
        let values = unsafe { zeroed_slice(out) };

        assert_eq!([0; 4], *values);
    }

    /// verifies that a record C left uninitialised reaches the method as a
    /// zeroed value.
    #[test]
    fn test_zeroed_uninitialised_record_reads_zero() {
        let mut place = MaybeUninit::<u64>::uninit();

        // SAFETY: the place is writable and aligned, and nothing else reaches
        // it while the reference lives.
        let value = unsafe { zeroed(NonNull::from(&mut place).cast::<u64>()) };

        assert_eq!(0, *value);
    }

    /// verifies that the empty slice of no address, as C passes it, is
    /// zeroed without a write.
    #[test]
    fn test_zeroed_slice_dangling_empty_is_empty() {
        let out = NonNull::slice_from_raw_parts(NonNull::<u64>::dangling(), 0);

        // SAFETY: an empty slice reaches no memory.
        let values = unsafe { zeroed_slice(out) };

        assert!(values.is_empty());
    }
}
