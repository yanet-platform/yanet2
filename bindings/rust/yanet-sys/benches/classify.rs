//! The classify query of a Rust module against the inline C query of a C
//! module, one key per query, over the same classifier.
//!
//! The query is the C modules' shape: two IPv4 addresses and two ports, the
//! addresses joined, the ports joined, the two joints joined at the root and
//! decoded into the first rule. The rulesets are one rule (destination port
//! 443), and the same rule behind a rule of 1000 destination /24 prefixes,
//! which the destination of the keys misses three bytes deep. Each variant is
//! timed in turn in one process, and the rule sums of all must agree:
//!
//! - `c`: the query of `shim/tests/classify_bench.c`, in a C loop, built by the
//!   dataplane's compiler and flags;
//! - `stages`: the per-stage lookups of the bindings, one key per call;
//! - `tree`: the lookup of the classifier tree;
//! - `c-burst`, `stages-burst`, `tree-burst`: the same over bursts of 32 keys,
//!   each stage over the whole burst before the next, as the batch query of a C
//!   module runs.
//!
//! `cargo bench -p yanet-sys --bench classify [-- ROUNDS REPS]` prints, per
//! ruleset, port and variant, the median nanoseconds per key and the median
//! of its ratios to C. `-- --only VARIANT RULESET PORT ROUNDS` runs one
//! variant alone, for `perf stat`; its instructions per key are those of a run
//! less those of a run of zero rounds, over the rounds times the keys.
//!
//! Those keys repeat and the classifier stays in cache. `-- --cold [REPS]`
//! measures past the cache instead, over `acl`, a seeded ruleset of 2000
//! rules of random source and destination networks and common destination
//! ports, and 65536 keys, half made to match a random rule and half random:
//!
//! - `large`: every key once per round, so the tables and the keys stream
//!   through the caches as a busy router's do;
//! - `cold`: bursts of 32 keys at random places, each after a read of a buffer
//!   larger than the last-level cache, timed alone, as the first burst after
//!   other work.

#[path = "../tests/support/arena.rs"]
mod arena;

use core::{ffi::CStr, hint::black_box};
use std::{env, time::Instant};

use arena::ArenaOwner;
// The workspace denies unused crate dependencies; this benchmark names no
// crate it imports `as _`.
use netip as _;
use yanet_sdk_testing as _;
use yanet_shm::rel::ShmBox;
use yanet_sys::{
    bindings,
    classify::{
        Addr4, Attribute, BURST, Decode, Join, Joint, Net4, Net4Attr, Nets4, PortAttr, PortRange, Ports, Rule, RuleMap,
    },
    cp::{BuildError, Builder, Built},
    dp::{Batch, CommitCx, Cx, Module},
};

type Tree = Decode<Join<Join<Net4Attr, Net4Attr>, Join<PortAttr, PortAttr>>>;

/// The module of the tree; never run.
struct TreeModule;

impl Module for TreeModule {
    const NAME: &'static str = "classify-bench";
    type Config = ShmBox<Tree>;
    type Prepared<'p> = ();

    fn handle<'id>(_: &mut Cx<'id, Self>, _: &mut Batch<'id>) {}

    fn prepare<'p>(_: &CommitCx<'p, Self>) {}
}

/// `struct yanet_sys_bench_query` of the C query.
#[repr(C)]
struct CQuery {
    src: *const bindings::classify_attr_net4,
    dst: *const bindings::classify_attr_net4,
    sport: *const bindings::classify_attr_port,
    dport: *const bindings::classify_attr_port,
    nets: *const bindings::value_table,
    ports: *const bindings::value_table,
    root: *const bindings::value_table,
    rules: *const bindings::vline,
}

/// `struct yanet_sys_bench_key` of the C query.
#[derive(Clone, Copy)]
#[repr(C)]
struct Key {
    src: [u8; 4],
    dst: [u8; 4],
    sport: u16,
    dport: u16,
}

