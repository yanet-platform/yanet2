extern crate alloc;

use alloc::{
    alloc::{alloc_zeroed, dealloc},
    vec,
    vec::Vec,
};
use core::{
    alloc::Layout,
    net::{Ipv4Addr, Ipv6Addr},
};

use super::*;

/// An mbuf with the given offset and lengths over a buffer of the test,
/// zeroed otherwise.
fn mbuf(buffer: &mut [u8], data_off: u16, data_len: u16) -> bindings::rte_mbuf {
    bindings::rte_mbuf {
        buf_addr: buffer.as_mut_ptr().cast(),
        buf_len: u16::try_from(buffer.len()).expect("the buffer exceeds the mbuf geometry"),
        data_off,
        data_len,
        pkt_len: u32::from(data_len),
        ..Default::default()
    }
}

/// A front whose input and output lists hold the given packets and whose
/// counters are the given values, which need not match the lists.
fn front(input: &mut [bindings::packet], output: &mut [bindings::packet], counts: [u64; 4]) -> bindings::packet_front {
    let mut front = bindings::packet_front {
        input: list(input),
        output: list(output),
        ..Default::default()
    };
    [
        front.input_count,
        front.input_bytes,
        front.output_count,
        front.output_bytes,
    ] = counts;
    front
}

/// An intrusive list of the packets in order, as the C append builds it;
/// the packets stay where they are.
fn list(packets: &mut [bindings::packet]) -> bindings::packet_list {
    let mut next: *mut bindings::packet = core::ptr::null_mut();
    let mut last: *mut *mut bindings::packet = core::ptr::null_mut();
    for packet in packets.iter_mut().rev() {
        packet.next = next;
        if last.is_null() {
            last = &raw mut packet.next;
        }
        next = packet;
    }
    bindings::packet_list { first: next, last }
}

/// Indices into `packets` of a front's output list in order, its counters
/// and whether its input list is empty.
fn observe(front: &bindings::packet_front, packets: &[bindings::packet]) -> (Vec<usize>, [u64; 4], bool) {
    let index = |cursor: *mut bindings::packet| {
        packets
            .iter()
            .position(|packet| core::ptr::eq(packet, cursor))
            .expect("a packet of this test")
    };
    let mut order = Vec::new();
    let mut cursor = front.output.first;
    while !cursor.is_null() {
        let idx = index(cursor);
        order.push(idx);
        cursor = packets[idx].next;
    }
    let counts = [
        front.input_count,
        front.input_bytes,
        front.output_count,
        front.output_bytes,
    ];
    (order, counts, front.input.first.is_null() && front.input.last.is_null())
}

/// Runs the Rust pass over a front of input packets, then output packets,
/// with the given counters; returns the output order, the counters and
/// whether the input list is empty.
fn rust_pass_all(inputs: usize, outputs: usize, counts: [u64; 4]) -> (Vec<usize>, [u64; 4], bool) {
    let mut packets = vec![bindings::packet::default(); inputs + outputs];
    let (input, output) = packets.split_at_mut(inputs);
    let mut front = front(input, output, counts);

    // SAFETY: the front is this test's; the batch reads only its front,
    // and the context and worker it is given are never read.
    unsafe { Batch::new(NonNull::from(&mut front), NonNull::dangling(), NonNull::dangling()).pass_all() };

    observe(&front, &packets)
}

/// Runs the Rust growth over an mbuf of 7 bytes at `data_off`; returns
/// whether it grew and the mbuf's data offset, data length and packet
/// length.
fn rust_grow_front(data_off: u16, len: u16) -> (bool, u16, u16, u32) {
    let mut buffer = [0u8; 1024];
    let mut mbuf = mbuf(&mut buffer, data_off, 7);

    // SAFETY: a valid mbuf of this test.
    let grew = unsafe { grow_front(&raw mut mbuf, len) };

    (grew, mbuf.data_off, mbuf.data_len, mbuf.pkt_len)
}

/// Runs the C pass over the same front as the Rust one.
#[cfg(not(miri))]
fn c_pass_all(inputs: usize, outputs: usize, counts: [u64; 4]) -> (Vec<usize>, [u64; 4], bool) {
    let mut packets = vec![bindings::packet::default(); inputs + outputs];
    let (input, output) = packets.split_at_mut(inputs);
    let mut front = front(input, output, counts);

    // SAFETY: the front is this test's.
    unsafe { bindings::yanet_sys_dp_front_pass(&raw mut front) };

    observe(&front, &packets)
}

/// Runs the C growth over the same mbuf as the Rust one.
#[cfg(not(miri))]
fn c_grow_front(data_off: u16, len: u16) -> (bool, u16, u16, u32) {
    let mut buffer = [0u8; 1024];
    let mut mbuf = mbuf(&mut buffer, data_off, 7);

    // SAFETY: a valid mbuf of this test.
    let grew = unsafe { !bindings::yanet_sys_dp_headroom_prepend(&raw mut mbuf, len).is_null() };

    (grew, mbuf.data_off, mbuf.data_len, mbuf.pkt_len)
}

/// verifies that passing moves the input packets after the output ones
/// and the input counters onto the output counters, for empty and filled
/// lists and for counters that already disagree with the input list, as
/// after a packet was taken; natively, the Rust twin of
/// `packet_front_pass` also matches the C routine.
#[test]
fn test_pass_all_moves_input_to_output() {
    for (inputs, outputs, counts, expected) in [
        // Empty front.
        (0, 0, [0, 0, 0, 0], (vec![], [0, 0, 0, 0], true)),
        // Input only.
        (3, 0, [3, 180, 0, 0], (vec![0, 1, 2], [0, 0, 3, 180], true)),
        // Output only.
        (0, 2, [0, 0, 2, 120], (vec![0, 1], [0, 0, 2, 120], true)),
        // Input appended after output.
        (2, 2, [2, 120, 2, 120], (vec![2, 3, 0, 1], [0, 0, 4, 240], true)),
        // Input counters still counting packets taken before the pass.
        (1, 1, [3, 180, 1, 60], (vec![1, 0], [0, 0, 4, 240], true)),
    ] {
        let actual = rust_pass_all(inputs, outputs, counts);
        assert_eq!(
            expected, actual,
            "{inputs} input, {outputs} output, counters {counts:?}"
        );
        #[cfg(not(miri))]
        assert_eq!(
            c_pass_all(inputs, outputs, counts),
            actual,
            "{inputs} input, {outputs} output, counters {counts:?}"
        );
    }
}

/// verifies that the growth takes exactly the room the headroom holds
/// beyond the descriptor, around the descriptor boundary and the
/// dataplane's headroom, and moves the three fields together; natively,
/// the Rust twin of `packet_headroom_prepend` also matches the C routine
/// at every offset and length around the descriptor and the headroom.
#[test]
fn test_grow_front_takes_the_headroom_beyond_the_descriptor() {
    let descriptor = size_of::<bindings::packet>() as u16;
    let headroom = bindings::RTE_PKTMBUF_HEADROOM as u16;
    let room = headroom - descriptor;

    // Nothing to grow, the data starting right after the descriptor.
    assert_eq!((true, descriptor, 7, 7), rust_grow_front(descriptor, 0));
    // Exactly the room the headroom holds beyond the descriptor.
    assert_eq!((true, descriptor, 15, 15), rust_grow_front(descriptor + 8, 8));
    // One byte more than that room.
    assert_eq!((false, descriptor + 8, 7, 7), rust_grow_front(descriptor + 8, 9));
    // Data starting inside the descriptor: no room at all.
    assert_eq!((false, descriptor - 1, 7, 7), rust_grow_front(descriptor - 1, 0));
    // The dataplane's headroom, used up and overrun.
    assert_eq!(
        (true, descriptor, 7 + room, 7 + u32::from(room)),
        rust_grow_front(headroom, room)
    );
    assert_eq!((false, headroom, 7, 7), rust_grow_front(headroom, 600));

    #[cfg(not(miri))]
    for data_off in (0..=descriptor + 40).chain([255, 256, 600]) {
        for len in (0..=descriptor + 40).chain([255, 256, 600]) {
            assert_eq!(
                c_grow_front(data_off, len),
                rust_grow_front(data_off, len),
                "offset {data_off}, len {len}"
            );
        }
    }
}

/// verifies that both UDP encapsulations refuse a chained packet and leave
/// it unchanged, since C would size and checksum the first segment only.
#[test]
fn test_encap_udp_refuses_a_chained_packet() {
    let f = |encap: fn(&mut Packet<'_>) -> Result<(), TunnelError>| {
        let (mut head_buffer, mut tail_buffer) = ([0u8; 512], [0u8; 512]);
        let mut tail = mbuf(&mut tail_buffer, 256, 60);
        let mut head = bindings::rte_mbuf {
            next: &raw mut tail,
            ..mbuf(&mut head_buffer, 256, 60)
        };
        let mut descriptor = bindings::packet {
            mbuf: &raw mut head,
            data_len: 60,
            network_header: bindings::network_header { type_: 0x0800u16.to_be(), offset: 14 },
            ..Default::default()
        };
        let mut packet = Packet {
            raw: NonNull::from(&mut descriptor),
            _brand: PhantomData,
            _local: PhantomData,
        };

        assert_eq!(Err(TunnelError), encap(&mut packet));
        assert_eq!(
            (256, 60, 14),
            (head.data_off, head.data_len, descriptor.network_header.offset)
        );
    };

    // UDP over IPv4.
    f(|packet| packet.encap_ipv4_udp([10, 0, 0, 1], [10, 0, 0, 2], 4789, 4789));
    // UDP over IPv6.
    f(|packet| packet.encap_ipv6_udp([1; 16], [2; 16], 4789, 4789));
}

