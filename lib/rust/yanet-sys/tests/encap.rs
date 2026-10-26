//! Tunnel operations of a packet, the C encapsulation and decapsulation
//! routines the C modules call: the bytes they write, the offsets they
//! move, and the packets they refuse.
//!
//! Native only, so the Miri suites leave it out: the routines are C, which
//! Miri cannot run. The headroom growth they share with prepending is
//! covered under Miri by the packet suite and checked against its Rust twin
//! by the unit tests.

use core::{
    mem::offset_of,
    net::{Ipv4Addr, Ipv6Addr},
};

use yanet_packet::{Ipv4Packet, Ipv6Header, UdpHeader, checksum};
use yanet_sdk_testing::{
    BlockArena,
    dp::{Fate, Invocation, Outcome, TestPacket, alloc_config, write_body_word},
};
use yanet_shm::shm::ShmLayout;
use yanet_sys::dp::{Batch, CommitCx, Cx, DscpMark, Module, Verdict};

const ENCAP_IPV4: u64 = 0;
const ENCAP_IPV6: u64 = 1;
const ENCAP_MPLS: u64 = 2;
const ENCAP_IPV4_UDP: u64 = 3;
const ENCAP_IPV6_UDP: u64 = 4;
const DECAP: u64 = 5;

const DSCP_NEVER: u64 = 0;
const DSCP_ALWAYS: u64 = 2;

const DST4: [u8; 4] = [198, 51, 100, 1];
const SRC4: [u8; 4] = [198, 51, 100, 2];
const DST6: [u8; 16] = [0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1];
const SRC6: [u8; 16] = [0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2];

const ETHERTYPE_MPLS: u16 = 0x8847;
const IPPROTO_IPIP: u16 = 4;
const IPPROTO_IPV6: u8 = 41;
const IPPROTO_UDP: u8 = 17;

/// Operation and its arguments, as the configuration carries them.
#[derive(Clone, Copy)]
#[repr(C)]
struct Rewrite {
    op: u64,
    dscp: u64,
    label: u64,
    traffic_class: u64,
}

// SAFETY: four words, `repr(C)`, every bit pattern valid, no padding,
// aligned to 8.
unsafe impl ShmLayout for Rewrite {}

/// Applies the configured operation to every packet; a packet the
/// operation refuses is dropped, as the C modules drop it.
struct Rewriter;

impl Module for Rewriter {
    const NAME: &'static str = "rewriter";
    type Config = Rewrite;
    type Prepared<'p> = ();
    fn prepare(_: &CommitCx<'_, Self>) {}

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let rewrite = cx.config();
        let dscp = match rewrite.dscp {
            DSCP_ALWAYS => DscpMark::Always,
            DSCP_NEVER => DscpMark::Never,
            _ => DscpMark::Default,
        };
        batch.dispatch(|packet| {
            let done = match rewrite.op {
                ENCAP_IPV4 => packet.encap_ipv4(DST4, SRC4, dscp),
                ENCAP_IPV6 => packet.encap_ipv6(DST6, SRC6, dscp),
                ENCAP_MPLS => packet.encap_mpls(rewrite.label as u32, rewrite.traffic_class as u8, true, 9),
                ENCAP_IPV4_UDP => packet.encap_ipv4_udp(SRC4, DST4, 4789, 6635),
                ENCAP_IPV6_UDP => packet.encap_ipv6_udp(SRC6, DST6, 4789, 6635),
                _ => packet.decap(),
            };
            if done.is_ok() { Verdict::Pass } else { Verdict::Drop }
        });
    }
}

/// Runs the operation on the packets and returns the outcome.
fn run(rewrite: Rewrite, packets: Vec<TestPacket>) -> Outcome {
    let arena = BlockArena::new();
    let config = alloc_config::<Rewrite>(arena);
    for (offset, value) in [
        (offset_of!(Rewrite, op), rewrite.op),
        (offset_of!(Rewrite, dscp), rewrite.dscp),
        (offset_of!(Rewrite, label), rewrite.label),
        (offset_of!(Rewrite, traffic_class), rewrite.traffic_class),
    ] {
        write_body_word::<Rewrite>(arena, config, offset, value);
    }
    // SAFETY: plain words, not written afterwards.
    let mut invocation = unsafe { Invocation::<Rewriter>::new(arena, config, 0) };
    invocation.run(packets)
}

