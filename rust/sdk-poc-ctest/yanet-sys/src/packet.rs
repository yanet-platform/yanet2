//! Per-round packet and pipeline-front handles.
//!
//! Packets and fronts are owned by the worker for one handler invocation; the
//! handles carry that exclusivity as a lifetime. The list operations are
//! ports of the static inline helpers in `packet.h` and `packet_front.h`.

use core::{
    ffi::c_int,
    fmt::{self, Display, Formatter},
    marker::PhantomData,
    ptr::{self, NonNull},
};

use crate::ffi::{
    YANET_RS_MBUF_BUF_ADDR_OFFSET, YANET_RS_MBUF_DATA_LEN_OFFSET, YANET_RS_MBUF_DATA_LEN_SIZE,
    YANET_RS_MBUF_DATA_OFF_OFFSET, YANET_RS_MBUF_DATA_OFF_SIZE, packet, packet_decap_fn, packet_front, packet_list,
};

const _: () =
    assert!(YANET_RS_MBUF_DATA_OFF_SIZE == size_of::<u16>() && YANET_RS_MBUF_DATA_LEN_SIZE == size_of::<u16>());

unsafe extern "C" {
    /// Strips the tunnel headers in place; exported by the dataplane binary.
    fn packet_decap(packet: *mut packet) -> c_int;
}

// Ties the declaration above to the signature mirror; both are hand-written
// from the C prototype, which the systest header pins on the C side.
const _: packet_decap_fn = packet_decap;

/// One packet, exclusively held by the current handler invocation.
pub struct Packet<'r> {
    raw: NonNull<packet>,
    _round: PhantomData<&'r mut packet>,
}

/// The tunnel stripper refused the packet.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct DecapError;

impl Display for DecapError {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        write!(f, "packet is not a decapsulable tunnel")
    }
}

impl core::error::Error for DecapError {}

impl Packet<'_> {
    /// Wraps a packet popped from a front.
    ///
    /// # Safety
    ///
    /// `raw` must be a parsed packet whose mbuf is live, with the first
    /// segment's data length counted from its data offset inside the mbuf
    /// buffer, and no one else may access the packet, the mbuf or its data
    /// while the handle lives.
    pub unsafe fn from_raw(raw: NonNull<packet>) -> Self {
        Self { raw, _round: PhantomData }
    }

    /// Releases the handle back to a raw pointer.
    pub fn into_raw(self) -> NonNull<packet> {
        self.raw
    }

    fn get(&self) -> &packet {
        // SAFETY: the handle holds exclusive access to a live packet.
        unsafe { self.raw.as_ref() }
    }

    /// EtherType of the network header, host order.
    pub fn ether_type(&self) -> u16 {
        u16::from_be(self.get().network_header.r#type)
    }

    /// First `N` bytes at the network header, if the first segment holds them.
    ///
    /// The bound is the mbuf's own segment length, not the descriptor's
    /// cached copy, which a C stage may leave stale.
    pub fn network_header<const N: usize>(&self) -> Option<&[u8; N]> {
        let offset = usize::from(self.get().network_header.offset);
        let mbuf = self.get().mbuf.cast::<u8>();
        // SAFETY: the packet's mbuf is live (handle contract); the three
        // fields are located by ctest-checked offsets and read without
        // forming a reference to the mbuf.
        let (buf_addr, data_off, data_len) = unsafe {
            (
                mbuf.add(YANET_RS_MBUF_BUF_ADDR_OFFSET).cast::<*mut u8>().read(),
                mbuf.add(YANET_RS_MBUF_DATA_OFF_OFFSET).cast::<u16>().read(),
                mbuf.add(YANET_RS_MBUF_DATA_LEN_OFFSET).cast::<u16>().read(),
            )
        };
        if offset + N > usize::from(data_len) {
            return None;
        }
        // SAFETY: the requested bytes lie inside the first segment's data,
        // which the handle owns exclusively for its lifetime.
        unsafe { Some(&*buf_addr.add(usize::from(data_off) + offset).cast::<[u8; N]>()) }
    }

    /// Records the IPv6 flow label (low 20 bits) for later stages.
    pub fn set_flow_label(&mut self, label: u32) {
        // SAFETY: the handle holds exclusive access to a live packet.
        unsafe { (&raw mut (*self.raw.as_ptr()).flow_label).write(label) }
    }

    /// Strips tunnel headers in place with the dataplane's C routine.
    pub fn decap(&mut self) -> Result<(), DecapError> {
        // SAFETY: the handle holds exclusive access to a live parsed packet,
        // which is all the C routine requires; no Rust reference into the
        // packet or its data outlives this call because it borrows `self`
        // mutably.
        match unsafe { packet_decap(self.raw.as_ptr()) } {
            0 => Ok(()),
            _ => Err(DecapError),
        }
    }
}

