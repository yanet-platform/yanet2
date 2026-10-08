//! Control-plane api of the vxlan device: creates, validates and reads vxlan
//! devices in the agent's shared memory.
//!
//! The Rust replacement of a C `devices/<name>/api/controlplane.c`. A device
//! is the C common header and the [`VxlanConfig`] body; it is created for
//! the body's fingerprint, which the dataplane's vxlan device type must have
//! been loaded with. The
//! whole device is validated before it is handed to the caller for publish:
//! every relative edge of the header and every part of the body must lie in
//! the owner agent's arenas, aligned, with the values in range. A device that
//! fails validation is destroyed again.

#![forbid(unsafe_code)]

use core::{
    ffi::CStr,
    fmt::{self, Display, Formatter},
    mem::size_of,
    net::Ipv4Addr,
};
use std::ffi::CString;

use yanet_cp_sys::{Agent, DeviceBlock, DeviceRequest, FreeError, Layout, ReadError};
use yanet_shm::{ItemLayout, ShmLayout, item_layout};
pub use yanet_vxlan_config::{TYPE_NAME, VNI_MAX, VxlanConfig};

/// Why a vxlan api call failed.
#[derive(Debug, PartialEq, Eq)]
pub enum Error {
    /// The request is wrong regardless of the system state.
    InvalidArgument(String),
    /// No live vxlan device has the name.
    NotFound(String),
    /// The dataplane has no vxlan device type, or one built for another
    /// configuration layout.
    FailedPrecondition(String),
    /// A live generation still references the device.
    StillReferenced,
    /// The C side or the validation of the built device refused.
    Failed(String),
}

impl Display for Error {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        match self {
            Self::InvalidArgument(message)
            | Self::NotFound(message)
            | Self::FailedPrecondition(message)
            | Self::Failed(message) => f.write_str(message),
            Self::StillReferenced => f.write_str("device still referenced by a live configuration generation"),
        }
    }
}

/// A pipeline binding of a device entry.
#[derive(Clone, Copy, Debug)]
pub struct Binding<'a> {
    pub pipeline: &'a str,
    pub weight: u64,
}

/// The layout of a vxlan device after a C header of the given layout.
pub fn device_layout(layout: &Layout) -> ItemLayout {
    item_layout::<VxlanConfig>(layout.cp_device_size(), layout.cp_device_align())
}

fn unicast_ip(field: &str, ip: [u8; 4]) -> Result<(), Error> {
    let addr = Ipv4Addr::from(ip);
    if addr.is_unspecified() || addr.is_multicast() || addr.is_broadcast() {
        return Err(Error::InvalidArgument(format!(
            "{field} {addr} must be a unicast address"
        )));
    }
    Ok(())
}

fn unicast_mac(field: &str, mac: [u8; 6]) -> Result<(), Error> {
    if mac == [0; 6] || mac[0] & 1 != 0 {
        return Err(Error::InvalidArgument(format!(
            "{field} {} must be a nonzero unicast address",
            mac.iter()
                .map(|byte| format!("{byte:02x}"))
                .collect::<Vec<_>>()
                .join(":")
        )));
    }
    Ok(())
}

/// Checks the tunnel parameters.
pub fn validate_tunnel(tunnel: &VxlanConfig) -> Result<(), Error> {
    unicast_ip("local ip", tunnel.local_ip)?;
    unicast_ip("remote ip", tunnel.remote_ip)?;
    unicast_mac("local mac", tunnel.local_mac)?;
    unicast_mac("remote mac", tunnel.remote_mac)?;
    if tunnel.vni > VNI_MAX {
        return Err(Error::InvalidArgument(format!(
            "vni {} must be in range 0..{VNI_MAX}",
            tunnel.vni
        )));
    }
    Ok(())
}

/// Checks a device or pipeline name against its C field and converts it.
pub fn c_name(field: &str, name: &str, capacity: usize) -> Result<CString, Error> {
    if name.is_empty() {
        return Err(Error::InvalidArgument(format!("{field} is required")));
    }
    if name.len() >= capacity {
        return Err(Error::InvalidArgument(format!(
            "{field} must be shorter than {capacity} bytes"
        )));
    }
    CString::new(name).map_err(|_| Error::InvalidArgument(format!("{field} must not contain NUL")))
}

fn borrow(owned: &[(CString, u64)]) -> Vec<(&CStr, u64)> {
    owned.iter().map(|(name, weight)| (name.as_c_str(), *weight)).collect()
}

/// A created device not handed out yet: destroyed when dropped, so neither
/// a refusal nor a panic before the hand-over leaks the block.
struct Unpublished<'a>(Option<DeviceBlock<'a>>);

impl<'a> Unpublished<'a> {
    fn block(&mut self) -> &mut DeviceBlock<'a> {
        self.0.as_mut().expect("held until handed out")
    }

    fn hand_out(mut self) -> DeviceBlock<'a> {
        self.0.take().expect("held until handed out")
    }
}

