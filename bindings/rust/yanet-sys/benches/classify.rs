//! The classify query of a Rust module against the inline C query of a C
//! module, one key or one burst of keys per query, over the same classifier.
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
//! - `tree`: the lookup of the classifier tree;
//! - `c-burst`, `tree-burst`: the same over bursts of 32 keys, each stage over
//!   the whole burst before the next, as the batch query of a C module runs.
//!
//! `cargo bench -p yanet-sys --bench classify [-- ROUNDS REPS]` prints, per
//! ruleset, port and variant, the median nanoseconds per key and the median
//! of its ratios to C. `-- --only VARIANT RULESET PORT ROUNDS` runs one
//! variant alone, for `perf stat`; its instructions per key are those of a run
//! less those of a run of zero rounds, over the rounds times the keys.
//!
//! Those keys repeat and the classifier stays in cache. `-- --workload
//! [REPS]` measures the shape of a dataplane worker instead, over `acl`, a
//! seeded ruleset of 2000 rules of random source and destination networks and
//! common destination ports, and 65536 keys, half made to match a random
//! rule and half random. The worker hands a module a batch of up to 32
//! packets per receive queue, up to 64 at a time, and between two batches
//! runs the rest of its pipeline, which takes part of the caches; so each
//! batch of 32 or 64 keys, at a random place, follows the reads and writes of
//! a given number of bytes of other memory, from none to more than the
//! last-level cache, and only the lookups of the batch are timed.
//!
//! `-- --paths-workload [REPS]` measures the same over `paths`, the rules of
//! `acl` split over a TCP and a UDP path with one of eight restricting no
//! port, as a [`Paths`] node composes them against the C query of the ACL
//! module's UDP path over the same composition: the networks joined into a
//! core, the ports of the rules that restrict one joined, and per path a rule
//! table of both and a rule map of the core, of which a lookup takes the
//! earlier rule.

#[path = "../tests/support/arena.rs"]
#[allow(dead_code, reason = "the benchmark checks no release")]
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
        Addr4, Attribute, BURST, Decode, Join, Joint, Net4, Net4Attr, Nets4, Paths, PortAttr, PortRange, Ports, Rule,
        RuleMap, RuleTable,
    },
    cp::{BuildError, Builder, Built, PathRules},
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

/// The paths of the `paths` ruleset: the networks joined into the core, the
/// ports joined, a TCP and a UDP path.
type PathsTree = Paths<Join<Net4Attr, Net4Attr>, Join<PortAttr, PortAttr>, 2>;

/// The module of the paths; never run.
struct PathsModule;

impl Module for PathsModule {
    const NAME: &'static str = "classify-bench-paths";
    type Config = ShmBox<PathsTree>;
    type Prepared<'p> = ();

    fn handle<'id>(_: &mut Cx<'id, Self>, _: &mut Batch<'id>) {}

    fn prepare<'p>(_: &CommitCx<'p, Self>) {}
}

/// `struct yanet_sys_bench_paths` of the C paths query.
#[repr(C)]
struct CPaths {
    src: *const bindings::classify_attr_net4,
    dst: *const bindings::classify_attr_net4,
    sport: *const bindings::classify_attr_port,
    dport: *const bindings::classify_attr_port,
    nets: *const bindings::value_table,
    ports: *const bindings::value_table,
    tables: [*const bindings::rule_table; 2],
    maps: [*const bindings::vline; 2],
}