/// A single-segment packet over a buffer of its own, laid out as the worker
/// pool lays one out: the descriptor in the buffer's first bytes, then the
/// headroom, the frame and the tailroom.
///
/// The buffer is exactly that long and aligned for the descriptor, so Miri
/// reports a write past its ends, and a write into the descriptor's room by a
/// prepend or a growth bug changes the offsets the tests read. The pointers
/// between the parts are set when the packet is taken, where the fixture then
/// stays.
struct Frame {
    base: NonNull<u8>,
    layout: Layout,
    mbuf: bindings::rte_mbuf,
}

impl Frame {
    fn new(descriptor: bindings::packet, headroom: u16, frame: &[u8], tailroom: u16) -> Self {
        let data_off = size_of::<bindings::packet>() as u16 + headroom;
        let len = u16::try_from(frame.len()).expect("the frame exceeds the mbuf geometry");
        let size = usize::from(data_off) + frame.len() + usize::from(tailroom);
        let layout = Layout::from_size_align(size, align_of::<bindings::packet>()).expect("a valid layout");
        // SAFETY: the size is not zero, as the descriptor is in it.
        let base = NonNull::new(unsafe { alloc_zeroed(layout) }).expect("the buffer is allocated");
        // SAFETY: the buffer is zeroed, exclusive and aligned for the
        // descriptor, and holds it and then the frame.
        unsafe {
            base.cast::<bindings::packet>()
                .write(bindings::packet { data_len: len, ..descriptor });
            base.as_ptr()
                .add(data_off.into())
                .copy_from_nonoverlapping(frame.as_ptr(), frame.len());
        }
        Self {
            base,
            layout,
            mbuf: bindings::rte_mbuf {
                buf_len: u16::try_from(size).expect("the buffer exceeds the mbuf geometry"),
                data_off,
                data_len: len,
                pkt_len: u32::from(len),
                ..Default::default()
            },
        }
    }

    /// The descriptor, linked to the mbuf and the mbuf to the buffer.
    fn link(&mut self) -> &mut bindings::packet {
        self.mbuf.buf_addr = self.base.as_ptr().cast();
        // SAFETY: the descriptor is initialised, in the buffer, and borrowed
        // exclusively through this fixture.
        let descriptor = unsafe { &mut *self.base.cast::<bindings::packet>().as_ptr() };
        descriptor.mbuf = &raw mut self.mbuf;
        descriptor
    }

    fn packet(&mut self) -> Packet<'_> {
        Packet {
            raw: NonNull::from(self.link()),
            _brand: PhantomData,
            _local: PhantomData,
        }
    }

    /// The frame as the mbuf describes it now.
    fn frame(&self) -> &[u8] {
        // SAFETY: the mbuf describes bytes of the buffer, which no packet
        // borrowed from this fixture writes while it is borrowed here.
        unsafe {
            core::slice::from_raw_parts(
                self.base.as_ptr().add(self.mbuf.data_off.into()),
                self.mbuf.data_len.into(),
            )
        }
    }

    /// The descriptor as it is now.
    fn descriptor(&self) -> bindings::packet {
        // SAFETY: the descriptor is initialised, in the buffer.
        unsafe { *self.base.cast::<bindings::packet>().as_ptr() }
    }

    /// Network and transport header offsets as they are now.
    fn offsets(&self) -> (u16, u16) {
        // SAFETY: the descriptor is initialised, in the buffer.
        let descriptor = unsafe { &*self.base.cast::<bindings::packet>().as_ptr() };
        (descriptor.network_header.offset, descriptor.transport_header.offset)
    }
}

impl Drop for Frame {
    fn drop(&mut self) {
        // SAFETY: the buffer came from `alloc_zeroed` with this layout.
        unsafe { dealloc(self.base.as_ptr(), self.layout) };
    }
}

/// Descriptor of a packet whose network header, of a type in host order,
/// follows a 14-byte Ethernet header, and whose transport header is of a
/// type at an offset (zeros: unparsed).
fn headers(network_type: u16, transport_type: u16, transport_offset: u16) -> bindings::packet {
    bindings::packet {
        network_header: bindings::network_header {
            type_: network_type.to_be(),
            offset: 14,
        },
        transport_header: bindings::transport_header {
            type_: transport_type,
            offset: transport_offset,
        },
        ..Default::default()
    }
}

/// An IPv4/UDP packet of a numbered frame behind 16 bytes of headroom,
/// with the parsed offsets, and `tailroom` bytes after it.
fn ipv4_udp(tailroom: u16) -> Frame {
    let frame: [u8; 42] = core::array::from_fn(|idx| idx as u8);
    Frame::new(headers(0x0800, 17, 34), 16, &frame, tailroom)
}

/// Source and destination of the IPv4 headers the tests build.
const IPV4_ADDRESSES: [u8; 8] = [10, 0, 0, 1, 10, 0, 0, 2];

/// Source and destination of the IPv6 headers the tests build.
const IPV6_ADDRESSES: [u8; 32] = [
    0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, //
    0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2,
];

/// An Ethernet frame of the ether type, host order, around the bytes.
fn ether(ether_type: u16, payload: &[u8]) -> Vec<u8> {
    let mut frame = vec![0u8; 12];
    frame.extend_from_slice(&ether_type.to_be_bytes());
    frame.extend_from_slice(payload);
    frame
}

/// An IPv4 header without options, of the protocol and the flags-and-offset
/// word, host order, from the test addresses, before the payload, whose
/// length the total length counts.
fn ipv4(protocol: u8, fragment: u16, payload: &[u8]) -> Vec<u8> {
    let total = u16::try_from(20 + payload.len()).expect("an IPv4 datagram");
    let mut header = vec![0x45, 0];
    header.extend_from_slice(&total.to_be_bytes());
    header.extend_from_slice(&[0, 0]);
    header.extend_from_slice(&fragment.to_be_bytes());
    header.extend_from_slice(&[64, protocol, 0, 0]);
    header.extend_from_slice(&IPV4_ADDRESSES);
    header.extend_from_slice(payload);
    header
}

/// An IPv6 header of the next header, from the test addresses, before the
/// payload, whose length the payload length counts.
fn ipv6(next_header: u8, payload: &[u8]) -> Vec<u8> {
    let len = u16::try_from(payload.len()).expect("an IPv6 payload");
    let mut header = vec![0x60, 0, 0, 0];
    header.extend_from_slice(&len.to_be_bytes());
    header.extend_from_slice(&[next_header, 64]);
    header.extend_from_slice(&IPV6_ADDRESSES);
    header.extend_from_slice(payload);
    header
}

/// An IPv6 Fragment header of the next header, offset in 8-byte units and
/// more-fragments bit, before the payload.
fn ipv6_fragment(next_header: u8, offset: u16, more: bool, payload: &[u8]) -> Vec<u8> {
    let mut header = vec![next_header, 0];
    header.extend_from_slice(&((offset << 3) | u16::from(more)).to_be_bytes());
    header.extend_from_slice(&[0, 0, 0, 1]);
    header.extend_from_slice(payload);
    header
}

/// A TCP header without options between the ports.
fn tcp(src_port: u16, dst_port: u16) -> Vec<u8> {
    let mut header = src_port.to_be_bytes().to_vec();
    header.extend_from_slice(&dst_port.to_be_bytes());
    header.extend_from_slice(&[0; 8]);
    header.extend_from_slice(&[0x50, 0x02, 0xff, 0xff, 0, 0, 0, 0]);
    header
}

/// A UDP header between the ports, of an empty datagram.
fn udp(src_port: u16, dst_port: u16) -> Vec<u8> {
    let mut header = src_port.to_be_bytes().to_vec();
    header.extend_from_slice(&dst_port.to_be_bytes());
    header.extend_from_slice(&[0, 8, 0, 0]);
    header
}

/// CRC-32C of the bytes onto a running value, as the x86 instruction the
/// parser's hash uses computes it: reflected, with no inversion.
fn crc32c(bytes: &[u8], mut crc: u32) -> u32 {
    for byte in bytes {
        crc ^= u32::from(*byte);
        for _ in 0..8 {
            crc = (crc >> 1) ^ (0x82f6_3b78 & (crc & 1).wrapping_neg());
        }
    }
    crc
}

