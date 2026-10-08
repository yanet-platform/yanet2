use zerocopy::IntoBytes;

use super::{ENCAP_LEN, Ingress, VxlanConfig, classify_ingress, ipv4_checksum, outer_headers, source_port};

const LOCAL_IP: [u8; 4] = [192, 0, 2, 1];
const REMOTE_IP: [u8; 4] = [198, 51, 100, 7];

fn config() -> VxlanConfig {
    VxlanConfig {
        local_mac: [0x02, 0, 0, 0, 0, 0x01],
        remote_mac: [0x02, 0, 0, 0, 0, 0x02],
        local_ip: LOCAL_IP,
        remote_ip: REMOTE_IP,
        vni: 0x00_1234,
    }
}

/// An Ethernet frame carrying a minimal IPv4/UDP datagram with a payload.
fn inner_frame() -> Vec<u8> {
    let mut frame = vec![
        0x02, 0, 0, 0, 0, 0x0a, // dst
        0x02, 0, 0, 0, 0, 0x0b, // src
        0x08, 0x00, // IPv4
        0x45, 0, 0, 33, 0, 0, 0, 0, 64, 17, 0, 0, 10, 0, 0, 1, 10, 0, 0, 2, // IPv4
        0x30, 0x39, 0x00, 0x35, 0, 13, 0, 0, // UDP 12345 -> 53
    ];
    frame.extend_from_slice(b"hello");
    frame
}

/// The inner frame encapsulated with the given device config and hash.
fn encapsulate(config: &VxlanConfig, hash: u32, inner: &[u8]) -> Vec<u8> {
    let headers = outer_headers(config, hash, inner.len()).expect("inner frame fits");
    let mut frame = headers.as_bytes().to_vec();
    frame.extend_from_slice(inner);
    frame
}

/// A frame the remote endpoint sends to the local one, encapsulating inner.
fn received(vni: u32, inner: &[u8]) -> Vec<u8> {
    let mut remote = config();
    remote.local_mac = config().remote_mac;
    remote.remote_mac = config().local_mac;
    remote.local_ip = REMOTE_IP;
    remote.remote_ip = LOCAL_IP;
    remote.vni = vni;
    encapsulate(&remote, 0x5555_aaaa, inner)
}

#[test]
fn test_encap_decap_round_trip() {
    let inner = inner_frame();
    let frame = received(config().vni, &inner);

    let ingress = classify_ingress(&config(), &frame);

    assert_eq!(Ingress::Decap(ENCAP_LEN), ingress);
    assert_eq!(inner.as_slice(), &frame[ENCAP_LEN..]);
}

#[test]
fn test_encap_outer_headers_wire_format() {
    let inner = inner_frame();
    let frame = encapsulate(&config(), 0x1234_5678, &inner);

    let expected_outer: [u8; ENCAP_LEN] = [
        0x02, 0, 0, 0, 0, 0x02, // dst: remote MAC
        0x02, 0, 0, 0, 0, 0x01, // src: local MAC
        0x08, 0x00, // IPv4
        0x45, 0x00, 0x00, 0x53, 0x00, 0x00, 0x40, 0x00, // len 83, DF
        0x40, 0x11, 0x00, 0x00, // TTL 64, UDP, checksum checked below
        192, 0, 2, 1, 198, 51, 100, 7, // local -> remote
        0xc4, 0x4c, 0x12, 0xb5, 0x00, 0x3f, 0x00, 0x00, // UDP -> 4789, len 63
        0x08, 0, 0, 0, 0x00, 0x12, 0x34, 0x00, // I flag, VNI 0x1234
    ];
    let mut outer = frame[..ENCAP_LEN].to_vec();
    outer[24] = 0;
    outer[25] = 0;
    assert_eq!(expected_outer.as_slice(), outer.as_slice());
    assert_eq!(inner.as_slice(), &frame[ENCAP_LEN..]);
}

#[test]
fn test_encap_ipv4_checksum_verifies() {
    let frame = encapsulate(&config(), 7, &inner_frame());

    assert_eq!(0, ipv4_checksum(&frame[14..34]));
}

#[test]
fn test_encap_rejects_oversized_inner_frame() {
    assert!(outer_headers(&config(), 0, usize::from(u16::MAX)).is_none());
}

#[test]
fn test_encap_masks_vni_to_24_bits() {
    let mut config = config();
    config.vni = 0xff00_0001;
    let frame = encapsulate(&config, 0, &inner_frame());

    assert_eq!([0x00, 0x00, 0x01, 0x00], frame[46..50]);
}

