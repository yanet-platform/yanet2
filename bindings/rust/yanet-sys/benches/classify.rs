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
//! - `tree`: the lookup of the classifier tree.
//!
//! `cargo bench -p yanet-sys --bench classify [-- ROUNDS REPS]` prints, per
//! ruleset, port and variant, the median nanoseconds per key and the median
//! of its ratios to C. `-- --only VARIANT RULESET PORT ROUNDS` runs one
//! variant alone, for `perf stat`; its instructions per key are those of a run
//! less those of a run of zero rounds, over the rounds times the keys.

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
        Addr4, Attribute, Decode, Join, Joint, Net4, Net4Attr, Nets4, PortAttr, PortRange, Ports, Rule, RuleMap,
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
        _ => panic!("no ruleset {name}"),
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
        let src = scope.branch(c"src", &slots(&views.src))?;
        let dst = scope.branch(c"dst", &slots(&views.dst))?;
        let sport = scope.branch(c"sport", &slots(&views.sports))?;
        let dport = scope.branch(c"dport", &slots(&views.dports))?;
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
            _ => panic!("no variant {variant}"),
        }
    }
}

const VARIANTS: [&str; 3] = ["c", "stages", "tree"];

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
