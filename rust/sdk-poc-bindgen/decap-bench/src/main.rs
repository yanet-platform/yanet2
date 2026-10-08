//! Per-packet cost of decap handlers on identical packet fronts.
//!
//! The C decap module, compiled into this binary with the meson flags, and
//! every Rust plugin named on the command line run through the same C
//! handler pointer over the same fronts of the same mixed traffic. Each
//! front is restored from a snapshot before its timed call, so every call
//! sees fresh, cache-warm packets; only the handler call is inside the
//! timestamp pair. Variants are sampled round-robin to spread host drift.
//!
//! Usage: `decap-bench [--front N] [--passes N] [--samples N] NAME=PLUGIN...`

#[cfg(target_arch = "x86_64")]
use core::arch::x86_64::{_mm_lfence, _rdtsc};
use core::{
    ffi::{c_char, c_int, c_void},
    hash::Hasher,
};
use std::{collections::hash_map::DefaultHasher, env, ffi::CString, time::Instant};

use decap_oracle::OraclePacket;
use yanet_sys::{bindings, testing::TestArena};

unsafe extern "C" {
    /// The C module's constructor, renamed by the oracle build.
    fn oracle_new_module_decap() -> *mut bindings::module;
    fn dlopen(file: *const c_char, mode: c_int) -> *mut c_void;
    fn dlsym(handle: *mut c_void, name: *const c_char) -> *mut c_void;
    fn dlerror() -> *const c_char;
    fn free(ptr: *mut c_void);
}

const RTLD_NOW: c_int = 2;

type Handler = unsafe extern "C" fn(*mut bindings::dp_worker, *mut bindings::module_ectx, *mut bindings::packet_front);

const IPPROTO_IPIP: u8 = 4;
const IPPROTO_TCP: u8 = 6;
const IPPROTO_UDP: u8 = 17;
const IPPROTO_IPV6: u8 = 41;
const IPPROTO_GRE: u8 = 47;
const IPPROTO_FRAGMENT: u8 = 44;

/// Deterministic xorshift64 generator.
struct Rng(u64);

impl Rng {
    fn next(&mut self) -> u64 {
        self.0 ^= self.0 << 13;
        self.0 ^= self.0 >> 7;
        self.0 ^= self.0 << 17;
        self.0
    }

    fn below(&mut self, bound: u64) -> u64 {
        self.next() % bound
    }
}

/// Untagged Ethernet frame around the given payload.
fn ether(ether_type: u16, payload: &[u8]) -> Vec<u8> {
    let mut frame = vec![0x02, 0, 0, 0, 0, 1, 0x02, 0, 0, 0, 0, 2];
    frame.extend_from_slice(&ether_type.to_be_bytes());
    frame.extend_from_slice(payload);
    frame
}

/// IPv4 header without options, with raw fragment flags and offset.
fn ipv4(proto: u8, dst: [u8; 4], fragment: u16, payload: &[u8]) -> Vec<u8> {
    let total = (20 + payload.len()) as u16;
    let mut header = vec![0x45, 0];
    header.extend_from_slice(&total.to_be_bytes());
    header.extend_from_slice(&[0x12, 0x34]);
    header.extend_from_slice(&fragment.to_be_bytes());
    header.extend_from_slice(&[64, proto, 0, 0, 198, 51, 100, 1]);
    header.extend_from_slice(&dst);
    header.extend_from_slice(payload);
    header
}

/// IPv6 header with a fixed source and the given flow label.
fn ipv6(next: u8, dst: [u8; 16], flow_label: u32, payload: &[u8]) -> Vec<u8> {
    let mut header = (0x6000_0000u32 | flow_label).to_be_bytes().to_vec();
    header.extend_from_slice(&(payload.len() as u16).to_be_bytes());
    header.extend_from_slice(&[next, 64]);
    header.extend_from_slice(&[0x20, 0x01, 0x0d, 0xb8, 0xff, 0xff, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1]);
    header.extend_from_slice(&dst);
    header.extend_from_slice(payload);
    header
}

