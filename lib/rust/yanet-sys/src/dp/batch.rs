//! Packets of one handler call and their disposal.
//!
//! The packet lists stay C's and are used as a C handler uses them: taking
//! a packet pops it off the input list, and its verdict moves it to the
//! output or drop list or routes it to a device. Every input packet is
//! passed, dropped or routed to a device (module: drain).

use core::{marker::PhantomData, mem::size_of, ptr::NonNull};

use crate::bindings;

/// Packets of one handler call.
///
/// Neither `Send` nor `Sync`; packets it hands out carry its brand `'id`
/// and are accepted back only by the batch of the same call.
///
/// A packet of one call's batch is refused by the batch of another call (a
/// lifetime error, which has no code):
///
/// ```compile_fail
/// use yanet_sys::dp::Batch;
/// fn mix<'a, 'b>(a: &mut Batch<'a>, b: &mut Batch<'b>) {
///     if let Some(packet) = a.next() {
///         b.pass(packet);
///     }
/// }
/// ```
///
/// The same batch disposes of its own packet:
///
/// ```
/// use yanet_sys::dp::Batch;
/// fn own<'a>(a: &mut Batch<'a>) {
///     if let Some(packet) = a.next() {
///         a.pass(packet);
///     }
/// }
/// ```
pub struct Batch<'id> {
    front: NonNull<bindings::packet_front>,
    ectx: NonNull<bindings::module_ectx>,
    worker: NonNull<bindings::dp_worker>,
    // Invariant brand: values of two different contexts never unify, so they cannot be mixed.
    _brand: PhantomData<fn(&'id ()) -> &'id ()>,
    // Neither Send nor Sync on purpose, independent of the pointer fields.
    _local: PhantomData<*mut ()>,
}

/// A packet taken from or allocated by the batch, owned until it is
/// disposed of.
///
/// Not `Copy` or `Clone`; consumed by passing, dropping or redirecting it. A
/// packet that is never disposed of is on no list
/// after the call, as one a C handler forgets.
///
/// A packet cannot be kept beyond the call it was taken in (a lifetime error,
/// which has no code):
///
/// ```compile_fail
/// use yanet_sys::dp::{Batch, Packet};
/// fn keep<'a>(batch: &mut Batch<'a>, kept: &mut Option<Packet<'static>>) {
///     *kept = batch.next();
/// }
/// ```
///
/// Held for the call, it is accepted:
///
/// ```
/// use yanet_sys::dp::{Batch, Packet};
/// fn keep<'a>(batch: &mut Batch<'a>, kept: &mut Option<Packet<'a>>) {
///     *kept = batch.next();
/// }
/// ```
#[must_use = "a packet is passed, dropped or redirected; one left alone is lost"]
pub struct Packet<'id> {
    raw: NonNull<bindings::packet>,
    // Invariant brand: values of two different contexts never unify, so they cannot be mixed.
    _brand: PhantomData<fn(&'id ()) -> &'id ()>,
    // Neither Send nor Sync on purpose, independent of the pointer fields.
    _local: PhantomData<*mut ()>,
}

/// Read-only view of an input packet the handler has not taken.
///
/// Lives while the batch is borrowed for [`Batch::peek`], so no input packet
/// is taken, disposed of or written meanwhile.
#[derive(Clone, Copy)]
pub struct PacketView<'b> {
    raw: NonNull<bindings::packet>,
    _batch: PhantomData<&'b ()>,
}

/// A resize the packet's buffer cannot take; the packet is unchanged.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct ResizeError;

/// A tunnel operation the C routine refused.
///
/// The inner or outer headers are not ones it handles, the packet has no
/// parsed network header (every encapsulation refuses one), the packet is
/// chained (UDP encapsulation), the packet is a fragment (decapsulation), or
/// the headroom is spent.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct TunnelError;

/// How an encapsulation takes the inner DSCP, the C `DSCP_MARK_*` values
/// (lib/dataplane/packet/dscp.h).
///
/// The inner ECN bits reach the outer header whichever is chosen.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
#[repr(u8)]
pub enum DscpMark {
    /// The outer DSCP is zero.
    Never = 0,
    /// The outer DSCP is zero, as for `Never`; the value of a module
    /// configuration that names no marking.
    Default = 1,
    /// The outer DSCP is the inner one.
    Always = 2,
}