#[link(name = "yanet_sys_classify_tests", kind = "static", modifiers = "+whole-archive")]
unsafe extern "C" {
    fn yanet_sys_bench_c_run(query: *const CQuery, keys: *const Key, count: u32, rounds: u64) -> u64;
    fn yanet_sys_bench_c_run_burst(query: *const CQuery, keys: *const Key, count: u32, rounds: u64) -> u64;
}

/// A rule of a ruleset: its source and destination networks and ports, an
/// empty set matching every value.
struct BenchRule {
    src: Vec<Net4>,
    dst: Vec<Net4>,
    sports: Vec<PortRange>,
    dports: Vec<PortRange>,
}

/// The rule of destination port 443.
fn https() -> BenchRule {
    BenchRule {
        src: vec![],
        dst: vec![],
        sports: vec![],
        dports: vec![PortRange { from: 443, to: 443 }],
    }
}

/// The rules of a ruleset by name.
fn ruleset(name: &str) -> Vec<BenchRule> {
    match name {
        "one" => vec![https()],
        "skip" => {
            // 10.0.0.0/24 to 10.3.249.0/24.
            let dst = (0..1000u32)
                .map(|idx| Net4 {
                    addr: [10, (idx / 250) as u8, (idx % 250) as u8, 0],
                    mask: [255, 255, 255, 0],
                })
                .collect();
            vec![
                BenchRule {
                    src: vec![],
                    dst,
                    sports: vec![],
                    dports: vec![],
                },
                https(),
            ]
        }
        "acl" => acl(),
        _ => panic!("no ruleset {name}"),
    }
}

/// A seeded xorshift generator, so a ruleset and its keys are the same in
/// every process.
struct Rng(u64);

impl Rng {
    fn next(&mut self) -> u64 {
        let Self(state) = self;
        *state ^= *state << 13;
        *state ^= *state >> 7;
        *state ^= *state << 17;
        *state
    }

    /// A value below the bound.
    fn below(&mut self, bound: u64) -> u64 {
        self.next() % bound
    }
}

/// The rules of the `acl` ruleset.
const ACL_RULES: usize = 2000;

/// The destination ports the `acl` rules name.
const ACL_PORTS: [u16; 16] = [
    22, 25, 53, 80, 110, 123, 143, 161, 389, 443, 636, 993, 1194, 3306, 5432, 8080,
];

/// A random network of a prefix length between the bounds.
fn random_net(rng: &mut Rng, shortest: u64, longest: u64) -> Net4 {
    let len = shortest + rng.below(longest - shortest + 1);
    let mask = u32::MAX.checked_shl(32 - len as u32).unwrap_or(0);
    Net4 {
        addr: (rng.next() as u32 & mask).to_be_bytes(),
        mask: mask.to_be_bytes(),
    }
}

/// An ACL of [`ACL_RULES`] rules: sources of /8 to /24, destinations of /16 to
/// /32, a common destination port or a range of them, and a source port range
/// in one rule of ten; the last rule matches everything.
fn acl() -> Vec<BenchRule> {
    let mut rng = Rng(0x9e37_79b9_7f4a_7c15);
    let mut rules = (0..ACL_RULES - 1)
        .map(|_| {
            let port = ACL_PORTS[rng.below(ACL_PORTS.len() as u64) as usize];
            let dports = if rng.below(4) == 0 {
                vec![PortRange { from: port, to: port + 99 }]
            } else {
                vec![PortRange { from: port, to: port }]
            };
            let sports = if rng.below(10) == 0 {
                vec![PortRange { from: 1024, to: 65535 }]
            } else {
                vec![]
            };
            BenchRule {
                src: vec![random_net(&mut rng, 8, 24)],
                dst: vec![random_net(&mut rng, 16, 32)],
                sports,
                dports,
            }
        })
        .collect::<Vec<_>>();
    rules.push(BenchRule {
        src: vec![],
        dst: vec![],
        sports: vec![],
        dports: vec![],
    });
    rules
}

