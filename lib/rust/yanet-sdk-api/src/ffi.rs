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
    cp::{Built, FfiRecord},
    dp::Module,
};

use crate::{
    agent::{Agent, cp_module_at, release_at},
    error::{ApiError, ErrorKind, from_str},
};

/// The `err` argument of an export: null, or the place for a chain.
type ErrOut = *mut *mut yanet_error;

fn invalid(argument: &str, what: &str) -> ApiError {
    from_str(ErrorKind::InvalidArgument, &format!("argument `{argument}` {what}"))
}

/// A pointer C passed for a non-null argument: not null, aligned.
fn checked<T>(pointer: *mut T, argument: &str) -> Result<NonNull<T>, ApiError> {
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
unsafe fn attached(pointer: *mut bindings::agent, argument: &str) -> Result<Agent, ApiError> {
    // SAFETY: caller contract.
    Ok(unsafe { Agent::from_raw(checked(pointer, argument)?) })
}

/// A NUL terminated UTF-8 string.
///
/// # Safety
///
/// `pointer` is null or a NUL terminated string that lives, unchanged, as long
/// as `'a`.
pub unsafe fn c_str<'a>(pointer: *const c_char, argument: &str) -> Result<&'a str, ApiError> {
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
pub unsafe fn record<'a, T: FfiRecord>(pointer: *const T, argument: &str) -> Result<&'a T, ApiError> {
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
pub unsafe fn slice_of<'a, T: FfiRecord>(pointer: *const T, len: usize, argument: &str) -> Result<&'a [T], ApiError> {
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

/// Preserves a panic message while containing a panicking payload destructor.
fn panic_error(payload: Box<dyn Any + Send>) -> ApiError {
    let message = payload
        .downcast_ref::<&str>()
        .copied()
        .or_else(|| payload.downcast_ref::<String>().map(String::as_str))
        .unwrap_or("no message");
    let error = from_str(ErrorKind::Untagged, &format!("panic: {message}"));
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
fn guarded<T>(body: impl FnOnce() -> Result<T, ApiError>) -> Result<T, ApiError> {
    catch_unwind(AssertUnwindSafe(body)).unwrap_or_else(|payload| Err(panic_error(payload)))
}

/// Hands `error` to C: written to `err` unless it is null or misaligned.
fn set_err(err: ErrOut, error: ApiError) {
    if !err.is_null() && err.is_aligned() {
        // SAFETY: `err` is null or writable (export contract).
        unsafe { err.write(error.into_c_chain()) };
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
/// header of the configuration it built, null with `*err` set on failure.
///
/// # Safety
///
/// The export contract: `agent` is null, misaligned or an attached agent
/// (see [`Agent::from_raw`]); `err` is null or writable.
pub unsafe fn build<M>(
    agent_pointer: *mut bindings::agent,
    err: ErrOut,
    body: impl FnOnce(&Agent) -> Result<Built<M, Agent>, ApiError>,
) -> *mut bindings::cp_module {
    let header = guarded(|| {
        // SAFETY: the export contract.
        let agent = unsafe { attached(agent_pointer, "agent") }?;
        Ok(cp_module_at(&agent, body(&agent)?.header()))
    });
    header.unwrap_or_else(|error| {
        set_err(err, error);
        ptr::null_mut()
    })
}

/// The body of a free export: releases the configuration of module `M` at
/// `module`, -1 with `*err` and errno set when it is not released.
///
/// # Safety
///
/// The export contract: `module` is null, misaligned or a configuration of
/// module `M` an export of this crate returned and nothing released; `err` is
/// null or writable.
pub unsafe fn free<M: Module>(module: *mut bindings::cp_module, err: ErrOut) -> c_int {
    // A release the live generation refused is busy whatever its chain says:
    // the chain has no kind when C could not allocate the frame.
    let mut busy = false;
    let released = guarded(|| {
        let module = checked(module, "module")?;
        // SAFETY: the export contract.
        unsafe { release_at::<M>(module) }.map_err(|retained| {
            busy = true;
            retained.error
        })
    });
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
