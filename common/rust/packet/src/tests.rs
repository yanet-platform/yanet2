//! Unit tests for header views and checksums.

use std::prelude::v1::*;

use crate::checksum::{Checksum, checksum, checksum_compose, checksum_update, verify};
use crate::ip6::{Ipv6, Ipv6ExtIter};
use crate::protocol::{EtherType, IpProtocol};
use crate::{Arp, Eth2, Eth2Mut, Gre, Icmp, Ipv4, Ipv4Mut, Ipv6Mut, Tcp, Udp, Vlan};

#[test]
fn eth2_round_trip() {
    let mut frame = [0u8; 14 + 4];
    frame[..6].copy_from_slice(&[0x01, 0x02, 0x03, 0x04, 0x05, 0x06]);
    frame[6..12].copy_from_slice(&[0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff]);
    frame[12..14].copy_from_slice(&[0x08, 0x00]);

    let eth = Eth2::from_bytes(&frame).expect("full frame parses");
    assert_eq!(eth.dst_mac(), [0x01, 0x02, 0x03, 0x04, 0x05, 0x06]);
    assert_eq!(eth.src_mac(), [0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff]);
    assert_eq!(eth.ether_type_known(), Some(EtherType::Ipv4));
    assert_eq!(eth.payload().len(), 4);

    {
        let mut eth_mut = Eth2Mut::from_bytes(&mut frame).expect("full frame");
        eth_mut.set_ether_type(EtherType::Ipv6.wire());
    }
    assert_eq!(Eth2::from_bytes(&frame).expect("frame").ether_type(), 0x86DD);
}

#[test]
fn eth2_short_buffer_refused() {
    let frame = [0u8; 13];
    assert!(Eth2::from_bytes(&frame).is_none());
}

#[test]
fn vlan_fields() {
    let tag = [0xB0, 0x64, 0x86, 0xDD];
    let vlan = Vlan::from_bytes(&tag).expect("tag parses");
    assert_eq!(vlan.pcp(), 5);
    assert!(vlan.dei());
    assert_eq!(vlan.vid(), 0x064);
    assert_eq!(vlan.ether_type(), 0x86DD);
}

#[test]
fn arp_fields() {
    let mut buf = [0u8; 28];
    buf[4] = 6;
    buf[5] = 4;
    buf[6..8].copy_from_slice(&[0x00, 0x02]);
    buf[14..18].copy_from_slice(&[10, 0, 0, 1]);
    buf[24..28].copy_from_slice(&[10, 0, 0, 2]);

    let arp = Arp::from_bytes(&buf).expect("arp parses");
    assert_eq!(arp.operation(), 2);
    assert_eq!(arp.sender_ip(), [10, 0, 0, 1]);
    assert_eq!(arp.target_ip(), [10, 0, 0, 2]);

    // Non-Ethernet/IPv4 address lengths are refused.
    let mut bad = buf;
    bad[4] = 8;
    assert!(Arp::from_bytes(&bad).is_none());
}

#[test]
fn ipv4_fields_and_options() {
    let mut buf = [0u8; 20 + 4 + 8];
    buf[0] = 0x45; // v4, IHL 5
    buf[1] = 0x40;
    buf[2..4].copy_from_slice(&[0x00, 0x20]);
    buf[6..8].copy_from_slice(&[0x20, 0x00]); // MF set
    buf[8] = 64;
    buf[9] = IpProtocol::Gre as u8;
    buf[12..16].copy_from_slice(&[192, 0, 2, 1]);
    buf[16..20].copy_from_slice(&[198, 51, 100, 2]);

    let ip = Ipv4::from_bytes(&buf).expect("header parses");
    assert_eq!(ip.header_len(), Some(20));
    assert_eq!(ip.tos(), 0x40);
    assert_eq!(ip.total_length(), 32);
    assert!(ip.is_fragmented());
    assert!(ip.more_fragments());
    assert_eq!(ip.fragment_offset(), 0);
    assert_eq!(ip.ttl(), 64);
    assert_eq!(ip.protocol(), 47);
    assert_eq!(ip.src(), [192, 0, 2, 1]);
    assert_eq!(ip.dst(), [198, 51, 100, 2]);
    assert_eq!(ip.options().expect("ihl 5").len(), 0);
    assert_eq!(ip.payload().expect("ihl 5").len(), 12);

    buf[0] = 0x46; // options grow the header by one word
    let ip = Ipv4::from_bytes(&buf).expect("header parses");
    assert_eq!(ip.header_len(), Some(24));
    assert_eq!(ip.options().expect("ihl 6").len(), 4);

    buf[0] = 0x44; // corrupt: below the fixed header
    assert_eq!(Ipv4::from_bytes(&buf).expect("fixed part").header_len(), None);
}