/// A random address of a network.
fn random_in(rng: &mut Rng, net: &Net4) -> [u8; 4] {
    let mask = u32::from_be_bytes(net.mask);
    (u32::from_be_bytes(net.addr) | (rng.next() as u32 & !mask)).to_be_bytes()
}

/// The keys of the `acl` ruleset: one of two made to match a random rule,
/// the other random.
fn acl_keys(rules: &[BenchRule]) -> Vec<Key> {
    let mut rng = Rng(0x2545_f491_4f6c_dd1d);
    (0..65536)
        .map(|idx| {
            if idx % 2 == 0 {
                let rule = &rules[rng.below(rules.len() as u64 - 1) as usize];
                let dport =
                    rule.dports[0].from + rng.below(u64::from(rule.dports[0].to - rule.dports[0].from) + 1) as u16;
                Key {
                    src: random_in(&mut rng, &rule.src[0]),
                    dst: random_in(&mut rng, &rule.dst[0]),
                    sport: 1024 + rng.below(64512) as u16,
                    dport,
                }
            } else {
                Key {
                    src: (rng.next() as u32).to_be_bytes(),
                    dst: (rng.next() as u32).to_be_bytes(),
                    sport: rng.next() as u16,
                    dport: rng.next() as u16,
                }
            }
        })
        .collect()
}

/// Bytes read to evict the caches: at least twice the last-level cache of a
/// CPU of up to 64 MiB of it, as the benchmark assumes.
const EVICT_BYTES: usize = 128 << 20;

/// Reads every cache line of the buffer, which pushes the classifier and the
/// keys out of every cache level.
#[inline(never)]
fn evict(buffer: &[u64]) {
    let mut sum = 0u64;
    for line in buffer.chunks(8) {
        sum = sum.wrapping_add(line[0]);
    }
    black_box(sum);
}

/// Times the variants over the `acl` ruleset in the `large` and `cold`
/// regimes, and prints the median nanoseconds per key and the median of the
/// ratios to C of each.
fn cold(owner: &ArenaOwner, reps: usize) {
    let rules = acl();
    let keys = acl_keys(&rules);
    let bench = Bench::new(owner, &rules);
    for (name, joint) in [
        ("nets", bench.parts.nets),
        ("ports", bench.parts.ports),
        ("root", bench.parts.root),
    ] {
        // SAFETY: a joint built whole in the arena and never freed.
        let table = unsafe { &*joint.cast::<bindings::value_table>() };
        let bytes = table.v_dim as usize * table.h_dim as usize * size_of::<u32>();
        eprintln!(
            "acl {name} joint: {} x {} classes, {} KiB",
            table.v_dim,
            table.h_dim,
            bytes >> 10
        );
    }
    let buffer = (0..EVICT_BYTES / 8).map(|idx| idx as u64).collect::<Vec<_>>();
    let mut rng = Rng(0xda94_2042_e4dd_58b5);
    let bursts = (0..256)
        .map(|_| rng.below(keys.len() as u64 / 32) as usize * 32)
        .collect::<Vec<_>>();
    for regime in ["large", "cold"] {
        let mut ns = VARIANTS.map(|_| Vec::new());
        let mut ratios = VARIANTS.map(|_| Vec::new());
        let mut sums = VARIANTS.map(|_| 0);
        for _ in 0..reps {
            let mut times = [0.0; VARIANTS.len()];
            for (idx, variant) in VARIANTS.iter().enumerate() {
                let (mut elapsed, mut sum, mut count) = (0.0, 0, 0);
                if regime == "large" {
                    let rounds = 20;
                    let start = Instant::now();
                    sum = bench.run(variant, &keys, rounds);
                    elapsed = start.elapsed().as_nanos() as f64;
                    count = rounds as usize * keys.len();
                } else {
                    for &at in &bursts {
                        evict(&buffer);
                        let start = Instant::now();
                        sum += bench.run(variant, &keys[at..at + 32], 1);
                        elapsed += start.elapsed().as_nanos() as f64;
                        count += 32;
                    }
                }
                sums[idx] = sum;
                times[idx] = elapsed / count as f64;
                ns[idx].push(times[idx]);
            }
            for idx in 0..VARIANTS.len() {
                ratios[idx].push(times[idx] / times[0]);
            }
        }
        assert!(
            sums.iter().all(|&sum| sum == sums[0]),
            "the variants disagree: {sums:?}"
        );
        for (idx, variant) in VARIANTS.iter().enumerate() {
            println!(
                "acl\t{regime}\t{variant}\t{:.3}\t{:.3}",
                median(&mut ns[idx]),
                median(&mut ratios[idx])
            );
        }
    }
}