/// TCP header without options and 32 payload bytes.
fn tcp() -> Vec<u8> {
    let mut segment = vec![0x30, 0x39, 0x00, 0x50];
    segment.resize(52, 0);
    segment[12] = 0x50;
    segment
}

/// UDP header and 32 payload bytes.
fn udp() -> Vec<u8> {
    let mut datagram = vec![0x30, 0x39, 0x00, 0x35, 0, 40, 0, 0];
    datagram.resize(40, 0);
    datagram
}

/// Decap prefixes: random IPv4 prefixes of length 16..=32 with a first
/// octet in 11..=200, random IPv6 prefixes of length 32..=64 under 2a00::/8.
///
/// Tunnel destinations are drawn from inside them, so lookups walk the
/// depth real prefixes have; pass-through traffic goes to 203.0.113.0/24
/// and 2001:db9::/32, which no prefix covers.
struct Prefixes {
    v4: Vec<[u8; 4]>,
    v6: Vec<[u8; 16]>,
}

/// First and last address of the prefix with the given number of leading
/// bits of an address.
fn prefix_range<const N: usize>(base: [u8; N], len: usize) -> ([u8; N], [u8; N]) {
    let (mut from, mut to) = (base, base);
    for bit in len..N * 8 {
        from[bit / 8] &= !(0x80 >> (bit % 8));
        to[bit / 8] |= 0x80 >> (bit % 8);
    }
    (from, to)
}

/// Decap configuration with both LPMs and the execution context pointing
/// at it, inside one C test arena.
struct Setup {
    _arena: TestArena,
    ectx: *mut bindings::module_ectx,
    prefixes: Prefixes,
}

impl Setup {
    fn new(rng: &mut Rng) -> Self {
        let arena = TestArena::new(256 << 20);
        let config = arena
            .alloc(size_of::<bindings::decap_module_config>())
            .cast::<bindings::decap_module_config>();
        // SAFETY: the zeroed configuration block lives in the arena.
        let (v4, v6) = unsafe { (&raw mut (*config).prefixes4, &raw mut (*config).prefixes6) };
        // SAFETY: both LPMs are unused fields of an arena block.
        let (mut lpm4, mut lpm6) = unsafe { (arena.init_lpm(v4), arena.init_lpm(v6)) };
        let mut prefixes = Prefixes { v4: Vec::new(), v6: Vec::new() };
        for _ in 0..1000 {
            let mut base = (rng.next() as u32).to_be_bytes();
            base[0] = 11 + rng.below(190) as u8;
            let (from, to) = prefix_range(base, 16 + rng.below(17) as usize);
            lpm4.insert(&from, &to, 1);
            prefixes.v4.push(from);
        }
        for _ in 0..500 {
            let mut base = [0u8; 16];
            for byte in &mut base {
                *byte = rng.next() as u8;
            }
            base[0] = 0x2a;
            let (from, to) = prefix_range(base, 32 + rng.below(33) as usize);
            lpm6.insert(&from, &to, 1);
            prefixes.v6.push(from);
        }
        let ectx = arena
            .alloc(size_of::<bindings::module_ectx>())
            .cast::<bindings::module_ectx>();
        // SAFETY: the zeroed context lives in the arena; the module header
        // is the first field of the configuration.
        unsafe { (*ectx).abs_cp_module = config.cast() };
        Self { _arena: arena, ectx, prefixes }
    }
}

/// Address inside a prefix: its last byte varies.
fn inside<const N: usize>(base: [u8; N], rng: &mut Rng) -> [u8; N] {
    let mut addr = base;
    addr[N - 1] = rng.next() as u8;
    addr
}

