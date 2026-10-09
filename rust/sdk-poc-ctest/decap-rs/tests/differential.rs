//! Differential tests of the Rust decap module against the C module.
//!
//! Both handlers run over the same frames, parsed by the C parser, with the
//! same C-built configuration; every packet must end with the same verdict,
//! the same bytes and the same descriptor state.

use core::ffi::c_void;

use decap_dp as _;
use yanet_sdk::sys::ffi::module;
use yanet_testkit::{
    CImage, Family, XorShift,
    dataplane::{self, Outcome, Verdict},
    random_prefix,
};
use zerocopy as _;

unsafe extern "C" {
    /// The Rust module's constructor, exported by the export macro.
    fn new_module_decap() -> *mut module;
    fn free(ptr: *mut c_void);
}

/// Name, handler address, presence of both commit hooks and prepared size of
/// the Rust descriptor.
fn rust_descriptor() -> (String, *const c_void, bool, bool, u64) {
    // SAFETY: the constructor returns NULL or a calloc'ed descriptor, which
    // is read and released here as the loader would.
    unsafe {
        let raw = new_module_decap();
        assert!(!raw.is_null());
        let descriptor = &*raw;
        let name: Vec<u8> = descriptor
            .name
            .iter()
            .take_while(|&&c| c != 0)
            .map(|&c| c as u8)
            .collect();
        let result = (
            String::from_utf8(name).expect("ASCII name"),
            descriptor.handler.map_or(core::ptr::null(), |h| h as *const c_void),
            descriptor.commit_handler.is_some(),
            descriptor.commit_ectx_handler.is_some(),
            descriptor.prepared_size,
        );
        free(raw.cast());
        result
    }
}

#[test]
fn test_descriptor_matches_loader_contract() {
    let (name, handler, commit, commit_ectx, prepared) = rust_descriptor();
    assert_eq!("decap", name);
    assert!(!handler.is_null());
    assert!(!commit, "decap needs no generation commit hook");
    assert!(!commit_ectx);
    assert_eq!(0, prepared);
}

const ETHER_IPV4: u16 = 0x0800;
const ETHER_IPV6: u16 = 0x86dd;
const ETHER_VLAN: u16 = 0x8100;
const ETHER_ARP: u16 = 0x0806;
const PROTO_IPIP: u8 = 4;
const PROTO_TCP: u8 = 6;
const PROTO_UDP: u8 = 17;
const PROTO_IPV6: u8 = 41;
const PROTO_FRAGMENT: u8 = 44;
const PROTO_GRE: u8 = 47;
const PROTO_ICMP: u8 = 1;

/// Ethernet header, optionally VLAN-tagged, carrying `ether_type`.
fn ethernet(ether_type: u16, vlan: bool) -> Vec<u8> {
    let mut frame = vec![0x02, 0, 0, 0, 0, 1, 0x02, 0, 0, 0, 0, 2];
    if vlan {
        frame.extend_from_slice(&ETHER_VLAN.to_be_bytes());
        frame.extend_from_slice(&[0x00, 0x64]);
    }
    frame.extend_from_slice(&ether_type.to_be_bytes());
    frame
}

/// IPv4 header with a correct total length followed by `payload`.
fn ipv4(proto: u8, dst: [u8; 4], fragment: u16, payload: &[u8]) -> Vec<u8> {
    let total = (20 + payload.len()) as u16;
    let mut header = vec![0x45, 0];
    header.extend_from_slice(&total.to_be_bytes());
    header.extend_from_slice(&[0x12, 0x34]);
    header.extend_from_slice(&fragment.to_be_bytes());
    header.extend_from_slice(&[64, proto, 0, 0, 192, 0, 2, 1]);
    header.extend_from_slice(&dst);
    header.extend_from_slice(payload);
    header
}