/// The keys of a ruleset and a destination port: sources and source ports
/// spread, and a destination no rule names, under the skipped prefixes.
fn keys(ruleset: &str, dport: u16) -> Vec<Key> {
    let dst = if ruleset == "skip" {
        [10, 3, 250, 1]
    } else {
        [198, 51, 100, 1]
    };
    (0..32u16)
        .map(|idx| Key {
            src: [192, 0, 2, (idx * 7) as u8],
            dst,
            sport: 1024 + idx * 37,
            dport,
        })
        .collect()
}

/// The views of the rules, one per attribute.
struct Views<'a> {
    src: Vec<Nets4<'a>>,
    dst: Vec<Nets4<'a>>,
    sports: Vec<Ports<'a>>,
    dports: Vec<Ports<'a>>,
}

impl<'a> Views<'a> {
    fn new(rules: &'a [BenchRule]) -> Self {
        Self {
            src: rules.iter().map(|rule| Nets4::new(&rule.src).unwrap()).collect(),
            dst: rules.iter().map(|rule| Nets4::new(&rule.dst).unwrap()).collect(),
            sports: rules.iter().map(|rule| Ports::new(&rule.sports).unwrap()).collect(),
            dports: rules.iter().map(|rule| Ports::new(&rule.dports).unwrap()).collect(),
        }
    }
}

fn slots<T>(views: &[T]) -> Vec<Option<&T>> {
    views.iter().map(Some).collect()
}

/// A zeroed block of the arena for one `T`.
fn alloc<T>(owner: &ArenaOwner) -> *mut T {
    // SAFETY: a live context.
    let block = unsafe { bindings::yanet_sys_common_memory_balloc(owner.raw_context(), size_of::<T>()) }.cast::<T>();
    assert!(!block.is_null(), "the C arena is exhausted");
    // SAFETY: a fresh block of one `T`.
    unsafe { block.write_bytes(0, 1) };
    block
}

/// A C object with the stage of its classes.
type Staged<T> = (*mut T, *mut bindings::classifier);

/// Compiles an attribute's classifier as a C module does.
fn compile<A: Attribute>(owner: &ArenaOwner, name: &CStr, rules: &[Option<&A>]) -> Staged<A::Classifier> {
    let (classifier, stage) = (alloc::<A::Classifier>(owner), alloc::<bindings::classifier>(owner));
    // SAFETY: each rule is a view of the attribute, borrowed for the call;
    // the outputs are zeroed blocks of the arena.
    let status = unsafe {
        A::compile(
            owner.raw_context(),
            name.as_ptr(),
            rules.as_ptr().cast(),
            rules.len() as u32,
            classifier,
            stage,
        )
    };
    assert_eq!(0, status, "compile {name:?}");
    (classifier, stage)
}

