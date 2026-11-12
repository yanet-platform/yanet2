//! Packets of one handler call and their disposal.
//!
//! The packet lists stay C's and are used as a C handler uses them: taking
//! a packet pops it off the input list, and its verdict moves it to the
//! output or drop list or routes it to a device. Every input packet is
//! passed, dropped or routed to a device (module: drain); a packet the module
//! made may go back to its pool instead.

use core::{marker::PhantomData, mem::size_of, ops::Deref, ptr::NonNull};

use yanet_packet::resize::{Insertion, Layout, Parsed};

use crate::bindings;

/// Length of the Ethernet header, before which no parsed network header
/// starts: the tunnel operations write its ether type in the two bytes
/// before it.
const ETHERNET_HEADER_LEN: usize = 14;

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
/// Not `Copy` or `Clone`; consumed by passing, dropping, redirecting or
/// freeing it. A packet that is never disposed of is on no list
/// after the call, as one a C handler forgets. It dereferences to its
/// [`PacketMeta`] for reading; writes take the packet mutably.
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
#[must_use = "a packet is passed, dropped, redirected or freed; one left alone is lost"]
pub struct Packet<'id> {
    raw: NonNull<bindings::packet>,
    // Invariant brand: values of two different contexts never unify, so they cannot be mixed.
    _brand: PhantomData<fn(&'id ()) -> &'id ()>,
    // Neither Send nor Sync on purpose, independent of the pointer fields.
    _local: PhantomData<*mut ()>,
}

/// Descriptor of a packet, read-only: the parser's results and the frame.
///
/// A taken packet dereferences to it, and [`Batch::peek`] yields it for an
/// input packet. It is the descriptor itself, in the headroom of its mbuf,
/// where C finds it from the mbuf: it is neither `Clone` nor `Copy`, as a
/// copy would outlive its packet, and never borrowed mutably, as swapping
/// two would part each from its mbuf:
///
/// ```compile_fail,E0596
/// use yanet_sys::dp::Packet;
/// fn swap<'a>(a: &mut Packet<'a>, b: &mut Packet<'a>) {
///     core::mem::swap(&mut **a, &mut **b);
/// }
/// ```
///
/// Reading both is accepted:
///
/// ```
/// use yanet_sys::dp::Packet;
/// fn same_flow<'a>(a: &Packet<'a>, b: &Packet<'a>) -> bool {
///     a.hash() == b.hash()
/// }
/// ```
#[repr(transparent)]
pub struct PacketMeta(bindings::packet);

/// Read-only view of an input packet the handler has not taken.
///
/// Lives while the batch is borrowed for [`Batch::peek`], so no input packet
/// is taken, disposed of or written meanwhile.
pub type PacketView<'b> = &'b PacketMeta;

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

/// A frame the packet parser refuses, or a chained packet, which it does not
/// parse.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct ParseError;

/// How an encapsulation takes the inner DSCP, the C `DSCP_MARK_*` values
/// (lib/dataplane/packet/dscp.h).
///
/// The inner ECN bits reach the outer header whichever is chosen.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
#[repr(u8)]
pub enum DscpMark {
    /// The outer DSCP is zero.
    #[doc(alias = "DSCP_MARK_NEVER")]
    Never = 0,
    /// The outer DSCP is zero, as for `Never`; the value of a module
    /// configuration that names no marking.
    #[doc(alias = "DSCP_MARK_DEFAULT")]
    Default = 1,
    /// The outer DSCP is the inner one.
    #[doc(alias = "DSCP_MARK_ALWAYS")]
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

// The packet allocator, copy and free of the host dataplane
// (lib/dataplane/worker), exported by its executable; the plugin resolves them
// at load time.
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

