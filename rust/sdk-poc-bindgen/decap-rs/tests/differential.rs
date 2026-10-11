//! Differential test: the Rust decap module against the unmodified C decap
//! module, on the same C-built configuration and C-parsed packets.

use decap_dp::Decap;
use decap_oracle::{OraclePacket, c_handle};
use yanet_sdk as _;
use yanet_sys::{bindings, testing::TestArena};

unsafe extern "C" {
    /// Exported by the Rust module through its export macro.
    fn new_module_decap() -> *mut bindings::module;
    fn free(ptr: *mut core::ffi::c_void);
}

const IPPROTO_IPIP: u8 = 4;
const IPPROTO_TCP: u8 = 6;
const IPPROTO_UDP: u8 = 17;
const IPPROTO_IPV6: u8 = 41;
const IPPROTO_GRE: u8 = 47;
const IPPROTO_FRAGMENT: u8 = 44;
const IPPROTO_HOPOPTS: u8 = 0;

/// Ethernet header, optionally VLAN-tagged, followed by `payload`.
fn ether(ether_type: u16, vlan: bool, payload: &[u8]) -> Vec<u8> {
    let mut frame = vec![0x02, 0, 0, 0, 0, 1, 0x02, 0, 0, 0, 0, 2];
    if vlan {
        frame.extend_from_slice(&0x8100u16.to_be_bytes());
        frame.extend_from_slice(&100u16.to_be_bytes());
    }
    frame.extend_from_slice(&ether_type.to_be_bytes());
    frame.extend_from_slice(payload);
    frame
}

/// IPv4 header without options; `fragment` is the raw flags and offset.
fn ipv4(proto: u8, dst: [u8; 4], fragment: u16, payload: &[u8]) -> Vec<u8> {
    let total = (20 + payload.len()) as u16;
    let mut header = vec![0x45, 0];
    header.extend_from_slice(&total.to_be_bytes());
    header.extend_from_slice(&[0x12, 0x34]);
    header.extend_from_slice(&fragment.to_be_bytes());
    header.extend_from_slice(&[64, proto, 0, 0]);
    header.extend_from_slice(&[198, 51, 100, 1]);
    header.extend_from_slice(&dst);
    header.extend_from_slice(payload);
    header
}

/// IPv6 header with a fixed source and the given flow label.
fn ipv6(next: u8, dst: [u8; 16], flow_label: u32, payload: &[u8]) -> Vec<u8> {
    let mut header = (0x6000_0000u32 | (0x2a << 20) | flow_label).to_be_bytes().to_vec();
    header.extend_from_slice(&(payload.len() as u16).to_be_bytes());
    header.extend_from_slice(&[next, 64]);
    header.extend_from_slice(&[0x20, 0x01, 0x0d, 0xb8, 0xff, 0xff, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1]);
    header.extend_from_slice(&dst);
    header.extend_from_slice(payload);
    header
}

/// GRE header with an optional key, carrying `proto`.
fn gre(proto: u16, key: Option<u32>, payload: &[u8]) -> Vec<u8> {
    let mut header = vec![if key.is_some() { 0x20 } else { 0 }, 0];
    header.extend_from_slice(&proto.to_be_bytes());
    if let Some(key) = key {
        header.extend_from_slice(&key.to_be_bytes());
    }
    header.extend_from_slice(payload);
    header
}

/// GRE header with the checksum and sequence number fields present.
fn gre_checksum_sequence(proto: u16, payload: &[u8]) -> Vec<u8> {
    let mut header = vec![0x90, 0];
    header.extend_from_slice(&proto.to_be_bytes());
    header.extend_from_slice(&[0xab, 0xcd, 0, 0, 0, 0, 0, 42]);
    header.extend_from_slice(payload);
    header
}

/// IPv6 hop-by-hop options header of 8 bytes in front of `payload`.
fn hop_by_hop(next: u8, payload: &[u8]) -> Vec<u8> {
    let mut header = vec![next, 0, 1, 4, 0, 0, 0, 0];
    header.extend_from_slice(payload);
    header
}