#[test]
fn ipv4_fragment_rules() {
    let mut buf = [0u8; 20];
    buf[0] = 0x45;
    // The field counts 8-byte units; offset 100 is 0x0064, MF clear but
    // still a fragment.
    buf[6..8].copy_from_slice(&[0x00, 0x64]);
    let ip = Ipv4::from_bytes(&buf).expect("header parses");
    assert!(ip.is_fragmented());
    assert_eq!(ip.fragment_offset(), 100);
    assert!(!ip.more_fragments());
}

#[test]
fn ipv4_mutation() {
    let mut buf = [0u8; 20];
    buf[0] = 0x45;
    let mut ip = Ipv4Mut::from_bytes(&mut buf).expect("header parses");
    ip.set_ttl(1);
    ip.set_dst([1, 1, 1, 1]);
    ip.set_total_length(500);

    let ip = Ipv4::from_bytes(&buf).expect("header parses");
    assert_eq!(ip.ttl(), 1);
    assert_eq!(ip.dst(), [1, 1, 1, 1]);
    assert_eq!(ip.total_length(), 500);
}

#[test]
fn ipv6_fields_and_extensions() {
    let mut buf = [0u8; 40 + 8 + 8 + 8];
    // Fixed header: traffic class 0x2c, flow label 0x12345, next hop-by-hop.
    buf[0] = 0x62;
    buf[1] = 0xc1;
    buf[2..4].copy_from_slice(&[0x23, 0x45]);
    buf[4..6].copy_from_slice(&[0x00, 0x0E]);
    buf[6] = 0;
    buf[7] = 64;
    buf[8..24].copy_from_slice(&[0x20; 16]);
    buf[24..40].copy_from_slice(&[0x01; 16]);
    // Hop-by-hop with one 8-byte unit, next fragment.
    buf[40] = 44;
    buf[41] = 0;
    // Fragment header: offset 2 units, MF set, next TCP.
    buf[48] = IpProtocol::Tcp as u8;
    buf[49] = 0;
    buf[50..52].copy_from_slice(&[0x00, 0x11]);
    buf[52..56].copy_from_slice(&[9, 9, 9, 9]);
    // A minimal TCP body follows.
    buf[62..64].copy_from_slice(&[0x00, 0x50]);

    let ip = Ipv6::from_bytes(&buf).expect("header parses");
    assert_eq!(ip.traffic_class(), 0x2c);
    assert_eq!(ip.flow_label(), 0x12345);
    assert_eq!(ip.payload_length(), 14);
    assert_eq!(ip.next_header(), IpProtocol::HopByHop as u8);
    assert_eq!(ip.hop_limit(), 64);
    assert_eq!(ip.src(), [0x20; 16]);
    assert_eq!(ip.dst(), [0x01; 16]);

    let mut ext = Ipv6ExtIter::new(ip.payload(), ip.next_header());
    let hop = ext.next().expect("hop-by-hop present");
    assert_eq!(hop.header_len(), 8);
    let frag = ext.next().expect("fragment present");
    assert_eq!(frag.next_header(), IpProtocol::Tcp as u8);
    assert!(matches!(frag, crate::ip6::Ipv6Ext::Fragment(_)));
    assert!(ext.next().is_none());
    assert_eq!(ext.next_header(), IpProtocol::Tcp as u8);
}

#[test]
fn ipv6_mutation() {
    let mut buf = [0u8; 40];
    buf[0] = 0x60;
    let mut ip = Ipv6Mut::from_bytes(&mut buf).expect("header parses");
    ip.set_hop_limit(1);
    ip.set_next_header(IpProtocol::Udp as u8);
    let dst = [7u8; 16];
    ip.set_dst(dst);

    let ip = Ipv6::from_bytes(&buf).expect("header parses");
    assert_eq!(ip.hop_limit(), 1);
    assert_eq!(ip.next_header(), IpProtocol::Udp as u8);
    assert_eq!(ip.dst(), dst);
}

#[test]
fn transport_views() {
    let mut tcp = [0u8; 20];
    tcp[0..2].copy_from_slice(&[0x00, 0x50]);
    tcp[2..4].copy_from_slice(&[0x1F, 0x90]);
    tcp[12] = 0x50;
    tcp[13] = 0x12;
    let tcp = Tcp::from_bytes(&tcp).expect("tcp parses");
    assert_eq!(tcp.src_port(), 80);
    assert_eq!(tcp.dst_port(), 8080);
    assert_eq!(tcp.header_len(), Some(20));
    assert!(tcp.syn());
    assert!(tcp.ack_flag());
    assert!(!tcp.fin());

    let mut udp = [0u8; 8 + 2];
    udp[0..2].copy_from_slice(&[0x00, 0x35]);
    udp[2..4].copy_from_slice(&[0x30, 0x39]);
    udp[4..6].copy_from_slice(&[0x00, 0x0A]);
    let udp = Udp::from_bytes(&udp).expect("udp parses");
    assert_eq!(udp.src_port(), 53);
    assert_eq!(udp.dst_port(), 12345);
    assert_eq!(udp.length(), 10);
    assert_eq!(udp.payload().len(), 2);

    let mut icmp = [0u8; 8 + 4];
    icmp[0] = 8;
    icmp[1] = 0;
    icmp[4..8].copy_from_slice(&[0x00, 0x01, 0x00, 0x02]);
    let icmp = Icmp::from_bytes(&icmp).expect("icmp parses");
    assert_eq!(icmp.icmp_type(), 8);
    assert_eq!(icmp.rest(), 0x00010002);
    assert_eq!(icmp.payload().len(), 4);
}