/// One frame of the traffic mix, with the share of each kind in percent.
///
/// 60% are tunnels to a decap prefix (IP-in-IP, GRE with a key, IPv6 in
/// IPv6, IPv4 in IPv6), 30% are plain TCP or UDP to uncovered addresses,
/// 5% are outer fragments and 5% are not IP.
fn mixed_frame(prefixes: &Prefixes, rng: &mut Rng) -> Vec<u8> {
    let v4 = |rng: &mut Rng| inside(prefixes.v4[rng.below(prefixes.v4.len() as u64) as usize], rng);
    let v6 = |rng: &mut Rng| inside(prefixes.v6[rng.below(prefixes.v6.len() as u64) as usize], rng);
    let inner4 = ipv4(IPPROTO_TCP, [192, 0, 2, 1], 0, &tcp());
    let mut pass6 = [0x20, 0x01, 0x0d, 0xb9, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0];
    pass6[15] = rng.next() as u8;
    let inner6 = ipv6(IPPROTO_UDP, pass6, 7, &udp());
    match rng.below(100) {
        0..30 => ether(0x0800, &ipv4(IPPROTO_IPIP, v4(rng), 0, &inner4)),
        30..40 => {
            let mut gre = vec![0x20, 0, 0x08, 0x00, 0, 0, 0, 7];
            gre.extend_from_slice(&inner4);
            ether(0x0800, &ipv4(IPPROTO_GRE, v4(rng), 0, &gre))
        }
        40..55 => {
            let label = rng.below(1 << 20) as u32;
            ether(0x86dd, &ipv6(IPPROTO_IPV6, v6(rng), label, &inner6))
        }
        55..60 => ether(0x86dd, &ipv6(IPPROTO_IPIP, v6(rng), 1, &inner4)),
        60..80 => ether(0x0800, &ipv4(IPPROTO_TCP, [203, 0, 113, rng.next() as u8], 0, &tcp())),
        80..90 => ether(0x86dd, &inner6),
        90..94 => ether(0x0800, &ipv4(IPPROTO_IPIP, v4(rng), 0x2000, &inner4)),
        94..95 => ether(
            0x86dd,
            &ipv6(IPPROTO_FRAGMENT, v6(rng), 3, &[IPPROTO_IPV6, 0, 0, 1, 0, 0, 0, 9]),
        ),
        _ => ether(0x0806, &[0; 28]),
    }
}

/// A packet with snapshots of its mbuf and buffer as parsed.
struct Slot {
    packet: OraclePacket,
    mbuf: Vec<u8>,
    buffer: Vec<u8>,
}

impl Slot {
    fn new(frame: &[u8]) -> Self {
        let packet = OraclePacket::new(frame).expect("the C parser accepts every bench frame");
        // SAFETY: the oracle packet owns its mbuf and buffer.
        let (mbuf, buffer) = unsafe {
            let mbuf = (*packet.raw()).mbuf;
            (
                core::slice::from_raw_parts(mbuf.cast::<u8>(), size_of::<bindings::rte_mbuf>()).to_vec(),
                core::slice::from_raw_parts((*mbuf).buf_addr.cast::<u8>(), usize::from((*mbuf).buf_len)).to_vec(),
            )
        };
        Self { packet, mbuf, buffer }
    }

    /// Restores the packet, its metadata in the headroom included, to its
    /// parsed state.
    fn restore(&self) {
        // SAFETY: the snapshots were taken from these very allocations.
        unsafe {
            let mbuf = (*self.packet.raw()).mbuf;
            let buffer = (*mbuf).buf_addr.cast::<u8>();
            buffer.copy_from_nonoverlapping(self.buffer.as_ptr(), self.buffer.len());
            mbuf.cast::<u8>()
                .copy_from_nonoverlapping(self.mbuf.as_ptr(), self.mbuf.len());
        }
    }
}

/// Restores the packets and links them into an emptied front in order.
fn refill(front: &mut bindings::packet_front, slots: &[Slot]) {
    // SAFETY: an all-zero front is empty.
    *front = unsafe { core::mem::zeroed() };
    for slot in slots {
        slot.restore();
        let raw = slot.packet.raw();
        // SAFETY: bench-owned packets, linked like the C list add.
        unsafe {
            (*raw).next = core::ptr::null_mut();
            if front.input.last.is_null() {
                front.input.last = &raw mut front.input.first;
            }
            *front.input.last = raw;
            front.input.last = &raw mut (*raw).next;
        }
    }
}