impl Drop for Unpublished<'_> {
    fn drop(&mut self) {
        if let Some(block) = self.0.take() {
            // Never published, so nothing can still reference it; a C
            // refusal leaves the block to the agent's wholesale reclaim.
            let _ = block.free();
        }
    }
}

/// Creates and validates a vxlan device, ready for publish.
///
/// The returned block is dangling until the caller publishes it.
pub fn create<'a>(
    agent: &'a Agent,
    name: &str,
    tunnel: &VxlanConfig,
    input: &[Binding<'_>],
    output: &[Binding<'_>],
) -> Result<DeviceBlock<'a>, Error> {
    validate_tunnel(tunnel)?;
    let layout = agent.layout();
    let name = c_name("name", name, layout.device_name_len())?;
    let bindings = |bindings: &[Binding<'_>]| -> Result<Vec<(CString, u64)>, Error> {
        bindings
            .iter()
            .map(|binding| {
                Ok((
                    c_name("pipeline", binding.pipeline, layout.pipeline_name_len())?,
                    binding.weight,
                ))
            })
            .collect()
    };
    let input = bindings(input)?;
    let output = bindings(output)?;
    let input = borrow(&input);
    let output = borrow(&output);
    let type_name = CString::new(TYPE_NAME).expect("type name has no NUL");

    let item = agent.device_layout::<VxlanConfig>();
    let request = DeviceRequest {
        type_name: &type_name,
        name: &name,
        input: &input,
        output: &output,
    };
    let block = agent.create_device::<VxlanConfig>(&request).map_err(|err| {
        if err.precondition {
            Error::FailedPrecondition(err.message)
        } else {
            Error::Failed(err.message)
        }
    })?;
    let mut device = Unpublished(Some(block));

    device
        .block()
        .write(item.body_offset, *tunnel)
        .map_err(|err| Error::Failed(format!("failed to write the device body: {err}")))?;
    if validate_device(agent, device.block())? != *tunnel {
        return Err(Error::Failed(
            "device body reads back different from the request".into(),
        ));
    }
    Ok(device.hand_out())
}

/// Validates a built device against the owner agent's arenas and returns
/// the tunnel it stores.
pub fn validate_device(agent: &Agent, block: &DeviceBlock<'_>) -> Result<VxlanConfig, Error> {
    let layout = agent.layout();
    let item = agent.device_layout::<VxlanConfig>();
    let failed = |what: &str, err: yanet_shm::ShmError| Error::Failed(format!("{what}: {err}"));

    if block.size() != item.size {
        return Err(Error::Failed(format!(
            "device block of {} bytes, the layout needs {}",
            block.size(),
            item.size
        )));
    }
    agent.validate(|validator| {
        validator
            .check_range(block.addr(), item.size, item.align)
            .map_err(|err| failed("device block", err))?;

        let edge = |offset: usize| block.rel_target(offset).map_err(|err| failed("device header", err));
        if edge(layout.cp_device_agent())? != Some(agent.addr()) {
            return Err(Error::Failed("device is not owned by this agent".into()));
        }
        for (what, offset) in [
            ("input pipelines", layout.cp_device_input()),
            ("output pipelines", layout.cp_device_output()),
        ] {
            let target = edge(offset)?.ok_or_else(|| Error::Failed(format!("{what} are missing")))?;
            validator
                .check_range(target, size_of::<u64>(), size_of::<u64>())
                .map_err(|err| failed(what, err))?;
        }

        VxlanConfig::validate(validator, block.addr() + item.body_offset).map_err(|err| failed("device body", err))
    })?;
    let tunnel: VxlanConfig = block.read(item.body_offset).map_err(|err| failed("device body", err))?;
    validate_tunnel(&tunnel)?;
    Ok(tunnel)
}

/// Reads the tunnel of the live vxlan device with the given name.
pub fn show(agent: &Agent, name: &str) -> Result<VxlanConfig, Error> {
    let c_name = c_name("name", name, agent.layout().device_name_len())?;
    let type_name = CString::new(TYPE_NAME).expect("type name has no NUL");
    agent
        .read_live::<VxlanConfig>(&type_name, &c_name)
        .map_err(|err| match err {
            ReadError::NotFound => Error::NotFound(format!("vxlan device '{name}' not found")),
            ReadError::Layout => Error::FailedPrecondition(format!(
                "vxlan device '{name}' belongs to a device type loaded for another configuration layout"
            )),
        })
}

/// Destroys a device, unless a live generation still references it.
pub fn free(block: DeviceBlock<'_>) -> Result<(), Error> {
    match block.free() {
        Ok(()) => Ok(()),
        Err(FreeError::StillReferenced(_)) => Err(Error::StillReferenced),
        Err(FreeError::Failed(message)) => Err(Error::Failed(message)),
    }
}

#[cfg(test)]
mod tests;