fn op(op: u64) -> Rewrite {
    Rewrite {
        op,
        dscp: DSCP_NEVER,
        label: 0,
        traffic_class: 0,
    }
}

/// Inner IPv4 frame: Ethernet, a header with DSCP 46 and ECN 1, TTL 33 and a
/// UDP datagram of ports 1000 and 2000 with a 4-byte payload.
fn inner_ipv4() -> Vec<u8> {
    let mut frame = vec![0u8; 14 + 20 + 8 + 4];
    frame[..6].copy_from_slice(&[2, 0, 0, 0, 0, 2]);
    frame[6..12].copy_from_slice(&[2, 0, 0, 0, 0, 1]);
    frame[12..14].copy_from_slice(&0x0800u16.to_be_bytes());
    frame[14] = 0x45;
    frame[15] = 0xb9;
    frame[16..18].copy_from_slice(&32u16.to_be_bytes());
    frame[18..20].copy_from_slice(&0x1234u16.to_be_bytes());
    frame[22] = 33;
    frame[23] = IPPROTO_UDP;
    frame[26..30].copy_from_slice(&[192, 0, 2, 1]);
    frame[30..34].copy_from_slice(&[192, 0, 2, 2]);
    frame[34..36].copy_from_slice(&1000u16.to_be_bytes());
    frame[36..38].copy_from_slice(&2000u16.to_be_bytes());
    frame[38..40].copy_from_slice(&12u16.to_be_bytes());
    frame[42..46].copy_from_slice(b"data");
    let mut ip = frame.split_off(14);
    yanet_packet::Ipv4PacketMut::parse(&mut ip)
        .expect("inner header")
        .recompute_checksum();
    frame.extend(ip);
    frame
}

/// Inner IPv6 frame: Ethernet, a header with traffic class 0xb8 and hop
/// limit 7, and a UDP header with no payload.
fn inner_ipv6() -> Vec<u8> {
    let mut frame = vec![0u8; 14 + 40 + 8];
    frame[12..14].copy_from_slice(&0x86ddu16.to_be_bytes());
    frame[14..18].copy_from_slice(&(0x6000_0000u32 | (0xb8 << 20)).to_be_bytes());
    frame[18..20].copy_from_slice(&8u16.to_be_bytes());
    frame[20] = IPPROTO_UDP;
    frame[21] = 7;
    frame[22..38].copy_from_slice(&SRC6);
    frame[38..54].copy_from_slice(&DST6);
    frame[54..56].copy_from_slice(&1000u16.to_be_bytes());
    frame[56..58].copy_from_slice(&2000u16.to_be_bytes());
    frame[58..60].copy_from_slice(&8u16.to_be_bytes());
    frame
}

/// Packet of a frame whose network header follows the Ethernet header and
/// whose transport header is a UDP one after the IPv4 or IPv6 header, with
/// room for the longest outer headers.
fn parsed(frame: Vec<u8>, ether_type: u16, network_len: u16) -> TestPacket {
    TestPacket {
        headroom: 64,
        transport_type: u16::from(IPPROTO_UDP),
        transport_offset: 14 + network_len,
        ..TestPacket::ether(ether_type, frame)
    }
}

fn ipv4_packet(frame: Vec<u8>) -> TestPacket {
    parsed(frame, 0x0800, 20)
}

fn ipv6_packet(frame: Vec<u8>) -> TestPacket {
    parsed(frame, 0x86dd, 40)
}

