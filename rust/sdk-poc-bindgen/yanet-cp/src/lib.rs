//! C ABI of the Rust control-plane api crates, for the Go control plane.
//!
//! One static library carries every Rust control-plane api (the list is the
//! dependency list of this crate: `decap-api`) and the rure C API, so Go
//! links exactly one copy of the Rust runtime.
//!
//! Safety contract of every export:
//! - Arguments are `repr(C)` values, caller-owned buffers and C handles;
//!   nothing Rust allocates is returned, so Go never holds a Rust-owned
//!   pointer. Module configurations live in agent shared memory and are owned
//!   by the caller once handed over, like those of the C API.
//! - Every body runs under `catch_unwind`; a panic becomes the
//!   `YANET_CP_EPANIC` code and never unwinds into the caller.
//! - Errors are a negative code plus a NUL-terminated message truncated into
//!   the caller's buffer; frees report through a C `yanet_error` chain and
//!   errno, exactly like the C module API.
//! - No threads, no signal handlers, no thread-local state of its own (the Rust
//!   standard library keeps a thread-local panic count, touched only while a
//!   panic is being caught). Calls are reentrant for distinct modules; Rust
//!   writes only into the caller's buffers and the agent's shared memory.
//! - Callers must check `yanet_cp_abi_query` once at start-up against the
//!   values of the header they compiled with.

#![allow(non_camel_case_types)]

use core::{
    ffi::{CStr, c_char, c_int, c_void},
    ptr::NonNull,
};

use decap_api::{ApiError, Decap, DecapConfig, Prefix};
use rure as _;
use yanet_sys::{
    bindings,
    cp::{self, Agent},
    shm::{ModuleConfig, config_layout},
};

/// Version of this C ABI; bump on any change of an export or a type.
pub const YANET_CP_ABI_VERSION: u32 = 1;

/// Success.
pub const YANET_CP_OK: c_int = 0;
/// A null or malformed argument.
pub const YANET_CP_EINVAL: c_int = -1;
/// Allocation, module setup, insert or validation failed.
pub const YANET_CP_EBUILD: c_int = -2;
/// A Rust panic was caught.
pub const YANET_CP_EPANIC: c_int = -3;

/// IPv4 prefix family tag.
pub const YANET_CP_FAMILY_IPV4: u8 = 4;
/// IPv6 prefix family tag.
pub const YANET_CP_FAMILY_IPV6: u8 = 6;

/// Library and layout facts the caller compares with its own build.
#[repr(C)]
pub struct yanet_cp_abi_info {
    /// `YANET_CP_ABI_VERSION` of the library.
    pub abi_version: u32,
    /// Dataplane module ABI version the library was generated against.
    pub module_abi_version: u32,
    /// Size of `struct yanet_cp_decap_prefix`.
    pub decap_prefix_size: u32,
    /// Size of the C module header.
    pub cp_module_size: u32,
    /// Size of the whole decap configuration block.
    pub decap_config_size: u32,
    /// Configuration layout the decap module declares to the dataplane.
    pub decap_config_layout: u64,
}

/// One decap prefix as an inclusive big-endian address range; IPv4 uses
/// the first four bytes of each bound.
#[repr(C)]
pub struct yanet_cp_decap_prefix {
    /// `YANET_CP_FAMILY_IPV4` or `YANET_CP_FAMILY_IPV6`.
    pub family: u8,
    pub from: [u8; 16],
    pub to: [u8; 16],
}

/// Copies a message into the caller's buffer, truncated and NUL-terminated.
fn write_error(buf: *mut c_char, len: usize, message: &str) {
    if buf.is_null() || len == 0 {
        return;
    }
    let count = message.len().min(len - 1);
    // SAFETY: the caller guarantees `buf` holds `len` writable bytes.
    unsafe {
        core::ptr::copy_nonoverlapping(message.as_ptr(), buf.cast::<u8>(), count);
        *buf.add(count) = 0;
    }
}

/// Runs an export body, turning a panic into an error code.
fn guarded(err_buf: *mut c_char, err_len: usize, body: impl FnOnce() -> Result<(), (c_int, String)>) -> c_int {
    match std::panic::catch_unwind(core::panic::AssertUnwindSafe(body)) {
        Ok(Ok(())) => YANET_CP_OK,
        Ok(Err((code, message))) => {
            write_error(err_buf, err_len, &message);
            code
        }
        Err(_) => {
            write_error(err_buf, err_len, "internal error: a Rust panic was caught");
            YANET_CP_EPANIC
        }
    }
}

