//! The build of a classifier of a production-shaped IPv6 ruleset, and the
//! cost of checking once that every class it holds fits the table that reads
//! it.
//!
//! The ruleset is seeded and synthetic, after the production profile the
//! ACL dataset benchmark (`modules/acl/tests/functional`) is calibrated
//! against, of its IPv6 rules: networks nest inside 64 supernets with a
//! weighted prefix length, and rules draw them from shared pools, sources
//! from one of 35 % of the rule count and destinations from one of 6 %. One
//! rule of ten names a source and a destination only; the others 1 to 3
//! sources, a destination and the destination ports of a popular port, a
//! random one or a range. A rule is of TCP or of UDP. Added to that profile,
//! one rule of a thousand names 100 to 10k sources, as a blocklist would; a
//! catch-all rule ends the set.
//!
//! By default the classifier is the IPv6 tree of the assign fixture: the
//! source and the destination joined, the protocol and the port joined, and
//! the rule table of both, each a C object built as the builder builds it.
//! The benchmark prints the time of each build step and the size of each
//! table, then scans every class against the bound the table that reads it
//! sets, as a validation at build would. `--acl-shape` builds instead after
//! the composition of the ACL module and prints its build steps and table
//! sizes only.
//!
//! `YANET_SYS_TEST_ARENA_MIB=65536 cargo bench -p yanet-sys --bench
//! classify_build -- [RULES] [--acl-shape]`, 200000 rules by default.

#[path = "../tests/support/arena.rs"]
#[allow(dead_code, reason = "the benchmark reads no configuration")]
mod arena;

use core::{ffi::CStr, hint::black_box, mem::size_of};
use std::{env, time::Instant};

use arena::ArenaOwner;
// The workspace denies unused crate dependencies; this benchmark names no
// crate it imports `as _`.
use netip as _;
use yanet_sdk_testing as _;
// The arena's root is not followed: the scan reads C's own pointers.
use yanet_shm as _;
use yanet_sys::{
    bindings,
    classify::{Attribute, Joint, LineRange, Lines, Net6, Nets6, PortRange, Ports, RuleTable},
};

/// A seeded xorshift generator, so a ruleset is the same in every process.
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

    /// A count between the bounds, log-uniform: small ones far more often.
    fn log_uniform(&mut self, low: u64, high: u64) -> usize {
        let (low, high) = (low as f64, high as f64);
        let unit = (self.next() >> 11) as f64 / (1u64 << 53) as f64;
        (low * (high / low).powf(unit)) as usize
    }
}

/// A rule: its source and destination networks, protocols and destination
/// ports, an empty set matching every value.
struct Rule {
    src: Vec<Net6>,
    dst: Vec<Net6>,
    protos: Vec<LineRange>,
    ports: Vec<PortRange>,
}

/// The supernets the networks nest in.
struct Supernets([[u8; 4]; 64]);

impl Supernets {
    fn new(rng: &mut Rng) -> Self {
        Self(core::array::from_fn(|_| {
            [0x2a, 0x02, rng.next() as u8, rng.next() as u8]
        }))
    }

    /// A network inside a random supernet, of a prefix length weighted as
    /// in production: /48 most, then /64, /56, /40 and single addresses.
    fn net(&self, rng: &mut Rng) -> Net6 {
        let Self(supernets) = self;
        let len = match rng.below(100) {
            0..45 => 48,
            45..75 => 64,
            75..90 => 56,
            90..95 => 40,
            _ => 128,
        };
        let mut addr = [0u8; 16];
        addr[..4].copy_from_slice(&supernets[rng.below(64) as usize]);
        for byte in &mut addr[4..len / 8] {
            *byte = rng.next() as u8;
        }
        let mask = u128::MAX.checked_shl(128 - len as u32).unwrap_or(0);
        Net6 {
            addr: (u128::from_be_bytes(addr) & mask).to_be_bytes(),
            mask: mask.to_be_bytes(),
        }
    }
}

/// The destination ports of a rule: a popular port, a random one, or a range
/// of up to 1000.
fn ports(rng: &mut Rng) -> Vec<PortRange> {
    const POPULAR: [u16; 8] = [80, 443, 22, 53, 123, 8080, 3128, 25];
    let range = match rng.below(100) {
        0..55 => {
            let port = POPULAR[rng.below(8) as usize];
            PortRange { from: port, to: port }
        }
        55..80 => {
            let port = 1 + rng.below(65000) as u16;
            PortRange { from: port, to: port }
        }
        _ => {
            let from = 1 + rng.below(60000) as u16;
            PortRange {
                from,
                to: from + rng.below(1000) as u16,
            }
        }
    };
    vec![range]
}