fn tcp() -> Vec<u8> {
    let mut header = vec![0x30, 0x39, 0x00, 0x50];
    header.resize(20, 0);
    header[12] = 0x50;
    header
}

fn udp() -> Vec<u8> {
    vec![0x30, 0x39, 0x00, 0x35, 0, 8, 0, 0]
}

fn v6(last: u8, prefix: [u8; 4]) -> [u8; 16] {
    let mut addr = [0; 16];
    addr[..4].copy_from_slice(&prefix);
    addr[15] = last;
    addr
}

const IN4: [u8; 4] = [10, 1, 2, 3];
const OUT4: [u8; 4] = [203, 0, 113, 9];
const IN6: [u8; 4] = [0x20, 0x01, 0x0d, 0xb8];
const OUT6: [u8; 4] = [0x20, 0x01, 0x0d, 0xb9];

/// Named frames covering every branch of the C handler.
fn cases() -> Vec<(&'static str, Vec<u8>)> {
    let inner4 = ipv4(IPPROTO_TCP, [192, 0, 2, 1], 0, &tcp());
    let inner6 = ipv6(IPPROTO_UDP, v6(2, OUT6), 7, &udp());
    vec![
        (
            "ipip to a matching prefix",
            ether(0x0800, false, &ipv4(IPPROTO_IPIP, IN4, 0, &inner4)),
        ),
        (
            "ipip to a foreign address",
            ether(0x0800, false, &ipv4(IPPROTO_IPIP, OUT4, 0, &inner4)),
        ),
        (
            "ipv6 in ipv4",
            ether(0x0800, false, &ipv4(IPPROTO_IPV6, IN4, 0, &inner6)),
        ),
        (
            "gre with key",
            ether(
                0x0800,
                false,
                &ipv4(IPPROTO_GRE, IN4, 0, &gre(0x0800, Some(7), &inner4)),
            ),
        ),
        (
            "gre without key",
            ether(0x0800, false, &ipv4(IPPROTO_GRE, IN4, 0, &gre(0x86dd, None, &inner6))),
        ),
        (
            "gre with unknown payload",
            ether(0x0800, false, &ipv4(IPPROTO_GRE, IN4, 0, &gre(0x0806, None, &udp()))),
        ),
        (
            "outer ipv4 more fragments",
            ether(0x0800, false, &ipv4(IPPROTO_IPIP, IN4, 0x2000, &inner4)),
        ),
        (
            "outer ipv4 fragment offset",
            ether(0x0800, false, &ipv4(IPPROTO_IPIP, OUT4, 0x0001, &inner4)),
        ),
        (
            "foreign outer ipv4 more fragments",
            ether(0x0800, false, &ipv4(IPPROTO_IPIP, OUT4, 0x2000, &inner4)),
        ),
        (
            "matching udp is not a tunnel",
            ether(0x0800, false, &ipv4(IPPROTO_UDP, IN4, 0, &udp())),
        ),
        (
            "vlan tagged ipip",
            ether(0x0800, true, &ipv4(IPPROTO_IPIP, IN4, 0, &inner4)),
        ),
        (
            "truncated inner tcp",
            ether(
                0x0800,
                false,
                &ipv4(IPPROTO_IPIP, IN4, 0, &ipv4(IPPROTO_TCP, OUT4, 0, &[0; 4])),
            ),
        ),
        (
            "ipv6 in ipv6",
            ether(0x86dd, false, &ipv6(IPPROTO_IPV6, v6(1, IN6), 0xabcde, &inner6)),
        ),
        (
            "ipv4 in ipv6",
            ether(0x86dd, false, &ipv6(IPPROTO_IPIP, v6(1, IN6), 0x12345, &inner4)),
        ),
        (
            "ipv6 to a foreign address",
            ether(0x86dd, false, &ipv6(IPPROTO_IPV6, v6(1, OUT6), 1, &inner6)),
        ),
        (
            "ipv6 fragment header",
            ether(
                0x86dd,
                false,
                &ipv6(IPPROTO_FRAGMENT, v6(1, IN6), 3, &[IPPROTO_IPV6, 0, 0, 1, 0, 0, 0, 9]),
            ),
        ),
        ("arp passes through", ether(0x0806, false, &[0; 28])),
        (
            "gre with checksum and sequence",
            ether(
                0x0800,
                false,
                &ipv4(IPPROTO_GRE, IN4, 0, &gre_checksum_sequence(0x0800, &inner4)),
            ),
        ),
        (
            "vlan tagged ipv6 in ipv6",
            ether(0x86dd, true, &ipv6(IPPROTO_IPV6, v6(1, IN6), 0x54321, &inner6)),
        ),
        (
            "hop-by-hop before an ipv6 tunnel",
            ether(
                0x86dd,
                false,
                &ipv6(IPPROTO_HOPOPTS, v6(1, IN6), 0x00777, &hop_by_hop(IPPROTO_IPV6, &inner6)),
            ),
        ),
    ]
}