/// Hash of what a handler did to a front: list membership and order,
/// counters, and every packet's bytes and header metadata.
fn digest(front: &bindings::packet_front, hasher: &mut DefaultHasher) {
    for list in [&front.output, &front.drop] {
        let mut cursor = list.first;
        while !cursor.is_null() {
            // SAFETY: bench-owned packets of a well-formed list.
            unsafe {
                let mbuf = (*cursor).mbuf;
                let data = core::slice::from_raw_parts(
                    (*mbuf).buf_addr.cast::<u8>().add(usize::from((*mbuf).data_off)),
                    usize::from((*mbuf).data_len),
                );
                hasher.write(data);
                hasher.write_u32((*cursor).flow_label);
                hasher.write_u16((*cursor).network_header.type_);
                hasher.write_u16((*cursor).network_header.offset);
                hasher.write_u16((*cursor).transport_header.type_);
                hasher.write_u16((*cursor).transport_header.offset);
                cursor = (*cursor).next;
            }
        }
        hasher.write_u8(0xff);
    }
    for counter in [
        front.output_count,
        front.output_bytes,
        front.drop_count,
        front.drop_bytes,
    ] {
        hasher.write_u64(counter);
    }
}

/// Cycles between two serialised timestamps around a call.
#[cfg(target_arch = "x86_64")]
#[inline(always)]
fn cycles(body: impl FnOnce()) -> u64 {
    // SAFETY: the fences and the timestamp counter have no preconditions
    // on x86-64.
    unsafe {
        _mm_lfence();
        let start = _rdtsc();
        _mm_lfence();
        body();
        _mm_lfence();
        let end = _rdtsc();
        end - start
    }
}

/// Nanoseconds around a call, where no timestamp counter is used.
#[cfg(not(target_arch = "x86_64"))]
#[inline(always)]
fn cycles(body: impl FnOnce()) -> u64 {
    let start = Instant::now();
    body();
    start.elapsed().as_nanos() as u64
}

/// Timestamp counter ticks per nanosecond, measured against the monotonic
/// clock.
#[cfg(target_arch = "x86_64")]
fn tsc_per_ns() -> f64 {
    let clock = Instant::now();
    // SAFETY: reading the timestamp counter has no preconditions.
    let start = unsafe { _rdtsc() };
    while clock.elapsed().as_millis() < 300 {}
    let elapsed = clock.elapsed();
    // SAFETY: as above.
    let end = unsafe { _rdtsc() };
    (end - start) as f64 / elapsed.as_nanos() as f64
}

/// The fallback timer already counts nanoseconds.
#[cfg(not(target_arch = "x86_64"))]
fn tsc_per_ns() -> f64 {
    1.0
}

struct Variant {
    name: String,
    handler: Handler,
    samples: Vec<f64>,
    digest: Option<u64>,
}

/// Handler of the descriptor a module constructor returns.
fn take_handler(module: *mut bindings::module) -> Handler {
    assert!(!module.is_null(), "module constructor failed");
    // SAFETY: a constructor returns a malloc'd, initialised descriptor.
    unsafe {
        let handler = (*module).handler.expect("module without a handler");
        free(module.cast());
        handler
    }
}

/// Loads a plugin and returns the handler of its decap module.
fn load_plugin(path: &str) -> Handler {
    let c_path = CString::new(path).unwrap();
    // SAFETY: dlopen and dlsym with valid strings; the library stays
    // loaded for the life of the process.
    unsafe {
        let library = dlopen(c_path.as_ptr(), RTLD_NOW);
        if library.is_null() {
            let error = core::ffi::CStr::from_ptr(dlerror());
            panic!("dlopen {path}: {}", error.to_string_lossy());
        }
        let constructor = dlsym(library, c"new_module_decap".as_ptr());
        assert!(!constructor.is_null(), "{path} does not export new_module_decap");
        let constructor: unsafe extern "C" fn() -> *mut bindings::module = core::mem::transmute(constructor);
        take_handler(constructor())
    }
}

fn percentile(sorted: &[f64], q: f64) -> f64 {
    sorted[((sorted.len() - 1) as f64 * q).round() as usize]
}