/// Descriptor of a packet that an earlier parse left: a VLAN id, a hash, the
/// fragment fields and a TCP header behind IPv4.
fn stale() -> bindings::packet {
    bindings::packet {
        hash: 0x5eed,
        flags: 1 << bindings::packet_flag_PACKET_FLAG_FRAGMENTED,
        vlan: 7,
        fragment_offset: 3,
        ..headers(0x0800, 6, 34)
    }
}

/// The parse results the tests compare: header types and offsets, the
/// availability mark, the VLAN id, hash, fragment flag and offset.
#[derive(Debug, PartialEq, Eq)]
struct Parsed {
    network: (u16, u16),
    transport: (u8, u16),
    available: bool,
    vlan: u16,
    hash: u32,
    is_fragment: bool,
    fragment_offset: u16,
}

impl Parsed {
    /// The results a packet reports, with the VLAN id of its descriptor,
    /// which has no accessor.
    fn of(frame: &mut Frame) -> Self {
        let vlan = frame.descriptor().vlan;
        let packet = frame.packet();
        Self {
            network: (packet.network_type(), packet.network_offset()),
            transport: (packet.transport_protocol(), packet.transport_offset()),
            available: packet.transport_header_available(),
            vlan,
            hash: packet.hash(),
            is_fragment: packet.is_fragment(),
            fragment_offset: packet.fragment_offset(),
        }
    }

    /// The results of an unparsed packet.
    fn none() -> Self {
        Self {
            network: (0, 0),
            transport: (0, 0),
            available: true,
            vlan: 0,
            hash: 0,
            is_fragment: false,
            fragment_offset: 0,
        }
    }
}

/// verifies that a prepend takes exactly the room the headroom holds
/// beyond the descriptor that shares its buffer, writing in front of the
/// frame and moving the parsed offsets with it, and refuses one byte more,
/// leaving the frame and its offsets as they were.
#[test]
fn test_prepend_takes_the_headroom_and_no_more() {
    for (len, accepted) in [(4, true), (16, true), (17, false)] {
        let mut frame = ipv4_udp(0);
        let (before, (network, transport)) = (frame.frame().to_vec(), frame.offsets());

        let prepended = frame.packet().prepend(len).map(|room| room.fill(0xaa));

        let shift = if accepted { len as u16 } else { 0 };
        let expected = if accepted {
            [vec![0xaa; len], before.clone()].concat()
        } else {
            before
        };
        assert_eq!(accepted, prepended.is_ok(), "{len} bytes");
        assert_eq!(expected, frame.frame(), "{len} bytes");
        assert_eq!((network + shift, transport + shift), frame.offsets(), "{len} bytes");
        assert_eq!(expected.len() as u32, frame.mbuf.pkt_len, "{len} bytes");
    }
}

/// verifies that appends and trims resize the tail within the tailroom
/// and keep the mbuf's packet length in step.
#[test]
fn test_append_and_trim_resize_the_tail() {
    let mut frame = ipv4_udp(8);
    let before = frame.frame().to_vec();
    let mut packet = frame.packet();

    packet.append(6).expect("tailroom").fill(0xee);
    packet.trim(6).expect("the appended bytes");
    packet.append(2).expect("tailroom").copy_from_slice(b"ok");

    let expected = [before, b"ok".to_vec()].concat();
    assert_eq!(expected, frame.frame());
    assert_eq!(expected.len() as u32, frame.mbuf.pkt_len);
}

#[test]
fn test_append_beyond_tailroom_is_refused() {
    let mut frame = ipv4_udp(0);
    let before = frame.frame().to_vec();

    assert_eq!(Some(ResizeError), frame.packet().append(1).err());

    assert_eq!(before, frame.frame());
}

/// verifies that a trim takes the payload behind the parsed headers and
/// refuses one byte more, which would cut into the header the case names
/// or move a parsed header start past the frame, leaving the frame
/// unchanged.
#[test]
fn test_trim_keeps_the_parsed_headers() {
    let unavailable = 0x100;
    // The frame is the Ethernet header, the headers of the case and four
    // payload bytes; `room` is how much of its tail may go.
    for (case, network_type, transport_type, transport_offset, len, room) in [
        ("udp header", 0x0800, 17, 34, 14 + 20 + 8 + 4, 4),
        ("tcp header", 0x0800, 6, 34, 14 + 20 + 20 + 4, 4),
        ("ipv4 header", 0x0800, 0, 0, 14 + 20 + 4, 4),
        ("ipv6 header", 0x86dd, 0, 0, 14 + 40 + 4, 4),
        (
            "unavailable udp starts in the frame",
            0x0800,
            17 | unavailable,
            34,
            14 + 20 + 8 + 4,
            12,
        ),
        ("non-ip network starts in the frame", 0x0806, 0, 0, 14 + 28 + 4, 32),
    ] {
        let build = || {
            Frame::new(
                headers(network_type, transport_type, transport_offset),
                0,
                &vec![0; len],
                0,
            )
        };
        let (mut accepted, mut refused) = (build(), build());

        assert_eq!(Ok(()), accepted.packet().trim(room), "{case}");
        assert_eq!(Err(ResizeError), refused.packet().trim(room + 1), "{case}");

        assert_eq!(
            (len - room, len),
            (accepted.frame().len(), refused.frame().len()),
            "{case}"
        );
    }
}

/// verifies that a trim of an unparsed packet takes up to the whole frame
/// and refuses one byte past it.
#[test]
fn test_trim_of_unparsed_packet_up_to_the_frame_length() {
    let (mut whole, mut past) = (
        Frame::new(Default::default(), 0, &[0; 42], 0),
        Frame::new(Default::default(), 0, &[0; 42], 0),
    );

    assert_eq!(Ok(()), whole.packet().trim(42));
    assert_eq!(Err(ResizeError), past.packet().trim(43));

    assert_eq!((0, 42), (whole.frame().len(), past.frame().len()));
}

/// A numbered frame of `len` bytes whose parsed headers carry no length: a
/// non-IP network header at 14 and an unavailable UDP header at 34.
fn numbered(headroom: u16, len: usize, tailroom: u16) -> Frame {
    numbered_as(0x0806, headroom, len, tailroom)
}

/// [`numbered`] with a network header of the ether type, host order.
fn numbered_as(network_type: u16, headroom: u16, len: usize, tailroom: u16) -> Frame {
    let frame: Vec<u8> = (0..len).map(|idx| idx as u8).collect();
    Frame::new(headers(network_type, 0x100 | 17, 34), headroom, &frame, tailroom)
}

/// verifies that an insert moves the shorter side, or the other when its
/// room is short, puts the new bytes at the offset with the bytes around
/// them in order, and moves the parsed headers at or after it.
#[test]
fn test_insert_moves_the_shorter_side_and_the_headers_after_it() {
    for (case, headroom, tailroom, at, len, front, offsets) in [
        ("front, before both headers", 16, 16, 4, 4, true, (18, 38)),
        ("tail, at the end", 16, 16, 42, 4, false, (14, 34)),
        ("tail, between the headers", 16, 16, 34, 8, false, (14, 42)),
        ("tail, the headroom short", 0, 8, 4, 4, false, (18, 38)),
        ("front, the tailroom short", 8, 0, 42, 4, true, (14, 34)),
        ("front, filling the headroom exactly", 4, 0, 4, 4, true, (18, 38)),
    ] {
        let mut frame = numbered(headroom, 42, tailroom);
        let (before, data_off) = (frame.frame().to_vec(), frame.mbuf.data_off);

        frame.packet().insert(at, len, |gap| gap.fill(0xaa)).expect(case);

        let expected = [&before[..at], &vec![0xaa; len], &before[at..]].concat();
        let moved = if front { len as u16 } else { 0 };
        assert_eq!(expected, frame.frame(), "{case}");
        assert_eq!(offsets, frame.offsets(), "{case}");
        assert_eq!(
            (data_off - moved, expected.len() as u32, expected.len() as u16),
            (frame.mbuf.data_off, frame.mbuf.pkt_len, frame.descriptor().data_len),
            "{case}"
        );
    }
}

/// verifies that a remove moves the shorter side, closes the gap with the
/// bytes around it in order, and moves the parsed headers after it.
#[test]
fn test_remove_moves_the_shorter_side_and_the_headers_after_it() {
    for (case, network, at, len, front, offsets) in [
        ("front, a vlan tag", 18, 12, 4, true, (14, 30)),
        ("tail, behind the headers", 14, 44, 4, false, (14, 34)),
        ("tail, up to the end", 14, 42, 8, false, (14, 34)),
    ] {
        let numbered: Vec<u8> = (0..50).map(|idx| idx as u8).collect();
        let descriptor = bindings::packet {
            network_header: bindings::network_header {
                type_: 0x0806u16.to_be(),
                offset: network,
            },
            ..headers(0x0806, 0x100 | 17, 34)
        };
        let mut frame = Frame::new(descriptor, 0, &numbered, 0);
        let (before, data_off) = (frame.frame().to_vec(), frame.mbuf.data_off);

        assert_eq!(Ok(()), frame.packet().remove(at, len), "{case}");

        let expected = [&before[..at], &before[at + len..]].concat();
        let moved = if front { len as u16 } else { 0 };
        assert_eq!(expected, frame.frame(), "{case}");
        assert_eq!(offsets, frame.offsets(), "{case}");
        assert_eq!(
            (data_off + moved, expected.len() as u32, expected.len() as u16),
            (frame.mbuf.data_off, frame.mbuf.pkt_len, frame.descriptor().data_len),
            "{case}"
        );
    }
}