/// Decap configurations and execution contexts inside one C test arena:
/// the C layout for the C module, the SDK layout for the Rust module, both
/// with identical LPMs built by the C insert.
struct Setup {
    _arena: TestArena,
    /// Context of the C module over the C-layout configuration.
    c_ectx: *mut bindings::module_ectx,
    /// Context of the Rust module over the SDK-layout configuration.
    rust_ectx: *mut bindings::module_ectx,
}

/// Inclusive `[from, to]` key ranges of one address family.
type Ranges<const K: usize> = Vec<([u8; K], [u8; K])>;

/// Inclusive ranges of the test configuration, IPv4 then IPv6.
fn ranges() -> (Ranges<4>, Ranges<16>) {
    let mut from = [0u8; 16];
    from[..4].copy_from_slice(&IN6);
    let mut to = [0xffu8; 16];
    to[..4].copy_from_slice(&IN6);
    (
        vec![([10, 0, 0, 0], [10, 255, 255, 255]), ([192, 0, 2, 0], [192, 0, 2, 255])],
        vec![(from, to)],
    )
}

/// Zeroed execution context naming `config`.
fn ectx(arena: &TestArena, config: *mut u8) -> *mut bindings::module_ectx {
    let ectx = arena
        .alloc(size_of::<bindings::module_ectx>())
        .cast::<bindings::module_ectx>();
    // SAFETY: the zeroed context lives in the arena; the module header is
    // the first field of either configuration layout.
    unsafe { (*ectx).abs_cp_module = config.cast() };
    ectx
}

impl Setup {
    fn new() -> Self {
        let arena = TestArena::new(8 << 20);
        let (v4_ranges, v6_ranges) = ranges();

        let config = arena.alloc(size_of::<bindings::decap_module_config>());
        let c_config = config.cast::<bindings::decap_module_config>();
        // SAFETY: both LPMs are unused fields of a zeroed arena block.
        let (mut v4, mut v6) = unsafe {
            (
                arena.init_lpm(&raw mut (*c_config).prefixes4),
                arena.init_lpm(&raw mut (*c_config).prefixes6),
            )
        };
        for (from, to) in &v4_ranges {
            v4.insert(from, to, 1);
        }
        for (from, to) in &v6_ranges {
            v6.insert(from, to, 1);
        }

        let mut rust = arena.build::<Decap>("decap0").expect("SDK config");
        for (from, to) in &v4_ranges {
            rust.insert(|c| &c.prefixes4, from, to, 1).unwrap();
        }
        for (from, to) in &v6_ranges {
            rust.insert(|c| &c.prefixes6, from, to, 1).unwrap();
        }
        let rust_config = rust.finish().expect("validated SDK config").as_ptr().cast::<u8>();

        let (c_ectx, rust_ectx) = (ectx(&arena, config), ectx(&arena, rust_config));
        Self { _arena: arena, c_ectx, rust_ectx }
    }
}

/// Front whose input list holds the given packets in order.
fn front(packets: &[&OraclePacket]) -> Box<bindings::packet_front> {
    // SAFETY: an all-zero front is empty.
    let mut front = Box::new(unsafe { core::mem::zeroed::<bindings::packet_front>() });
    for packet in packets {
        let raw = packet.raw();
        // SAFETY: test-owned packets, linked like the C list add.
        unsafe {
            (*raw).next = core::ptr::null_mut();
            if front.input.last.is_null() {
                front.input.last = &raw mut front.input.first;
            }
            *front.input.last = raw;
            front.input.last = &raw mut (*raw).next;
        }
    }
    front
}