    /// Returns the mbuf of a packet, every segment of its chain, to the pool
    /// it came from.
    fn worker_packet_free(packet: *mut bindings::packet);
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
    /// the input packets already passed), dropped, redirected or freed; one
    /// left alone never returns to the pool. Its frame starts empty, with the
    /// pool's headroom in front; grow it with [`Packet::append`] and
    /// [`Packet::prepend`]. Its metadata starts zeroed, so its headers count
    /// as unparsed.
    #[doc(alias = "worker_packet_alloc")]
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
    /// like an allocated packet: it ends by its verdict or is freed. C
    /// splits the source's recirculation budget between the two, so the
    /// source is borrowed mutably; nothing else of it changes.
    ///
    /// No view of the source lives across the copy:
    ///
    /// ```compile_fail,E0502
    /// use yanet_sys::dp::{Batch, Packet};
    /// fn view_across<'a>(batch: &mut Batch<'a>, packet: &mut Packet<'a>) {
    ///     let meta = &**packet;
    ///     let copy = batch.clone(packet);
    ///     let _ = (meta.hash(), copy);
    /// }
    /// ```
    ///
    /// Reading the source before and after the copy is accepted:
    ///
    /// ```
    /// use yanet_sys::dp::{Batch, Packet};
    /// fn view_around<'a>(batch: &mut Batch<'a>, packet: &mut Packet<'a>) {
    ///     let hash = packet.hash();
    ///     let copy = batch.clone(packet);
    ///     let _ = (hash, packet.hash(), copy);
    /// }
    /// ```
    #[doc(alias = "worker_clone_packet")]
    #[inline(always)]
    pub fn clone(&mut self, packet: &mut Packet<'id>) -> Option<Packet<'id>> {
        // SAFETY: the call's worker and context (constructor contract), the
        // context frozen (attach: frozen); the source is an owned packet of
        // this call, borrowed mutably, so no Rust reference covers the
        // metadata C updates.
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

    /// Returns a packet to its pool, as C's `worker_packet_free` does.
    ///
    /// For a packet the module made and gives up on, such as one the packet
    /// parser refuses: it ends on no list and in no count of the front, where
    /// a dropped one is counted. An input packet is dropped instead, so that
    /// every input has its verdict (module: drain).
    #[doc(alias = "worker_packet_free")]
    #[inline(always)]
    pub fn free(&mut self, packet: Packet<'id>) {
        // SAFETY: an owned packet of this batch is on no list and
        // exclusively ours (only taking, allocating and copying create one,
        // and its value is consumed here), so nothing reaches its mbuf once
        // C has returned it.
        unsafe { worker_packet_free(packet.raw.as_ptr()) }
    }

    /// Passes every input packet not yet taken, with the front's input
    /// counters, as C's `packet_front_pass` does.
    ///
    /// The input counters count the whole input, so, as in C
    /// (`lib/dataplane/module/packet_front.h`), a handler calls this before it
    /// takes any packet: packets taken first are counted again in the output.
    /// Core tracks this in yanet2 #3078 (keep a front's input counters in
    /// step with its input list).
    #[doc(alias = "packet_front_pass")]
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
    pub fn peek(&self) -> impl Iterator<Item = &PacketMeta> {
        let view = |raw: *mut bindings::packet| {
            // SAFETY: an input packet of the borrowed batch is valid and
            // initialised (attach: packet), and the transparent wrapper has
            // its layout; nothing writes it while the batch is borrowed.
            NonNull::new(raw).map(|raw| unsafe { raw.cast::<PacketMeta>().as_ref() })
        };
        // SAFETY: the input list is exclusively this call's (constructor
        // contract) and stays unchanged while the batch is borrowed.
        let first = unsafe { (*self.front.as_ptr()).input.first };
        core::iter::successors(view(first), move |PacketMeta(packet)| view(packet.next))
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

/// Start and length of the first segment of a packet's frame, from its mbuf:
/// C's `packet_data` and `packet_data_len` (lib/dataplane/packet/data.h).
///
/// # Safety
///
/// The mbuf must be valid.
#[inline(always)]
unsafe fn segment(mbuf: *mut bindings::rte_mbuf) -> (*mut u8, usize) {
    // SAFETY: guaranteed by the caller.
    unsafe {
        (
            (*mbuf).buf_addr.cast::<u8>().add(usize::from((*mbuf).data_off)),
            usize::from((*mbuf).data_len),
        )
    }
}

/// Bytes of each segment of an mbuf chain, in order: C's `rte_pktmbuf_mtod`
/// and `rte_pktmbuf_data_len` of every segment.
///
/// # Safety
///
/// The chain must be valid, its segments' bytes unwritten while `'a` lasts.
#[inline(always)]
unsafe fn segments<'a>(mbuf: *mut bindings::rte_mbuf) -> impl Iterator<Item = &'a [u8]> {
    core::iter::successors(NonNull::new(mbuf), |segment| {
        // SAFETY: a segment of a valid chain (caller contract).
        NonNull::new(unsafe { (*segment.as_ptr()).next })
    })
    .map(|segment| {
        let segment = segment.as_ptr();
        // SAFETY: a segment of a valid chain spans its data length from its
        // data offset, unwritten while `'a` lasts (caller contract).
        unsafe {
            core::slice::from_raw_parts(
                (*segment).buf_addr.cast::<u8>().add(usize::from((*segment).data_off)),
                usize::from((*segment).data_len),
            )
        }
    })
}

/// Reports whether the parser found a header: an unparsed one is all zero,
/// as an allocated packet's are.
#[inline(always)]
fn parsed(type_: u16, offset: u16) -> bool {
    type_ != 0 || offset != 0
}

/// Moves the offset of each parsed header starting at or after a frame
/// offset; unparsed headers stay unparsed.
///
/// # Safety
///
/// The packet must be valid and exclusively owned.
#[inline(always)]
unsafe fn move_parsed(packet: *mut bindings::packet, from: usize, shift: impl Fn(u16) -> u16) {
    // SAFETY: guaranteed by the caller.
    unsafe {
        let network = &raw mut (*packet).network_header;
        if parsed((*network).type_, (*network).offset) && usize::from((*network).offset) >= from {
            (*network).offset = shift((*network).offset);
        }
        let transport = &raw mut (*packet).transport_header;
        if parsed((*transport).type_, (*transport).offset) && usize::from((*transport).offset) >= from {
            (*transport).offset = shift((*transport).offset);
        }
    }
}

/// Clears what the parser writes of a packet, as an allocated packet has
/// it: the headers unparsed, no VLAN id, hash or flags.
///
/// # Safety
///
/// The packet must be valid and exclusively owned.
#[inline(always)]
unsafe fn clear_parse(packet: *mut bindings::packet) {
    // SAFETY: guaranteed by the caller.
    unsafe {
        (*packet).hash = 0;
        (*packet).flags = 0;
        (*packet).vlan = 0;
        (*packet).fragment_offset = 0;
        (*packet).network_header = bindings::network_header::default();
        (*packet).transport_header = bindings::transport_header::default();
    }
}

/// Maps the C result of a tunnel routine: zero is success.
#[inline(always)]
fn check(result: core::ffi::c_int) -> Result<(), TunnelError> {
    if result == 0 { Ok(()) } else { Err(TunnelError) }
}

impl Deref for Packet<'_> {
    type Target = PacketMeta;

    #[inline(always)]
    fn deref(&self) -> &PacketMeta {
        // SAFETY: an owned packet's descriptor is valid and initialised and
        // exclusively ours for the call (attach: packet), and the
        // transparent wrapper has its layout. Nothing writes it while the
        // packet is borrowed shared: every write of a descriptor takes its
        // packet mutably or consumes it.
        unsafe { self.raw.cast::<PacketMeta>().as_ref() }
    }
}

impl<'id> Packet<'id> {
    /// Mutable bytes of the first segment of the frame.
    #[inline(always)]
    pub fn data_mut(&mut self) -> &mut [u8] {
        // SAFETY: the packet and its mbuf are exclusively ours for the call,
        // with no shared or cloned data reachable (attach: packet); the first
        // segment spans the data length from the data offset.
        unsafe {
            let (start, len) = segment((*self.raw.as_ptr()).mbuf);
            core::slice::from_raw_parts_mut(start, len)
        }
    }

    /// Grows the frame at the front, into the headroom, and returns the new
    /// bytes; parsed header offsets move with the frame.
    ///
    /// Header views read the same headers and unparsed headers stay
    /// unparsed. The packet descriptor shares the buffer with the headroom,
    /// as in C's `packet_headroom_prepend`, so the room is what the headroom
    /// holds beyond the descriptor; beyond it the packet is left unchanged.
    #[doc(alias = "packet_headroom_prepend")]
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
            move_parsed(raw, 0, |offset| offset + len16);
            let (start, _) = segment((*raw).mbuf);
            Ok(core::slice::from_raw_parts_mut(start, len))
        }
    }

    /// Grows the frame at the end, into the tailroom of a single-segment
    /// packet, and returns the new bytes, as `rte_pktmbuf_append` does.
    ///
    /// Beyond the tailroom, or on a chained packet, the packet is left
    /// unchanged.
    #[doc(alias = "rte_pktmbuf_append")]
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
            let (start, old_len) = segment(mbuf);
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
    #[doc(alias = "rte_pktmbuf_trim")]
    #[inline]
    pub fn trim(&mut self, len: usize) -> Result<(), ResizeError> {
        let raw = self.raw.as_ptr();
        // SAFETY: as for prepending.
        unsafe {
            let mbuf = (*raw).mbuf;
            let data_len = usize::from((*mbuf).data_len);
            if !(*mbuf).next.is_null() || len > data_len || data_len - len < self.parsed_end() {
                return Err(ResizeError);
            }
            (*mbuf).data_len -= len as u16;
            (*mbuf).pkt_len -= len as u32;
            (*raw).data_len = (*mbuf).data_len;
        }
        Ok(())
    }

    /// Inserts `len` payload bytes at frame offset `at` of a single-segment
    /// packet, has `fill` write them, and keeps the headers consistent.
    ///
    /// The bytes before `at` keep their offsets: the shorter side moves, into
    /// the headroom beyond the descriptor or into the tailroom, the other side
    /// when that room is short. Parsed headers at or after `at` move with
    /// their bytes; unparsed headers stay unparsed. The packet is resized
    /// before `fill` runs, so the bytes it gets, and leaves unwritten, hold
    /// whatever the buffer held there; what it returns is returned. The header
    /// types, offsets and fragment fields are the parse's: a frame whose
    /// headers changed them is parsed again first, [`Packet::parse`].
    ///
    /// The IPv4 total length, with the header checksum, the IPv6 payload
    /// length and the UDP length grow by the bytes inserted in what each
    /// covers. Once `fill` returns, the TCP or UDP checksum is updated over
    /// the new bytes from the old checksum, so a segment that arrived damaged
    /// stays damaged; a UDP checksum covers the bytes up to the UDP length
    /// only. A fragment, a UDP datagram without a checksum or with a length
    /// past its datagram, and a segment the frame does not hold whole keep
    /// their transport checksum. Linking an inserted header into a chain,
    /// such as an IPv6 extension header, is the caller's.
    ///
    /// Refused, leaving the packet unchanged:
    /// - for memory safety: a chained packet, an offset past the frame, and a
    ///   length neither room takes;
    /// - for header consistency, as the lengths and checksums could not
    ///   describe the result: an offset inside a fixed IPv4, IPv6, TCP or UDP
    ///   header or inside the IPv4 or TCP options, and a length a length field
    ///   cannot hold.
    ///
    /// [`Packet::prepend`] and [`Packet::append`] edit the frame raw, as DPDK
    /// does, refusing only what memory safety needs.
    #[inline]
    pub fn insert<R>(&mut self, at: usize, len: usize, fill: impl FnOnce(&mut [u8]) -> R) -> Result<R, ResizeError> {
        self.insert_as(at, len, Insertion::Payload, fill)
    }

    /// Inserts `len` bytes of TCP options at frame offset `at`, between the
    /// end of the fixed TCP header and the end of its options, as
    /// [`Packet::insert`] inserts payload; `fill` writes them.
    ///
    /// The data offset grows with the lengths, and the TCP checksum covers
    /// both. The offset is the caller's to put between options: one inside an
    /// option splits it.
    ///
    /// Refused, besides what an insert refuses for memory safety, for header
    /// consistency: an offset outside the options, a length not of whole
    /// 32-bit words or one making the header longer than 60 bytes, a header
    /// that runs past its datagram or whose data offset is below five words,
    /// and a packet without an available TCP header.
    #[inline]
    pub fn insert_tcp_options<R>(
        &mut self,
        at: usize,
        len: usize,
        fill: impl FnOnce(&mut [u8]) -> R,
    ) -> Result<R, ResizeError> {
        self.insert_as(at, len, Insertion::TcpOptions, fill)
    }

    /// Inserts bytes as the insertion names them; see [`Packet::insert`].
    #[inline(always)]
    fn insert_as<R>(
        &mut self,
        at: usize,
        len: usize,
        insertion: Insertion,
        fill: impl FnOnce(&mut [u8]) -> R,
    ) -> Result<R, ResizeError> {
        let len16 = u16::try_from(len).map_err(|_| ResizeError)?;
        let data_len = self.data().len();
        if self.chained() || at > data_len {
            return Err(ResizeError);
        }
        // The plan refuses an offset inside a header the parser vouches for.
        let mut edit = Layout::read(self.data(), self.parsed())
            .plan_insert(self.data(), at, len, insertion)
            .map_err(|_| ResizeError)?;
        self.move_for_insert(at, len16)?;
        let frame = self.data_mut();
        edit.write_headers(frame);
        let filled = fill(&mut frame[at..at + len]);
        edit.write_transport_checksum(frame);
        Ok(filled)
    }

    /// Opens room for `len` bytes at frame offset `at`, moving the shorter
    /// side.
    ///
    /// The bytes before `at` keep their offsets and parsed headers at or after
    /// it move with their bytes. The caller has checked the packet is
    /// single-segment and `at` lies in the frame; the headers' contents are
    /// its to keep. A length neither room takes leaves the packet unchanged.
    #[inline(always)]
    fn move_for_insert(&mut self, at: usize, len16: u16) -> Result<(), ResizeError> {
        let (len, data_len) = (usize::from(len16), self.data().len());
        let raw = self.raw.as_ptr();
        // SAFETY: the packet, its mbuf and buffer are exclusively ours for
        // the call (attach: packet); callers keep `at` in its one segment.
        //
        // The moved bytes and the new ones lie in that segment and the room
        // checked for them: the headroom beyond the descriptor or the
        // tailroom. A parsed offset at or after the insert is no later than
        // the end of the frame, so the shifted one is no later than the end of
        // the grown frame, inside the buffer.
        unsafe {
            let mbuf = (*raw).mbuf;
            let data_off = usize::from((*mbuf).data_off);
            let front_room = data_off >= size_of::<bindings::packet>() + len;
            let tail_room = usize::from((*mbuf).buf_len) - data_off - data_len >= len;
            let front = match (at <= data_len - at, front_room, tail_room) {
                (_, false, false) => return Err(ResizeError),
                (true, true, _) | (false, true, false) => true,
                _ => false,
            };
            let (start, _) = segment(mbuf);
            if front {
                core::ptr::copy(start, start.sub(len), at);
                (*mbuf).data_off -= len16;
            } else {
                core::ptr::copy(start.add(at), start.add(at + len), data_len - at);
            }
            (*mbuf).data_len += len16;
            (*mbuf).pkt_len += u32::from(len16);
            // `packet_refresh_data_len`: the packet is on no counted list.
            (*raw).data_len = (*mbuf).data_len;
            move_parsed(raw, at, |offset| offset + len16);
        }
        Ok(())
    }

    /// Removes `len` bytes at frame offset `at` of a single-segment packet
    /// and keeps the headers consistent.
    ///
    /// The bytes before `at` keep their offsets: the shorter side moves.
    /// Parsed headers after the range move with their bytes; unparsed
    /// headers stay unparsed.
    ///
    /// At once, the lengths of the parsed headers shrink by the removed bytes
    /// in what each covers, removed option bytes shrink the IPv4 header length
    /// or the TCP data offset, and the IPv4 header checksum and the TCP or
    /// UDP checksum are updated over all of it. The transport checksum is
    /// left unchanged in the same cases as for [`Packet::insert`], and the
    /// parse is trusted as there.
    ///
    /// Refused, leaving the packet unchanged:
    /// - for memory safety: a chained packet, a range past the frame, one
    ///   holding a parsed header's start or a byte the parser vouches for, and
    ///   one that would leave a parsed network header starting inside the
    ///   14-byte Ethernet header, whose ether type the tunnel operations write
    ///   in the two bytes before it;
    /// - for header consistency: a range holding both option bytes and bytes
    ///   after the options, option bytes not of whole 32-bit words, and a
    ///   length a length field holds less of than the range removes.
    #[inline]
    pub fn remove(&mut self, at: usize, len: usize) -> Result<(), ResizeError> {
        let len16 = u16::try_from(len).map_err(|_| ResizeError)?;
        let data_len = self.data().len();
        let end = at.checked_add(len).ok_or(ResizeError)?;
        // A header vouching for no bytes still has its start, which the
        // removed range must not hold either.
        let overlaps = |&(start, span): &(usize, usize)| at < start + span.max(1) && start < end;
        // The network header must not move into the Ethernet header. A
        // parsed one may be of ether type zero, so its offset counts too.
        let PacketMeta(packet) = &**self;
        let network = packet.network_header;
        let offset = usize::from(network.offset);
        let into_ethernet =
            parsed(network.type_, network.offset) && offset >= end && offset - len < ETHERNET_HEADER_LEN;
        if self.chained()
            || end > data_len
            || into_ethernet
            || len > 0 && self.parsed_spans().iter().flatten().any(overlaps)
        {
            return Err(ResizeError);
        }
        let mut edit = Layout::read(self.data(), self.parsed())
            .plan_remove(self.data(), at, len)
            .map_err(|_| ResizeError)?;
        self.move_for_remove(at, len16);
        let frame = self.data_mut();
        edit.write_headers(frame);
        edit.write_transport_checksum(frame);
        Ok(())
    }

    /// Closes `len` bytes at frame offset `at`, moving the shorter side.
    ///
    /// The bytes before `at` keep their offsets and parsed headers after the
    /// range move with their bytes. The caller has checked the packet is
    /// single-segment, the range lies in the frame and holds no parsed
    /// header's start nor a byte the parser vouches for, and the network
    /// header stays behind the Ethernet header; the headers' contents are its
    /// to keep.
    #[inline(always)]
    fn move_for_remove(&mut self, at: usize, len16: u16) {
        let (len, data_len) = (usize::from(len16), self.data().len());
        let end = at + len;
        let raw = self.raw.as_ptr();
        // SAFETY: the packet, its mbuf and buffer are exclusively ours for
        // the call (attach: packet); callers keep the range in its one segment.
        //
        // A parsed offset after the removed range moves down by its length
        // and its vouched bytes stay whole, as none of them was removed; one
        // before it keeps its place, inside the shrunk frame.
        unsafe {
            let mbuf = (*raw).mbuf;
            let (start, _) = segment(mbuf);
            if at <= data_len - end {
                core::ptr::copy(start, start.add(len), at);
                (*mbuf).data_off += len16;
            } else {
                core::ptr::copy(start.add(end), start.add(at), data_len - end);
            }
            (*mbuf).data_len -= len16;
            (*mbuf).pkt_len -= u32::from(len16);
            (*raw).data_len = (*mbuf).data_len;
            move_parsed(raw, end, |offset| offset - len16);
        }
    }

    /// Replaces the options area of the parsed TCP header by `len` bytes,
    /// has `fill` write them, and keeps the headers consistent.
    ///
    /// The difference in size opens or closes at the start of the options as
    /// an insert or a remove there does, the packet resized before `fill`
    /// runs on bytes that hold whatever the buffer held; [`TcpOptions::write`]
    /// fills an area of [`TcpOptions::len`] bytes. The data offset and the IP
    /// length follow, and once `fill` returns the TCP checksum is updated over
    /// the old and the new options from the old checksum, so a damaged segment
    /// stays damaged. The parse is trusted as for [`Packet::insert`]; options
    /// read by [`TcpOptions::parse`] lose those of kinds it does not know.
    ///
    /// # Errors
    ///
    /// Refused, leaving the packet unchanged, are for memory safety a chained
    /// packet, a datagram that runs past the frame and a length neither room
    /// takes, and for header consistency a packet without an available TCP
    /// header behind a parsed IP header, a fragment, whose checksum covers
    /// bytes it does not hold and whose followers would keep their offsets, an
    /// IP header reaching into the TCP header, a header whose data offset is
    /// below five words or that runs past its datagram, and a length not of
    /// whole 32-bit words, beyond the 40 bytes a header holds, or one an IP
    /// length field cannot hold.
    ///
    /// [`TcpOptions::write`]: yanet_packet::tcp_options::TcpOptions::write
    /// [`TcpOptions::len`]: yanet_packet::tcp_options::TcpOptions::len
    /// [`TcpOptions::parse`]: yanet_packet::tcp_options::TcpOptions::parse
    #[inline]
    pub fn rewrite_tcp_options<R>(&mut self, len: usize, fill: impl FnOnce(&mut [u8]) -> R) -> Result<R, ResizeError> {
        if self.chained() {
            return Err(ResizeError);
        }
        let (mut edit, options) = Layout::read(self.data(), self.parsed())
            .plan_tcp_options(self.data(), len)
            .map_err(|_| ResizeError)?;
        // The plan bounds both lengths by the 40 bytes a header holds, and
        // the options lie behind the fixed TCP header, inside the frame.
        let old = options.len();
        if len > old {
            self.move_for_insert(options.start, (len - old) as u16)?;
        } else if old > len {
            self.move_for_remove(options.start, (old - len) as u16);
        }
        let frame = self.data_mut();
        edit.write_headers(frame);
        let filled = fill(&mut frame[options.start..options.start + len]);
        edit.write_transport_checksum(frame);
        Ok(filled)
    }

    /// Replaces the options area of the parsed TCP header by the options, as
    /// [`Packet::rewrite_tcp_options`] does with bytes a closure writes.
    ///
    /// # Errors
    ///
    /// The refusals of [`Packet::rewrite_tcp_options`]; options beyond the 40
    /// bytes a header holds are one of them, unless trimmed by
    /// [`TcpOptions::fit_to_header`] first.
    ///
    /// [`TcpOptions::fit_to_header`]: yanet_packet::tcp_options::TcpOptions::fit_to_header
    #[inline]
    pub fn write_tcp_options(&mut self, options: &yanet_packet::tcp_options::TcpOptions) -> Result<(), ResizeError> {
        self.rewrite_tcp_options(options.len(), |area| {
            let written = options.write(area);
            debug_assert!(written.is_ok());
        })
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
    #[doc(alias = "packet_ip4_encap")]
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
    #[doc(alias = "packet_ip6_encap")]
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
    #[doc(alias = "packet_mpls_encap")]
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
    #[doc(alias = "packet_ip4_encap_udp")]
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
        let PacketMeta(packet) = &**self;
        let transport = packet.transport_header;
        let raw = self.raw.as_ptr();
        let result = encap(raw);
        if !parsed(transport.type_, transport.offset) {
            // SAFETY: the packet is exclusively ours for the call (attach:
            // packet).
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
    #[doc(alias = "packet_decap")]
    #[inline]
    pub fn decap(&mut self) -> Result<(), TunnelError> {
        // SAFETY: the packet is exclusively ours for the call (attach: packet).
        check(unsafe { bindings::yanet_sys_dp_decap(self.raw.as_ptr()) })
    }

    /// Parses the frame as the dataplane parses a received packet
    /// (`parse_packet`, lib/dataplane/packet/packet.h): the header types and
    /// offsets, the VLAN id, the flow hash and the fragment fields.
    ///
    /// For a frame the module built or rewrote. Nothing of an earlier parse
    /// remains: the parser leaves the VLAN id of an untagged frame and the
    /// transport header of a frame that is neither IPv4 nor IPv6 as they
    /// were, so both are cleared first. A frame the parser refuses leaves the
    /// packet unparsed, as an allocated one. A chained packet is refused and
    /// left as it was, since the parser reads the first segment only.
    #[doc(alias = "parse_packet")]
    #[inline]
    pub fn parse(&mut self) -> Result<(), ParseError> {
        if self.chained() {
            return Err(ParseError);
        }
        let raw = self.raw.as_ptr();
        // SAFETY: the packet is exclusively ours for the call (attach:
        // packet); the parser writes only its descriptor and reads its only
        // segment within the packet length, which is that segment's length.
        // A refused frame is cleared again: the parser may have recorded a
        // header before finding it cut short, which no reader may see.
        unsafe {
            clear_parse(raw);
            if bindings::yanet_sys_dp_parse(raw) == 0 {
                return Ok(());
            }
            clear_parse(raw);
        }
        Err(ParseError)
    }
}

impl PacketMeta {
    /// Bytes of the first segment of the frame, which holds the parsed
    /// headers; a chained frame goes on in further segments.
    #[inline(always)]
    pub fn data(&self) -> &[u8] {
        let Self(packet) = self;
        // SAFETY: a descriptor is reached only through a shared borrow of
        // its packet or batch, so its mbuf is valid and unwritten meanwhile;
        // the first segment spans the data length from the data offset
        // (attach: packet).
        unsafe {
            let (start, len) = segment(packet.mbuf);
            core::slice::from_raw_parts(start, len)
        }
    }

    /// Bytes of the frame segment by segment, in order: the first segment,
    /// as [`PacketMeta::data`] holds it, then those a chained packet goes on
    /// in.
    #[inline(always)]
    pub fn segments(&self) -> impl Iterator<Item = &[u8]> {
        let Self(packet) = self;
        // SAFETY: as for the first segment, for every segment of the chain.
        unsafe { segments(packet.mbuf) }
    }

    /// Length of the frame across its segments: the mbuf's packet length,
    /// as C's `rte_pktmbuf_pkt_len` reads it.
    #[doc(alias = "rte_pktmbuf_pkt_len")]
    #[inline(always)]
    pub fn frame_len(&self) -> usize {
        let Self(packet) = self;
        // SAFETY: as for the first segment.
        unsafe { (*packet.mbuf).pkt_len as usize }
    }

    /// Ether type of the network header the parser found, host order.
    #[inline(always)]
    pub fn network_type(&self) -> u16 {
        let Self(packet) = self;
        u16::from_be(packet.network_header.type_)
    }

    /// Offset of the network header from the start of the frame.
    #[inline(always)]
    pub fn network_offset(&self) -> u16 {
        let Self(packet) = self;
        packet.network_header.offset
    }

    /// Raw transport tag the parser wrote: the protocol number in the low
    /// byte and the unavailable mark in bit 8.
    ///
    /// Read it through the protocol and availability accessors.
    #[inline(always)]
    pub fn transport_type(&self) -> u16 {
        let Self(packet) = self;
        packet.transport_header.type_
    }

    /// Declared transport protocol: the protocol number of the IP header,
    /// meaningful also when the transport header itself is unavailable
    /// inside a non-initial fragment (`packet_transport_protocol`,
    /// lib/dataplane/packet/packet.h).
    #[doc(alias = "packet_transport_protocol")]
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
    #[doc(alias = "packet_transport_header_available")]
    #[inline(always)]
    pub fn transport_header_available(&self) -> bool {
        u32::from(self.transport_type()) & bindings::PACKET_TRANSPORT_HEADER_UNAVAILABLE == 0
    }

    /// Offset of the packet's fragment in its datagram, as parsed: nonzero
    /// exactly for a fragment after the first.
    ///
    /// The scale is the parser's and differs by the parsed family: IPv4
    /// counts 8-byte units, IPv6 keeps its header field with the flag bits
    /// cleared, which is bytes. A tunnel operation leaves it as parsed.
    #[inline(always)]
    pub fn fragment_offset(&self) -> u16 {
        let Self(packet) = self;
        packet.fragment_offset
    }

    /// Offset of the transport header from the start of the frame.
    #[inline(always)]
    pub fn transport_offset(&self) -> u16 {
        let Self(packet) = self;
        packet.transport_header.offset
    }

    /// Flow hash the parser computed: CRC-32C over the IP addresses and,
    /// unless the packet is a fragment after the first, the TCP or UDP
    /// ports.
    ///
    /// A tunnel operation leaves it as parsed.
    #[inline(always)]
    pub fn hash(&self) -> u32 {
        let Self(packet) = self;
        packet.hash
    }

    /// Reports whether the parser found the packet to be a fragment of a
    /// datagram, the first one included (`PACKET_FLAG_FRAGMENTED`,
    /// lib/dataplane/packet/packet.h).
    ///
    /// The fragment offset is zero for a first fragment and an unfragmented
    /// packet alike; this tells them apart.
    #[inline(always)]
    pub fn is_fragment(&self) -> bool {
        let Self(packet) = self;
        packet.flags & (1 << bindings::packet_flag_PACKET_FLAG_FRAGMENTED) != 0
    }

    /// Whether the packet's mbuf continues in further segments.
    #[inline(always)]
    fn chained(&self) -> bool {
        let Self(packet) = self;
        // SAFETY: as for the first segment.
        unsafe { !(*packet.mbuf).next.is_null() }
    }

    /// End of what the parser vouches for, from the start of the frame; zero
    /// for an unparsed packet.
    ///
    /// That is (attach: packet) the start of a parsed network or transport
    /// header, the fixed IPv4 or IPv6 header and an available TCP or UDP
    /// header.
    #[inline(always)]
    fn parsed_end(&self) -> usize {
        self.parsed_spans()
            .iter()
            .flatten()
            .map(|&(start, span)| start + span)
            .max()
            .unwrap_or(0)
    }

    /// What the parser found, as the resize bookkeeping reads it: the
    /// network header's type and offset, the protocol and offset of an
    /// available transport header, and whether the packet is a fragment.
    #[inline(always)]
    fn parsed(&self) -> Parsed {
        let Self(packet) = self;
        let transport = packet.transport_header;
        let available = parsed(transport.type_, transport.offset) && self.transport_header_available();
        Parsed {
            network_type: self.network_type(),
            network_offset: self.network_offset(),
            transport: available.then_some((self.transport_protocol(), transport.offset)),
            fragment: self.is_fragment(),
        }
    }

    /// Start and vouched length of the parsed network and transport
    /// headers, from the start of the frame; `None` for an unparsed one.
    ///
    /// The parser vouches (attach: packet) for the fixed IPv4 or IPv6 header
    /// and an available TCP or UDP header, and for no bytes of other headers.
    #[inline(always)]
    fn parsed_spans(&self) -> [Option<(usize, usize)>; 2] {
        let Self(packet) = self;
        let (network, transport) = (packet.network_header, packet.transport_header);
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
        [
            parsed(network.type_, network.offset).then_some((usize::from(network.offset), network_len)),
            parsed(transport.type_, transport.offset).then_some((usize::from(transport.offset), transport_len)),
        ]
    }
}

#[cfg(test)]
mod tests;
