//! The vxlan device body shared by the dataplane device and its control
//! plane.
//!
//! Both sides read and write the same bytes in shared memory: the control
//! plane writes the body once before publishing the device, the dataplane
//! reads it afterwards. This type is the layout of record; the dataplane
//! registers its structural fingerprint with the vxlan device type, and the
//! C device init refuses a control plane that presents another one.

#![no_std]
#![forbid(unsafe_code)]

use yanet_shm::ShmLayout;

/// Device type name the dataplane registers and the control plane creates
/// devices under.
pub const TYPE_NAME: &str = "vxlan";

/// UDP destination port IANA assigned to VXLAN.
pub const VXLAN_PORT: u16 = 4789;

/// The largest VXLAN network identifier: the field is 24 bits wide.
pub const VNI_MAX: u32 = 0x00ff_ffff;

/// Device body as stored after the common device header.
///
/// Addresses are in network byte order, the VNI in host byte order.
#[derive(ShmLayout, Clone, Copy, Debug, Default, PartialEq, Eq)]
#[repr(C)]
pub struct VxlanConfig {
    pub local_mac: [u8; 6],
    pub remote_mac: [u8; 6],
    pub local_ip: [u8; 4],
    pub remote_ip: [u8; 4],
    pub vni: u32,
}
