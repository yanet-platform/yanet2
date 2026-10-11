//! Worker-owned packet front and packets of one module invocation.
//!
//! Packets, their mbufs and the front belong to the invoking worker for the
//! duration of the handler call; their pointers come from C and keep the
//! provenance C gave them (`ffi` fields), since they live outside the
//! shared-memory mapping.

use core::{marker::PhantomData, ptr::NonNull};

use crate::{
    bindings,
    views::{MbufRaw, PacketFrontRaw, PacketListRaw, PacketRaw},
};

/// Exclusive handle to the packet front of one handler invocation.
pub struct PacketFront<'r> {
    raw: NonNull<bindings::packet_front>,
    _round: PhantomData<&'r mut bindings::packet_front>,
}

/// A packet taken from the input list.
///
/// Not `Copy`: a packet is either output or dropped exactly once. A packet
/// that is neither leaks its mbuf, as in C.
#[must_use = "a packet must be output or dropped"]
pub struct Packet<'r> {
    raw: NonNull<bindings::packet>,
    _round: PhantomData<&'r mut bindings::packet>,
}

/// The tunnel could not be removed; the packet should be dropped.
#[derive(Debug, PartialEq, Eq)]
pub struct DecapError;

impl<'r> PacketFront<'r> {
    /// Wraps the front passed to a module handler.
    ///
    /// # Safety
    ///
    /// `raw` must be the front C passed to the running handler: valid,
    /// exclusively owned by this invocation for 'r, with well-formed lists
    /// of packets that are themselves exclusively owned.
    pub unsafe fn from_raw(raw: NonNull<bindings::packet_front>) -> Self {
        Self { raw, _round: PhantomData }
    }

    fn list(&self, field: fn(*mut bindings::packet_front) -> *mut bindings::packet_list) -> *mut bindings::packet_list {
        field(self.raw.as_ptr())
    }

    /// Takes the next input packet.
    #[inline(always)]
    pub fn pop(&mut self) -> Option<Packet<'r>> {
        let list = self.list(PacketFrontRaw::input);
        // SAFETY: the front is exclusively ours and its lists well formed
        // (construction contract); this is the C list pop.
        unsafe {
            let first = PacketListRaw::first(list);
            let packet = NonNull::new(*first)?;
            *first = *PacketRaw::next(packet.as_ptr());
            if (*first).is_null() {
                *PacketListRaw::last(list) = core::ptr::null_mut();
            }
            Some(Packet { raw: packet, _round: PhantomData })
        }
    }

    /// Passes the packet to the next pipeline stage.
    #[inline(always)]
    pub fn output(&mut self, packet: Packet<'r>) {
        let list = self.list(PacketFrontRaw::output);
        let front = self.raw.as_ptr();
        // SAFETY: as in `pop`; the packet is exclusively ours.
        unsafe {
            push(list, packet.raw.as_ptr());
            *PacketFrontRaw::output_count(front) += 1;
            *PacketFrontRaw::output_bytes(front) += u64::from(*PacketRaw::data_len(packet.raw.as_ptr()));
        }
    }

    /// Drops the packet.
    #[inline(always)]
    pub fn drop(&mut self, packet: Packet<'r>) {
        let list = self.list(PacketFrontRaw::drop);
        let front = self.raw.as_ptr();
        // SAFETY: as in `output`.
        unsafe {
            push(list, packet.raw.as_ptr());
            *PacketFrontRaw::drop_count(front) += 1;
            *PacketFrontRaw::drop_bytes(front) += u64::from(*PacketRaw::data_len(packet.raw.as_ptr()));
        }
    }
}

/// Appends a packet to an intrusive list, as the C list add does.
///
/// # Safety
///
/// `list` and `packet` must be valid and exclusively owned.
#[inline(always)]
unsafe fn push(list: *mut bindings::packet_list, packet: *mut bindings::packet) {
    // SAFETY: guaranteed by the caller.
    unsafe {
        *PacketRaw::next(packet) = core::ptr::null_mut();
        let last = PacketListRaw::last(list);
        if (*last).is_null() {
            *last = PacketListRaw::first(list);
        }
        **last = packet;
        *last = PacketRaw::next(packet);
    }
}

impl Packet<'_> {
    /// Ether type of the network header, host byte order.
    #[inline(always)]
    pub fn network_type(&self) -> u16 {
        // SAFETY: the packet is exclusively ours for the round.
        u16::from_be(unsafe { (*PacketRaw::network_header(self.raw.as_ptr())).type_ })
    }

    /// Offset of the network header from the start of the frame.
    #[inline(always)]
    pub fn network_offset(&self) -> u16 {
        // SAFETY: as above.
        unsafe { (*PacketRaw::network_header(self.raw.as_ptr())).offset }
    }

    /// Bytes of the first segment of the frame.
    #[inline(always)]
    pub fn data(&self) -> &[u8] {
        // SAFETY: the mbuf of an owned packet is valid; its first segment
        // spans data_len bytes from buf_addr + data_off.
        unsafe {
            let mbuf = *PacketRaw::mbuf(self.raw.as_ptr());
            let base = (*MbufRaw::buf_addr(mbuf)).cast::<u8>();
            let start = base.add(usize::from(*MbufRaw::data_off(mbuf)));
            core::slice::from_raw_parts(start, usize::from(*MbufRaw::data_len(mbuf)))
        }
    }

    /// Bytes of the first segment from the network header on.
    #[inline(always)]
    pub fn network_data(&self) -> &[u8] {
        self.data().get(usize::from(self.network_offset())..).unwrap_or(&[])
    }

    /// Sets the 20-bit IPv6 flow label carried with the packet.
    #[inline(always)]
    pub fn set_flow_label(&mut self, label: u32) {
        // SAFETY: as above.
        unsafe { *PacketRaw::flow_label(self.raw.as_ptr()) = label }
    }

    /// Flow label carried with the packet.
    pub fn flow_label(&self) -> u32 {
        // SAFETY: as above.
        unsafe { *PacketRaw::flow_label(self.raw.as_ptr()) }
    }

    /// Removes the outer IP-in-IP or GRE tunnel headers.
    ///
    /// Backed by the C `packet_decap`, compiled into the module: porting it
    /// needs mbuf adjustment and the inner header parser.
    pub fn decap(&mut self) -> Result<(), DecapError> {
        // SAFETY: the packet and its mbuf are exclusively ours.
        match unsafe { bindings::packet_decap(self.raw.as_ptr()) } {
            0 => Ok(()),
            _ => Err(DecapError),
        }
    }
}