/// verifies that an IPv4 encapsulation writes a valid outer header carrying
/// the inner TTL, protocol and length, takes the inner DSCP only when asked
/// to and always the ECN bits, keeps the inner packet behind it and moves
/// the transport offset.
#[test]
fn test_encap_ipv4_writes_outer_header_and_keeps_inner_packet() {
    let inner = inner_ipv4();
    for (dscp, expected_dscp) in [(DSCP_ALWAYS, 46), (DSCP_NEVER, 0)] {
        let outcome = run(Rewrite { dscp, ..op(ENCAP_IPV4) }, vec![ipv4_packet(inner.clone())]);

        assert_eq!(vec![Fate::Output], outcome.fates);
        let frame = &outcome.frames[0].bytes;
        assert_eq!(14 + 20 + 32, frame.len());
        let outer = Ipv4Packet::parse(&frame[14..]).expect("outer header");
        assert!(outer.checksum_is_valid());
        assert_eq!(
            (
                Ipv4Addr::from(SRC4),
                Ipv4Addr::from(DST4),
                33,
                IPPROTO_IPIP as u8,
                52,
                expected_dscp,
                1
            ),
            (
                outer.header.src(),
                outer.header.dst(),
                outer.header.ttl(),
                outer.header.protocol(),
                outer.header.total_len(),
                outer.header.dscp(),
                outer.header.ecn(),
            )
        );
        assert_eq!(&inner[14..], outer.payload);
        assert_eq!(&inner[..12], &frame[..12]);
        assert_eq!(
            (14, 14 + 20 + 20),
            (outcome.frames[0].network_offset, outcome.frames[0].transport_offset)
        );
    }
}

/// verifies that an IPv6 encapsulation of an IPv4 packet writes an outer
/// IPv6 header whose next header is IP-in-IP, whose hop limit is the inner
/// TTL and whose payload length is the inner packet's.
#[test]
fn test_encap_ipv6_of_ipv4_packet_writes_outer_header() {
    let inner = inner_ipv4();

    let outcome = run(
        Rewrite { dscp: DSCP_ALWAYS, ..op(ENCAP_IPV6) },
        vec![ipv4_packet(inner.clone())],
    );

    let frame = &outcome.frames[0].bytes;
    assert_eq!(0x86dd, u16::from_be_bytes([frame[12], frame[13]]));
    let (outer, payload) = Ipv6Header::parse(&frame[14..]).expect("outer header");
    assert_eq!(
        (
            Ipv6Addr::from(SRC6),
            Ipv6Addr::from(DST6),
            IPPROTO_IPIP as u8,
            33,
            32,
            0xb9
        ),
        (
            outer.src(),
            outer.dst(),
            outer.next_header(),
            outer.hop_limit(),
            outer.payload_len(),
            outer.traffic_class(),
        )
    );
    assert_eq!(&inner[14..], payload);
    assert_eq!(14 + 40 + 20, outcome.frames[0].transport_offset);
}

/// verifies that an IPv4 encapsulation of an IPv6 packet names IPv6 as the
/// protocol and counts the whole inner packet in the outer length.
#[test]
fn test_encap_ipv4_of_ipv6_packet_names_ipv6_protocol() {
    let inner = inner_ipv6();

    let outcome = run(op(ENCAP_IPV4), vec![ipv6_packet(inner.clone())]);

    let frame = &outcome.frames[0].bytes;
    assert_eq!(0x0800, u16::from_be_bytes([frame[12], frame[13]]));
    let outer = Ipv4Packet::parse(&frame[14..]).expect("outer header");
    assert!(outer.checksum_is_valid());
    assert_eq!(
        (IPPROTO_IPV6, 20 + 48, 7),
        (outer.header.protocol(), outer.header.total_len(), outer.header.ttl())
    );
    assert_eq!(&inner[14..], outer.payload);
}

/// verifies that an MPLS entry is written in front of the network header
/// with the label and traffic class bounded to their widths, and the
/// Ethernet type names MPLS.
#[test]
fn test_encap_mpls_prepends_label_stack_entry() {
    let inner = inner_ipv4();
    for (label, traffic_class, expected) in [
        (0x12345, 5, 0x12345u32 << 12 | 5 << 9),
        (0x1ff_ffff, 0xff, 0xf_ffff << 12 | 7 << 9),
    ] {
        let outcome = run(
            Rewrite {
                label,
                traffic_class,
                ..op(ENCAP_MPLS)
            },
            vec![ipv4_packet(inner.clone())],
        );

        let frame = &outcome.frames[0].bytes;
        assert_eq!(ETHERTYPE_MPLS, u16::from_be_bytes([frame[12], frame[13]]));
        assert_eq!(
            expected | 1 << 8 | 9,
            u32::from_be_bytes(frame[14..18].try_into().unwrap())
        );
        assert_eq!(&inner[14..], &frame[18..]);
        assert_eq!(14 + 4 + 20, outcome.frames[0].transport_offset);
    }
}