/// Routing target of a device for the running generation.
///
/// Branded with the call it was made in, invariantly, so a batch redirects
/// only to a target of its own call's context.
///
/// A target of one call's context is refused by the batch of another call (a
/// lifetime error, which has no code):
///
/// ```compile_fail
/// # use yanet_sys::dp::{Batch, Cx, Device, Module};
/// fn cross<'a, 'b, M: Module<Config = Device>>(
///     cx: &Cx<'a, M>,
///     device: &'a Device,
///     batch: &mut Batch<'b>,
/// ) {
///     if let (Some(packet), Some(target)) = (batch.next(), device.resolve(cx)) {
///         batch.redirect(packet, target);
///     }
/// }
/// ```
///
/// Redirecting within the call is accepted:
///
/// ```
/// # use yanet_sys::dp::{Batch, Cx, Device, Module};
/// fn cross<'a, M: Module<Config = Device>>(
///     cx: &Cx<'a, M>,
///     device: &'a Device,
///     batch: &mut Batch<'a>,
/// ) {
///     if let (Some(packet), Some(target)) = (batch.next(), device.resolve(cx)) {
///         batch.redirect(packet, target);
///     }
/// }
/// ```
#[derive(Clone, Copy)]
pub struct DeviceRef<'d> {
    target: NonNull<bindings::module_device_target>,
    input: bool,
    // Invariant brand: values of two different contexts never unify, so they cannot be mixed.
    _brand: PhantomData<fn(&'d ()) -> &'d ()>,
}

impl DeviceRef<'_> {
    /// Wraps a target of the running context.
    ///
    /// # Safety
    ///
    /// The target must be a present routing target of the running execution
    /// context, valid for the lifetime.
    #[inline(always)]
    pub(crate) unsafe fn new(target: NonNull<bindings::module_device_target>, input: bool) -> Self {
        Self { target, input, _brand: PhantomData }
    }
}

/// What happens to a packet.
#[non_exhaustive]
pub enum Verdict<'d> {
    /// Pass to the next pipeline stage.
    Pass,
    /// Drop.
    Drop,
    /// Route to a device of the generation.
    Redirect(DeviceRef<'d>),
}

// The packet allocator and copy of the host dataplane (lib/dataplane/worker),
// exported by its executable; the plugin resolves them at load time.
unsafe extern "C" {
    /// Allocates a packet from the worker's pool, null when it is empty; the
    /// packet metadata is zeroed and the mbuf empty.
    fn worker_packet_alloc(worker: *mut bindings::dp_worker) -> *mut bindings::packet;

    /// Copies a packet into a new one from the worker's pool, null when it
    /// is empty.
    ///
    /// The source's recirculation budget, initialised to the limit when
    /// unset, is split between the two.
    fn worker_clone_packet(
        worker: *mut bindings::dp_worker,
        packet: *mut bindings::packet,
        packet_recirc_limit: u16,
    ) -> *mut bindings::packet;
}

impl<'id> Batch<'id> {
    /// Wraps the front of one handler call.
    ///
    /// # Safety
    ///
    /// The front must be the one C passed to the running handler, valid and
    /// exclusively owned by this call, with well-formed lists of packets that
    /// are themselves exclusively owned; the execution context and the worker
    /// must be the call's.
    #[inline(always)]
    pub(crate) unsafe fn new(
        front: NonNull<bindings::packet_front>,
        ectx: NonNull<bindings::module_ectx>,
        worker: NonNull<bindings::dp_worker>,
    ) -> Self {
        Self {
            front,
            ectx,
            worker,
            _brand: PhantomData,
            _local: PhantomData,
        }
    }

