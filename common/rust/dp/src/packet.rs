//! Safe packet handle over the mbuf-embedded descriptor.

use core::marker::PhantomData;

use yanet_packet::{Eth2, EtherType, Gre, Icmp, Ipv4, Ipv4Mut, Ipv6, Ipv6Mut, Tcp, TcpMut, Udp, UdpMut};

use crate::raw;

// Dataplane symbols the module links against; they resolve inside the
// dataplane binary and the dataplane_ut harness.
unsafe extern "C" {
    fn parse_packet(packet: *mut raw::Packet) -> i32;
    fn packet_decap(packet: *mut raw::Packet) -> i32;
    fn worker_packet_alloc(worker: *mut raw::DpWorker) -> *mut raw::Packet;
    fn worker_clone_packet(
        worker: *mut raw::DpWorker,
        packet: *mut raw::Packet,
        packet_recirc_limit: u16,
    ) -> *mut raw::Packet;
    fn worker_packet_free(packet: *mut raw::Packet);
}

/// An unlinked packet between a pop and its next counted push.
///
/// The handle borrows the worker round: the packet dies with the front
/// that owns it, so it cannot outlive the module call it arrived in.
pub struct Packet<'a> {
    raw: *mut raw::Packet,
    _marker: PhantomData<&'a ()>,
}

impl<'a> Packet<'a> {
    /// Wrap a packet pointer the dataplane produced.
    ///
    /// # Safety
    ///
    /// `raw` must be non-null and unlinked for the duration `'a`.
    pub(crate) unsafe fn from_raw(raw: *mut raw::Packet) -> Self {
        debug_assert!(!raw.is_null());
        Self { raw, _marker: PhantomData }
    }

    pub(crate) fn as_raw(&self) -> *mut raw::Packet {
        self.raw
    }

    /// The raw mirror, for SDK-internal use.
    pub(crate) fn raw(&self) -> &raw::Packet {
        // SAFETY: the handle is only built around a live packet.
        unsafe { &*self.raw }
    }

    fn mbuf(&self) -> &raw::RteMbuf {
        // SAFETY: the descriptor's mbuf pointer is set by the RX path or
        // the allocation helper before any module sees the packet.
        unsafe { &*self.raw().mbuf }
    }

    fn mbuf_mut(&mut self) -> &mut raw::RteMbuf {
        // SAFETY: same as mbuf(), with the mutable borrow of the handle.
        unsafe { &mut *self.raw().mbuf }
    }

    /// The packet's first-segment bytes.
    pub fn data(&self) -> &[u8] {
        let mbuf = self.mbuf();
        // SAFETY: buf_addr + data_off names data_len bytes of a live
        // packet buffer owned by the mbuf pool.
        unsafe {
            core::slice::from_raw_parts(
                mbuf.buf_addr.add(mbuf.data_off as usize) as *const u8,
                mbuf.data_len as usize,
            )
        }
    }

    /// The packet's first-segment bytes, mutable.
    pub fn data_mut(&mut self) -> &mut [u8] {
        let mbuf = self.mbuf_mut();
        // SAFETY: same region as data(), uniquely borrowed here.
        unsafe {
            core::slice::from_raw_parts_mut(
                mbuf.buf_addr.add(mbuf.data_off as usize) as *mut u8,
                mbuf.data_len as usize,
            )
        }
    }

    /// Bytes from `offset` to the first segment's end.
    fn data_from(&self, offset: usize) -> Option<&[u8]> {
        self.data().get(offset..)
    }

    /// First-segment length in bytes.
    pub fn len(&self) -> u16 {
        self.mbuf().data_len
    }

    /// Whether the first segment is empty.
    pub fn is_empty(&self) -> bool {
        self.len() == 0
    }

    /// Whole-packet length across chained segments.
    pub fn total_len(&self) -> u32 {
        self.mbuf().pkt_len
    }

    /// The parser's flow hash.
    pub fn hash(&self) -> u32 {
        self.raw().hash
    }

    /// Flags, as [`raw::PACKET_FLAG_FRAGMENTED`] bit positions.
    pub fn flags(&self) -> u16 {
        self.raw().flags
    }

    /// Whether the parser marked the packet fragmented.
    pub fn is_fragmented(&self) -> bool {
        self.flags() & raw::PACKET_FLAG_FRAGMENTED != 0
    }

    /// The parsed VLAN id, zero when untagged.
    pub fn vlan(&self) -> u16 {
        self.raw().vlan
    }

    /// The device the packet was received on.
    pub fn rx_device_id(&self) -> u16 {
        self.raw().rx_device_id
    }

    /// The device the packet is routed to for transmission.
    pub fn tx_device_id(&self) -> u16 {
        self.raw().tx_device_id
    }

    /// Route the packet to a device for transmission.
    pub fn set_tx_device_id(&mut self, device_id: u16) {
        raw::Packet::set(self.raw, |raw| raw.tx_device_id = device_id);
    }

