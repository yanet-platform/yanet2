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

/// An optional writable output for transferring a native error chain.
type ErrOut = *mut *mut yanet_error;

fn invalid(argument: &str, what: &str) -> ApiError {
    from_str(ErrorKind::InvalidArgument, &format!("argument `{argument}` {what}"))
}

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

/// Borrows a NUL terminated UTF-8 string from C.
///
/// # Safety
///
/// A non-null input must identify a readable NUL terminated string that remains
/// unchanged for the returned borrow.
pub unsafe fn c_str<'a>(pointer: *const c_char, argument: &str) -> Result<&'a str, ApiError> {
    let pointer = checked(pointer.cast_mut(), argument)?;
    // SAFETY: a non-null string is NUL terminated (caller contract).
    unsafe { CStr::from_ptr(pointer.as_ptr()) }
        .to_str()
        .map_err(|_| invalid(argument, "is not UTF-8"))
}

/// Borrows one input record from C.
///
/// # Safety
///
/// An aligned, non-null input must identify a complete readable record that
/// remains unchanged for the returned borrow.
pub unsafe fn record<'a, T: FfiRecord>(pointer: *const T, argument: &str) -> Result<&'a T, ApiError> {
    let pointer = checked(pointer.cast_mut(), argument)?;
    // SAFETY: the complete record is aligned, readable and valid for every bit
    // pattern (caller and input-record contracts).
    Ok(unsafe { pointer.as_ref() })
}

/// Borrows a contiguous run of input records; a null, zero-length run is empty.
///
/// # Safety
///
/// An aligned, non-null input must identify the entire requested run within one
/// allocation. Its records must remain readable and unchanged for the returned
/// borrow.
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
    // SAFETY: the aligned run stays within one allocation and every record bit
    // pattern is valid (caller and input-record contracts).
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

/// Contains callback unwinding and reports its panic as a native error.
fn guarded<T>(body: impl FnOnce() -> Result<T, ApiError>) -> Result<T, ApiError> {
    catch_unwind(AssertUnwindSafe(body)).unwrap_or_else(|payload| Err(panic_error(payload)))
}

/// Transfers the error chain through an aligned, non-null output when provided.
fn set_err(err: ErrOut, error: ApiError) {
    if !err.is_null() && err.is_aligned() {
        // SAFETY: the export contract guarantees writable storage for a
        // non-null error output.
        unsafe { err.write(error.into_c_chain()) };
    }
}

fn set_errno(code: c_int) {
    // SAFETY: the location is that of the calling thread.
    unsafe { *bindings::__errno_location() = code };
}

fn errno_of(kind: ErrorKind) -> c_int {
    (match kind {
        ErrorKind::InvalidArgument => bindings::EINVAL,
        _ => bindings::EIO,
    }) as c_int
}

/// Builds a configuration behind the C boundary, returning null on failure.
///
/// # Safety
///
/// An aligned, non-null agent input must satisfy [`Agent::from_raw`]. A
/// non-null error output must be writable; failures transfer their error chain
/// there.
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

/// Releases a configuration behind the C boundary, returning -1 on failure.
///
/// # Safety
///
/// An aligned, non-null configuration input must be an unreleased value of the
/// requested module returned by this SDK. A non-null error output must be
/// writable; refusal preserves the configuration and reports its error chain
/// and errno.
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