/// Joins two stages as a C module does.
fn join<L, R>(owner: &ArenaOwner, name: &CStr, left: Staged<L>, right: Staged<R>, rules: u32) -> Staged<Joint> {
    let (joint, stage) = (alloc::<Joint>(owner), alloc::<bindings::classifier>(owner));
    // SAFETY: both stages were filled over the rule count.
    let status = unsafe {
        bindings::yanet_sys_classify_join(
            owner.raw_context(),
            name.as_ptr(),
            left.1,
            right.1,
            rules,
            joint.cast(),
            stage,
        )
    };
    assert_eq!(0, status, "join {name:?}");
    (joint, stage)
}

/// The parts of a ruleset's classifier as a C module composes it.
struct Parts {
    src: *mut Net4Attr,
    dst: *mut Net4Attr,
    sport: *mut PortAttr,
    dport: *mut PortAttr,
    nets: *mut Joint,
    ports: *mut Joint,
    root: *mut Joint,
    rules: *mut RuleMap,
}

fn parts(owner: &ArenaOwner, rules: &[BenchRule]) -> Parts {
    let views = Views::new(rules);
    let count = rules.len() as u32;
    let src = compile(owner, c"src", &slots(&views.src));
    let dst = compile(owner, c"dst", &slots(&views.dst));
    let sport = compile(owner, c"sport", &slots(&views.sports));
    let dport = compile(owner, c"dport", &slots(&views.dports));
    let nets = join(owner, c"nets", src, dst, count);
    let ports = join(owner, c"ports", sport, dport, count);
    let root = join(owner, c"root", nets, ports, count);
    let map = alloc::<RuleMap>(owner);
    let projection = vec![Some(&()); rules.len()];
    // SAFETY: the root stage was filled over the rule count of the
    // projection.
    let status = unsafe {
        bindings::yanet_sys_classify_decode(
            owner.raw_context(),
            c"rules".as_ptr(),
            root.1,
            projection.as_ptr().cast(),
            count,
            map.cast(),
        )
    };
    assert_eq!(0, status, "decode");
    Parts {
        src: src.0,
        dst: dst.0,
        sport: sport.0,
        dport: dport.0,
        nets: nets.0,
        ports: ports.0,
        root: root.0,
        rules: map,
    }
}

fn tree(owner: &ArenaOwner, rules: &[BenchRule]) -> Built<TreeModule, ArenaOwner> {
    let views = Views::new(rules);
    let projection = vec![Some(&()); rules.len()];
    Builder::build(owner, "bench", |scope| -> Result<_, BuildError<arena::Never>> {
        let src = scope.compile(c"src", &slots(&views.src))?;
        let dst = scope.compile(c"dst", &slots(&views.dst))?;
        let sport = scope.compile(c"sport", &slots(&views.sports))?;
        let dport = scope.compile(c"dport", &slots(&views.dports))?;
        let nets = scope.join_branches(c"nets", src, dst)?;
        let ports = scope.join_branches(c"ports", sport, dport)?;
        let root = scope.join_branches(c"root", nets, ports)?;
        scope.decode_branch(c"rules", root, &projection)
    })
    .unwrap()
}

/// The word of a rule as C sums it.
fn word(rule: Rule) -> u64 {
    rule.index().map_or(u64::from(u32::MAX), |idx| idx as u64)
}

/// Queries the keys once per round, a burst of [`BURST`] at a time, and
/// returns the sum of the rules.
#[inline(never)]
fn run_burst(keys: &[Key], rounds: u64, query: impl Fn(&[Key], &mut [Rule])) -> u64 {
    let mut sum = 0;
    let mut rules = [Rule::NONE; BURST];
    for _ in 0..rounds {
        for burst in black_box(keys).chunks(BURST) {
            let rules = &mut rules[..burst.len()];
            query(burst, rules);
            sum += rules.iter().map(|&rule| word(rule)).sum::<u64>();
        }
    }
    sum
}