/// Pipeline front of one handler invocation.
pub struct Front<'r> {
    raw: NonNull<packet_front>,
    _round: PhantomData<&'r mut packet_front>,
}

impl<'r> Front<'r> {
    /// Wraps the front passed to a module handler.
    ///
    /// # Safety
    ///
    /// `raw` must be the live front of the current invocation, accessed by no
    /// one else while the handle lives, whose input packets each satisfy the
    /// [`Packet::from_raw`] contract.
    pub unsafe fn from_raw(raw: NonNull<packet_front>) -> Self {
        Self { raw, _round: PhantomData }
    }

    /// Takes the next input packet.
    pub fn pop_input(&mut self) -> Option<Packet<'r>> {
        // SAFETY: the front is exclusively held and its lists are well formed.
        let raw = unsafe { list_pop(&raw mut (*self.raw.as_ptr()).input) }?;
        // SAFETY: a popped packet is unlinked and owned by this invocation.
        Some(unsafe { Packet::from_raw(raw) })
    }

    /// Passes a packet to the next stage.
    pub fn output(&mut self, packet: Packet<'r>) {
        let raw = packet.into_raw();
        // SAFETY: the front is exclusively held; the packet came from it.
        unsafe {
            let front = self.raw.as_ptr();
            list_add(&raw mut (*front).output, raw);
            (*front).output_count += 1;
            (*front).output_bytes += u64::from((*raw.as_ptr()).data_len);
        }
    }

    /// Drops a packet.
    pub fn drop_packet(&mut self, packet: Packet<'r>) {
        let raw = packet.into_raw();
        // SAFETY: the front is exclusively held; the packet came from it.
        unsafe {
            let front = self.raw.as_ptr();
            list_add(&raw mut (*front).drop, raw);
            (*front).drop_count += 1;
            (*front).drop_bytes += u64::from((*raw.as_ptr()).data_len);
        }
    }
}

/// Port of `packet_list_pop`.
///
/// # Safety
///
/// `list` must be a live, well-formed list exclusively held by the caller.
unsafe fn list_pop(list: *mut packet_list) -> Option<NonNull<packet>> {
    // SAFETY: forwarded caller contract.
    unsafe {
        let first = NonNull::new((*list).first)?;
        (*list).first = (*first.as_ptr()).next;
        if (*list).first.is_null() {
            (*list).last = ptr::null_mut();
        }
        Some(first)
    }
}

/// Port of `packet_list_add`.
///
/// # Safety
///
/// `list` must be a live, well-formed list exclusively held by the caller and
/// `packet` a live packet in no list.
unsafe fn list_add(list: *mut packet_list, packet: NonNull<packet>) {
    // SAFETY: forwarded caller contract.
    unsafe {
        let packet = packet.as_ptr();
        (*packet).next = ptr::null_mut();
        if (*list).last.is_null() {
            (*list).last = &raw mut (*list).first;
        }
        *(*list).last = packet;
        (*list).last = &raw mut (*packet).next;
    }
}