    /// Allocates an empty packet from the worker's pool, `None` when the
    /// pool is exhausted, as C's `worker_packet_alloc` does.
    ///
    /// The packet is owned like a taken one: it is passed (emitted after
    /// the input packets already passed), dropped or redirected; one left
    /// alone never returns to the pool. Its frame starts empty, with the
    /// pool's headroom in front; grow it with [`Packet::append`] and
    /// [`Packet::prepend`]. Its metadata starts zeroed, so its headers count
    /// as unparsed.
    #[inline(always)]
    pub fn alloc(&mut self) -> Option<Packet<'id>> {
        // SAFETY: the call's worker (constructor contract).
        let raw = NonNull::new(unsafe { worker_packet_alloc(self.worker.as_ptr()) })?;
        Some(Packet {
            raw,
            _brand: PhantomData,
            _local: PhantomData,
        })
    }

    /// Copies a packet into a new one from the worker's pool, `None` when
    /// the pool is exhausted, as C's `worker_clone_packet` does.
    ///
    /// The copy carries the frame and metadata of the source and is owned
    /// like an allocated packet: it ends by its verdict. C splits the
    /// source's recirculation budget between the two; nothing else of the
    /// source changes.
    #[inline(always)]
    pub fn clone(&mut self, packet: &Packet<'id>) -> Option<Packet<'id>> {
        // SAFETY: the call's worker and context (constructor contract), the
        // context frozen (attach: frozen); the source is an owned packet of
        // this call, and no Rust reference covers the metadata C updates.
        let raw = NonNull::new(unsafe {
            worker_clone_packet(
                self.worker.as_ptr(),
                packet.raw.as_ptr(),
                (*self.ectx.as_ptr()).packet_recirc_limit,
            )
        })?;
        Some(Packet {
            raw,
            _brand: PhantomData,
            _local: PhantomData,
        })
    }

    /// Passes every input packet not yet taken, with the front's input
    /// counters, as C's `packet_front_pass` does.
    ///
    /// The input counters count the whole input, so, as in C
    /// (lib/dataplane/module/packet_front.h), a handler calls this before it
    /// takes any packet: packets taken first are counted again in the output.
    /// Core tracks this in yanet2 #3078 (keep a front's input counters in
    /// step with its input list).
    #[inline(always)]
    pub fn pass_all(&mut self) {
        let front = self.front.as_ptr();
        // SAFETY: the front and its packets are exclusively this call's
        // (constructor contract).
        unsafe {
            concat(&raw mut (*front).output, &raw mut (*front).input);
            (*front).output_count += (*front).input_count;
            (*front).output_bytes += (*front).input_bytes;
            (*front).input_count = 0;
            (*front).input_bytes = 0;
        }
    }

    /// Walks the input packets the handler has not taken, in order,
    /// without taking them.
    ///
    /// No packet is taken while the walk lives:
    ///
    /// ```compile_fail,E0502
    /// use yanet_sys::dp::Batch;
    /// fn peek_and_take(batch: &mut Batch<'_>) {
    ///     let mut views = batch.peek();
    ///     let taken = batch.next();
    ///     let _ = (views.next(), taken);
    /// }
    /// ```
    ///
    /// Taking after the walk is accepted:
    ///
    /// ```
    /// use yanet_sys::dp::Batch;
    /// fn peek_then_take(batch: &mut Batch<'_>) {
    ///     if batch.peek().count() > 1 {
    ///         if let Some(packet) = batch.next() {
    ///             batch.drop(packet);
    ///         }
    ///     }
    /// }
    /// ```
    #[inline(always)]
    pub fn peek(&self) -> impl Iterator<Item = PacketView<'_>> {
        let view = |raw| PacketView { raw, _batch: PhantomData };
        // SAFETY: the input list is exclusively this call's (constructor
        // contract) and stays unchanged while the batch is borrowed.
        let first = unsafe { (*self.front.as_ptr()).input.first };
        core::iter::successors(NonNull::new(first).map(view), move |current: &PacketView<'_>| {
            // SAFETY: as above.
            NonNull::new(unsafe { (*current.raw.as_ptr()).next }).map(view)
        })
    }

    /// Takes the next input packet.
    ///
    /// Not an `Iterator`: adaptors would keep the batch borrowed while the
    /// handler disposes of the packets they yield.
    #[allow(clippy::should_implement_trait)]
    #[inline(always)]
    pub fn next(&mut self) -> Option<Packet<'id>> {
        // SAFETY: the front and its packets are exclusively this call's
        // (constructor contract); this is the C list pop.
        let packet = NonNull::new(unsafe { pop(&raw mut (*self.front.as_ptr()).input) })?;
        Some(Packet {
            raw: packet,
            _brand: PhantomData,
            _local: PhantomData,
        })
    }

    /// Passes the packet to the next pipeline stage.
    #[inline(always)]
    pub fn pass(&mut self, packet: Packet<'id>) {
        self.dispose(packet, Verdict::Pass);
    }

    /// Drops the packet.
    #[inline(always)]
    pub fn drop(&mut self, packet: Packet<'id>) {
        self.dispose(packet, Verdict::Drop);
    }

    /// Routes the packet to a device of the running generation.
    #[inline(always)]
    pub fn redirect(&mut self, packet: Packet<'id>, device: DeviceRef<'id>) {
        self.dispose(packet, Verdict::Redirect(device));
    }

    /// Applies a verdict to the packet.
    #[inline(always)]
    pub fn dispose(&mut self, packet: Packet<'id>, verdict: Verdict<'id>) {
        let raw = packet.raw.as_ptr();
        let front = self.front.as_ptr();
        // SAFETY: an owned packet of this batch is on no list and
        // exclusively ours (only taking, allocating and copying create one,
        // and its value is consumed once); the front is this call's, and a
        // redirect target is a present target of this call's context
        // (device reference contract) on which the C routing runs
        // (attach: unwind).
        unsafe {
            match verdict {
                Verdict::Pass => {
                    // `packet_front_output`,
                    // lib/dataplane/module/packet_front.h.
                    push(&raw mut (*front).output, raw);
                    (*front).output_count += 1;
                    (*front).output_bytes += u64::from((*raw).data_len);
                }
                Verdict::Drop => {
                    // `packet_front_drop`, as above.
                    push(&raw mut (*front).drop, raw);
                    (*front).drop_count += 1;
                    (*front).drop_bytes += u64::from((*raw).data_len);
                }
                // `module_ectx_route_input` and `module_ectx_route_output`,
                // lib/dataplane/pipeline/schedule.h, through the shim.
                Verdict::Redirect(device) => bindings::yanet_sys_dp_redirect(
                    self.ectx.as_ptr(),
                    front,
                    device.target.as_ptr(),
                    core::ffi::c_int::from(device.input),
                    raw,
                ),
            }
        }
    }

    /// Runs the closure on every remaining input packet and applies the
    /// verdict it returns.
    #[inline(always)]
    pub fn dispatch(&mut self, mut decide: impl FnMut(&mut Packet<'id>) -> Verdict<'id>) {
        while let Some(mut packet) = self.next() {
            let verdict = decide(&mut packet);
            self.dispose(packet, verdict);
        }
    }
}