    /// Network header type in host byte order of the wire EtherType.
    pub fn network_type(&self) -> u16 {
        self.raw().network_type()
    }

    /// The parsed network header type, when recognized.
    ///
    /// The parser stores the wire value in a native u16 (byte-swapped on
    /// little-endian, like the C side's `rte_cpu_to_be_16` constants),
    /// so it converts back to host order here.
    pub fn network_type_known(&self) -> Option<EtherType> {
        EtherType::from_wire(u16::from_be(self.network_type()))
    }

    /// Network header offset in bytes from the packet start.
    pub fn network_offset(&self) -> u16 {
        self.raw().network_offset()
    }

    /// Transport header type, protocol plus tag bits.
    pub fn transport_type(&self) -> u16 {
        self.raw().transport_type()
    }

    /// Whether the transport header was parsed and is safe to read.
    pub fn transport_available(&self) -> bool {
        self.transport_type() & raw::PACKET_TRANSPORT_HEADER_UNAVAILABLE == 0
    }

    /// The declared transport protocol, valid even for fragment payload.
    pub fn transport_protocol(&self) -> u8 {
        self.raw().transport_protocol()
    }

    /// Transport header offset in bytes from the packet start.
    pub fn transport_offset(&self) -> u16 {
        self.raw().transport_offset()
    }

    // -- typed header views ------------------------------------------------