fn members(list: &bindings::packet_list) -> Vec<*mut bindings::packet> {
    let mut out = Vec::new();
    let mut cursor = list.first;
    while !cursor.is_null() {
        out.push(cursor);
        // SAFETY: test-owned list.
        cursor = unsafe { (*cursor).next };
    }
    out
}

/// What a handler did to one packet, comparable across implementations.
#[derive(Debug, PartialEq, Eq)]
struct Outcome {
    output: bool,
    data: Vec<u8>,
    flow_label: u32,
    network: (u16, u16),
    transport: (u16, u16),
    data_len: u16,
    flags: u16,
}

fn outcome(front: &bindings::packet_front, packet: &OraclePacket) -> Outcome {
    let output = members(&front.output).contains(&packet.raw());
    assert_ne!(
        output,
        members(&front.drop).contains(&packet.raw()),
        "packet in exactly one list"
    );
    let descriptor = packet.descriptor();
    Outcome {
        output,
        data: packet.data(),
        flow_label: descriptor.flow_label,
        network: (descriptor.network_header.type_, descriptor.network_header.offset),
        transport: (descriptor.transport_header.type_, descriptor.transport_header.offset),
        data_len: descriptor.data_len,
        flags: descriptor.flags,
    }
}

/// Counters a front accumulated: (output count, bytes, drop count, bytes).
fn counters(front: &bindings::packet_front) -> (u64, u64, u64, u64) {
    (
        front.output_count,
        front.output_bytes,
        front.drop_count,
        front.drop_bytes,
    )
}

fn rust_handle(ectx: *mut bindings::module_ectx, front: *mut bindings::packet_front) {
    // SAFETY: the exported constructor returns a malloc'd descriptor whose
    // handler takes a module context and a front.
    unsafe {
        let module = new_module_decap();
        let handler = (*module).handler.expect("module handler");
        free(module.cast());
        handler(core::ptr::null_mut(), ectx, front);
    }
}

/// Runs both handlers on fresh copies of the frames, one front each.
fn run_both(setup: &Setup, frames: &[Vec<u8>]) -> Option<(Vec<Outcome>, Vec<Outcome>)> {
    let c_packets: Vec<_> = frames.iter().map(|f| OraclePacket::new(f)).collect::<Option<_>>()?;
    let rust_packets: Vec<_> = frames.iter().map(|f| OraclePacket::new(f).unwrap()).collect();
    let mut c_front = front(&c_packets.iter().collect::<Vec<_>>());
    let mut rust_front = front(&rust_packets.iter().collect::<Vec<_>>());
    // SAFETY: arena-backed context and test-owned fronts.
    unsafe { c_handle(setup.c_ectx, &mut *c_front) };
    rust_handle(setup.rust_ectx, &mut *rust_front);
    assert_eq!(counters(&c_front), counters(&rust_front), "front counters");
    let order = |front: &bindings::packet_front, packets: &[OraclePacket]| {
        let index = |p: &*mut bindings::packet| packets.iter().position(|q| q.raw() == *p).unwrap();
        (
            members(&front.output).iter().map(index).collect::<Vec<_>>(),
            members(&front.drop).iter().map(index).collect::<Vec<_>>(),
        )
    };
    assert_eq!(
        order(&c_front, &c_packets),
        order(&rust_front, &rust_packets),
        "list order"
    );
    Some((
        c_packets.iter().map(|p| outcome(&c_front, p)).collect(),
        rust_packets.iter().map(|p| outcome(&rust_front, p)).collect(),
    ))
}

#[test]
fn test_decap_matches_c_module_per_case() {
    let setup = Setup::new();
    for (name, frame) in cases() {
        let (c, rust) = run_both(&setup, &[frame]).unwrap_or_else(|| panic!("{name}: the C parser rejected the frame"));
        assert_eq!(c, rust, "{name}");
    }
}