/// The protocol of a rule: TCP seven times of ten, UDP otherwise.
fn protos(rng: &mut Rng) -> Vec<LineRange> {
    let proto = if rng.below(10) < 7 { 6 } else { 17 };
    vec![LineRange { from: proto, to: proto }]
}

/// Networks of a pool, picked at random.
fn pick(rng: &mut Rng, pool: &[Net6], count: usize) -> Vec<Net6> {
    (0..count)
        .map(|_| pool[rng.below(pool.len() as u64) as usize])
        .collect()
}

/// The ruleset of the benchmark, a catch-all rule last.
fn ruleset(count: usize) -> Vec<Rule> {
    let mut rng = Rng(0x5851_f42d_4c95_7f2d);
    let supernets = Supernets::new(&mut rng);
    let src_pool = (0..count * 35 / 100 + 1)
        .map(|_| supernets.net(&mut rng))
        .collect::<Vec<_>>();
    let dst_pool = (0..count * 6 / 100 + 1)
        .map(|_| supernets.net(&mut rng))
        .collect::<Vec<_>>();
    let mut rules = (0..count.saturating_sub(1))
        .map(|_| {
            let protos = protos(&mut rng);
            let dst = pick(&mut rng, &dst_pool, 1);
            match rng.below(1000) {
                0..100 => Rule {
                    src: pick(&mut rng, &src_pool, 1),
                    dst,
                    protos,
                    ports: vec![],
                },
                100..101 => {
                    let sources = rng.log_uniform(100, 10_000);
                    Rule {
                        src: pick(&mut rng, &src_pool, sources),
                        dst,
                        protos,
                        ports: ports(&mut rng),
                    }
                }
                _ => {
                    let sources = 1 + rng.below(3) as usize;
                    Rule {
                        src: pick(&mut rng, &src_pool, sources),
                        dst,
                        protos,
                        ports: ports(&mut rng),
                    }
                }
            }
        })
        .collect::<Vec<_>>();
    rules.push(Rule {
        src: vec![],
        dst: vec![],
        protos: vec![],
        ports: vec![],
    });
    rules
}

/// A C object with the stage of its classes.
type Staged<T> = (*mut T, *mut bindings::classifier);

/// Compiles an attribute's classifier as the builder does.
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

/// Joins two stages as the builder does.
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

/// Times a step and prints its time.
fn timed<T>(name: &str, step: impl FnOnce() -> T) -> T {
    let start = Instant::now();
    let value = step();
    println!("build\t{name}\t{:.1} ms", start.elapsed().as_secs_f64() * 1e3);
    value
}

/// The target of an offset word, 0 for none.
fn target(slot: usize) -> usize {
    // SAFETY: the slot is an aligned offset word of a built table.
    let offset = unsafe { (slot as *const isize).read() };
    if offset == 0 {
        0
    } else {
        slot.wrapping_add_signed(offset)
    }
}

/// The values of a table and how many exceed a bound.
#[derive(Default)]
struct Scan {
    values: u64,
    bad: u64,
}

impl Scan {
    fn check(&mut self, value: u32, bound: u32) {
        self.values += 1;
        self.bad += u64::from(value >= bound);
    }

    /// As [`Scan::check`] for a rule, which may also be no rule.
    fn check_rule(&mut self, value: u32, bound: u32) {
        self.values += 1;
        self.bad += u64::from(value >= bound && value != u32::MAX);
    }
}

/// Scans the leaves of an LPM against a bound; a marked IPv6 value is
/// checked against the rows of its table instead.
fn scan_lpm(lpm: *const bindings::lpm, bound: u32, rows: Option<u32>, scan: &mut Scan) {
    // SAFETY: a built LPM.
    let page_count = unsafe { (*lpm).page_count };
    // SAFETY: as above.
    let chunks = target(unsafe { &raw const (*lpm).pages }.addr());
    for page in 0..page_count {
        let chunk = target(chunks + page / 16 * size_of::<usize>());
        let entries = (chunk + page % 16 * 256 * size_of::<u64>()) as *const u64;
        for entry in 0..256 {
            // SAFETY: a page of 256 entries of a built LPM.
            let entry = unsafe { entries.add(entry).read() };
            if entry & u64::from(bindings::LPM_VALUE_FLAG) == 0 {
                continue;
            }
            let value = (entry >> 1) as u32;
            match rows {
                Some(rows) if value & bindings::FILTER_NET6_ROW_MARK != 0 => {
                    scan.check(value & !bindings::FILTER_NET6_ROW_MARK, rows)
                }
                _ => scan.check(value, bound),
            }
        }
    }
}