/// A resize of a packet, refused or not.
type Resize = fn(&mut Packet<'_>) -> Result<(), ResizeError>;

/// verifies that a resize memory safety rules out is refused and leaves the
/// packet as it was: a remove pulling the network header into the Ethernet
/// header, where the tunnel operations write before it, or starting at a
/// header that vouches for no bytes, and an insert one byte past the
/// headroom with no tailroom.
#[test]
fn test_resize_refuses_what_memory_safety_rules_out() {
    // Each case: the network header's ether type, the headroom and the
    // resize.
    let cases: [(&str, u16, u16, Resize); 5] = [
        ("the network header into the ethernet header", 0x0806, 16, |packet| {
            packet.remove(0, 13)
        }),
        ("a network header of ether type zero into it", 0, 16, |packet| {
            packet.remove(0, 13)
        }),
        ("a cut of the ethernet header", 0x0806, 16, |packet| packet.remove(2, 4)),
        (
            "a range starting at an unavailable transport header",
            0x0806,
            16,
            |packet| packet.remove(34, 4),
        ),
        ("one byte past the headroom", 0x0806, 4, |packet| {
            packet.insert(4, 5, |gap| gap.fill(0))
        }),
    ];
    for (case, network_type, headroom, resize) in cases {
        let mut frame = numbered_as(network_type, headroom, 42, 0);
        let (before, offsets) = (frame.frame().to_vec(), frame.offsets());

        assert_eq!(Err(ResizeError), resize(&mut frame.packet()), "{case}");
        assert_eq!((before, offsets), (frame.frame().to_vec(), frame.offsets()), "{case}");
    }
}

/// Sets the header checksum of the IPv4 header at the offset.
fn seal_ipv4(frame: &mut [u8], network: usize) {
    let header = (usize::from(frame[network] & 0xf)) * 4;
    frame[network + 10..network + 12].fill(0);
    let sum = yanet_packet::checksum::sum(&frame[network..network + header]);
    frame[network + 10..network + 12].copy_from_slice(&(!sum).to_be_bytes());
}

/// The upper-layer protocol of the IPv6 header at the offset and the offset
/// of its header, stepping over one Hop-by-Hop header.
fn ipv6_upper(frame: &[u8], network: usize) -> (u8, usize) {
    match frame[network + 6] {
        0 => (
            frame[network + 40],
            network + 40 + (usize::from(frame[network + 41]) + 1) * 8,
        ),
        protocol => (protocol, network + 40),
    }
}

/// Sets the TCP or UDP checksum of a segment from the offset to the end of
/// the frame, behind the IPv4 or IPv6 header at 14.
fn seal_transport(frame: &mut [u8], transport: usize) {
    let (ipv4, protocol) = match frame[12..14] {
        [0x08, 0x00] => (true, frame[14 + 9]),
        _ => (false, ipv6_upper(frame, 14).0),
    };
    let field = transport + if protocol == 6 { 16 } else { 6 };
    frame[field..field + 2].fill(0);
    let segment = &frame[transport..];
    let checksum = if ipv4 {
        let address = |at: usize| Ipv4Addr::from(<[u8; 4]>::try_from(&frame[at..at + 4]).expect("an address"));
        yanet_packet::checksum::l4_checksum_ipv4(address(26), address(30), protocol, segment)
    } else {
        let address = |at: usize| Ipv6Addr::from(<[u8; 16]>::try_from(&frame[at..at + 16]).expect("an address"));
        yanet_packet::checksum::l4_checksum_ipv6(address(22), address(38), protocol, segment)
    };
    let checksum = if checksum == 0 && protocol == 17 {
        0xffff
    } else {
        checksum
    };
    frame[field..field + 2].copy_from_slice(&checksum.to_be_bytes());
}

/// A TCP header with the options, padded to whole words by the caller,
/// before the payload.
fn tcp_with(options: &[u8], payload: &[u8]) -> Vec<u8> {
    let mut header = tcp(1000, 80);
    header[12] = (((20 + options.len()) / 4) << 4) as u8;
    header.extend_from_slice(options);
    header.extend_from_slice(payload);
    header
}

/// A UDP header before the payload, whose length counts it.
fn udp_with(payload: &[u8]) -> Vec<u8> {
    let mut header = udp(1000, 53);
    header[4..6].copy_from_slice(&(8 + payload.len() as u16).to_be_bytes());
    header.extend_from_slice(payload);
    header
}

/// A sealed Ethernet frame of an IPv4 datagram with the header options
/// around the transport segment, and trailer bytes after the datagram.
fn sealed_ipv4(options: &[u8], protocol: u8, fragment: u16, segment: &[u8], trailer: usize) -> Vec<u8> {
    let mut datagram = ipv4(protocol, fragment, segment);
    datagram[0] = 0x40 | ((20 + options.len()) / 4) as u8;
    let total = (20 + options.len() + segment.len()) as u16;
    datagram[2..4].copy_from_slice(&total.to_be_bytes());
    datagram.splice(20..20, options.iter().copied());
    let mut frame = ether(0x0800, &datagram);
    seal_ipv4(&mut frame, 14);
    if fragment == 0 {
        let transport = 14 + 20 + options.len();
        let end = frame.len();
        let mut sealed = frame[..end].to_vec();
        seal_transport(&mut sealed, transport);
        frame = sealed;
    }
    frame.extend(core::iter::repeat_n(0x77, trailer));
    frame
}

/// A sealed Ethernet frame of an IPv6 datagram of the transport segment.
fn sealed_ipv6(protocol: u8, segment: &[u8]) -> Vec<u8> {
    let mut frame = ether(0x86dd, &ipv6(protocol, segment));
    let transport = ipv6_upper(&frame, 14).1;
    seal_transport(&mut frame, transport);
    frame
}

/// Reports what of the frame's headers, at the parsed offsets, disagrees
/// with its bytes: the IPv4 header checksum, the IP length against the frame
/// without its trailer, the UDP length against the IP one less the surplus
/// area behind it, or the TCP or UDP checksum, over the UDP length for UDP,
/// unless told the transport checksum was not kept.
fn inconsistency(
    frame: &[u8],
    (network, transport): (u16, u16),
    trailer: usize,
    surplus: usize,
    transport_kept: bool,
) -> Option<&'static str> {
    let (network, transport) = (usize::from(network), usize::from(transport));
    let word = |at: usize| usize::from(u16::from_be_bytes([frame[at], frame[at + 1]]));
    let (ipv4, network_end, protocol) = match frame[network] >> 4 {
        4 => {
            let header = usize::from(frame[network] & 0xf) * 4;
            if yanet_packet::checksum::sum(&frame[network..network + header]) != 0xffff {
                return Some("ipv4 header checksum");
            }
            (true, network + word(network + 2), frame[network + 9])
        }
        _ => (false, network + 40 + word(network + 4), ipv6_upper(frame, network).0),
    };
    if network_end + trailer != frame.len() {
        return Some("ip length");
    }
    if protocol == 17 && word(transport + 4) + surplus != network_end - transport {
        return Some("udp length");
    }
    let covered_end = if protocol == 17 {
        network_end - surplus
    } else {
        network_end
    };
    let segment = &frame[transport..covered_end];
    let valid = if ipv4 {
        let address = |at: usize| Ipv4Addr::from(<[u8; 4]>::try_from(&frame[at..at + 4]).expect("an address"));
        yanet_packet::checksum::l4_is_valid_ipv4(address(network + 12), address(network + 16), protocol, segment)
    } else {
        let address = |at: usize| Ipv6Addr::from(<[u8; 16]>::try_from(&frame[at..at + 16]).expect("an address"));
        yanet_packet::checksum::l4_is_valid_ipv6(address(network + 8), address(network + 24), protocol, segment)
    };
    (transport_kept && !valid).then_some("transport checksum")
}

/// An edit of a frame: an insert of payload or TCP options, filled with
/// bytes counting up from one, or a remove.
#[derive(Clone, Copy, Debug)]
enum Op {
    Insert(usize, usize),
    InsertOptions(usize, usize),
    Remove(usize, usize),
}

/// The descriptor the parser would write for an IPv4 or IPv6 frame built by
/// the sealing helpers, built here so that Miri, which runs no C, runs the
/// edits: the network header at 14, the transport header behind it, and
/// the fragment flag of an IPv4 fragment.
fn described(frame: &[u8]) -> bindings::packet {
    let (network_type, transport_offset, protocol, fragment) = match frame[12..14] {
        [0x08, 0x00] => {
            let fragment = u16::from_be_bytes([frame[20], frame[21]]) & 0x3fff != 0;
            (0x0800, 14 + usize::from(frame[14] & 0xf) * 4, frame[23], fragment)
        }
        _ => {
            let (protocol, transport) = ipv6_upper(frame, 14);
            (0x86dd, transport, protocol, false)
        }
    };
    let flags = if fragment {
        1 << bindings::packet_flag_PACKET_FLAG_FRAGMENTED
    } else {
        0
    };
    bindings::packet {
        flags,
        ..headers(network_type, u16::from(protocol), transport_offset as u16)
    }
}

