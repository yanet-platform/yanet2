//! The C ABI of the Rust control plane, the one Rust archive the Go control
//! plane links.
//!
//! Every exported function takes only `#[repr(C)]` values and raw pointers,
//! runs its body under a panic guard, writes results only into caller
//! buffers or shared memory, and returns a status code with a message in a
//! caller buffer. No Rust-owned pointer escapes: a device pointer is a block
//! of the agent's shared memory. The archive also carries the regex C API
//! (`rure_*`), so the Go link holds exactly one Rust runtime.
//!
//! The C header `yanet_cp.h` is generated from this file by cbindgen at
//! build time and copied next to the archive in the build directory.

#![allow(non_camel_case_types)]

use core::ffi::{c_char, c_void};

use rure as _;
use yanet_cp_sys::{
    Agent, DeviceBlock,
    abi::{self, Failure},
    c_layout,
};
use yanet_vxlan_api as vxlan;

/// Version of this ABI: bumped on any change of an exported signature or
/// type. The Go control plane refuses an archive of another version.
pub const YANET_CP_ABI_VERSION: u32 = 1;

/// The call succeeded.
pub const YANET_CP_OK: i32 = 0;
/// The request is wrong regardless of the system state.
pub const YANET_CP_INVALID_ARGUMENT: i32 = 1;
/// The named entity does not exist.
pub const YANET_CP_NOT_FOUND: i32 = 2;
/// A live configuration generation still references the device.
pub const YANET_CP_STILL_REFERENCED: i32 = 3;
/// The C side or the validation of the built configuration refused.
pub const YANET_CP_FAILED: i32 = 4;
/// A Rust panic was caught at the boundary.
pub const YANET_CP_PANIC: i32 = 5;
/// The system is not in the state the call needs, such as a dataplane
/// without the device type or with another configuration layout.
pub const YANET_CP_FAILED_PRECONDITION: i32 = 6;

// The exported codes are the codes the guard returns.
const _: () = assert!(
    YANET_CP_OK == abi::OK
        && YANET_CP_INVALID_ARGUMENT == abi::INVALID_ARGUMENT
        && YANET_CP_NOT_FOUND == abi::NOT_FOUND
        && YANET_CP_STILL_REFERENCED == abi::STILL_REFERENCED
        && YANET_CP_FAILED == abi::FAILED
        && YANET_CP_PANIC == abi::PANIC
        && YANET_CP_FAILED_PRECONDITION == abi::FAILED_PRECONDITION
);

/// Capacity a caller should give the error buffer.
pub const YANET_CP_ERROR_LEN: usize = 256;

/// Tunnel parameters of a vxlan device.
///
/// Addresses are in network byte order, the VNI in host byte order.
#[repr(C)]
#[derive(Clone, Copy)]
pub struct yanet_cp_vxlan_tunnel {
    pub local_mac: [u8; 6],
    pub remote_mac: [u8; 6],
    pub local_ip: [u8; 4],
    pub remote_ip: [u8; 4],
    pub vni: u32,
}

/// One pipeline binding of a device entry.
#[repr(C)]
#[derive(Clone, Copy)]
pub struct yanet_cp_pipeline {
    /// Zero-terminated pipeline name.
    pub name: *const c_char,
    pub weight: u64,
}

/// Everything a vxlan device is created with.
#[repr(C)]
#[derive(Clone, Copy)]
pub struct yanet_cp_vxlan_device_request {
    /// Zero-terminated device name.
    pub name: *const c_char,
    pub tunnel: yanet_cp_vxlan_tunnel,
    pub input: *const yanet_cp_pipeline,
    pub input_len: usize,
    pub output: *const yanet_cp_pipeline,
    pub output_len: usize,
}

impl From<yanet_cp_vxlan_tunnel> for vxlan::VxlanConfig {
    fn from(tunnel: yanet_cp_vxlan_tunnel) -> Self {
        Self {
            local_mac: tunnel.local_mac,
            remote_mac: tunnel.remote_mac,
            local_ip: tunnel.local_ip,
            remote_ip: tunnel.remote_ip,
            vni: tunnel.vni,
        }
    }
}

impl From<vxlan::VxlanConfig> for yanet_cp_vxlan_tunnel {
    fn from(config: vxlan::VxlanConfig) -> Self {
        Self {
            local_mac: config.local_mac,
            remote_mac: config.remote_mac,
            local_ip: config.local_ip,
            remote_ip: config.remote_ip,
            vni: config.vni,
        }
    }
}

/// Maps an api error onto its status code and message.
fn failure(err: vxlan::Error) -> Failure {
    let code = match &err {
        vxlan::Error::InvalidArgument(_) => abi::INVALID_ARGUMENT,
        vxlan::Error::NotFound(_) => abi::NOT_FOUND,
        vxlan::Error::FailedPrecondition(_) => abi::FAILED_PRECONDITION,
        vxlan::Error::StillReferenced => abi::STILL_REFERENCED,
        vxlan::Error::Failed(_) => abi::FAILED,
    };
    Failure::new(code, err.to_string())
}