    /// Ethernet header view at the packet start.
    pub fn eth(&self) -> Option<Eth2<'_>> {
        Eth2::from_bytes(self.data())
    }

    /// IPv4 header view at the parsed network offset.
    pub fn ipv4(&self) -> Option<Ipv4<'_>> {
        Ipv4::from_bytes(self.data_from(self.network_offset() as usize)?)
    }

    /// Mutable IPv4 header view at the parsed network offset.
    pub fn ipv4_mut(&mut self) -> Option<Ipv4Mut<'_>> {
        let offset = self.network_offset() as usize;
        Ipv4Mut::from_bytes(self.data_mut().get_mut(offset..)?)
    }

    /// IPv6 header view at the parsed network offset.
    pub fn ipv6(&self) -> Option<Ipv6<'_>> {
        Ipv6::from_bytes(self.data_from(self.network_offset() as usize)?)
    }

    /// Mutable IPv6 header view at the parsed network offset.
    pub fn ipv6_mut(&mut self) -> Option<Ipv6Mut<'_>> {
        let offset = self.network_offset() as usize;
        Ipv6Mut::from_bytes(self.data_mut().get_mut(offset..)?)
    }

    /// TCP header view at the parsed transport offset.
    pub fn tcp(&self) -> Option<Tcp<'_>> {
        if !self.transport_available() {
            return None;
        }
        Tcp::from_bytes(self.data_from(self.transport_offset() as usize)?)
    }

    /// Mutable TCP header view at the parsed transport offset.
    pub fn tcp_mut(&mut self) -> Option<TcpMut<'_>> {
        if !self.transport_available() {
            return None;
        }
        let offset = self.transport_offset() as usize;
        TcpMut::from_bytes(self.data_mut().get_mut(offset..)?)
    }

    /// UDP header view at the parsed transport offset.
    pub fn udp(&self) -> Option<Udp<'_>> {
        if !self.transport_available() {
            return None;
        }
        Udp::from_bytes(self.data_from(self.transport_offset() as usize)?)
    }

    /// Mutable UDP header view at the parsed transport offset.
    pub fn udp_mut(&mut self) -> Option<UdpMut<'_>> {
        if !self.transport_available() {
            return None;
        }
        let offset = self.transport_offset() as usize;
        UdpMut::from_bytes(self.data_mut().get_mut(offset..)?)
    }

    /// ICMP/ICMPv6 header view at the parsed transport offset.
    pub fn icmp(&self) -> Option<Icmp<'_>> {
        if !self.transport_available() {
            return None;
        }
        Icmp::from_bytes(self.data_from(self.transport_offset() as usize)?)
    }

    /// GRE header view at the parsed transport offset.
    pub fn gre(&self) -> Option<Gre<'_>> {
        Gre::from_bytes(self.data_from(self.transport_offset() as usize)?)
    }

    // -- geometry ----------------------------------------------------------

    /// Strip `len` bytes from the packet head and shift both header
    /// offsets down, mirroring the decap convention.
    ///
    /// Valid only while the packet is unlinked (between a pop and the
    /// next push), and only when at least `len` bytes are present. The
    /// cached length and the mbuf geometry move together, so front byte
    /// accounting stays consistent for the next counted push.
    pub fn strip_head(&mut self, len: u16) -> bool {
        let new_data_len = {
            let mbuf = self.mbuf_mut();
            if mbuf.data_len < len {
                return false;
            }
            mbuf.data_off = mbuf.data_off.saturating_add(len);
            mbuf.data_len -= len;
            if mbuf.nb_segs == 1 {
                mbuf.pkt_len = mbuf.pkt_len.saturating_sub(len as u32);
            }
            mbuf.data_len
        };
        let raw = self.raw();
        let network = raw.network_offset().saturating_sub(len);
        let transport = raw.transport_offset().saturating_sub(len);
        let transport_type = raw.transport_type();
        raw::Packet::set(self.raw, move |raw| {
            raw.set_network_offset(network);
            raw.set_transport(transport_type, transport);
            raw.data_len = new_data_len;
        });
        true
    }

    /// Grow the packet head by `len` bytes and shift both header offsets
    /// up, so existing headers stay reachable.
    ///
    /// Returns the grown data region (whose first `len` bytes are the
    /// fresh head), or `None` when the headroom left above the packet
    /// descriptor cannot supply `len` bytes — the refusal contract of
    /// `packet_headroom_prepend`. Valid only while the packet is
    /// unlinked.
    pub fn prepend_head(&mut self, len: u16) -> Option<&mut [u8]> {
        let new_data_len = {
            let mbuf = self.mbuf_mut();
            let need = core::mem::size_of::<raw::Packet>() as u32 + len as u32;
            if (mbuf.data_off as u32) < need {
                return None;
            }
            mbuf.data_off -= len;
            mbuf.data_len = mbuf.data_len.saturating_add(len);
            if mbuf.nb_segs == 1 {
                mbuf.pkt_len = mbuf.pkt_len.saturating_add(len as u32);
            }
            mbuf.data_len
        };
        let raw = self.raw();
        let network = raw.network_offset().saturating_add(len);
        let transport = raw.transport_offset().saturating_add(len);
        let transport_type = raw.transport_type();
        raw::Packet::set(self.raw, move |raw| {
            raw.set_network_offset(network);
            raw.set_transport(transport_type, transport);
            raw.data_len = new_data_len;
        });
        Some(self.data_mut())
    }

    /// Consume one redirect credit of the packet lineage.
    ///
    /// Mirrors `packet_recirc_try_redirect`: the first call seeds the
    /// budget from `limit`; an exhausted budget returns false and the
    /// caller must drop the packet instead of routing it.
    pub fn recirc_try_redirect(&mut self, limit: u16) -> bool {
        raw::Packet::set(self.raw, |raw| {
            if raw.recirc_initialized == 0 {
                raw.recirc_remaining = limit;
                raw.recirc_initialized = 1;
            }
            if raw.recirc_remaining == 0 {
                return false;
            }
            raw.recirc_remaining -= 1;
            true
        })
    }

    /// Re-run the RX parser over the packet, refreshing metadata and hash.
    pub fn parse(&mut self) -> bool {
        // SAFETY: the handle wraps a live packet for this round.
        unsafe { parse_packet(self.raw) == 0 }
    }

    /// Decapsulate a GRE or IP-in-IP tunnel, mirroring `packet_decap`.
    ///
    /// Returns false (leaving the packet untouched) for a fragmented
    /// outer packet or an unrecognized tunnel.
    pub fn decap(&mut self) -> bool {
        // SAFETY: same live-packet contract.
        unsafe { packet_decap(self.raw) == 0 }
    }

    /// Allocate a fresh empty packet from the worker's pool.
    pub(crate) fn alloc(worker: *mut raw::DpWorker) -> Option<Self> {
        // SAFETY: the worker pointer comes from the module trampoline.
        let packet = unsafe { worker_packet_alloc(worker) };
        if packet.is_null() {
            return None;
        }
        // SAFETY: the allocation is unlinked by contract.
        Some(unsafe { Self::from_raw(packet) })
    }

    /// Deep-copy the packet, partitioning the redirect credits.
    pub(crate) fn clone_from(worker: *mut raw::DpWorker, packet: &Packet<'a>, limit: u16) -> Option<Self> {
        // SAFETY: the worker pointer comes from the trampoline and the
        // source packet is live for the round.
        let clone = unsafe { worker_clone_packet(worker, packet.raw, limit) };
        if clone.is_null() {
            return None;
        }
        // SAFETY: the clone is unlinked on success.
        Some(unsafe { Self::from_raw(clone) })
    }

    /// Return the packet to its pool without routing it anywhere.
    pub(crate) fn free(self) {
        // SAFETY: the handle owns one unlinked packet.
        unsafe { worker_packet_free(self.raw) };
    }
}

// Raw mirror helper used by the safe wrapper to keep every mutation in
// one auditable place: mutation goes through the raw pointer, never a
// reference cast.
impl raw::Packet {
    pub(crate) fn set<R>(raw: *mut Self, f: impl FnOnce(&mut Self) -> R) -> R {
        // SAFETY: callers hold the only live handle to the packet.
        unsafe { f(&mut *raw) }
    }
}