/// Fills bytes counting up from one.
fn count_up(gap: &mut [u8]) {
    for (idx, byte) in gap.iter_mut().enumerate() {
        *byte = idx as u8 + 1;
    }
}

/// Applies the edit to a described copy of the frame between 16 bytes of
/// headroom and of tailroom, and returns the frame and its parsed network
/// and transport offsets.
fn edited(bytes: &[u8], op: Op) -> (Vec<u8>, (u16, u16)) {
    let mut frame = Frame::new(described(bytes), 16, bytes, 16);
    let mut packet = frame.packet();
    let result = match op {
        Op::Insert(at, len) => packet.insert(at, len, count_up),
        Op::InsertOptions(at, len) => packet.insert_tcp_options(at, len, count_up),
        Op::Remove(at, len) => packet.remove(at, len),
    };
    result.unwrap_or_else(|_| panic!("{op:?} refused"));
    (frame.frame().to_vec(), frame.offsets())
}

/// verifies that an edit inside a parsed datagram leaves every length and
/// checksum its headers carry agreeing with its bytes, after even and odd
/// shifts alike, and grows or shrinks the header lengths with the options
/// it edits.
#[test]
fn test_edit_keeps_lengths_and_checksums_consistent() {
    let payload: Vec<u8> = (0..24).map(|idx| (idx * 7) as u8).collect();
    let udp4 = sealed_ipv4(&[], 17, 0, &udp_with(&payload), 0);
    let udp4_trailer = sealed_ipv4(&[], 17, 0, &udp_with(&payload), 6);
    let options = [2, 4, 0x05, 0xb4, 1, 1, 4, 2];
    let tcp4 = sealed_ipv4(&[], 6, 0, &tcp_with(&options, &payload), 0);
    let ip_options = sealed_ipv4(&[1, 1, 1, 0], 6, 0, &tcp_with(&[], &payload), 0);
    let tcp6 = sealed_ipv6(6, &tcp_with(&options, &payload));
    // A Hop-by-Hop header of eight bytes, PadN filling it, before TCP.
    let hop_by_hop = [[6, 0, 1, 4, 0, 0, 0, 0].as_slice(), &tcp_with(&[], &payload)].concat();
    let tcp6_extended = sealed_ipv6(0, &hop_by_hop);
    // Each case: the frame, the edit, the trailer bytes left after it, and
    // the TCP data offset or IPv4 header length byte expected after it at
    // its new offset.
    for (case, bytes, op, trailer, header_byte) in [
        ("udp payload, even at even", &udp4, Op::Insert(46, 4), 0, None),
        ("udp payload, even at odd", &udp4, Op::Insert(47, 6), 0, None),
        ("udp payload, odd", &udp4, Op::Insert(46, 3), 0, None),
        ("udp payload at the end", &udp4, Op::Insert(66, 2), 0, None),
        ("before the network header", &udp4, Op::Insert(12, 4), 0, None),
        ("between the ip and udp headers", &udp4, Op::Insert(34, 4), 0, None),
        ("udp payload removed, even", &udp4, Op::Remove(44, 4), 0, None),
        ("udp payload removed, odd at odd", &udp4, Op::Remove(45, 3), 0, None),
        (
            "trailer removed, datagram kept",
            &udp4_trailer,
            Op::Remove(68, 4),
            2,
            None,
        ),
        (
            "datagram end and trailer removed",
            &udp4_trailer,
            Op::Remove(64, 4),
            4,
            None,
        ),
        (
            "tcp options at their start",
            &tcp4,
            Op::InsertOptions(54, 4),
            0,
            Some((46, 0x80)),
        ),
        (
            "tcp options at their end",
            &tcp4,
            Op::InsertOptions(62, 8),
            0,
            Some((46, 0x90)),
        ),
        (
            "tcp payload at the options' end",
            &tcp4,
            Op::Insert(62, 6),
            0,
            Some((46, 0x70)),
        ),
        ("tcp options removed", &tcp4, Op::Remove(54, 4), 0, Some((46, 0x60))),
        (
            "ipv4 options removed",
            &ip_options,
            Op::Remove(34, 4),
            0,
            Some((14, 0x45)),
        ),
        ("ipv6 tcp payload", &tcp6, Op::Insert(90, 5), 0, Some((66, 0x70))),
        (
            "ipv6 tcp payload removed",
            &tcp6,
            Op::Remove(83, 7),
            0,
            Some((66, 0x70)),
        ),
        ("ipv6 tcp options", &tcp6, Op::InsertOptions(82, 4), 0, Some((66, 0x80))),
        (
            "ipv6 hop-by-hop, tcp payload, odd",
            &tcp6_extended,
            Op::Insert(85, 3),
            0,
            None,
        ),
        (
            "ipv6 hop-by-hop, tcp payload removed",
            &tcp6_extended,
            Op::Remove(84, 4),
            0,
            None,
        ),
        (
            "ipv6 hop-by-hop, a header linked before tcp",
            &tcp6_extended,
            Op::Insert(62, 8),
            0,
            None,
        ),
    ] {
        let (frame, offsets) = edited(bytes, op);

        assert_eq!(None, inconsistency(&frame, offsets, trailer, 0, true), "{case}");
        if let Some((at, byte)) = header_byte {
            assert_eq!(byte, frame[at], "{case}");
        }
    }
}

/// verifies that a fragment or a UDP datagram without a checksum has its
/// lengths kept by an edit and its transport checksum left as it was.
#[test]
fn test_edit_leaves_a_transport_checksum_it_cannot_keep() {
    let payload = [0x11; 16];
    let mut unchecked = sealed_ipv4(&[], 17, 0, &udp_with(&payload), 0);
    unchecked[40..42].fill(0);
    let fragment = sealed_ipv4(&[], 6, 0x2000, &tcp_with(&[], &payload), 0);
    for (case, bytes, checksum_at) in [
        ("udp without a checksum", &unchecked, 40),
        ("first fragment", &fragment, 50),
    ] {
        let (frame, offsets) = edited(bytes, Op::Insert(bytes.len() - 4, 4));

        assert_eq!(None, inconsistency(&frame, offsets, 0, 0, false), "{case}");
        assert_eq!(
            bytes[checksum_at..checksum_at + 2],
            frame[checksum_at..checksum_at + 2],
            "{case}"
        );
    }
}

/// verifies that an edit keeps a damaged transport checksum damaged, after
/// odd shifts too.
#[test]
fn test_edit_keeps_a_damaged_transport_checksum_damaged() {
    let payload: Vec<u8> = (0..24).map(|idx| (idx * 5) as u8).collect();
    let mut damaged = sealed_ipv4(&[], 17, 0, &udp_with(&payload), 0);
    // A payload byte changed after the checksum was sealed.
    damaged[60] ^= 0x10;
    for (case, op) in [
        ("odd insert", Op::Insert(46, 3)),
        ("even insert", Op::Insert(46, 4)),
        ("odd remove", Op::Remove(45, 3)),
    ] {
        let (frame, offsets) = edited(&damaged, op);

        assert_eq!(
            Some("transport checksum"),
            inconsistency(&frame, offsets, 0, 0, true),
            "{case}"
        );
    }
}

/// A sealed Ethernet frame of an IPv4 UDP datagram whose IP payload runs
/// `surplus` bytes past the UDP length, the checksum over the UDP length.
fn sealed_udp4_with_surplus(payload: &[u8], surplus: usize) -> Vec<u8> {
    let mut segment = udp_with(payload);
    segment.extend(core::iter::repeat_n(0x44, surplus));
    let mut frame = ether(0x0800, &ipv4(17, 0, &segment));
    seal_ipv4(&mut frame, 14);
    let mut covered = frame[..34 + 8 + payload.len()].to_vec();
    seal_transport(&mut covered, 34);
    frame[40..42].copy_from_slice(&covered[40..42]);
    frame
}

/// verifies that an edit of a UDP datagram with a surplus area behind its
/// UDP length keeps the UDP checksum over the UDP length, and that an edit
/// of the surplus area changes the IP length alone.
#[test]
fn test_edit_keeps_a_udp_checksum_up_to_the_udp_length() {
    let payload: Vec<u8> = (0..24).map(|idx| (idx * 11) as u8).collect();
    let bytes = sealed_udp4_with_surplus(&payload, 6);
    // Each case: the edit and the surplus bytes left after it.
    for (case, op, surplus) in [
        ("udp payload insert, even", Op::Insert(46, 4), 6),
        ("udp payload insert, odd", Op::Insert(47, 3), 6),
        ("udp payload remove, odd", Op::Remove(45, 5), 6),
        ("surplus insert", Op::Insert(68, 4), 10),
        ("surplus remove", Op::Remove(67, 3), 3),
    ] {
        let (frame, offsets) = edited(&bytes, op);

        assert_eq!(None, inconsistency(&frame, offsets, 0, surplus, true), "{case}");
    }
}

