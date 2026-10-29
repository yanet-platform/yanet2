//! Configuration field types backed by C registries of the module.

use yanet_shm::shm::ShmLayout;

/// Per-worker packet counter of the module, registered by the control plane.
///
/// Holds the counter's index in the module's C counter registry; the
/// dataplane adds to the current worker's value without atomic
/// read-modify-write, as C modules do.
#[repr(C)]
pub struct Counter {
    id: u64,
}

impl Counter {
    /// Index in the module's counter registry.
    #[inline(always)]
    pub fn id(&self) -> u64 {
        self.id
    }
}

/// An index relative to a module configuration's device namespace.
///
/// The dataplane resolves it to the device's routing target of the running
/// generation, which is absent when the index or device is not part of it.
#[repr(C)]
pub struct Device {
    index: u64,
}

impl Device {
    /// Index in the module's device list.
    #[inline(always)]
    pub fn index(&self) -> u64 {
        self.index
    }
}

// SAFETY: a registry index accepts every bit pattern; the control plane
// writes the id the module's counter registry assigned.
unsafe impl ShmLayout for Counter {}

// SAFETY: every device index is a valid word; resolution checks namespace
// bounds.
unsafe impl ShmLayout for Device {}