fn main() {
    let mut front_size = 32usize;
    let mut passes = 50usize;
    let mut sample_count = 31usize;
    // SAFETY: the oracle constructor has no preconditions.
    let mut variants = vec![Variant {
        name: "c".to_owned(),
        handler: take_handler(unsafe { oracle_new_module_decap() }),
        samples: Vec::new(),
        digest: None,
    }];
    let mut args = env::args().skip(1);
    while let Some(arg) = args.next() {
        let mut value = |flag: &str| -> usize {
            args.next()
                .and_then(|v| v.parse().ok())
                .unwrap_or_else(|| panic!("{flag} needs a number"))
        };
        match arg.as_str() {
            "--front" => front_size = value("--front"),
            "--passes" => passes = value("--passes"),
            "--samples" => sample_count = value("--samples"),
            _ => {
                let (name, path) = arg.split_once('=').expect("plugin argument is NAME=PATH");
                variants.push(Variant {
                    name: name.to_owned(),
                    handler: load_plugin(path),
                    samples: Vec::new(),
                    digest: None,
                });
            }
        }
    }

    const PACKETS: usize = 2048;
    let mut rng = Rng(0x0dec_a95e_be0c);
    let setup = Setup::new(&mut rng);
    let slots: Vec<Slot> = (0..PACKETS)
        .map(|_| Slot::new(&mixed_frame(&setup.prefixes, &mut rng)))
        .collect();
    let fronts: Vec<&[Slot]> = slots.chunks(front_size).collect();
    // SAFETY: an all-zero front is empty.
    let mut front: bindings::packet_front = unsafe { core::mem::zeroed() };

    let overhead = (0..100_000).map(|_| cycles(|| {})).min().unwrap();
    let tsc = tsc_per_ns();
    let packets_per_sample = (passes * PACKETS) as f64;

    for sample in 0..=sample_count {
        for variant in &mut variants {
            let mut total = 0u64;
            let mut hasher = DefaultHasher::new();
            for _ in 0..passes {
                for slots in &fronts {
                    refill(&mut front, slots);
                    let handler = variant.handler;
                    let ectx = setup.ectx;
                    let front_ptr = &raw mut front;
                    // SAFETY: the arena context carries a decap
                    // configuration and the front a well-formed input list.
                    total +=
                        cycles(|| unsafe { handler(core::ptr::null_mut(), ectx, front_ptr) }).saturating_sub(overhead);
                    if sample == 0 {
                        digest(&front, &mut hasher);
                    }
                }
            }
            // Sample 0 warms caches and branch predictors and records what
            // the handler did; its timings are discarded.
            if sample == 0 {
                variant.digest = Some(hasher.finish());
            } else {
                variant.samples.push(total as f64 / tsc / packets_per_sample);
            }
        }
    }

    let reference = variants[0].digest;
    for variant in &variants {
        assert_eq!(
            reference, variant.digest,
            "{} processed the fronts differently from C",
            variant.name
        );
    }
    println!(
        "{PACKETS} packets in fronts of {front_size}, {passes} passes per sample, {sample_count} samples, TSC \
         {tsc:.3} GHz, timer overhead {overhead} cycles subtracted; every variant produced the C module's output"
    );
    println!("| variant | best ns/pkt | median ns/pkt | p10-p90 ns/pkt | Mpps (median) | median vs c |");
    println!("|---|---|---|---|---|---|");
    let mut c_median = 0.0;
    for variant in &mut variants {
        variant.samples.sort_by(f64::total_cmp);
        let median = percentile(&variant.samples, 0.5);
        if variant.name == "c" {
            c_median = median;
        }
        println!(
            "| {} | {:.2} | {:.2} | {:.2}-{:.2} | {:.1} | {:+.1}% |",
            variant.name,
            variant.samples[0],
            median,
            percentile(&variant.samples, 0.1),
            percentile(&variant.samples, 0.9),
            1e3 / median,
            (median / c_median - 1.0) * 100.0
        );
    }
}