/// verifies that an edit whose transport sum comes out all ones stores the
/// checksum as all ones, for TCP as the header mutations do and for UDP,
/// where zero would mean no checksum.
#[test]
fn test_edit_stores_an_all_ones_checksum_as_all_ones() {
    let payload = [0x21; 16];
    // Each case: the frame, the payload offset, the checksum offset, and how
    // much the edit adds to the lengths the checksum covers: the pseudo
    // header's, and for UDP the UDP length too.
    let cases = [
        ("tcp", sealed_ipv4(&[], 6, 0, &tcp_with(&[], &payload), 0), 54, 50, 2),
        ("udp", sealed_ipv4(&[], 17, 0, &udp_with(&payload), 0), 42, 40, 4),
    ];
    for (case, bytes, at, checksum, grown) in cases {
        // The old sum, the lengths' growth, and the inserted word that makes
        // the whole all ones.
        let mut sum = u32::from(!u16::from_be_bytes([bytes[checksum], bytes[checksum + 1]])) + grown;
        while sum >> 16 != 0 {
            sum = (sum & 0xffff) + (sum >> 16);
        }
        let word = !(sum as u16);
        let mut frame = Frame::new(described(&bytes), 16, &bytes, 16);

        let inserted = frame
            .packet()
            .insert(at, 2, |gap| gap.copy_from_slice(&word.to_be_bytes()));

        assert_eq!(Ok(()), inserted, "{case}");
        assert_eq!([0xff, 0xff], frame.frame()[checksum..checksum + 2], "{case}");
        assert_eq!(
            None,
            inconsistency(frame.frame(), frame.offsets(), 0, 0, true),
            "{case}"
        );
    }
}

/// verifies that an edit a header would not count, or one past the frame,
/// a parsed header or a length field, is refused and leaves the packet as
/// it was.
#[test]
fn test_resize_refusal_leaves_the_packet_unchanged() {
    let payload = [0x22; 16];
    let udp4 = sealed_ipv4(&[], 17, 0, &udp_with(&payload), 0);
    let tcp6 = sealed_ipv6(6, &tcp_with(&[], &payload));
    let tcp4 = sealed_ipv4(&[], 6, 0, &tcp_with(&[1, 1, 1, 1, 1, 1, 1, 1], &payload), 0);
    let ip_options = sealed_ipv4(&[1, 1, 1, 0], 6, 0, &tcp_with(&[], &payload), 0);
    let mut too_long = udp4.clone();
    too_long[16..18].copy_from_slice(&65530u16.to_be_bytes());
    seal_ipv4(&mut too_long, 14);
    let full_options = sealed_ipv4(&[], 6, 0, &tcp_with(&[1; 40], &payload), 0);
    // A data offset of 48 bytes over a 20-byte segment.
    let mut overlong_header = sealed_ipv4(&[], 6, 0, &tcp_with(&[], &[]), 0);
    overlong_header[46] = 0xc0;
    // A data offset of zero words over a 20-byte header.
    let mut underlong_header = sealed_ipv4(&[], 6, 0, &tcp_with(&[], &payload), 0);
    underlong_header[46] = 0x00;
    for (case, bytes, op) in [
        ("an insert past the frame", &udp4, Op::Insert(udp4.len() + 1, 1)),
        ("an insert inside the ipv4 header", &udp4, Op::Insert(20, 4)),
        ("an insert inside the udp header", &udp4, Op::Insert(38, 2)),
        ("an insert inside the ipv6 header", &tcp6, Op::Insert(30, 4)),
        ("an insert beyond both rooms", &udp4, Op::Insert(40, 40)),
        ("payload inside the tcp options", &tcp4, Op::Insert(58, 4)),
        ("payload inside the ipv4 options", &ip_options, Op::Insert(36, 4)),
        ("tcp options not of whole words", &tcp4, Op::InsertOptions(54, 2)),
        ("tcp options in the payload", &tcp4, Op::InsertOptions(66, 4)),
        ("tcp options beyond 60 bytes", &full_options, Op::InsertOptions(54, 4)),
        (
            "tcp options of a header past its datagram",
            &overlong_header,
            Op::InsertOptions(54, 4),
        ),
        (
            "tcp options of a header under five words",
            &underlong_header,
            Op::InsertOptions(54, 4),
        ),
        ("tcp options into udp", &udp4, Op::InsertOptions(42, 4)),
        ("an ip length overflowing", &too_long, Op::Insert(50, 8)),
        ("a remove past the frame", &udp4, Op::Remove(udp4.len() - 2, 3)),
        ("a remove of an ipv4 header byte", &udp4, Op::Remove(20, 2)),
        ("a remove of a udp header byte", &udp4, Op::Remove(40, 2)),
        ("a remove of options and payload", &tcp4, Op::Remove(58, 8)),
        ("a remove of options not of whole words", &tcp4, Op::Remove(54, 2)),
        (
            "a remove of ipv4 options not of whole words",
            &ip_options,
            Op::Remove(34, 2),
        ),
    ] {
        let mut frame = Frame::new(described(bytes), 16, bytes, 16);
        let offsets = frame.offsets();
        let mut packet = frame.packet();

        let refused = match op {
            Op::Insert(at, len) => packet.insert(at, len, count_up),
            Op::InsertOptions(at, len) => packet.insert_tcp_options(at, len, count_up),
            Op::Remove(at, len) => packet.remove(at, len),
        };

        assert_eq!(Err(ResizeError), refused, "{case}");
        assert_eq!((bytes.as_slice(), offsets), (frame.frame(), frame.offsets()), "{case}");
    }
}

/// verifies that a chained packet is refused an insert and a remove, as
/// the bytes after the offset may lie in another segment.
#[test]
fn test_insert_and_remove_refuse_a_chained_packet() {
    let (mut head_buffer, mut tail_buffer) = ([0u8; 512], [0u8; 512]);
    let mut tail = mbuf(&mut tail_buffer, 256, 60);
    let mut head = bindings::rte_mbuf {
        next: &raw mut tail,
        ..mbuf(&mut head_buffer, 256, 60)
    };
    let mut descriptor = bindings::packet {
        mbuf: &raw mut head,
        ..Default::default()
    };
    let mut packet = Packet {
        raw: NonNull::from(&mut descriptor),
        _brand: PhantomData,
        _local: PhantomData,
    };

    assert_eq!(
        (Some(ResizeError), Err(ResizeError)),
        (packet.insert(10, 4, |_| ()).err(), packet.remove(10, 4))
    );
    assert_eq!((256, 60), (head.data_off, head.data_len));
}

#[test]
fn test_data_mut_writes_the_frame() {
    let mut frame = ipv4_udp(0);

    frame.packet().data_mut()[0] = 0xee;

    assert_eq!(0xee, frame.frame()[0]);
}

/// verifies that a chained frame reads segment by segment in chain order,
/// each segment from its own data offset and length, empty ones included,
/// and that its length is the head's packet length, for a taken packet and a
/// peeked one alike.
#[test]
fn test_segments_read_a_chained_frame_in_order() {
    let (mut first, mut second, mut third, mut fourth) = ([1u8; 64], [2u8; 64], [3u8; 64], [4u8; 64]);
    let mut fourth = mbuf(&mut fourth, 0, 3);
    let mut third = bindings::rte_mbuf {
        next: &raw mut fourth,
        ..mbuf(&mut third, 32, 0)
    };
    let mut second = bindings::rte_mbuf {
        next: &raw mut third,
        ..mbuf(&mut second, 8, 5)
    };
    let mut head = bindings::rte_mbuf {
        next: &raw mut second,
        pkt_len: 14,
        ..mbuf(&mut first, 16, 6)
    };
    let mut descriptor = bindings::packet {
        mbuf: &raw mut head,
        data_len: 6,
        ..Default::default()
    };
    let mut front = bindings::packet_front::default();
    // SAFETY: the front is this test's and the packet is linked and on no
    // list.
    unsafe { push(&raw mut front.input, &raw mut descriptor) };
    // SAFETY: the front is this test's; the batch reads only its front, and
    // the context and worker it is given are never read.
    let mut batch = unsafe { Batch::new(NonNull::from(&mut front), NonNull::dangling(), NonNull::dangling()) };
    let expected = (vec![vec![1; 6], vec![2; 5], vec![], vec![4; 3]], 14);

    let peeked = batch
        .peek()
        .map(|view| {
            (
                view.segments().map(<[u8]>::to_vec).collect::<Vec<_>>(),
                view.frame_len(),
            )
        })
        .collect::<Vec<_>>();
    let packet = batch.next().expect("the input packet");

    assert_eq!(vec![expected.clone()], peeked);
    assert_eq!(
        expected,
        (packet.segments().map(<[u8]>::to_vec).collect(), packet.frame_len())
    );
}