/// `struct yanet_sys_bench_path_key` of the C paths query.
#[derive(Clone, Copy)]
#[repr(C)]
struct PathKey {
    key: Key,
    path: u32,
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
    fn yanet_sys_bench_c_run_paths(query: *const CPaths, keys: *const PathKey, count: u32, rounds: u64) -> u64;
    fn yanet_sys_bench_c_run_paths_burst(query: *const CPaths, keys: *const PathKey, count: u32, rounds: u64) -> u64;
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

/// The rules of the `paths` ruleset: those of `acl`, each on the TCP or the
/// UDP path, and one of eight restricting no port; the last rule, on both
/// paths, matches everything. A rule's path is `None` for both.
fn paths_acl() -> (Vec<BenchRule>, Vec<Option<usize>>) {
    let mut rng = Rng(0x6a09_e667_f3bc_c908);
    let mut rules = acl();
    let paths = rules
        .iter_mut()
        .enumerate()
        .map(|(idx, rule)| {
            if idx == ACL_RULES - 1 {
                return None;
            }
            if rng.below(8) == 0 {
                rule.sports.clear();
                rule.dports.clear();
            }
            Some(rng.below(2) as usize)
        })
        .collect();
    (rules, paths)
}

/// The keys of the `paths` ruleset: one of two made to match a random rule on
/// its path, the other random on a random path.
fn paths_keys(rules: &[BenchRule], paths: &[Option<usize>]) -> Vec<PathKey> {
    let mut rng = Rng(0xbb67_ae85_84ca_a73b);
    (0..65536)
        .map(|idx| {
            if idx % 2 == 0 {
                let at = rng.below(rules.len() as u64 - 1) as usize;
                let rule = &rules[at];
                let dport = rule.dports.first().map_or(rng.next() as u16, |range| {
                    range.from + rng.below(u64::from(range.to - range.from) + 1) as u16
                });
                PathKey {
                    key: Key {
                        src: random_in(&mut rng, &rule.src[0]),
                        dst: random_in(&mut rng, &rule.dst[0]),
                        sport: 1024 + rng.below(64512) as u16,
                        dport,
                    },
                    path: paths[at].unwrap() as u32,
                }
            } else {
                PathKey {
                    key: Key {
                        src: (rng.next() as u32).to_be_bytes(),
                        dst: (rng.next() as u32).to_be_bytes(),
                        sport: rng.next() as u16,
                        dport: rng.next() as u16,
                    },
                    path: rng.below(2) as u32,
                }
            }
        })
        .collect()
}

/// Bytes of the memory the work between two batches walks: at least twice the
/// last-level cache of a CPU with up to 64 MiB of it, as the benchmark
/// assumes, so the walk touches lines no batch used lately.
const OTHER_BYTES: usize = 256 << 20;

/// The work between two batches: reads and writes one word of each cache line
/// of the next bytes of other memory, from a cursor that wraps around it, as
/// the rest of a pipeline reads and writes packets and state.
#[inline(never)]
fn other_work(memory: &mut [u64], cursor: &mut usize, bytes: usize) {
    let words = memory.len();
    for _ in 0..bytes / 64 {
        memory[*cursor] = memory[*cursor].wrapping_add(1);
        *cursor = (*cursor + 8) % words;
    }
    black_box(&mut *memory);
}

/// The batch sizes of [`workload`]: one receive burst, and the most a module
/// handles at once.
const BATCHES: [usize; 2] = [32, 64];

/// The bytes of other memory each batch of [`workload`] follows.
const OTHER: [usize; 6] = [0, 64 << 10, 256 << 10, 1 << 20, 4 << 20, 16 << 20];

/// The batches timed per variant and repetition of [`workload`].
const WORKLOAD_BATCHES: usize = 512;

/// Times the variants over the `acl` ruleset in the shape of a worker, and
/// prints per batch size, bytes of other memory and variant the median
/// nanoseconds per key and the median of the ratios to C.
fn acl_workload(owner: &ArenaOwner, reps: usize) {
    let rules = acl();
    let keys = acl_keys(&rules);
    // A copy of the classifier for each variant, so that no variant reads
    // tables another has just brought into the caches: two of the parts of
    // a C module and two trees, of three builds.
    let benches: [Bench<'_>; 3] = core::array::from_fn(|_| Bench::new(owner, &rules));
    let bench_of = |variant: usize| match VARIANTS[variant] {
        "c" | "tree" => &benches[0],
        "tree-burst" => &benches[1],
        _ => &benches[2],
    };
    for (name, joint) in [
        ("nets", benches[0].parts.nets),
        ("ports", benches[0].parts.ports),
        ("root", benches[0].parts.root),
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
    workload(&keys, VARIANTS, reps, |idx, batch| {
        bench_of(idx).run(VARIANTS[idx], batch, 1)
    });
}

/// Times the variants over the `paths` ruleset in the shape of a worker, as
/// [`acl_workload`] does.
fn paths_workload(owner: &ArenaOwner, reps: usize) {
    let (rules, paths) = paths_acl();
    let keys = paths_keys(&rules, &paths);
    let benches: [PathsBench<'_>; 3] = core::array::from_fn(|_| PathsBench::new(owner, &rules, &paths));
    let bench_of = |variant: usize| match PATHS_VARIANTS[variant] {
        "c" | "paths" => &benches[0],
        "paths-burst" => &benches[1],
        _ => &benches[2],
    };
    workload(&keys, PATHS_VARIANTS, reps, |idx, batch| {
        bench_of(idx).run(PATHS_VARIANTS[idx], batch, 1)
    });
}

/// Times each variant over batches of the keys at random places, each after
/// the work between two batches, and prints per batch size, bytes of other
/// memory and variant the median nanoseconds per key and the median of the
/// ratios to the first variant.
fn workload<K>(keys: &[K], variants: [&str; 4], reps: usize, run: impl Fn(usize, &[K]) -> u64) {
    let mut memory = (0..OTHER_BYTES / 8).map(|idx| idx as u64).collect::<Vec<_>>();
    let mut cursor = 0;
    let mut rng = Rng(0xda94_2042_e4dd_58b5);
    for batch in BATCHES {
        let places = (0..WORKLOAD_BATCHES)
            .map(|_| rng.below((keys.len() / batch) as u64) as usize * batch)
            .collect::<Vec<_>>();
        for other in OTHER {
            let mut ns = variants.map(|_| Vec::new());
            let mut ratios = variants.map(|_| Vec::new());
            let mut sums = variants.map(|_| 0);
            // One pass untimed, then every batch through every variant, each
            // after its own work and first in turn, so a drift of the machine
            // and the keys a batch shares fall on all of them alike.
            for rep in 0..=reps {
                let mut elapsed = [0.0; 4];
                sums = variants.map(|_| 0);
                for (place, &at) in places.iter().enumerate() {
                    for turn in 0..variants.len() {
                        let idx = (place + turn) % variants.len();
                        other_work(&mut memory, &mut cursor, other);
                        let start = Instant::now();
                        sums[idx] += run(idx, &keys[at..at + batch]);
                        elapsed[idx] += start.elapsed().as_nanos() as f64;
                    }
                }
                if rep == 0 {
                    continue;
                }
                let times = elapsed.map(|elapsed| elapsed / (places.len() * batch) as f64);
                for idx in 0..variants.len() {
                    ns[idx].push(times[idx]);
                    ratios[idx].push(times[idx] / times[0]);
                }
            }
            assert!(
                sums.iter().all(|&sum| sum == sums[0]),
                "the variants disagree: {sums:?}"
            );
            for (idx, variant) in variants.iter().enumerate() {
                println!(
                    "{batch}\t{}\t{variant}\t{:.3}\t{:.3}",
                    other >> 10,
                    median(&mut ns[idx]),
                    median(&mut ratios[idx])
                );
            }
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

/// A C object with the stage of its classes.
type Staged<T> = (*mut T, *mut bindings::classifier);

/// Compiles an attribute's classifier as a C module does.
fn compile<A: Attribute>(owner: &ArenaOwner, name: &CStr, rules: &[Option<&A>]) -> Staged<A::Classifier> {
    let (classifier, stage) = (owner.zeroed::<A::Classifier>(), owner.zeroed::<bindings::classifier>());
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
    let (joint, stage) = (owner.zeroed::<Joint>(), owner.zeroed::<bindings::classifier>());
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
    let map = owner.zeroed::<RuleMap>();
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
        let nets = scope.join(c"nets", src, dst)?;
        let ports = scope.join(c"ports", sport, dport)?;
        let root = scope.join(c"root", nets, ports)?;
        scope.decode(c"rules", root, &projection)
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
fn run_burst<K>(keys: &[K], rounds: u64, query: impl Fn(&[K], &mut [Rule])) -> u64 {
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
fn run_rust<K>(keys: &[K], rounds: u64, query: impl Fn(&K) -> Rule) -> u64 {
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
            "tree" => run_rust(keys, rounds, |key| {
                let nets = (Addr4::from(key.src), Addr4::from(key.dst));
                self.tree.lookup(cx, (nets, (key.sport, key.dport)))
            }),
            // SAFETY: as for `c`.
            "c-burst" => unsafe { yanet_sys_bench_c_run_burst(&self.c, keys.as_ptr(), keys.len() as u32, rounds) },
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

const VARIANTS: [&str; 4] = ["c", "tree", "c-burst", "tree-burst"];

/// The projections of the rules of a path: those with a port, and those
/// without.
fn path_projections<'r>(
    rules: &[BenchRule],
    paths: &[Option<usize>],
    path: usize,
) -> (Vec<Option<&'r ()>>, Vec<Option<&'r ()>>) {
    let on = |idx: usize, ported: bool| {
        let rule = &rules[idx];
        let has_ports = !rule.sports.is_empty() || !rule.dports.is_empty();
        (paths[idx].is_none_or(|of| of == path) && has_ports == ported).then_some(&())
    };
    (
        (0..rules.len()).map(|idx| on(idx, true)).collect(),
        (0..rules.len()).map(|idx| on(idx, false)).collect(),
    )
}

/// The port views of the rules that restrict a port, the others out.
fn ported<'v>(rules: &[BenchRule], views: &'v [Ports<'_>]) -> Vec<Option<&'v Ports<'v>>> {
    views
        .iter()
        .zip(rules)
        .map(|(view, rule)| (!rule.sports.is_empty() || !rule.dports.is_empty()).then_some(view))
        .collect()
}

/// The C paths query over parts of the paths composed through the shims, as
/// the ACL module composes them.
fn c_paths(owner: &ArenaOwner, rules: &[BenchRule], paths: &[Option<usize>]) -> CPaths {
    let views = Views::new(rules);
    let count = rules.len() as u32;
    let src = compile(owner, c"src", &slots(&views.src));
    let dst = compile(owner, c"dst", &slots(&views.dst));
    let sport = compile(owner, c"sport", &ported(rules, &views.sports));
    let dport = compile(owner, c"dport", &ported(rules, &views.dports));
    let nets = join(owner, c"nets", src, dst, count);
    let ports = join(owner, c"ports", sport, dport, count);
    let [tcp, udp] = [0, 1].map(|path| path_projections(rules, paths, path));
    let table = |name: &CStr, projection: &[Option<&()>]| {
        let table = owner.zeroed::<RuleTable>();
        // SAFETY: both stages were filled over the rule count of the
        // projection.
        let status = unsafe {
            bindings::yanet_sys_classify_join_rules(
                owner.raw_context(),
                name.as_ptr(),
                nets.1,
                ports.1,
                projection.as_ptr().cast(),
                count,
                table.cast(),
            )
        };
        assert_eq!(0, status, "join rules {name:?}");
        table.cast_const().cast()
    };
    let map = |name: &CStr, projection: &[Option<&()>]| {
        let map = owner.zeroed::<RuleMap>();
        // SAFETY: the stage was filled over the rule count of the
        // projection.
        let status = unsafe {
            bindings::yanet_sys_classify_decode(
                owner.raw_context(),
                name.as_ptr(),
                nets.1,
                projection.as_ptr().cast(),
                count,
                map.cast(),
            )
        };
        assert_eq!(0, status, "decode {name:?}");
        map.cast_const().cast()
    };
    CPaths {
        src: src.0.cast(),
        dst: dst.0.cast(),
        sport: sport.0.cast(),
        dport: dport.0.cast(),
        nets: nets.0.cast(),
        ports: ports.0.cast(),
        tables: [table(c"tcp table", &tcp.0), table(c"udp table", &udp.0)],
        maps: [map(c"tcp map", &tcp.1), map(c"udp map", &udp.1)],
    }
}

fn paths_tree(owner: &ArenaOwner, rules: &[BenchRule], paths: &[Option<usize>]) -> Built<PathsModule, ArenaOwner> {
    let views = Views::new(rules);
    let [tcp, udp] = [0, 1].map(|path| path_projections(rules, paths, path));
    Builder::build(owner, "bench-paths", |scope| -> Result<_, BuildError<arena::Never>> {
        let src = scope.compile(c"src", &slots(&views.src))?;
        let dst = scope.compile(c"dst", &slots(&views.dst))?;
        let sport = scope.compile(c"sport", &ported(rules, &views.sports))?;
        let dport = scope.compile(c"dport", &ported(rules, &views.dports))?;
        let nets = scope.join(c"nets", src, dst)?;
        let ports = scope.join(c"ports", sport, dport)?;
        scope.join_paths(
            nets,
            ports,
            [
                PathRules {
                    table: c"tcp table",
                    ported: &tcp.0,
                    map: c"tcp map",
                    plain: &tcp.1,
                },
                PathRules {
                    table: c"udp table",
                    ported: &udp.0,
                    map: c"udp map",
                    plain: &udp.1,
                },
            ],
        )
    })
    .unwrap()
}

/// The variants of the query over the classifiers of the `paths` ruleset.
struct PathsBench<'o> {
    owner: &'o ArenaOwner,
    c: CPaths,
    paths: &'o PathsTree,
}

impl<'o> PathsBench<'o> {
    fn new(owner: &'o ArenaOwner, rules: &[BenchRule], paths: &[Option<usize>]) -> Self {
        let c = c_paths(owner, rules, paths);
        // The configuration is never released: a dropped `Built` leaves it.
        let tree = owner.body(&paths_tree(owner, rules, paths)).get(owner);
        Self { owner, c, paths: tree }
    }

    fn run(&self, variant: &str, keys: &[PathKey], rounds: u64) -> u64 {
        let cx = self.owner;
        let key_of = |key: &PathKey| {
            let PathKey { key, path } = *key;
            let nets = (Addr4::from(key.src), Addr4::from(key.dst));
            (path as usize, (nets, (key.sport, key.dport)))
        };
        match variant {
            // SAFETY: the parts are built whole, the keys a live slice.
            "c" => unsafe { yanet_sys_bench_c_run_paths(&self.c, keys.as_ptr(), keys.len() as u32, rounds) },
            "paths" => run_rust(keys, rounds, |key| {
                let (path, key) = key_of(key);
                self.paths.lookup(cx, path, key)
            }),
            // SAFETY: as for `c`.
            "c-burst" => unsafe {
                yanet_sys_bench_c_run_paths_burst(&self.c, keys.as_ptr(), keys.len() as u32, rounds)
            },
            "paths-burst" => run_burst(keys, rounds, |burst, rules| {
                self.paths.lookup_burst(cx, burst, key_of, rules);
            }),
            _ => panic!("no variant {variant}"),
        }
    }
}

const PATHS_VARIANTS: [&str; 4] = ["c", "paths", "c-burst", "paths-burst"];

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
    if args.first().map(String::as_str) == Some("--workload") {
        acl_workload(&owner, args.get(1).map_or(5, |arg| arg.parse().unwrap()));
        return;
    }
    if args.first().map(String::as_str) == Some("--paths-workload") {
        paths_workload(&owner, args.get(1).map_or(5, |arg| arg.parse().unwrap()));
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