/// Pops the first packet of an intrusive list, null when it is empty, as the
/// C `packet_list_pop` does (lib/dataplane/packet/packet.h).
///
/// # Safety
///
/// The list must be valid and exclusively owned with its packets.
#[inline(always)]
unsafe fn pop(list: *mut bindings::packet_list) -> *mut bindings::packet {
    // SAFETY: guaranteed by the caller.
    unsafe {
        let packet = (*list).first;
        if packet.is_null() {
            return packet;
        }
        (*list).first = (*packet).next;
        if (*list).first.is_null() {
            (*list).last = core::ptr::null_mut();
        }
        packet
    }
}

/// Appends a packet to an intrusive list, as the C `packet_list_add` does
/// (lib/dataplane/packet/packet.h).
///
/// # Safety
///
/// The list and the packet must be valid and exclusively owned.
#[inline(always)]
unsafe fn push(list: *mut bindings::packet_list, packet: *mut bindings::packet) {
    // SAFETY: guaranteed by the caller.
    unsafe {
        (*packet).next = core::ptr::null_mut();
        if (*list).last.is_null() {
            (*list).last = &raw mut (*list).first;
        }
        *(*list).last = packet;
        (*list).last = &raw mut (*packet).next;
    }
}

/// Moves every packet of one intrusive list to the end of another, leaving
/// the source empty.
///
/// As the C `packet_list_concat` does (lib/dataplane/packet/packet.h).
///
/// # Safety
///
/// Both lists must be valid and exclusively owned with their packets.
#[inline(always)]
unsafe fn concat(dst: *mut bindings::packet_list, src: *mut bindings::packet_list) {
    // SAFETY: guaranteed by the caller.
    unsafe {
        if (*src).first.is_null() {
            return;
        }
        if (*dst).first.is_null() {
            (*dst).first = (*src).first;
        } else {
            *(*dst).last = (*src).first;
        }
        (*dst).last = (*src).last;
        (*src).first = core::ptr::null_mut();
        (*src).last = core::ptr::null_mut();
    }
}