/// Verifies that the expected verdicts hold, so the differential test is
/// not comparing two identical mistakes.
#[test]
fn test_decap_verdicts_per_case() {
    let setup = Setup::new();
    let expected = [
        ("ipip to a matching prefix", true, true),
        ("ipip to a foreign address", true, false),
        ("gre with key", true, true),
        ("outer ipv4 more fragments", false, false),
        ("outer ipv4 fragment offset", false, false),
        ("matching udp is not a tunnel", false, false),
        ("ipv6 in ipv6", true, true),
        ("ipv6 fragment header", false, false),
        ("arp passes through", true, false),
        ("gre with checksum and sequence", true, true),
        ("vlan tagged ipv6 in ipv6", true, true),
        ("hop-by-hop before an ipv6 tunnel", true, true),
    ];
    let cases = cases();
    for (name, output, decapsulated) in expected {
        let frame = &cases.iter().find(|(n, _)| *n == name).unwrap().1;
        let (_, rust) = run_both(&setup, core::slice::from_ref(frame)).unwrap();
        assert_eq!(output, rust[0].output, "{name}: verdict");
        assert_eq!(decapsulated, rust[0].data.len() < frame.len(), "{name}: decapsulated");
    }
}

/// Verifies that the descriptor declares the configuration layout the
/// control-plane api names, and only the packet handler.
#[test]
fn test_descriptor_declares_config_layout() {
    // SAFETY: the exported constructor returns a malloc'd descriptor.
    unsafe {
        let module = new_module_decap();
        assert_eq!(yanet_sys::shm::config_layout::<Decap>(), (*module).config_layout);
        assert_ne!(0, (*module).config_layout);
        assert!((*module).handler.is_some());
        assert!((*module).commit_ectx_handler.is_none());
        assert_eq!(0, (*module).prepared_size);
        free(module.cast());
    }
}

/// Verifies that the Rust module drops every packet of a context without
/// a configuration.
#[test]
fn test_decap_drops_without_config() {
    let setup = Setup::new();
    let frames: Vec<_> = cases().into_iter().map(|(_, f)| f).collect();
    let packets: Vec<_> = frames.iter().map(|f| OraclePacket::new(f).unwrap()).collect();
    let mut raw = front(&packets.iter().collect::<Vec<_>>());
    rust_handle(ectx(&setup._arena, core::ptr::null_mut()), &mut *raw);
    assert!(members(&raw.output).is_empty());
    assert_eq!(frames.len() as u64, raw.drop_count);
}

/// Verifies that the IPv6 flow label of a decapsulated packet is recorded.
#[test]
fn test_decap_records_ipv6_flow_label() {
    let setup = Setup::new();
    let frame = ether(
        0x86dd,
        false,
        &ipv6(
            IPPROTO_IPV6,
            v6(1, IN6),
            0xabcde,
            &ipv6(IPPROTO_UDP, v6(2, OUT6), 7, &udp()),
        ),
    );
    let (_, rust) = run_both(&setup, &[frame]).unwrap();
    assert_eq!(0xabcde, rust[0].flow_label);
}

/// Verifies that both modules agree on a batch of all cases and on randomly
/// mutated frames.
#[test]
fn test_decap_matches_c_module_on_batches_and_mutations() {
    let setup = Setup::new();
    let frames: Vec<_> = cases().into_iter().map(|(_, f)| f).collect();
    let (c, rust) = run_both(&setup, &frames).unwrap();
    assert_eq!(c, rust, "batch of all cases");

    let mut state = 0x0dec_a95eu64;
    let mut next = move || {
        state ^= state << 13;
        state ^= state >> 7;
        state ^= state << 17;
        state
    };
    let mut compared = 0;
    for _ in 0..20_000 {
        let mut frame = frames[(next() % frames.len() as u64) as usize].clone();
        for _ in 0..1 + next() % 4 {
            let idx = 12 + (next() as usize) % (frame.len() - 12);
            frame[idx] = next() as u8;
        }
        if let Some((c, rust)) = run_both(&setup, &[frame.clone()]) {
            assert_eq!(c, rust, "mutated frame {frame:02x?}");
            compared += 1;
        }
    }
    assert!(compared > 10_000, "too few parsable mutations: {compared}");
}