#[test]
fn gre_flags() {
    let buf = [0x80, 0x01, 0x08, 0x00];
    let gre = Gre::from_bytes(&buf).expect("gre parses");
    assert!(gre.has_checksum());
    assert!(gre.has_sequence());
    assert!(!gre.has_key());
    assert_eq!(gre.protocol(), 0x0800);
}

#[test]
fn checksum_rfc1071_example() {
    // The worked example from RFC 1071: data 00 01 f2 03 f4 f5 f6 f7.
    let data = [0x00, 0x01, 0xf2, 0x03, 0xf4, 0xf5, 0xf6, 0xf7];
    let sum_words: u32 = 0x0001 + 0xf203 + 0xf4f5 + 0xf6f7;
    let folded = fold_ref(sum_words);
    assert_eq!(checksum(&data), folded);
    assert_eq!(checksum(&data), 0x220d);
}

fn fold_ref(mut sum: u32) -> u16 {
    while (sum >> 16) != 0 {
        sum = (sum & 0xffff) + (sum >> 16);
    }
    !(sum as u16)
}

#[test]
fn checksum_odd_length_pads_zero_low() {
    let data = [0x01, 0xf2, 0xf4, 0xf5, 0xf7];
    let expected = fold_ref(0x01f2 + 0xf4f5 + 0xf700);
    assert_eq!(checksum(&data), expected);
}

#[test]
fn checksum_compose_concatenation_equivalence() {
    let a = [0x00, 0x01, 0xf2, 0x03];
    let b = [0xf4, 0xf5, 0xf6];
    let c = [0xf7, 0x11];
    let mut whole = Vec::new();
    whole.extend_from_slice(&a);
    whole.extend_from_slice(&b);
    whole.extend_from_slice(&c);
    assert_eq!(checksum_compose(&[&a, &b, &c]), checksum(&whole));
}

#[test]
fn checksum_accumulator_matches_compose() {
    let a = [0x11u8, 0x22];
    let b = [0x33u8, 0x44, 0x55];
    let mut acc = Checksum::new();
    acc.push(&a);
    acc.push(&b);
    let mut whole = Vec::new();
    whole.extend_from_slice(&a);
    whole.extend_from_slice(&b);
    assert_eq!(acc.value(), checksum(&whole));
}

#[test]
fn checksum_update_matches_recompute() {
    let mut header = [0x45u8, 0x00, 0x00, 0x28, 0x12, 0x34, 0x00, 0x01];
    let old = checksum(&header);
    // Rewrite TTL byte (index 8 in a real header; here just rewrite one
    // word) and verify the RFC 1624 update equals a full recompute.
    let old_word = u16::from_be_bytes([header[0], header[1]]);
    header[1] = 0x10;
    let new_word = u16::from_be_bytes([header[0], header[1]]);
    let updated = checksum_update(old, &[old_word], &[new_word]);
    assert_eq!(updated, checksum(&header));
}

#[test]
fn checksum_verify_detects_corruption() {
    let mut data = [0x45u8, 0x00, 0x00, 0x14, 0x00, 0x00, 0x00, 0x00, 0x40, 0x01, 0x00, 0x00];
    let sum = checksum(&data[..]);
    data[10..12].copy_from_slice(&sum.to_be_bytes());
    assert!(verify(&data));
    data[8] = 0x20;
    assert!(!verify(&data));
}

#[test]
fn checksum_pseudo_headers() {
    let mut acc = Checksum::new();
    acc.push_ipv4_pseudo_header([10, 0, 0, 1], [10, 0, 0, 2], 6, 20);
    let mut flat = Vec::new();
    flat.extend_from_slice(&[10, 0, 0, 1]);
    flat.extend_from_slice(&[10, 0, 0, 2]);
    flat.extend_from_slice(&[0, 6, 0, 20]);
    assert_eq!(acc.value(), checksum(&flat));

    let mut acc6 = Checksum::new();
    acc6.push_ipv6_pseudo_header([0x20; 16], [0x01; 16], 17, 40);
    let mut flat6 = Vec::new();
    flat6.extend_from_slice(&[0x20; 16]);
    flat6.extend_from_slice(&[0x01; 16]);
    flat6.extend_from_slice(&40u32.to_be_bytes());
    flat6.extend_from_slice(&[0, 0, 0, 17]);
    assert_eq!(acc6.value(), checksum(&flat6));
}