/// Grows the first segment of an mbuf at the front, into the headroom beyond
/// the packet descriptor; reports whether it did, else the mbuf is unchanged.
///
/// The Rust twin of C's `packet_headroom_prepend`
/// (lib/dataplane/packet/data.h) over `rte_pktmbuf_prepend`, inlined here; a
/// test compares the two.
///
/// # Safety
///
/// The mbuf must be the valid mbuf of an exclusively owned packet whose
/// descriptor shares the buffer.
#[inline(always)]
unsafe fn grow_front(mbuf: *mut bindings::rte_mbuf, len: u16) -> bool {
    // SAFETY: guaranteed by the caller.
    unsafe {
        if usize::from((*mbuf).data_off) < size_of::<bindings::packet>() + usize::from(len) {
            return false;
        }
        (*mbuf).data_off -= len;
        (*mbuf).data_len += len;
        (*mbuf).pkt_len += u32::from(len);
    }
    true
}

/// Start and length of the first segment of a packet's frame: C's
/// `packet_data` and `packet_data_len` (lib/dataplane/packet/data.h).
///
/// # Safety
///
/// The packet must be valid, with its mbuf.
#[inline(always)]
unsafe fn segment(packet: *mut bindings::packet) -> (*mut u8, usize) {
    // SAFETY: guaranteed by the caller.
    unsafe {
        let mbuf = (*packet).mbuf;
        (
            (*mbuf).buf_addr.cast::<u8>().add(usize::from((*mbuf).data_off)),
            usize::from((*mbuf).data_len),
        )
    }
}

/// Reports whether the parser found a header: an unparsed one is all zero,
/// as an allocated packet's are.
#[inline(always)]
fn parsed(type_: u16, offset: u16) -> bool {
    type_ != 0 || offset != 0
}

/// End of what the parser vouches for, from the start of the frame; zero for
/// an unparsed packet.
///
/// That is (attach: packet) the start of a parsed network or transport
/// header, the fixed IPv4 or IPv6 header and an available TCP or UDP header.
///
/// # Safety
///
/// The packet must be valid.
#[inline(always)]
unsafe fn parsed_end(packet: *mut bindings::packet) -> usize {
    // SAFETY: guaranteed by the caller.
    let (network, transport) = unsafe { ((*packet).network_header, (*packet).transport_header) };
    let network_len = match u16::from_be(network.type_) {
        0x0800 => 20,
        0x86dd => 40,
        _ => 0,
    };
    let available = u32::from(transport.type_) & bindings::PACKET_TRANSPORT_HEADER_UNAVAILABLE == 0;
    let transport_len = match (available, transport.type_ & 0xff) {
        (true, 6) => 20,
        (true, 17) => 8,
        _ => 0,
    };
    (usize::from(network.offset) + network_len).max(usize::from(transport.offset) + transport_len)
}

/// Maps the C result of a tunnel routine: zero is success.
#[inline(always)]
fn check(result: core::ffi::c_int) -> Result<(), TunnelError> {
    if result == 0 { Ok(()) } else { Err(TunnelError) }
}

