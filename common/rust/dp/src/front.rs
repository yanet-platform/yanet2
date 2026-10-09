//! Safe packet front and list access.

use core::marker::PhantomData;

use crate::{packet::Packet, raw};

/// The packet batch a module call receives.
///
/// The contract mirrors `struct packet_front`: pop packets from the
/// input, push each exactly once to the output or the drop list. A
/// force-polled tick can hand over an empty front, so an empty input is
/// normal.
pub struct PacketFront<'a> {
    raw: &'a mut raw::PacketFront,
}

impl<'a> PacketFront<'a> {
    /// Wrap a front the dataplane handed to the module.
    ///
    /// # Safety
    ///
    /// `raw` must point at a live front for the duration `'a`, and no
    /// other wrapper may cover it.
    pub unsafe fn from_raw(raw: &'a mut raw::PacketFront) -> Self {
        Self { raw }
    }

    /// The wrapped mirror, for SDK-internal routing helpers.
    pub(crate) fn raw_mut(&mut self) -> &mut raw::PacketFront {
        self.raw
    }

    /// Pop the next input packet, `None` once the input is drained.
    pub fn pop_input(&mut self) -> Option<Packet<'a>> {
        // SAFETY: the input list is well-formed by the dataplane's
        // construction; popping owns the returned packet until it is
        // pushed to a list or freed.
        unsafe {
            let packet = packet_list_pop(&mut self.raw.input);
            Some(Packet::from_raw(packet?))
        }
    }

    /// Queue a packet as this module's output, continuing the pipeline.
    pub fn output(&mut self, packet: Packet<'a>) {
        // SAFETY: the packet came from this front's input (or a worker
        // allocation on the same worker) and is currently unlinked.
        unsafe {
            packet_list_add(&mut self.raw.output, packet.as_raw());
            self.raw.output_count += 1;
            self.raw.output_bytes += (*packet.as_raw()).data_len as u64;
        }
    }

    /// Queue a packet for dropping after the round.
    pub fn drop_packet(&mut self, packet: Packet<'a>) {
        // SAFETY: same ownership contract as output.
        unsafe {
            packet_list_add(&mut self.raw.drop, packet.as_raw());
            self.raw.drop_count += 1;
            self.raw.drop_bytes += (*packet.as_raw()).data_len as u64;
        }
    }

    /// Packets this front has taken as input so far.
    pub fn input_count(&self) -> u64 {
        self.raw.input_count
    }

    /// First-segment bytes across the inputs counted so far.
    pub fn input_bytes(&self) -> u64 {
        self.raw.input_bytes
    }

    /// Packets queued as output so far.
    pub fn output_count(&self) -> u64 {
        self.raw.output_count
    }

    /// First-segment bytes across the outputs queued so far.
    pub fn output_bytes(&self) -> u64 {
        self.raw.output_bytes
    }

    /// Packets queued for dropping so far.
    pub fn drop_count(&self) -> u64 {
        self.raw.drop_count
    }

    /// First-segment bytes across the drops queued so far.
    pub fn drop_bytes(&self) -> u64 {
        self.raw.drop_bytes
    }
}

/// Mirror of `packet_list_add`.
///
/// # Safety
///
/// `packet` must be unlinked and not covered by any other list.
pub(crate) unsafe fn packet_list_add(list: &mut raw::PacketList, packet: *mut raw::Packet) {
    unsafe {
        (*packet).next = core::ptr::null_mut();
        if list.last.is_null() {
            list.last = &mut list.first;
        }
        *list.last = packet;
        list.last = &mut (*packet).next;
    }
}

/// Mirror of `packet_list_pop`.
///
/// # Safety
///
/// The list must be well-formed.
pub(crate) unsafe fn packet_list_pop(list: &mut raw::PacketList) -> Option<*mut raw::Packet> {
    unsafe {
        let packet = list.first;
        if packet.is_null() {
            return None;
        }
        list.first = (*packet).next;
        if list.first.is_null() {
            list.last = core::ptr::null_mut();
        }
        (*packet).next = core::ptr::null_mut();
        Some(packet)
    }
}

/// An owned handle for building a packet list outside a front.
///
/// Used by tests and by helpers that stage packets before moving them
/// into a front in bulk.
pub struct PacketListBuilder<'a> {
    raw: raw::PacketList,
    _marker: PhantomData<&'a mut ()>,
}

impl<'a> PacketListBuilder<'a> {
    /// A fresh empty list.
    pub fn new() -> Self {
        Self {
            raw: raw::PacketList {
                first: core::ptr::null_mut(),
                last: core::ptr::null_mut(),
            },
            _marker: PhantomData,
        }
    }

    /// Append a packet.
    pub fn push(&mut self, packet: Packet<'a>) {
        // SAFETY: the builder owns its list and the packet is unlinked.
        unsafe { packet_list_add(&mut self.raw, packet.as_raw()) };
    }

    /// Pop the head packet.
    pub fn pop(&mut self) -> Option<Packet<'a>> {
        // SAFETY: the list is well-formed by construction.
        unsafe { packet_list_pop(&mut self.raw).map(|p| Packet::from_raw(p)) }
    }
}

impl<'a> Default for PacketListBuilder<'a> {
    fn default() -> Self {
        Self::new()
    }
}