/// Scans the values of a line against a bound.
fn scan_line(line: *const bindings::vline, bound: u32, scan: &mut Scan) {
    // SAFETY: a built line.
    let size = unsafe { (*line).size };
    // SAFETY: as above.
    let values = target(unsafe { &raw const (*line).values }.addr()) as *const u32;
    for idx in 0..size as usize {
        // SAFETY: a value of the line.
        scan.check(unsafe { values.add(idx).read() }, bound);
    }
}

/// Scans the rows of a table directory with a check, an absent row
/// skipped, and every row of an absent directory: a compile that frees an
/// IPv6 table it needs no row of keeps the table's dimensions.
fn scan_rows(directory: usize, rows: u32, columns: u32, scan: &mut Scan, mut check: impl FnMut(&mut Scan, u32)) {
    if directory == 0 {
        return;
    }
    for row in 0..rows as usize {
        let cells = target(directory + row * size_of::<usize>()) as *const u32;
        if cells.is_null() {
            continue;
        }
        for column in 0..columns as usize {
            // SAFETY: a cell of a built row.
            check(scan, unsafe { cells.add(column).read() });
        }
    }
}

/// Scans a value table against a bound.
fn scan_table(table: *const bindings::value_table, bound: u32, scan: &mut Scan) {
    // SAFETY: a built table.
    let (rows, columns) = unsafe { ((*table).v_dim, (*table).h_dim) };
    // SAFETY: as above.
    let directory = target(unsafe { &raw const (*table).values }.addr());
    scan_rows(directory, rows, columns, scan, |scan, value| scan.check(value, bound));
}

fn dims(table: *const bindings::value_table) -> (u32, u32) {
    // SAFETY: a built table.
    unsafe { ((*table).v_dim, (*table).h_dim) }
}

fn mib(cells: u64) -> f64 {
    (cells * 4) as f64 / f64::from(1 << 20)
}

/// A rule table of a projection of the rules over two stages.
fn join_rules<L, R>(
    owner: &ArenaOwner,
    name: &CStr,
    left: Staged<L>,
    right: Staged<R>,
    projection: &[Option<&()>],
) -> *const bindings::rule_table {
    let table = owner.zeroed::<RuleTable>();
    // SAFETY: both stages were filled over the rule count of the projection.
    let status = unsafe {
        bindings::yanet_sys_classify_join_rules(
            owner.raw_context(),
            name.as_ptr(),
            left.1,
            right.1,
            projection.as_ptr().cast(),
            projection.len() as u32,
            table.cast(),
        )
    };
    assert_eq!(0, status, "join rules {name:?}");
    table.cast()
}

/// A rule map of a projection of the rules over a stage.
fn decode<T>(owner: &ArenaOwner, name: &CStr, stage: Staged<T>, projection: &[Option<&()>]) -> *const bindings::vline {
    let map = owner.zeroed::<bindings::vline>();
    // SAFETY: the stage was filled over the rule count of the projection.
    let status = unsafe {
        bindings::yanet_sys_classify_decode(
            owner.raw_context(),
            name.as_ptr(),
            stage.1,
            projection.as_ptr().cast(),
            projection.len() as u32,
            map,
        )
    };
    assert_eq!(0, status, "decode {name:?}");
    map
}

/// Prints the size of a rule table.
fn print_rule_table(name: &str, table: *const bindings::rule_table) {
    // SAFETY: a rule table built above.
    let (rows, columns, distinct) = unsafe { ((*table).v_dim, (*table).h_dim, (*table).class_count) };
    println!(
        "table\t{name}\t{rows} x {columns}, {distinct} distinct rows, {:.1} MiB",
        mib(u64::from(distinct) * u64::from(columns) + u64::from(rows))
    );
}