impl Packet<'_> {
    /// Bytes of the first segment of the frame.
    #[inline(always)]
    pub fn data(&self) -> &[u8] {
        let (start, len) = self.segment();
        // SAFETY: the mbuf of an owned packet is valid; its first segment
        // spans the data length from the data offset, exclusively ours
        // (attach: packet).
        unsafe { core::slice::from_raw_parts(start, len) }
    }

    /// Mutable bytes of the first segment of the frame.
    #[inline(always)]
    pub fn data_mut(&mut self) -> &mut [u8] {
        let (start, len) = self.segment();
        // SAFETY: as above; the packet is exclusively owned, with no shared
        // or cloned data reachable (attach: packet).
        unsafe { core::slice::from_raw_parts_mut(start, len) }
    }

    #[inline(always)]
    fn segment(&self) -> (*mut u8, usize) {
        // SAFETY: the packet and its first mbuf segment are valid for the
        // call (attach: packet).
        unsafe { segment(self.raw.as_ptr()) }
    }

    /// Grows the frame at the front, into the headroom, and returns the new
    /// bytes; parsed header offsets move with the frame.
    ///
    /// Header views read the same headers and unparsed headers stay
    /// unparsed. The packet descriptor shares the buffer with the headroom,
    /// as in C, so the room is what the headroom holds beyond the
    /// descriptor; beyond it the packet is left unchanged.
    #[inline]
    pub fn prepend(&mut self, len: usize) -> Result<&mut [u8], ResizeError> {
        let raw = self.raw.as_ptr();
        let len16 = u16::try_from(len).map_err(|_| ResizeError)?;
        // SAFETY: the packet, its mbuf and buffer are exclusively ours for
        // the call (attach: packet); the new bytes lie in the first
        // segment's headroom. A parsed offset points into the buffer
        // (`data_off + offset <= buf_len`, a trim moves neither), so the
        // shifted one does not overflow.
        unsafe {
            if !grow_front((*raw).mbuf, len16) {
                return Err(ResizeError);
            }
            // `packet_refresh_data_len`: the packet is on no counted list.
            (*raw).data_len = (*(*raw).mbuf).data_len;
            let network = &raw mut (*raw).network_header;
            if parsed((*network).type_, (*network).offset) {
                (*network).offset += len16;
            }
            let transport = &raw mut (*raw).transport_header;
            if parsed((*transport).type_, (*transport).offset) {
                (*transport).offset += len16;
            }
            let (start, _) = segment(raw);
            Ok(core::slice::from_raw_parts_mut(start, len))
        }
    }

    /// Whether the packet's mbuf continues in further segments.
    #[inline(always)]
    fn chained(&self) -> bool {
        // SAFETY: an owned packet's descriptor and mbuf are this call's
        // (attach: packet).
        unsafe { !(*(*self.raw.as_ptr()).mbuf).next.is_null() }
    }

    /// Grows the frame at the end, into the tailroom of a single-segment
    /// packet, and returns the new bytes, as `rte_pktmbuf_append` does.
    ///
    /// Beyond the tailroom, or on a chained packet, the packet is left
    /// unchanged.
    #[inline]
    pub fn append(&mut self, len: usize) -> Result<&mut [u8], ResizeError> {
        let raw = self.raw.as_ptr();
        // SAFETY: as for prepending; the new bytes lie in the tailroom of
        // the only segment.
        unsafe {
            let mbuf = (*raw).mbuf;
            if !(*mbuf).next.is_null() {
                return Err(ResizeError);
            }
            let used = usize::from((*mbuf).data_off) + usize::from((*mbuf).data_len);
            if len > usize::from((*mbuf).buf_len) - used {
                return Err(ResizeError);
            }
            let (start, old_len) = segment(raw);
            (*mbuf).data_len += len as u16;
            (*mbuf).pkt_len += len as u32;
            (*raw).data_len = (*mbuf).data_len;
            Ok(core::slice::from_raw_parts_mut(start.add(old_len), len))
        }
    }

    /// Shrinks the frame of a single-segment packet at the end, as
    /// `rte_pktmbuf_trim` does, without breaking what the parser vouches for.
    ///
    /// Modules read headers at their offsets unchecked (attach: packet), so
    /// a trim must leave every parsed header starting no later than the end
    /// of the frame and the fixed IPv4 or IPv6 header and an available TCP or
    /// UDP header whole. A trim that would not, one beyond the frame length,
    /// or one on a chained packet leaves the packet unchanged.
    #[inline]
    pub fn trim(&mut self, len: usize) -> Result<(), ResizeError> {
        let raw = self.raw.as_ptr();
        // SAFETY: as for prepending.
        unsafe {
            let mbuf = (*raw).mbuf;
            let data_len = usize::from((*mbuf).data_len);
            if !(*mbuf).next.is_null() || len > data_len || data_len - len < parsed_end(raw) {
                return Err(ResizeError);
            }
            (*mbuf).data_len -= len as u16;
            (*mbuf).pkt_len -= len as u32;
            (*raw).data_len = (*mbuf).data_len;
        }
        Ok(())
    }

    /// Prepends an outer IPv4 header to an IPv4 or IPv6 packet, as the C
    /// `packet_ip4_encap` does.
    ///
    /// The outer TTL, protocol (IP-in-IP or IPv6-in-IP) and length come from
    /// the inner header, and the outer checksum is computed. The network
    /// header offset stays, now at the outer header, and a parsed transport
    /// header offset moves with the inner packet. The refusal of an
    /// unsupported inner type (an unparsed packet's included) or of a spent
    /// headroom leaves the packet unchanged.
    #[inline]
    pub fn encap_ipv4(&mut self, dst: [u8; 4], src: [u8; 4], dscp: DscpMark) -> Result<(), TunnelError> {
        // SAFETY: the packet is exclusively ours for the call (attach:
        // packet), handed over by `prepend_at_network` with its network
        // offset in bounds; the address arrays outlive the call.
        self.prepend_at_network(|raw| unsafe {
            bindings::yanet_sys_dp_ip4_encap(raw, dst.as_ptr(), src.as_ptr(), dscp as u8)
        })
    }

    /// Prepends an outer IPv6 header to an IPv4 or IPv6 packet, as
    /// [`Packet::encap_ipv4`] does with IPv4, through `packet_ip6_encap`.
    #[inline]
    pub fn encap_ipv6(&mut self, dst: [u8; 16], src: [u8; 16], dscp: DscpMark) -> Result<(), TunnelError> {
        // SAFETY: as for `encap_ipv4`.
        self.prepend_at_network(|raw| unsafe {
            bindings::yanet_sys_dp_ip6_encap(raw, dst.as_ptr(), src.as_ptr(), dscp as u8)
        })
    }

    /// Prepends one MPLS label stack entry in front of the network header,
    /// as the C `packet_mpls_encap` does.
    ///
    /// The label is taken modulo 2^20 (C shifts the high bits out) and the
    /// traffic class modulo 8. The packet needs its network header parsed: an
    /// unparsed one is refused. A spent headroom leaves the packet
    /// unchanged.
    #[inline]
    pub fn encap_mpls(&mut self, label: u32, traffic_class: u8, bottom: bool, ttl: u8) -> Result<(), TunnelError> {
        // SAFETY: as for `encap_ipv4`.
        self.prepend_at_network(|raw| unsafe {
            bindings::yanet_sys_dp_mpls_encap(raw, label, traffic_class & 0x7, u8::from(bottom), ttl)
        })
    }

    /// Wraps the packet from its network header on in UDP over IPv4, as the C
    /// `packet_ip4_encap_udp` does.
    ///
    /// The UDP checksum is computed. The packet needs its network header
    /// parsed: an unparsed one is refused, and so is a chained packet, whose
    /// UDP length and checksum C computes over the first segment only (yanet2
    /// #2145, mpls-over-udp encapsulation sizes and checksums the head mbuf
    /// segment only). Two headers are prepended one after the other, so a
    /// spent headroom may leave the UDP header written: the caller drops a
    /// packet it could not wrap.
    #[inline]
    pub fn encap_ipv4_udp(
        &mut self,
        src: [u8; 4],
        dst: [u8; 4],
        src_port: u16,
        dst_port: u16,
    ) -> Result<(), TunnelError> {
        if self.chained() {
            return Err(TunnelError);
        }
        // SAFETY: as for `encap_ipv4`.
        self.prepend_at_network(|raw| unsafe {
            bindings::yanet_sys_dp_ip4_encap_udp(raw, src.as_ptr(), dst.as_ptr(), src_port, dst_port)
        })
    }

    /// Wraps the packet in UDP over IPv6, as [`Packet::encap_ipv4_udp`] does
    /// over IPv4.
    ///
    /// The network header must be parsed and the packet unchained, as there.
    #[inline]
    pub fn encap_ipv6_udp(
        &mut self,
        src: [u8; 16],
        dst: [u8; 16],
        src_port: u16,
        dst_port: u16,
    ) -> Result<(), TunnelError> {
        if self.chained() {
            return Err(TunnelError);
        }
        // SAFETY: as for `encap_ipv4`.
        self.prepend_at_network(|raw| unsafe {
            bindings::yanet_sys_dp_ip6_encap_udp(raw, src.as_ptr(), dst.as_ptr(), src_port, dst_port)
        })
    }

    /// Runs a C encapsulation that prepends at the network offset on a packet
    /// with a parsed network header; an unparsed transport header stays so.
    ///
    /// Zero means unparsed: a parsed Ethernet frame has the offset of at
    /// least the Ethernet header, and an allocated packet has none. The C
    /// routines copy the bytes before the network offset and the UDP ones
    /// checksum the frame from it, which stays in bounds because a parsed
    /// network offset is no later than the end of the frame (attach: packet).
    /// They move the transport offset unconditionally, which would make an
    /// unparsed one look parsed.
    #[inline(always)]
    fn prepend_at_network(
        &mut self,
        encap: impl FnOnce(*mut bindings::packet) -> core::ffi::c_int,
    ) -> Result<(), TunnelError> {
        if self.network_offset() == 0 {
            return Err(TunnelError);
        }
        let raw = self.raw.as_ptr();
        // SAFETY: the packet is exclusively ours for the call (attach:
        // packet).
        let transport = unsafe { (*raw).transport_header };
        let result = encap(raw);
        if !parsed(transport.type_, transport.offset) {
            // SAFETY: as above.
            unsafe { (*raw).transport_header = transport };
        }
        check(result)
    }

    /// Removes the IP-in-IP or GRE tunnel headers the parser found, as the C
    /// `packet_decap` does.
    ///
    /// The inner IPv4 or IPv6 packet is left behind the Ethernet header. The
    /// frame is unchanged when the packet is a fragment, the tunnel is of
    /// another kind, or the inner header is truncated; C may still have
    /// updated the packet's parse metadata (hash, fragment fields).
    #[inline]
    pub fn decap(&mut self) -> Result<(), TunnelError> {
        // SAFETY: the packet is exclusively ours for the call (attach: packet).
        check(unsafe { bindings::yanet_sys_dp_decap(self.raw.as_ptr()) })
    }

    /// Ether type of the network header the parser found, host order.
    #[inline(always)]
    pub fn network_type(&self) -> u16 {
        // SAFETY: the packet is exclusively ours for the call (attach: packet).
        u16::from_be(unsafe { (*self.raw.as_ptr()).network_header.type_ })
    }

    /// Offset of the network header from the start of the frame.
    #[inline(always)]
    pub fn network_offset(&self) -> u16 {
        // SAFETY: as above.
        unsafe { (*self.raw.as_ptr()).network_header.offset }
    }

    /// Raw transport tag the parser wrote: the protocol number in the low
    /// byte and the unavailable mark in bit 8.
    ///
    /// Read it through the protocol and availability accessors.
    #[inline(always)]
    pub fn transport_type(&self) -> u16 {
        // SAFETY: as above.
        unsafe { (*self.raw.as_ptr()).transport_header.type_ }
    }

    /// Declared transport protocol: the protocol number of the IP header,
    /// meaningful also when the transport header itself is unavailable
    /// inside a non-initial fragment (`packet_transport_protocol`,
    /// lib/dataplane/packet/packet.h).
    #[inline(always)]
    pub fn transport_protocol(&self) -> u8 {
        (self.transport_type() & 0xff) as u8
    }

    /// Reports whether the parser left the unavailable mark off the
    /// transport header (`packet_transport_header_available`,
    /// lib/dataplane/packet/packet.h).
    ///
    /// A true result does not guarantee readable bytes; callers check
    /// lengths.
    #[inline(always)]
    pub fn transport_header_available(&self) -> bool {
        u32::from(self.transport_type()) & bindings::PACKET_TRANSPORT_HEADER_UNAVAILABLE == 0
    }

    /// Offset of the transport header from the start of the frame.
    #[inline(always)]
    pub fn transport_offset(&self) -> u16 {
        // SAFETY: as above.
        unsafe { (*self.raw.as_ptr()).transport_header.offset }
    }
}