/// verifies that a UDP-over-IPv4 encapsulation writes the outer IPv4 and
/// UDP headers around the inner network packet, with a valid UDP checksum.
#[test]
fn test_encap_ipv4_udp_wraps_inner_packet() {
    let inner = inner_ipv4();

    let outcome = run(op(ENCAP_IPV4_UDP), vec![ipv4_packet(inner.clone())]);

    let frame = &outcome.frames[0].bytes;
    assert_eq!(14 + 20 + 8 + 32, frame.len());
    let outer = Ipv4Packet::parse(&frame[14..]).expect("outer header");
    assert!(outer.checksum_is_valid());
    assert_eq!((IPPROTO_UDP, 60), (outer.header.protocol(), outer.header.total_len()));
    let (udp, payload) = UdpHeader::parse(outer.payload).expect("udp header");
    assert_eq!((4789, 6635, 40), (udp.src_port(), udp.dst_port(), udp.length()));
    assert_eq!(&inner[14..], payload);
    assert!(checksum::l4_is_valid_ipv4(
        Ipv4Addr::from(SRC4),
        Ipv4Addr::from(DST4),
        IPPROTO_UDP,
        outer.payload
    ));
}

/// verifies that a UDP-over-IPv6 encapsulation does the same over IPv6.
#[test]
fn test_encap_ipv6_udp_wraps_inner_packet() {
    let inner = inner_ipv6();

    let outcome = run(op(ENCAP_IPV6_UDP), vec![ipv6_packet(inner.clone())]);

    let frame = &outcome.frames[0].bytes;
    let (outer, segment) = Ipv6Header::parse(&frame[14..]).expect("outer header");
    assert_eq!((IPPROTO_UDP, 8 + 48), (outer.next_header(), outer.payload_len()));
    let (udp, payload) = UdpHeader::parse(segment).expect("udp header");
    assert_eq!((4789, 6635), (udp.src_port(), udp.dst_port()));
    assert_eq!(&inner[14..], payload);
    assert!(checksum::l4_is_valid_ipv6(
        Ipv6Addr::from(SRC6),
        Ipv6Addr::from(DST6),
        IPPROTO_UDP,
        segment
    ));
}

/// verifies that decapsulation undoes IPv4 and IPv6 IP-in-IP
/// encapsulation of IPv4 and IPv6 packets, restoring the original frame and
/// the inner transport offset.
#[test]
fn test_decap_restores_the_encapsulated_frame() {
    let cases = [
        (inner_ipv4(), 0x0800, 20, ENCAP_IPV4, 20, IPPROTO_IPIP),
        (inner_ipv4(), 0x0800, 20, ENCAP_IPV6, 40, IPPROTO_IPIP),
        (inner_ipv6(), 0x86dd, 40, ENCAP_IPV4, 20, u16::from(IPPROTO_IPV6)),
        (inner_ipv6(), 0x86dd, 40, ENCAP_IPV6, 40, u16::from(IPPROTO_IPV6)),
    ];
    for (inner, ether_type, inner_len, encap, outer_len, protocol) in cases {
        let encapsulated = run(op(encap), vec![parsed(inner.clone(), ether_type, inner_len)])
            .frames
            .remove(0)
            .bytes;
        // The routine leaves the transport tag of the inner packet; the
        // parser would tag the tunnel, which is what the decap reads.
        let outer_type = if outer_len == 20 { 0x0800 } else { 0x86dd };
        let tunnel = TestPacket {
            headroom: 64,
            transport_type: protocol,
            transport_offset: 14 + outer_len,
            ..TestPacket::ether(outer_type, encapsulated)
        };

        let outcome = run(op(DECAP), vec![tunnel]);

        assert_eq!(vec![Fate::Output], outcome.fates);
        let frame = &outcome.frames[0];
        assert_eq!(
            (&inner, 14, 14 + inner_len),
            (&frame.bytes, frame.network_offset, frame.transport_offset)
        );
    }
}