/// IPv6 header with a correct payload length followed by `payload`.
fn ipv6(next: u8, dst: [u8; 16], flow: u32, payload: &[u8]) -> Vec<u8> {
    let mut header = (0x6000_0000u32 | (flow & 0x000f_ffff)).to_be_bytes().to_vec();
    header.extend_from_slice(&(payload.len() as u16).to_be_bytes());
    header.extend_from_slice(&[next, 64]);
    header.extend_from_slice(&[0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1]);
    header.extend_from_slice(&dst);
    header.extend_from_slice(payload);
    header
}

/// UDP or TCP transport header with a small payload.
fn transport(proto: u8, rng: &mut XorShift) -> Vec<u8> {
    let len = if proto == PROTO_TCP { 20 } else { 8 };
    let mut bytes = vec![0; len + rng.below(16) as usize];
    rng.fill(&mut bytes);
    if proto == PROTO_TCP {
        bytes[12] = 0x50;
    }
    bytes
}

/// A random inner packet; sometimes deliberately malformed.
fn inner(rng: &mut XorShift) -> (u8, u16, Vec<u8>) {
    let proto = [PROTO_UDP, PROTO_TCP, PROTO_ICMP][rng.below(3) as usize];
    let mut payload = transport(proto, rng);
    if rng.below(10) == 0 {
        // A truncated TCP or UDP header inside the tunnel.
        payload.truncate(4);
    }
    let fragment = if rng.below(12) == 0 {
        0x2000 | rng.below(4) as u16
    } else {
        0
    };
    let mut dst = [0; 16];
    rng.fill(&mut dst);
    if rng.below(2) == 0 {
        let mut bytes = ipv4(proto, dst[..4].try_into().unwrap(), fragment, &payload);
        if rng.below(15) == 0 {
            // Inner total length beyond the frame.
            bytes[2] = 0xff;
        }
        (PROTO_IPIP, ETHER_IPV4, bytes)
    } else {
        (
            PROTO_IPV6,
            ETHER_IPV6,
            ipv6(proto, dst, rng.next_u64() as u32, &payload),
        )
    }
}

/// GRE header for an inner packet of `ether_type`, with random optional
/// fields and occasionally reserved bits, a version or an unknown protocol.
fn gre(rng: &mut XorShift, ether_type: u16) -> Vec<u8> {
    let mut byte0 = [0x00, 0x80, 0x20, 0x10, 0xb0][rng.below(5) as usize];
    let mut byte1 = 0;
    match rng.below(12) {
        0 => byte0 |= 0x40,
        1 => byte1 = 0x01,
        _ => {}
    }
    let proto = if rng.below(12) == 0 { 0x6558 } else { ether_type };
    let mut header = vec![byte0, byte1];
    header.extend_from_slice(&proto.to_be_bytes());
    let optional = (byte0 & 0xb0).count_ones() as usize;
    header.extend(core::iter::repeat_n(0xab, optional * 4));
    header
}

