//! Packets of one handler call and their disposal.
//!
//! The packet lists stay C's and are used as a C handler uses them: taking
//! a packet pops it off the input list, and its verdict moves it to the
//! output or drop list or routes it to a device. A packet the handler does
//! not take stays on the input list.

use core::{marker::PhantomData, ptr::NonNull};

use crate::bindings;

/// Packets of one handler call.
///
/// Neither `Send` nor `Sync`; packets it hands out carry its brand `'id`
/// and are accepted back only by the batch of the same call.
///
/// A packet of one call's batch is refused by the batch of another call:
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
    // Invariant brand: values of two different contexts never unify, so they cannot be mixed.
    _brand: PhantomData<fn(&'id ()) -> &'id ()>,
    // Neither Send nor Sync on purpose, independent of the pointer fields.
    _local: PhantomData<*mut ()>,
}

/// A packet taken from the batch, owned until it is disposed of.
///
/// Not `Copy` or `Clone`; consumed by passing, dropping or redirecting it. A
/// packet that is never disposed of is on no list
/// after the call, as one a C handler forgets.
///
/// A packet cannot be kept beyond the call it was taken in:
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

/// Routing target of a device for the running generation.
///
/// Branded with the call it was made in, invariantly, so a batch redirects
/// only to a target of its own call's context.
///
/// A target of one call's context is refused by the batch of another call:
///
/// ```compile_fail
/// # use yanet_sdk_testing::dp::Model;
/// # use yanet_sys::dp::{Batch, Cx, Device};
/// # type Mine = Model<Device>;
/// fn cross<'a, 'b>(cx: &Cx<'a, Mine>, device: &'a Device, batch: &mut Batch<'b>) {
///     if let (Some(packet), Some(target)) = (batch.next(), device.resolve(cx)) {
///         batch.redirect(packet, target);
///     }
/// }
/// ```
///
/// Redirecting within the call is accepted:
///
/// ```
/// # use yanet_sdk_testing::dp::Model;
/// # use yanet_sys::dp::{Batch, Cx, Device};
/// # type Mine = Model<Device>;
/// fn cross<'a, 'b>(cx: &Cx<'a, Mine>, device: &'a Device, batch: &mut Batch<'a>) {
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

impl<'id> Batch<'id> {
    /// Wraps the front of one handler call.
    ///
    /// # Safety
    ///
    /// The front must be the one C passed to the running handler, valid and
    /// exclusively owned by this call, with well-formed lists of packets that
    /// are themselves exclusively owned; the execution context must be the
    /// call's.
    #[inline(always)]
    pub(crate) unsafe fn new(front: NonNull<bindings::packet_front>, ectx: NonNull<bindings::module_ectx>) -> Self {
        Self {
            front,
            ectx,
            _brand: PhantomData,
            _local: PhantomData,
        }
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
        // exclusively ours (only taking creates one, and its value is
        // consumed once); the front is this call's, and a redirect target is
        // a present target of this call's context (device reference
        // contract) on which the C routing runs (attach: unwind).
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

    /// Start and length of the first segment of the frame: C's
    /// `packet_data` and `packet_data_len` (lib/dataplane/packet/data.h).
    #[inline(always)]
    fn segment(&self) -> (*mut u8, usize) {
        // SAFETY: the packet and its first mbuf segment are valid for the
        // call (attach: packet).
        unsafe {
            let mbuf = (*self.raw.as_ptr()).mbuf;
            let buf_addr = (*mbuf).buf_addr;
            (
                buf_addr.cast::<u8>().add(usize::from((*mbuf).data_off)),
                usize::from((*mbuf).data_len),
            )
        }
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