/// verifies that an unchained frame is one segment, its first, and its
/// length that segment's.
#[test]
fn test_segments_of_an_unchained_frame_are_its_data() {
    let mut frame = ipv4_udp(4);
    let packet = frame.packet();

    assert_eq!(vec![packet.data()], packet.segments().collect::<Vec<_>>());
    assert_eq!(42, packet.frame_len());
}

/// verifies that the header types and offsets a packet reports are the
/// parser's, the network type in host order, and the frame has the length
/// of the first segment.
#[test]
fn test_packet_reports_the_parsers_headers() {
    let mut frame = ipv4_udp(0);
    let packet = frame.packet();

    assert_eq!(
        (0x0800, 14, 34, 42),
        (
            packet.network_type(),
            packet.network_offset(),
            packet.transport_offset(),
            packet.data().len()
        )
    );
}

/// verifies that the declared protocol is the low byte of the transport
/// tag and that the unavailable mark, bit 8, is not part of it, for a taken
/// packet and a peeked one alike.
#[test]
fn test_transport_protocol_masks_the_unavailable_mark() {
    let mark = bindings::PACKET_TRANSPORT_HEADER_UNAVAILABLE as u16;
    for (tag, protocol, available) in [
        (mark | 17, 17, false),
        (17, 17, true),
        (mark | 6, 6, false),
        (6, 6, true),
    ] {
        let mut frame = Frame::new(headers(0x0800, tag, 34), 0, &[0; 60], 0);
        let mut front = bindings::packet_front::default();
        // SAFETY: the front is this test's and the packet is linked and on
        // no list.
        unsafe { push(&raw mut front.input, frame.link()) };
        // SAFETY: the front is this test's; the batch reads only its front,
        // and the context and worker it is given are never read.
        let mut batch = unsafe { Batch::new(NonNull::from(&mut front), NonNull::dangling(), NonNull::dangling()) };

        let peeked = batch
            .peek()
            .map(|view| (view.transport_protocol(), view.transport_header_available()))
            .collect::<Vec<_>>();
        let packet = batch.next().expect("the input packet");

        assert_eq!(vec![(protocol, available)], peeked, "tag {tag:#x}");
        assert_eq!(
            (protocol, available),
            (packet.transport_protocol(), packet.transport_header_available()),
            "tag {tag:#x}"
        );
    }
}

/// verifies that the fragment offset is the parser's, zero for a packet that
/// is not a fragment after the first.
#[test]
fn test_fragment_offset_reports_the_parsers_offset() {
    for offset in [0, 1, 0x1fff] {
        let descriptor = bindings::packet {
            fragment_offset: offset,
            ..headers(0x0800, 17, 34)
        };
        let mut frame = Frame::new(descriptor, 0, &[0; 60], 0);

        assert_eq!(offset, frame.packet().fragment_offset());
    }
}

/// verifies that a taken and a peeked packet report the parser's hash and
/// that only the parser's fragment mark among the flags makes a fragment.
#[test]
fn test_hash_and_fragment_flag_report_the_parsers() {
    let fragment = 1 << bindings::packet_flag_PACKET_FLAG_FRAGMENTED;
    let sync = 1 << bindings::packet_flag_PACKET_FLAG_FWSTATE_SYNC_INTERNAL;
    for (flags, is_fragment) in [(0, false), (fragment, true), (sync, false), (fragment | sync, true)] {
        let descriptor = bindings::packet {
            hash: 0xdead_beef,
            flags,
            ..headers(0x0800, 17, 34)
        };
        let mut frame = Frame::new(descriptor, 0, &[0; 60], 0);
        let mut front = bindings::packet_front::default();
        // SAFETY: the front is this test's and the packet is linked and on
        // no list.
        unsafe { push(&raw mut front.input, frame.link()) };
        // SAFETY: the front is this test's; the batch reads only its front,
        // and the context and worker it is given are never read.
        let mut batch = unsafe { Batch::new(NonNull::from(&mut front), NonNull::dangling(), NonNull::dangling()) };

        let peeked = batch
            .peek()
            .map(|view| (view.hash(), view.is_fragment()))
            .collect::<Vec<_>>();
        let packet = batch.next().expect("the input packet");

        assert_eq!(vec![(0xdead_beef, is_fragment)], peeked, "flags {flags:#x}");
        assert_eq!(
            (0xdead_beef, is_fragment),
            (packet.hash(), packet.is_fragment()),
            "flags {flags:#x}"
        );
    }
}

/// verifies that parsing a frame built in a packet with no parse yet
/// reports the C parser's headers and its hash of the addresses and ports.
#[cfg(not(miri))]
#[test]
fn test_parse_reports_the_headers_and_hash_of_a_built_frame() {
    let ipv4_hash = |ports: [u16; 2]| {
        let hash = crc32c(&IPV4_ADDRESSES, 0);
        crc32c(&[ports[0].to_be_bytes(), ports[1].to_be_bytes()].concat(), hash)
    };
    let ipv6_hash = crc32c(
        &[53u16.to_be_bytes(), 5353u16.to_be_bytes()].concat(),
        crc32c(&IPV6_ADDRESSES, 0),
    );
    for (name, frame, expected) in [
        (
            "IPv4 TCP",
            ether(0x0800, &ipv4(6, 0, &tcp(1000, 443))),
            Parsed {
                network: (0x0800, 14),
                transport: (6, 34),
                hash: ipv4_hash([1000, 443]),
                ..Parsed::none()
            },
        ),
        (
            "IPv6 UDP",
            ether(0x86dd, &ipv6(17, &udp(53, 5353))),
            Parsed {
                network: (0x86dd, 14),
                transport: (17, 54),
                hash: ipv6_hash,
                ..Parsed::none()
            },
        ),
        (
            "VLAN-tagged IPv4 UDP",
            ether(
                0x8100,
                &[&[0x21, 0x23, 0x08, 0x00][..], &ipv4(17, 0, &udp(4789, 4790))].concat(),
            ),
            Parsed {
                network: (0x0800, 18),
                transport: (17, 38),
                vlan: 0x123,
                hash: ipv4_hash([4789, 4790]),
                ..Parsed::none()
            },
        ),
    ] {
        let mut frame = Frame::new(Default::default(), 0, &frame, 0);

        assert_eq!(Ok(()), frame.packet().parse(), "{name}");
        assert_eq!(expected, Parsed::of(&mut frame), "{name}");
    }
}

/// verifies that the parser's fragment flag tells a first fragment, whose
/// offset is zero, from an unfragmented packet, and marks a fragment after
/// the first, whose transport header is unavailable, for both families.
#[cfg(not(miri))]
#[test]
fn test_parse_marks_first_and_later_fragments() {
    let udp_header = udp(53, 53);
    let v4 = |fragment| ether(0x0800, &ipv4(17, fragment, &udp_header));
    let v6 = |offset, more| ether(0x86dd, &ipv6(44, &ipv6_fragment(17, offset, more, &udp_header)));
    // Expected: the fragment flag, the fragment offset as the parser scales
    // it and the availability of the transport header.
    for (name, frame, expected) in [
        ("unfragmented IPv4", v4(0), (false, 0, true)),
        ("first IPv4 fragment", v4(0x2000), (true, 0, true)),
        ("later IPv4 fragment", v4(2), (true, 2, false)),
        ("first IPv6 fragment", v6(0, true), (true, 0, true)),
        ("later IPv6 fragment", v6(2, false), (true, 16, false)),
    ] {
        let mut frame = Frame::new(Default::default(), 0, &frame, 0);
        assert_eq!(Ok(()), frame.packet().parse(), "{name}");

        let packet = frame.packet();
        let actual = (
            packet.is_fragment(),
            packet.fragment_offset(),
            packet.transport_header_available(),
        );
        assert_eq!(expected, actual, "{name}");
    }
}

/// verifies that a parse keeps nothing an earlier parse left: a frame that
/// is neither IPv4 nor IPv6 and carries no VLAN tag leaves its transport
/// header unparsed and no VLAN id, hash or fragment fields.
#[cfg(not(miri))]
#[test]
fn test_parse_clears_what_an_earlier_parse_left() {
    let mut frame = Frame::new(stale(), 0, &ether(0x0806, &[0; 28]), 0);

    assert_eq!(Ok(()), frame.packet().parse());
    assert_eq!(
        Parsed {
            network: (0x0806, 14),
            ..Parsed::none()
        },
        Parsed::of(&mut frame)
    );
}

/// verifies that a frame the parser refuses, including one cut short in a
/// header it records before checking its length, leaves the packet
/// unparsed and the frame as it was.
#[cfg(not(miri))]
#[test]
fn test_parse_refusal_leaves_the_packet_unparsed() {
    let ipv4_tcp = ipv4(6, 0, &tcp(1000, 443));
    for (name, frame) in [
        ("frame shorter than an Ethernet header", vec![0; 10]),
        ("IPv4 header cut short", ether(0x0800, &ipv4_tcp[..12])),
        ("TCP header cut short", ether(0x0800, &ipv4(6, 0, &[0; 10]))),
        ("IPv6 payload beyond the frame", ether(0x86dd, &ipv6(17, &[0; 8])[..44])),
    ] {
        let mut built = Frame::new(stale(), 0, &frame, 0);

        assert_eq!(Err(ParseError), built.packet().parse(), "{name}");
        assert_eq!(Parsed::none(), Parsed::of(&mut built), "{name}");
        assert_eq!(&frame[..], built.frame(), "{name}");
    }
}