#[test]
fn test_source_port_stays_in_dynamic_range() {
    for hash in [0, 1, 0xffff, 0x1_0000, 0xdead_beef, u32::MAX] {
        assert!(source_port(hash) >= 0xc000);
    }
}

#[test]
fn test_source_port_follows_hash() {
    assert_ne!(source_port(1), source_port(2));
}

#[test]
fn test_decap_vni_mismatch_drops() {
    let frame = received(config().vni + 1, &inner_frame());

    assert_eq!(Ingress::Drop, classify_ingress(&config(), &frame));
}

#[test]
fn test_decap_missing_vni_flag_drops() {
    let mut frame = received(config().vni, &inner_frame());
    frame[42] = 0;

    assert_eq!(Ingress::Drop, classify_ingress(&config(), &frame));
}

#[test]
fn test_decap_truncated_vxlan_header_drops() {
    let frame = received(config().vni, &inner_frame());

    assert_eq!(Ingress::Drop, classify_ingress(&config(), &frame[..46]));
}

#[test]
fn test_decap_truncated_inner_frame_drops() {
    let frame = received(config().vni, &inner_frame());

    assert_eq!(Ingress::Drop, classify_ingress(&config(), &frame[..ENCAP_LEN + 13]));
}

#[test]
fn test_decap_short_runt_passes() {
    assert_eq!(Ingress::Pass, classify_ingress(&config(), &[0x02; 10]));
}

#[test]
fn test_decap_truncated_ipv4_header_passes() {
    let frame = received(config().vni, &inner_frame());

    assert_eq!(Ingress::Pass, classify_ingress(&config(), &frame[..30]));
}

#[test]
fn test_decap_non_udp_passes() {
    let mut frame = received(config().vni, &inner_frame());
    frame[23] = 6;

    assert_eq!(Ingress::Pass, classify_ingress(&config(), &frame));
}

#[test]
fn test_decap_other_udp_port_passes() {
    let mut frame = received(config().vni, &inner_frame());
    frame[36..38].copy_from_slice(&53u16.to_be_bytes());

    assert_eq!(Ingress::Pass, classify_ingress(&config(), &frame));
}

#[test]
fn test_decap_other_destination_passes() {
    let mut frame = received(config().vni, &inner_frame());
    frame[33] = 99;

    assert_eq!(Ingress::Pass, classify_ingress(&config(), &frame));
}

#[test]
fn test_decap_non_ipv4_passes() {
    let mut frame = received(config().vni, &inner_frame());
    frame[12..14].copy_from_slice(&0x86ddu16.to_be_bytes());

    assert_eq!(Ingress::Pass, classify_ingress(&config(), &frame));
}

#[test]
fn test_decap_fragment_passes() {
    let mut frame = received(config().vni, &inner_frame());
    frame[20] |= 0x20;

    assert_eq!(Ingress::Pass, classify_ingress(&config(), &frame));
}

#[test]
fn test_decap_honours_ipv4_options() {
    let inner = inner_frame();
    let plain = received(config().vni, &inner);
    let mut frame = plain[..34].to_vec();
    frame[14] = 0x46;
    frame.extend_from_slice(&[1, 1, 1, 0]);
    frame.extend_from_slice(&plain[34..]);

    assert_eq!(Ingress::Decap(ENCAP_LEN + 4), classify_ingress(&config(), &frame));
}

#[test]
fn test_encap_udp_checksum_is_zero() {
    let frame = encapsulate(&config(), 0xdead_beef, &inner_frame());

    assert_eq!([0, 0], frame[40..42]);
}

#[test]
fn test_encap_reserved_vxlan_bits_are_zero() {
    let frame = encapsulate(&config(), 0, &inner_frame());

    assert_eq!([0x08, 0, 0, 0], frame[42..46]);
    assert_eq!(0, frame[49]);
}

#[test]
fn test_decap_accepts_any_udp_checksum() {
    let mut frame = received(config().vni, &inner_frame());
    frame[40..42].copy_from_slice(&0x1234u16.to_be_bytes());

    assert_eq!(Ingress::Decap(ENCAP_LEN), classify_ingress(&config(), &frame));
}

#[test]
fn test_decap_ignores_reserved_vxlan_bits() {
    let mut frame = received(config().vni, &inner_frame());
    frame[42] |= 0xf7;
    frame[43..46].copy_from_slice(&[0xff; 3]);
    frame[49] = 0xff;

    assert_eq!(Ingress::Decap(ENCAP_LEN), classify_ingress(&config(), &frame));
}

#[test]
fn test_source_port_of_zero_hash_is_range_start() {
    assert_eq!(49152, source_port(0));
}