fn utf8(value: &core::ffi::CStr, what: &str) -> Result<String, Failure> {
    value
        .to_str()
        .map(str::to_owned)
        .map_err(|_| Failure::new(abi::INVALID_ARGUMENT, format!("{what} must be UTF-8")))
}

/// # Safety
///
/// `agent` must be null or a live attached agent.
unsafe fn agent(agent: *mut c_void) -> Result<Agent, Failure> {
    // SAFETY: per the caller contract.
    unsafe { Agent::from_raw(agent) }.ok_or_else(|| Failure::new(abi::INVALID_ARGUMENT, "null agent"))
}

fn borrow(owned: &[(String, u64)]) -> Vec<vxlan::Binding<'_>> {
    owned
        .iter()
        .map(|(pipeline, weight)| vxlan::Binding { pipeline, weight: *weight })
        .collect()
}

/// Returns the ABI version of the linked archive.
#[unsafe(no_mangle)]
pub extern "C" fn yanet_cp_abi_version() -> u32 {
    YANET_CP_ABI_VERSION
}

/// Creates and validates a vxlan device in the agent's memory.
///
/// On success stores the dangling device in `*device`; the caller publishes
/// it with an agent device update and frees it with
/// `yanet_cp_vxlan_device_free`.
///
/// # Safety
///
/// `agent` must be a live attached agent, `request` a valid request whose
/// strings and arrays stay unchanged during the call, `device` writable,
/// and `err` null or writable for `err_len` bytes.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn yanet_cp_vxlan_device_new(
    agent_ptr: *mut c_void,
    request: *const yanet_cp_vxlan_device_request,
    device: *mut *mut c_void,
    err: *mut c_char,
    err_len: usize,
) -> i32 {
    // SAFETY: the caller contract covers every pointer the body reads.
    unsafe {
        abi::guard(err, err_len, || {
            let agent = agent(agent_ptr)?;
            let request = abi::read(request)?;
            let name = utf8(abi::cstr(request.name)?, "device name")?;
            let bindings = |ptr, len| -> Result<Vec<(String, u64)>, Failure> {
                abi::slice::<yanet_cp_pipeline>(ptr, len)?
                    .iter()
                    .map(|binding| Ok((utf8(abi::cstr(binding.name)?, "pipeline name")?, binding.weight)))
                    .collect()
            };
            let input = bindings(request.input, request.input_len)?;
            let output = bindings(request.output, request.output_len)?;

            let block = vxlan::create(&agent, &name, &request.tunnel.into(), &borrow(&input), &borrow(&output))
                .map_err(failure)?;
            let raw = block.into_raw();
            abi::write(device, raw).inspect_err(|_| {
                // Nobody can receive the device, so it goes back at once.
                // SAFETY: the block was just created and never published.
                if let Some(block) = DeviceBlock::from_raw(raw, vxlan::device_layout(&c_layout()).size) {
                    let _ = vxlan::free(block);
                }
            })
        })
    }
}

/// Destroys a vxlan device created by `yanet_cp_vxlan_device_new`.
///
/// Returns `YANET_CP_STILL_REFERENCED`, leaving the device intact, while a
/// live generation references it.
///
/// # Safety
///
/// `device` must come from `yanet_cp_vxlan_device_new` and not be destroyed
/// yet; `err` must be null or writable for `err_len` bytes.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn yanet_cp_vxlan_device_free(device: *mut c_void, err: *mut c_char, err_len: usize) -> i32 {
    // SAFETY: the caller contract covers the device and the buffer.
    unsafe {
        abi::guard(err, err_len, || {
            let size = vxlan::device_layout(&c_layout()).size;
            let block = DeviceBlock::from_raw(device, size)
                .ok_or_else(|| Failure::new(abi::INVALID_ARGUMENT, "null device"))?;
            vxlan::free(block).map_err(failure)
        })
    }
}

/// Reads the tunnel of the live vxlan device with the given name.
///
/// # Safety
///
/// `agent` must be a live attached agent, `name` a zero-terminated string,
/// `tunnel` writable, and `err` null or writable for `err_len` bytes.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn yanet_cp_vxlan_device_show(
    agent_ptr: *mut c_void,
    name: *const c_char,
    tunnel: *mut yanet_cp_vxlan_tunnel,
    err: *mut c_char,
    err_len: usize,
) -> i32 {
    // SAFETY: the caller contract covers every pointer the body touches.
    unsafe {
        abi::guard(err, err_len, || {
            let agent = agent(agent_ptr)?;
            let name = utf8(abi::cstr(name)?, "device name")?;
            let config = vxlan::show(&agent, &name).map_err(failure)?;
            abi::write(tunnel, config.into())
        })
    }
}