/// Builds the classifier after the composition of the ACL module: a core of
/// the networks every protocol path shares, the ports over only the rules
/// that restrict a port, a rule table per protocol over the rules of that
/// protocol with ports, and a rule map per protocol of those without, which a
/// lookup takes the earlier of. The ACL also folds the device and the
/// protocol into its core and joins its roots apart from the rule tables;
/// those are left out.
fn acl_shape<'a>(
    owner: &ArenaOwner,
    rules: &[Rule],
    src: &[Option<&Nets6<'a>>],
    dst: &[Option<&Nets6<'a>>],
    ports: &[Option<&Ports<'a>>],
) {
    let of = |proto: u32, rule: &Rule| rule.protos.is_empty() || rule.protos.iter().any(|range| range.from == proto);
    let projection =
        |keep: &dyn Fn(&Rule) -> bool| rules.iter().map(|rule| keep(rule).then_some(&())).collect::<Vec<_>>();
    let ported = ports
        .iter()
        .zip(rules)
        .map(|(view, rule)| (!rule.ports.is_empty()).then_some(*view).flatten())
        .collect::<Vec<_>>();
    let tcp = projection(&|rule| of(6, rule) && !rule.ports.is_empty());
    let udp = projection(&|rule| of(17, rule) && !rule.ports.is_empty());
    let tcp_plain = projection(&|rule| of(6, rule) && rule.ports.is_empty());
    let udp_plain = projection(&|rule| of(17, rule) && rule.ports.is_empty());

    let start = Instant::now();
    let src = timed("compile src", || compile(owner, c"src", src));
    let dst = timed("compile dst", || compile(owner, c"dst", dst));
    let port = timed("compile port", || compile(owner, c"port", &ported));
    let core = timed("join core", || join(owner, c"core", src, dst, rules.len() as u32));
    let tcp = timed("join tcp rules", || join_rules(owner, c"tcp", core, port, &tcp));
    let udp = timed("join udp rules", || join_rules(owner, c"udp", core, port, &udp));
    timed("decode tcp plain", || decode(owner, c"tcp plain", core, &tcp_plain));
    timed("decode udp plain", || decode(owner, c"udp plain", core, &udp_plain));
    println!("build\ttotal\t{:.1} ms", start.elapsed().as_secs_f64() * 1e3);
    println!(
        "table\tcore joint\t{} x {}",
        dims(core.0.cast()).0,
        dims(core.0.cast()).1
    );
    print_rule_table("tcp rule table", tcp);
    print_rule_table("udp rule table", udp);
}

fn main() {
    let count = env::args().skip(1).find_map(|arg| arg.parse().ok()).unwrap_or(200_000);
    let owner = ArenaOwner::new();
    let rules = timed("generate", || ruleset(count));
    let networks = |side: fn(&Rule) -> &Vec<Net6>| rules.iter().map(|rule| side(rule).len()).sum::<usize>();
    let largest = rules
        .iter()
        .map(|rule| rule.src.len().max(rule.dst.len()))
        .max()
        .unwrap_or(0);
    println!(
        "rules\t{}\tsources {}, destinations {}, largest list {largest}",
        rules.len(),
        networks(|rule| &rule.src),
        networks(|rule| &rule.dst),
    );

    let views = timed("views", || {
        rules
            .iter()
            .map(|rule| {
                (
                    Nets6::new(&rule.src).unwrap(),
                    Nets6::new(&rule.dst).unwrap(),
                    Lines::new(&rule.protos).unwrap(),
                    Ports::new(&rule.ports).unwrap(),
                )
            })
            .collect::<Vec<_>>()
    });
    let rule_count = rules.len() as u32;
    let src = views.iter().map(|view| Some(&view.0)).collect::<Vec<_>>();
    let dst = views.iter().map(|view| Some(&view.1)).collect::<Vec<_>>();
    let protos = views.iter().map(|view| Some(&view.2)).collect::<Vec<_>>();
    let ports = views.iter().map(|view| Some(&view.3)).collect::<Vec<_>>();
    let projection = views.iter().map(|_| Some(&())).collect::<Vec<_>>();
    if env::args().any(|arg| arg == "--acl-shape") {
        acl_shape(&owner, &rules, &src, &dst, &ports);
        return;
    }

    let start = Instant::now();
    let src = timed("compile src", || compile(&owner, c"src", &src));
    let dst = timed("compile dst", || compile(&owner, c"dst", &dst));
    let proto = timed("compile proto", || compile(&owner, c"proto", &protos));
    let port = timed("compile port", || compile(&owner, c"port", &ports));
    let nets = timed("join nets", || join(&owner, c"nets", src, dst, rule_count));
    let transport = timed("join transport", || join(&owner, c"transport", proto, port, rule_count));
    let table = timed("join rules", || {
        let table = owner.zeroed::<RuleTable>();
        // SAFETY: both stages were filled over the rule count of the
        // projection.
        let status = unsafe {
            bindings::yanet_sys_classify_join_rules(
                owner.raw_context(),
                c"rules".as_ptr(),
                nets.1,
                transport.1,
                projection.as_ptr().cast(),
                rule_count,
                table.cast(),
            )
        };
        assert_eq!(0, status, "join rules");
        table.cast::<bindings::rule_table>()
    });
    let build = start.elapsed();
    println!("build\ttotal\t{:.1} ms", build.as_secs_f64() * 1e3);

    let (src, dst) = (
        src.0.cast::<bindings::classify_attr_net6>(),
        dst.0.cast::<bindings::classify_attr_net6>(),
    );
    let (proto, port) = (
        proto.0.cast::<bindings::classify_attr_line>(),
        port.0.cast::<bindings::classify_attr_port>(),
    );
    let (nets, transport) = (
        nets.0.cast::<bindings::value_table>(),
        transport.0.cast::<bindings::value_table>(),
    );
    // SAFETY: the rule table built above.
    let (table_rows, table_columns, class_count) = unsafe { ((*table).v_dim, (*table).h_dim, (*table).class_count) };
    let (nets_dims, transport_dims) = (dims(nets), dims(transport));
    // SAFETY: the IPv6 attributes built above.
    let (src_comb, dst_comb) = unsafe { (&raw const (*src).comb, &raw const (*dst).comb) };
    for (name, (rows, columns)) in [
        ("src comb", dims(src_comb)),
        ("dst comb", dims(dst_comb)),
        ("nets joint", nets_dims),
        ("transport joint", transport_dims),
    ] {
        println!(
            "table\t{name}\t{rows} x {columns}, {:.1} MiB",
            mib(u64::from(rows) * u64::from(columns))
        );
    }
    println!(
        "table\trule table\t{table_rows} x {table_columns}, {class_count} distinct rows, {:.1} MiB",
        mib(u64::from(class_count) * u64::from(table_columns) + u64::from(table_rows))
    );
    // SAFETY: the LPMs of the attributes built above.
    let lpm_pages =
        unsafe { (*src).hi.page_count + (*src).lo.page_count + (*dst).hi.page_count + (*dst).lo.page_count };
    println!(
        "table\tLPM pages\t{lpm_pages}, {:.1} MiB",
        (lpm_pages * 256 * 8) as f64 / f64::from(1 << 20)
    );

    // The scan: every value against the bound of the table that reads it.
    let start = Instant::now();
    let mut scan = Scan::default();
    for (attr, comb, bound) in [(src, src_comb, nets_dims.0), (dst, dst_comb, nets_dims.1)] {
        let (rows, columns) = dims(comb);
        // SAFETY: the attribute built above.
        unsafe {
            scan_lpm(&raw const (*attr).hi, bound, Some(rows), &mut scan);
            scan_lpm(&raw const (*attr).lo, columns, None, &mut scan);
        }
        scan_table(comb, bound, &mut scan);
    }
    // SAFETY: the attributes built above.
    unsafe {
        scan_line(&raw const (*proto).line, transport_dims.0, &mut scan);
        scan_line(&raw const (*port).line, transport_dims.1, &mut scan);
    }
    scan_table(nets, table_rows, &mut scan);
    scan_table(transport, table_columns, &mut scan);
    // SAFETY: the rule table built above.
    unsafe {
        scan_line(&raw const (*table).class_map, class_count, &mut scan);
        let directory = target((&raw const (*table).rows).addr());
        scan_rows(directory, class_count, table_columns, &mut scan, |scan, rule| {
            scan.check_rule(rule, rule_count)
        });
    }
    let elapsed = start.elapsed();
    black_box(&scan);
    println!(
        "scan\t{} values, {} past their bound\t{:.1} ms, {:.2} % of the build",
        scan.values,
        scan.bad,
        elapsed.as_secs_f64() * 1e3,
        elapsed.as_secs_f64() / build.as_secs_f64() * 100.0
    );
}