/// A random frame addressed to a matching or a random destination.
fn frame(rng: &mut XorShift, dst4: &[[u8; 4]], dst6: &[[u8; 16]]) -> Vec<u8> {
    let vlan = rng.below(8) == 0;
    if rng.below(25) == 0 {
        let mut frame = ethernet(ETHER_ARP, vlan);
        frame.extend_from_slice(&[0; 28]);
        return frame;
    }
    let (inner_proto, inner_ether, inner_bytes) = inner(rng);
    let (proto, payload) = match rng.below(10) {
        0..=4 => (inner_proto, inner_bytes),
        5..=7 => {
            let mut payload = gre(rng, inner_ether);
            payload.extend_from_slice(&inner_bytes);
            (PROTO_GRE, payload)
        }
        8 => (PROTO_UDP, transport(PROTO_UDP, rng)),
        _ => (PROTO_TCP, transport(PROTO_TCP, rng)),
    };
    let matching = rng.below(4) != 0;
    if rng.below(2) == 0 {
        let dst = if matching {
            dst4[rng.below(dst4.len() as u64) as usize]
        } else {
            let mut dst = [0; 4];
            rng.fill(&mut dst);
            dst
        };
        let fragment = match rng.below(15) {
            0 => 0x2000,
            1 => 0x0010,
            _ => 0,
        };
        let mut frame = ethernet(ETHER_IPV4, vlan);
        frame.extend_from_slice(&ipv4(proto, dst, fragment, &payload));
        frame
    } else {
        let dst = if matching {
            dst6[rng.below(dst6.len() as u64) as usize]
        } else {
            let mut dst = [0; 16];
            rng.fill(&mut dst);
            dst
        };
        let mut frame = ethernet(ETHER_IPV6, vlan);
        if rng.below(15) == 0 {
            // An IPv6 Fragment extension header in front of the tunnel.
            let mut fragment = vec![proto, 0, 0, if rng.below(2) == 0 { 1 } else { 0 }, 0, 0, 0, 7];
            fragment.extend_from_slice(&payload);
            frame.extend_from_slice(&ipv6(PROTO_FRAGMENT, dst, rng.next_u64() as u32, &fragment));
        } else {
            frame.extend_from_slice(&ipv6(proto, dst, rng.next_u64() as u32, &payload));
        }
        frame
    }
}

/// Decap configuration with random tunnel-endpoint prefixes, built by C.
fn configuration(rng: &mut XorShift) -> (CImage, Vec<[u8; 4]>, Vec<[u8; 16]>) {
    let mut image = CImage::new(16 << 20);
    let mut dst4 = Vec::new();
    let mut dst6 = Vec::new();
    for _ in 0..64 {
        let (from, to) = random_prefix(rng, 4, 16..=32);
        image.insert(Family::V4, &from, &to, 1);
        dst4.push(from.try_into().expect("IPv4 key"));
        let (from, to) = random_prefix(rng, 16, 32..=128);
        image.insert(Family::V6, &from, &to, 1);
        dst6.push(from.try_into().expect("IPv6 key"));
    }
    (image, dst4, dst6)
}

/// Verifies that the Rust module and the C module agree on verdict, bytes and
/// packet state for every frame of a random mix covering every branch, and
/// on the front's output and drop counters.
#[test]
fn test_rust_handler_matches_c_handler() {
    const FRAMES: usize = 20_000;
    let mut rng = XorShift::new(0xdec4_9000);
    let (image, dst4, dst6) = configuration(&mut rng);
    let frames: Vec<Vec<u8>> = (0..FRAMES).map(|_| frame(&mut rng, &dst4, &dst6)).collect();

    let (_, rust_handler, ..) = rust_descriptor();
    // SAFETY: both addresses are module handlers over a decap configuration.
    let ((c_totals, c), (rust_totals, rust)) = unsafe {
        (
            dataplane::run(dataplane::c_decap_handler(), &image, &frames),
            dataplane::run(rust_handler, &image, &frames),
        )
    };

    assert_eq!(c_totals, rust_totals, "front counters");
    let mut stats = [0usize; 4];
    for (idx, (c, rust)) in c.iter().zip(&rust).enumerate() {
        assert_eq!(c, rust, "frame {idx}: {:02x?}", frames[idx]);
        let slot = match c {
            None => 0,
            Some(Outcome { verdict: Verdict::Drop, .. }) => 1,
            Some(Outcome { verdict: Verdict::Output, frame, .. }) if frame.len() < frames[idx].len() => 2,
            Some(_) => 3,
        };
        stats[slot] += 1;
    }
    let [unparsed, dropped, decapped, passed] = stats;
    println!("frames {FRAMES}: unparsed {unparsed}, dropped {dropped}, decapsulated {decapped}, passed {passed}");
    assert!(dropped > FRAMES / 20 && decapped > FRAMES / 10 && passed > FRAMES / 10);
}