/// Queries each key once per round and returns the sum of the rules.
#[inline(never)]
fn run_rust(keys: &[Key], rounds: u64, query: impl Fn(&Key) -> Rule) -> u64 {
    let mut sum = 0;
    for _ in 0..rounds {
        // The keys and the classifier may have changed: each round reads
        // them again, as the C loop does.
        for key in black_box(keys) {
            sum += word(query(key));
        }
    }
    sum
}

/// The variants of the query over a ruleset's classifiers.
struct Bench<'o> {
    owner: &'o ArenaOwner,
    parts: Parts,
    c: CQuery,
    tree: &'o Tree,
}

impl<'o> Bench<'o> {
    fn new(owner: &'o ArenaOwner, rules: &[BenchRule]) -> Self {
        let parts = parts(owner, rules);
        let c = CQuery {
            src: parts.src.cast(),
            dst: parts.dst.cast(),
            sport: parts.sport.cast(),
            dport: parts.dport.cast(),
            nets: parts.nets.cast(),
            ports: parts.ports.cast(),
            root: parts.root.cast(),
            rules: parts.rules.cast(),
        };
        // The configuration is never released: a dropped `Built` leaves it.
        let tree = owner.body(&tree(owner, rules)).get(owner);
        Self { owner, parts, c, tree }
    }

    fn run(&self, variant: &str, keys: &[Key], rounds: u64) -> u64 {
        let cx = self.owner;
        match variant {
            // SAFETY: the parts are built whole, the keys a live slice.
            "c" => unsafe { yanet_sys_bench_c_run(&self.c, keys.as_ptr(), keys.len() as u32, rounds) },
            "stages" => {
                // SAFETY: the parts are built whole in the arena and never
                // freed.
                let p = unsafe {
                    (
                        &*self.parts.src,
                        &*self.parts.dst,
                        &*self.parts.sport,
                        &*self.parts.dport,
                        &*self.parts.nets,
                        &*self.parts.ports,
                        &*self.parts.root,
                        &*self.parts.rules,
                    )
                };
                run_rust(keys, rounds, |key| {
                    let (mut src, mut dst, mut sport, mut dport) = ([0], [0], [0], [0]);
                    p.0.lookup(cx, &[Addr4::from(key.src)], &mut src);
                    p.1.lookup(cx, &[Addr4::from(key.dst)], &mut dst);
                    p.2.lookup(cx, &[key.sport], &mut sport);
                    p.3.lookup(cx, &[key.dport], &mut dport);
                    let (mut nets, mut ports, mut root) = ([0], [0], [0]);
                    p.4.lookup(cx, &src, &dst, &mut nets);
                    p.5.lookup(cx, &sport, &dport, &mut ports);
                    p.6.lookup(cx, &nets, &ports, &mut root);
                    let mut rule = [Rule::NONE];
                    p.7.lookup(cx, &root, &mut rule);
                    rule[0]
                })
            }
            "tree" => run_rust(keys, rounds, |key| {
                let nets = (Addr4::from(key.src), Addr4::from(key.dst));
                self.tree.lookup(cx, (nets, (key.sport, key.dport)))
            }),
            // SAFETY: as for `c`.
            "c-burst" => unsafe { yanet_sys_bench_c_run_burst(&self.c, keys.as_ptr(), keys.len() as u32, rounds) },
            "stages-burst" => {
                // SAFETY: as for `stages`.
                let p = unsafe {
                    (
                        &*self.parts.src,
                        &*self.parts.dst,
                        &*self.parts.sport,
                        &*self.parts.dport,
                        &*self.parts.nets,
                        &*self.parts.ports,
                        &*self.parts.root,
                        &*self.parts.rules,
                    )
                };
                run_burst(keys, rounds, |burst, rules| {
                    let n = burst.len();
                    let mut src_addrs = [Addr4::from([0; 4]); BURST];
                    let mut dst_addrs = [Addr4::from([0; 4]); BURST];
                    let (mut sports, mut dports) = ([0; BURST], [0; BURST]);
                    for (idx, key) in burst.iter().enumerate() {
                        (src_addrs[idx], dst_addrs[idx]) = (Addr4::from(key.src), Addr4::from(key.dst));
                        (sports[idx], dports[idx]) = (key.sport, key.dport);
                    }
                    let (mut src, mut dst, mut sport, mut dport) = ([0; BURST], [0; BURST], [0; BURST], [0; BURST]);
                    p.0.lookup(cx, &src_addrs[..n], &mut src);
                    p.1.lookup(cx, &dst_addrs[..n], &mut dst);
                    p.2.lookup(cx, &sports[..n], &mut sport);
                    p.3.lookup(cx, &dports[..n], &mut dport);
                    let (mut nets, mut ports, mut root) = ([0; BURST], [0; BURST], [0; BURST]);
                    p.4.lookup(cx, &src[..n], &dst[..n], &mut nets);
                    p.5.lookup(cx, &sport[..n], &dport[..n], &mut ports);
                    p.6.lookup(cx, &nets[..n], &ports[..n], &mut root);
                    p.7.lookup(cx, &root[..n], rules);
                })
            }
            "tree-burst" => run_burst(keys, rounds, |burst, rules| {
                self.tree.lookup_burst(
                    cx,
                    burst,
                    |key| ((Addr4::from(key.src), Addr4::from(key.dst)), (key.sport, key.dport)),
                    rules,
                );
            }),
            _ => panic!("no variant {variant}"),
        }
    }
}