/// verifies that a packet the C routines refuse is dropped with its frame
/// untouched: an inner type that is neither IPv4 nor IPv6, a headroom
/// short by one byte, and a packet that is no tunnel.
#[test]
fn test_tunnel_refusals_leave_the_frame_unchanged() {
    let inner = inner_ipv4();
    let arp = TestPacket::ether(0x0806, vec![0; 42]);
    let short = TestPacket {
        headroom: 19,
        ..ipv4_packet(inner.clone())
    };

    for (op_kind, packet, frame) in [
        (ENCAP_IPV4, arp, vec![0; 42]),
        (ENCAP_IPV4, short, inner.clone()),
        (DECAP, ipv4_packet(inner.clone()), inner.clone()),
    ] {
        let outcome = run(op(op_kind), vec![packet]);

        assert_eq!(vec![Fate::Drop], outcome.fates, "operation {op_kind}");
        assert_eq!(frame, outcome.frames[0].bytes, "operation {op_kind}");
    }
}

/// verifies that every encapsulation refuses a packet whose network header
/// the parser did not find, leaving the frame untouched.
///
/// The MPLS and UDP routines would otherwise write at the frame's start.
#[test]
fn test_encap_refuses_packet_without_network_header() {
    let frame = inner_ipv4();
    for op_kind in [ENCAP_IPV4, ENCAP_IPV6, ENCAP_MPLS, ENCAP_IPV4_UDP, ENCAP_IPV6_UDP] {
        let packet = TestPacket {
            headroom: 64,
            network_offset: 0,
            ..TestPacket::ether(0, frame.clone())
        };

        let outcome = run(op(op_kind), vec![packet]);

        assert_eq!(vec![Fate::Drop], outcome.fates, "operation {op_kind}");
        assert_eq!(frame, outcome.frames[0].bytes, "operation {op_kind}");
    }
}

/// verifies that a headroom of exactly the outer header's size is enough
/// for an encapsulation.
#[test]
fn test_encap_ipv4_headroom_of_exactly_the_header_succeeds() {
    let packet = TestPacket {
        headroom: 20,
        ..ipv4_packet(inner_ipv4())
    };

    let outcome = run(op(ENCAP_IPV4), vec![packet]);

    assert_eq!(vec![Fate::Output], outcome.fates);
}

/// verifies that an MPLS encapsulation of a parsed non-IP packet keeps its
/// unparsed transport header unparsed, though the C routine moves the
/// transport offset with the inner packet.
#[test]
fn test_encap_mpls_keeps_unparsed_transport_header_unparsed() {
    let arp = TestPacket {
        headroom: 64,
        ..TestPacket::ether(0x0806, vec![0; 42])
    };

    let outcome = run(op(ENCAP_MPLS), vec![arp]);

    assert_eq!(vec![Fate::Output], outcome.fates);
    assert_eq!(
        (14, 0),
        (outcome.frames[0].network_offset, outcome.frames[0].transport_offset)
    );
}

/// Wraps every packet in UDP over IPv4, then in IPv4, dropping a packet
/// either refuses.
struct UdpThenIpv4;

impl Module for UdpThenIpv4 {
    const NAME: &'static str = "udp_then_ipv4";
    type Config = Rewrite;
    type Prepared<'p> = ();
    fn prepare(_: &CommitCx<'_, Self>) {}

    fn handle(_: &mut Cx<Self>, batch: &mut Batch) {
        batch.dispatch(|packet| {
            let wrapped = packet.encap_ipv4_udp(SRC4, DST4, 4789, 6635).is_ok()
                && packet.encap_ipv4(DST4, SRC4, DscpMark::Never).is_ok();
            if wrapped { Verdict::Pass } else { Verdict::Drop }
        });
    }
}

/// verifies that an IPv4 encapsulation of a non-IP packet that a UDP
/// encapsulation already made IPv4 keeps its unparsed transport header
/// unparsed.
#[test]
fn test_encap_ipv4_after_udp_keeps_unparsed_transport_header_unparsed() {
    let arp = TestPacket {
        headroom: 64,
        ..TestPacket::ether(0x0806, vec![0; 42])
    };
    let arena = BlockArena::new();
    let config = alloc_config::<Rewrite>(arena);
    // SAFETY: a zeroed configuration of plain words, never written.
    let mut invocation = unsafe { Invocation::<UdpThenIpv4>::new(arena, config, 0) };

    let outcome = invocation.run(vec![arp]);

    assert_eq!(vec![Fate::Output], outcome.fates);
    assert_eq!(
        (14, 0),
        (outcome.frames[0].network_offset, outcome.frames[0].transport_offset)
    );
}