/// Fills `out` with the library's ABI and layout facts.
///
/// # Safety
///
/// `out` must be null or point at a writable `yanet_cp_abi_info`.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn yanet_cp_abi_query(out: *mut yanet_cp_abi_info) -> c_int {
    guarded(core::ptr::null_mut(), 0, || {
        let Some(out) = NonNull::new(out) else {
            return Err((YANET_CP_EINVAL, String::new()));
        };
        let info = yanet_cp_abi_info {
            abi_version: YANET_CP_ABI_VERSION,
            module_abi_version: bindings::YANET_MODULE_ABI_VERSION,
            decap_prefix_size: size_of::<yanet_cp_decap_prefix>() as u32,
            cp_module_size: size_of::<bindings::cp_module>() as u32,
            decap_config_size: size_of::<ModuleConfig<DecapConfig>>() as u32,
            decap_config_layout: config_layout::<Decap>(),
        };
        // SAFETY: guaranteed by the caller.
        unsafe { out.as_ptr().write(info) };
        Ok(())
    })
}

/// Builds and validates a decap configuration in the agent's memory.
///
/// On success stores the module header in `*out` for the caller to publish
/// and later free with `yanet_cp_decap_config_free`; on failure nothing is
/// left allocated and the message goes to `err_buf`.
///
/// # Safety
///
/// `agent` must be an attached agent; `name` a NUL-terminated string;
/// `prefixes` null with `count` zero or `count` readable entries; `out` a
/// writable slot; `err_buf` null or `err_len` writable bytes.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn yanet_cp_decap_config_build(
    agent: *mut c_void,
    name: *const c_char,
    prefixes: *const yanet_cp_decap_prefix,
    count: usize,
    out: *mut *mut c_void,
    err_buf: *mut c_char,
    err_len: usize,
) -> c_int {
    guarded(err_buf, err_len, || {
        let invalid = |msg: &str| Err((YANET_CP_EINVAL, msg.to_owned()));
        let (Some(agent), Some(out)) = (NonNull::new(agent), NonNull::new(out)) else {
            return invalid("agent and output slot must not be null");
        };
        if name.is_null() || (prefixes.is_null() && count != 0) {
            return invalid("name and prefixes must not be null");
        }
        // SAFETY: guaranteed by the caller.
        let (agent, name, raw) = unsafe {
            (
                Agent::from_raw(agent),
                CStr::from_ptr(name),
                if count == 0 {
                    &[][..]
                } else {
                    core::slice::from_raw_parts(prefixes, count)
                },
            )
        };
        let Ok(name) = name.to_str() else {
            return invalid("name is not UTF-8");
        };
        let mut parsed = Vec::with_capacity(raw.len());
        for prefix in raw {
            parsed.push(match prefix.family {
                YANET_CP_FAMILY_IPV4 => Prefix::V4 {
                    from: [prefix.from[0], prefix.from[1], prefix.from[2], prefix.from[3]],
                    to: [prefix.to[0], prefix.to[1], prefix.to[2], prefix.to[3]],
                },
                YANET_CP_FAMILY_IPV6 => Prefix::V6 { from: prefix.from, to: prefix.to },
                _ => return invalid("unknown prefix family"),
            });
        }
        match decap_api::build(&agent, name, &parsed) {
            Ok(config) => {
                // SAFETY: guaranteed by the caller.
                unsafe { out.as_ptr().write(config.as_ptr()) };
                Ok(())
            }
            Err(ApiError::InvalidArgument(msg)) => Err((YANET_CP_EINVAL, msg)),
            Err(ApiError::Build(msg)) => Err((YANET_CP_EBUILD, msg)),
        }
    })
}

/// Destroys a decap configuration once no generation references it.
///
/// Returns -1 with errno EAGAIN and a C error chain in `*err` while a live
/// generation still holds the module, like the C module API.
///
/// # Safety
///
/// `cp_module` must come from `yanet_cp_decap_config_build` and not be
/// destroyed yet; `err` must be null or a writable `yanet_error *` slot.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn yanet_cp_decap_config_free(cp_module: *mut c_void, err: *mut *mut c_void) -> c_int {
    let mut rc = -1;
    let caught = std::panic::catch_unwind(core::panic::AssertUnwindSafe(|| {
        if let Some(config) = NonNull::new(cp_module) {
            // SAFETY: guaranteed by the caller.
            rc = unsafe { cp::free::<Decap>(config, err.cast()) };
        }
    }));
    if caught.is_err() { YANET_CP_EPANIC } else { rc }
}