/// verifies that a parse refuses a chained packet, whose headers the parser
/// would read in the first segment alone, and leaves it as it was.
#[test]
fn test_parse_refuses_a_chained_packet() {
    let (mut head_buffer, mut tail_buffer) = ([0u8; 512], [0u8; 512]);
    let mut tail = mbuf(&mut tail_buffer, 256, 60);
    let mut head = bindings::rte_mbuf {
        next: &raw mut tail,
        ..mbuf(&mut head_buffer, 256, 60)
    };
    let mut descriptor = bindings::packet { mbuf: &raw mut head, ..stale() };
    let mut packet = Packet {
        raw: NonNull::from(&mut descriptor),
        _brand: PhantomData,
        _local: PhantomData,
    };

    assert_eq!(Err(ParseError), packet.parse());
    assert_eq!(
        (0x5eed, 7, 34),
        (descriptor.hash, descriptor.vlan, descriptor.transport_header.offset)
    );
}

/// verifies that peeking reads every input packet in order without taking
/// one, so the packets taken afterwards are the whole input.
#[test]
fn test_peek_reads_without_taking() {
    let mut frames: Vec<_> = (0..3)
        .map(|idx| Frame::new(Default::default(), 0, &[idx; 60], 0))
        .collect();
    let mut front = bindings::packet_front::default();
    for frame in &mut frames {
        // SAFETY: the front is this test's and the packet is linked and on
        // no list.
        unsafe { push(&raw mut front.input, frame.link()) };
    }
    // SAFETY: the front is this test's; the batch reads only its front,
    // and the context and worker it is given are never read.
    let mut batch = unsafe { Batch::new(NonNull::from(&mut front), NonNull::dangling(), NonNull::dangling()) };

    assert_eq!(
        vec![0, 1, 2],
        batch.peek().map(|view| view.data()[0]).collect::<Vec<_>>()
    );

    let mut taken = Vec::new();
    while let Some(packet) = batch.next() {
        taken.push(packet.data()[0]);
    }
    assert_eq!(vec![0, 1, 2], taken);
}

/// verifies that every encapsulation refuses a packet whose network
/// header the parser did not find, leaving the frame untouched.
#[test]
fn test_encap_refuses_packet_without_network_header() {
    let mut frame = Frame::new(Default::default(), 64, &[0; 42], 0);

    assert_eq!(
        Err(TunnelError),
        frame.packet().encap_ipv4([0; 4], [0; 4], DscpMark::Never)
    );
    assert_eq!(Err(TunnelError), frame.packet().encap_mpls(0, 0, true, 9));

    assert_eq!(&[0; 42], frame.frame());
}

/// verifies that an MPLS encapsulation bounds the traffic class to its three
/// bits and keeps an unparsed transport header unparsed.
#[cfg(not(miri))]
#[test]
fn test_encap_mpls_masks_traffic_class_and_keeps_transport_unparsed() {
    let mut frame = Frame::new(headers(0x0806, 0, 0), 64, &[0; 42], 0);

    assert_eq!(Ok(()), frame.packet().encap_mpls(1, 0xff, true, 9));

    let entry = u32::from_be_bytes(frame.frame()[14..18].try_into().unwrap());
    assert_eq!(1 << 12 | 7 << 9 | 1 << 8 | 9, entry);
    assert_eq!((14, 0), frame.offsets());
}

/// Writes the options over those of a described copy of the frame, returning
/// the frame and its parsed network and transport offsets.
///
/// The copy has 16 bytes of headroom and of tailroom.
fn rewritten(bytes: &[u8], options: yanet_packet::tcp_options::TcpOptions) -> (Vec<u8>, (u16, u16)) {
    let mut frame = Frame::new(described(bytes), 16, bytes, 16);
    assert_eq!(Ok(()), frame.packet().write_tcp_options(&options));
    (frame.frame().to_vec(), frame.offsets())
}

/// verifies that rewriting the TCP options to any length keeps every length,
/// checksum and the data offset agreeing with the bytes written.
#[test]
fn test_rewrite_tcp_options_keeps_lengths_and_checksums_consistent() {
    use yanet_packet::tcp_options::{Sack, SackBlock, TcpOptions, Timestamps};

    let payload: Vec<u8> = (0..24).map(|idx| (idx * 5) as u8).collect();
    let tcp4 = sealed_ipv4(&[], 6, 0, &tcp_with(&[2, 4, 0x05, 0xb4, 1, 1, 4, 2], &payload), 0);
    let tcp6 = sealed_ipv6(6, &tcp_with(&[2, 4, 0x05, 0xb4], &payload));
    let hop_by_hop = [6, 0, 1, 4, 0, 0, 0, 0];
    let extended6 = sealed_ipv6(0, &[&hop_by_hop[..], &tcp_with(&[2, 4, 0x05, 0xb4], &payload)].concat());
    let mut blocks = Sack::default();
    blocks.push(SackBlock { start: 100, end: 200 });
    let sack = TcpOptions::default()
        .with_timestamps(Some(Timestamps { value: 7, echo: 9 }))
        .with_sack(blocks);
    let same = TcpOptions::default().with_mss(Some(1400)).with_sack_permitted(true);
    let shorter = TcpOptions::default().with_mss(Some(1200));
    // Each case: the frame, the options, and the data offset expected after.
    for (case, bytes, options, words) in [
        ("ipv4, more options", &tcp4, sack, 5 + 5),
        ("ipv4, as many options", &tcp4, same, 5 + 2),
        ("ipv4, fewer options", &tcp4, shorter, 5 + 1),
        ("ipv4, no options", &tcp4, TcpOptions::default(), 5),
        ("ipv6, more options", &tcp6, sack, 5 + 5),
        ("ipv6 behind a hop-by-hop header", &extended6, sack, 5 + 5),
    ] {
        let (frame, offsets) = rewritten(bytes, options);

        let transport = usize::from(offsets.1);
        let header = usize::from(frame[transport + 12] >> 4) * 4;
        assert_eq!(None, inconsistency(&frame, offsets, 0, 0, true), "{case}");
        assert_eq!(words, frame[transport + 12] >> 4, "{case}");
        assert_eq!(
            Ok(options),
            TcpOptions::parse(&frame[transport + 20..transport + header]),
            "{case}"
        );
    }
}

/// verifies that rewriting the options of a segment whose TCP checksum
/// arrived damaged keeps it damaged.
#[test]
fn test_rewrite_tcp_options_keeps_a_damaged_checksum_damaged() {
    let payload = [0x31; 16];
    let mut damaged = sealed_ipv4(&[], 6, 0, &tcp_with(&[1, 1, 1, 1], &payload), 0);
    damaged[60] ^= 0x10;
    let options = yanet_packet::tcp_options::TcpOptions::default()
        .with_mss(Some(1460))
        .with_window_scale(Some(7));

    let (frame, offsets) = rewritten(&damaged, options);

    assert_eq!(Some("transport checksum"), inconsistency(&frame, offsets, 0, 0, true));
}

/// verifies that a rewrite the TCP header cannot take is refused and leaves
/// the packet as it was.
#[test]
fn test_rewrite_tcp_options_refusal_leaves_the_packet_unchanged() {
    let payload = [0x22; 16];
    let tcp4 = sealed_ipv4(&[], 6, 0, &tcp_with(&[1, 1, 1, 1], &payload), 0);
    let udp4 = sealed_ipv4(&[], 17, 0, &udp_with(&payload), 0);
    let mut underlong = tcp4.clone();
    underlong[46] = 0x00;
    let fragment = sealed_ipv4(&[], 6, 0x2000, &tcp_with(&[1, 1, 1, 1], &payload), 0);
    // The IP header grown after the parse to reach into the TCP header.
    let mut overlapping = tcp4.clone();
    overlapping[14] = 0x46;
    for (case, parsed, bytes, len) in [
        ("options not of whole words", &tcp4, &tcp4, 6),
        ("options beyond 40 bytes", &tcp4, &tcp4, 44),
        ("a udp datagram", &udp4, &udp4, 4),
        ("a data offset below five words", &underlong, &underlong, 4),
        ("a first fragment", &fragment, &fragment, 4),
        ("an ip header reaching into tcp", &tcp4, &overlapping, 4),
    ] {
        let mut frame = Frame::new(described(parsed), 16, bytes, 16);
        let offsets = frame.offsets();

        let refused = frame.packet().rewrite_tcp_options(len, |area| area.fill(1));

        assert_eq!(Err(ResizeError), refused, "{case}");
        assert_eq!((bytes.as_slice(), offsets), (frame.frame(), frame.offsets()), "{case}");
    }
}
