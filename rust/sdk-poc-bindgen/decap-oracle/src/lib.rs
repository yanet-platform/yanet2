//! C reference implementation of the decap module for differential tests.

use core::ptr::NonNull;

use yanet_sys::bindings::{module_ectx, packet, packet_front};

unsafe extern "C" {
    fn decap_oracle_packet_new(frame: *const u8, len: u16) -> *mut packet;
    fn decap_oracle_packet_free(packet: *mut packet);
    fn decap_oracle_packet_data(packet: *mut packet, len: *mut u16) -> *const u8;
    fn decap_oracle_handle(ectx: *mut module_ectx, front: *mut packet_front);
}

/// Packet parsed by the C parser over a heap mbuf with default headroom.
pub struct OraclePacket {
    raw: NonNull<packet>,
}

impl OraclePacket {
    /// Builds and parses a packet, `None` when the C parser rejects the
    /// frame (the worker would drop it before any module).
    pub fn new(frame: &[u8]) -> Option<Self> {
        let len = u16::try_from(frame.len()).ok()?;
        // SAFETY: the frame is a valid buffer of `len` bytes.
        NonNull::new(unsafe { decap_oracle_packet_new(frame.as_ptr(), len) }).map(|raw| Self { raw })
    }

    /// Raw descriptor, for linking into a front.
    pub fn raw(&self) -> *mut packet {
        self.raw.as_ptr()
    }

    /// Current first-segment bytes.
    pub fn data(&self) -> Vec<u8> {
        let mut len = 0;
        // SAFETY: the packet is live.
        let ptr = unsafe { decap_oracle_packet_data(self.raw.as_ptr(), &mut len) };
        // SAFETY: the C helper returns the first segment.
        unsafe { core::slice::from_raw_parts(ptr, usize::from(len)) }.to_vec()
    }

    /// Copy of the descriptor.
    pub fn descriptor(&self) -> packet {
        // SAFETY: the packet is live.
        unsafe { self.raw.as_ptr().read() }
    }
}

impl Drop for OraclePacket {
    fn drop(&mut self) {
        // SAFETY: allocated by the C helper, freed once.
        unsafe { decap_oracle_packet_free(self.raw.as_ptr()) }
    }
}

/// Runs the C decap handler.
///
/// # Safety
///
/// `ectx` must carry an absolutized decap configuration and `front` a
/// well-formed input list of live packets.
pub unsafe fn c_handle(ectx: *mut module_ectx, front: *mut packet_front) {
    // SAFETY: guaranteed by the caller.
    unsafe { decap_oracle_handle(ectx, front) }
}