impl<'b> PacketView<'b> {
    /// Bytes of the first segment of the frame.
    #[inline(always)]
    pub fn data(&self) -> &'b [u8] {
        // SAFETY: an input packet of the borrowed batch is valid with its
        // mbuf (attach: packet), and nothing writes it while the batch is
        // borrowed.
        unsafe {
            let (start, len) = segment(self.raw.as_ptr());
            core::slice::from_raw_parts(start, len)
        }
    }

    /// Ether type of the network header the parser found, host order.
    #[inline(always)]
    pub fn network_type(&self) -> u16 {
        // SAFETY: as above.
        u16::from_be(unsafe { (*self.raw.as_ptr()).network_header.type_ })
    }

    /// Offset of the network header from the start of the frame.
    #[inline(always)]
    pub fn network_offset(&self) -> u16 {
        // SAFETY: as above.
        unsafe { (*self.raw.as_ptr()).network_header.offset }
    }

    /// Raw transport tag the parser wrote, as [`Packet::transport_type`].
    #[inline(always)]
    pub fn transport_type(&self) -> u16 {
        // SAFETY: as above.
        unsafe { (*self.raw.as_ptr()).transport_header.type_ }
    }

    /// Offset of the packet's fragment in its datagram, in the units of its
    /// header: nonzero for a fragment after the first.
    #[inline(always)]
    pub fn fragment_offset(&self) -> u16 {
        // SAFETY: as above.
        unsafe { (*self.raw.as_ptr()).fragment_offset }
    }

    /// Offset of the transport header from the start of the frame.
    #[inline(always)]
    pub fn transport_offset(&self) -> u16 {
        // SAFETY: as above.
        unsafe { (*self.raw.as_ptr()).transport_header.offset }
    }
}

#[cfg(test)]
mod tests;