const VARIANTS: [&str; 6] = ["c", "stages", "tree", "c-burst", "stages-burst", "tree-burst"];

fn median(values: &mut [f64]) -> f64 {
    values.sort_by(f64::total_cmp);
    values[values.len() / 2]
}

fn main() {
    let args = env::args().skip(1).filter(|arg| arg != "--bench").collect::<Vec<_>>();
    let owner = ArenaOwner::new();
    if args.first().map(String::as_str) == Some("--only") {
        let [_, variant, set, port, rounds] = args.as_slice() else {
            panic!("--only VARIANT RULESET PORT ROUNDS");
        };
        let bench = Bench::new(&owner, &ruleset(set));
        let keys = keys(set, port.parse().unwrap());
        println!("{}", bench.run(variant, &keys, rounds.parse().unwrap()));
        return;
    }
    if args.first().map(String::as_str) == Some("--cold") {
        cold(&owner, args.get(1).map_or(5, |arg| arg.parse().unwrap()));
        return;
    }
    let rounds = args.first().map_or(200_000, |arg| arg.parse().unwrap());
    let reps = args.get(1).map_or(9, |arg| arg.parse().unwrap());
    for set in ["one", "skip"] {
        let bench = Bench::new(&owner, &ruleset(set));
        for port in [80, 443] {
            let keys = keys(set, port);
            let mut ns = VARIANTS.map(|_| Vec::new());
            let mut ratios = VARIANTS.map(|_| Vec::new());
            let mut sums = VARIANTS.map(|_| 0);
            for _ in 0..reps {
                let mut times = [0.0; VARIANTS.len()];
                for (idx, variant) in VARIANTS.iter().enumerate() {
                    let start = Instant::now();
                    sums[idx] = bench.run(variant, &keys, rounds);
                    times[idx] = start.elapsed().as_nanos() as f64 / (rounds as f64 * keys.len() as f64);
                    ns[idx].push(times[idx]);
                }
                for idx in 0..VARIANTS.len() {
                    ratios[idx].push(times[idx] / times[0]);
                }
            }
            assert!(
                sums.iter().all(|&sum| sum == sums[0]),
                "the variants disagree: {sums:?}"
            );
            for (idx, variant) in VARIANTS.iter().enumerate() {
                println!(
                    "{set}\t{port}\t{variant}\t{:.3}\t{:.3}",
                    median(&mut ns[idx]),
                    median(&mut ratios[idx])
                );
            }
        }
    }
}
