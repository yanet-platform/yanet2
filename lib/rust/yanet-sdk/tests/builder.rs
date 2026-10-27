//! The configuration builder over the test owner: in-place construction of a
//! route FIB with its two C LPM tables and of nested blocks, its refusals, and
//! the release of everything a finished or a failed build allocated.
//!
//! Everything runs under Miri except the tests marked `cfg(not(miri))`, which
//! build the FIB: its LPM tables are real C objects, and Miri runs no C. Those
//! run natively against the owner's real C memory. The FIB tests that run under
//! Miri fail before any C runs: the registrations and the header are refused,
//! or the root block cannot be allocated, and the LPM tables are written only
//! after all of those.

#[cfg(not(miri))]
use core::net::Ipv6Addr;
use core::{
    marker::PhantomData,
    sync::atomic::{AtomicU32, Ordering},
};

use yanet_packet as _;
use yanet_sdk::{
    Batch, Build, CommitCx, Counter, Cx, Device, Lpm4, Lpm6, LpmRange, Module, Refusal, Registered, ShmBox, ShmLayout,
    ShmSlice, Verdict,
};
use yanet_sdk_derive as _;
use yanet_sdk_testing::{
    View,
    cp::{Call, TestError, TestOwner},
};
use yanet_shm as _;
use yanet_sys::{
    cp::{BuildError, Builder, Built, MemoryContext, Owner, Scope},
    dp::ModuleConfig,
};

/// A route as the C FIB keeps it: the neighbour's addresses, where it goes
/// and what counts it.
#[derive(ShmLayout)]
#[repr(C)]
struct Route {
    dst_addr: [u8; 6],
    src_addr: [u8; 6],
    pad: u32,
    device: Device,
    counter: Counter,
}

/// A run of `count` entries of the route index, from `start`.
#[derive(ShmLayout)]
#[repr(C)]
struct RouteList {
    start: u64,
    count: u64,
}

/// A FIB with the fields of C's `route_fib`, in its order: two LPM tables
/// mapping a destination to a route list, and the routes the lists select from.
///
/// The bytes differ from C's: a `ShmSlice` is a pointer then a length, where
/// `route_fib` keeps a count then a pointer.
#[derive(ShmLayout)]
#[repr(C)]
struct RouteFib {
    lpm_v6: Lpm6,
    lpm_v4: Lpm4,
    routes: ShmSlice<Route>,
    route_lists: ShmSlice<RouteList>,
    route_indexes: ShmSlice<u64>,
}

/// A rule owning its own block of ports.
#[derive(ShmLayout)]
#[repr(C)]
struct Rule {
    id: u64,
    ports: ShmSlice<u64>,
}

#[derive(ShmLayout)]
#[repr(C)]
struct Policy {
    rules: ShmSlice<Rule>,
    default: ShmBox<Rule>,
    flags: [u8; 4],
    mask: AtomicU32,
}

/// Fields named like the names the derive's expansion uses itself.
#[derive(ShmLayout)]
#[repr(C)]
struct Hygiene {
    __addr: u64,
    __root: u64,
    __parts: u64,
    __f0: ShmBox<u64>,
}

/// Two runs of ports, each an edge of its own.
#[derive(ShmLayout)]
#[repr(C)]
struct Lanes {
    lanes: [ShmSlice<u64>; 2],
}

/// A word whose write is refused when its parts say so.
#[repr(C)]
struct Flaky(u64);

/// The parts of a refusable word: the word, and whether writing it fails.
struct FlakyParts {
    word: u64,
    fails: bool,
}

// SAFETY: a word accepts every bit pattern and has no padding.
unsafe impl ShmLayout for Flaky {}

// SAFETY: the word is the whole value, written or refused as a whole. It
// stands in, under Miri, for an embedded C object whose initialisation runs
// out of memory.
unsafe impl Build for Flaky {
    type Parts<'b, W: Owner> = FlakyParts;

    unsafe fn write<W: Owner>(parts: FlakyParts, memory: &MemoryContext<'_, W>, addr: usize) -> Result<(), Refusal> {
        if parts.fails {
            return Err(Refusal::OutOfMemory);
        }
        // SAFETY: aligned and exclusive (caller contract).
        unsafe { memory.root().ptr::<u64>(addr).write(parts.word) };
        Ok(())
    }

    unsafe fn free<W: Owner>(_: usize, _: &MemoryContext<'_, W>) {}
}

/// A device, a counter, a block of ports and a word whose write may be
/// refused, in this order: a refused word leaves the three fields before it to
/// be returned.
#[derive(ShmLayout)]
#[repr(C)]
struct Entry {
    device: Device,
    counter: Counter,
    ports: ShmSlice<u64>,
    word: Flaky,
}

/// Every kind of place an entry is written in, and a root word that is refused
/// between written fields and before a field never written.
#[derive(ShmLayout)]
#[repr(C)]
struct Faulty {
    boxed: ShmBox<Entry>,
    entries: ShmSlice<Entry>,
    pair: [Entry; 2],
    gate: Flaky,
    tail: ShmSlice<u64>,
}

/// A module whose configuration is `C`: the builder reads only its name and
/// its configuration type.
struct Fixture<C>(PhantomData<fn() -> C>);

impl<C: Build + Sync> Module for Fixture<C> {
    const NAME: &'static str = "fixture";
    type Config = C;
    type Prepared<'p> = ();

    fn prepare(_: &CommitCx<'_, Self>) {}

    fn handle<'id>(_: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        batch.dispatch(|_| Verdict::Pass);
    }
}

type FibModule = Fixture<RouteFib>;
type PolicyModule = Fixture<Policy>;
type HygieneModule = Fixture<Hygiene>;
type WordsModule = Fixture<ShmSlice<u64>>;
type LanesModule = Fixture<Lanes>;
type FaultyModule = Fixture<Faulty>;

type Failure = BuildError<TestError>;

/// The error type of an author with failures of its own.
#[derive(Debug, PartialEq)]
enum AuthorError {
    Build(Failure),
    Input(&'static str),
}

impl From<Failure> for AuthorError {
    fn from(err: Failure) -> Self {
        Self::Build(err)
    }
}

/// A route of the input: its neighbour, the index of its device in the device
/// list and the name of its counter.
struct RouteSpec {
    dst_addr: [u8; 6],
    device: usize,
    counter: &'static str,
}

/// What the FIB is built from, made at run time and borrowed by the build.
struct FibInput {
    devices: Vec<&'static str>,
    routes: Vec<RouteSpec>,
    lists: Vec<(u64, u64)>,
    indexes: Vec<u64>,
    v6: Vec<LpmRange<16>>,
    v4: Vec<LpmRange<4>>,
}

/// A FIB of three routes over two devices, two route lists (routes 0 and 2,
/// then route 1), overlapping IPv4 prefixes and enough IPv6 /64 prefixes under
/// a /32 for the table to need a second chunk of pages.
///
/// Ranges are in insertion order, the broad before the narrow, as the later
/// insert wins on an overlap.
fn fib_input() -> FibInput {
    let mut v6 = vec![LpmRange::prefix(
        [0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0],
        32,
        0,
    )];
    for subnet in 1..=12 {
        v6.push(LpmRange::prefix(
            [0x20, 0x01, 0x0d, 0xb8, 0, subnet, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0],
            64,
            1,
        ));
    }
    FibInput {
        devices: vec!["eth0", "eth1"],
        routes: vec![
            RouteSpec {
                dst_addr: [0, 0, 0, 0, 0, 1],
                device: 0,
                counter: "via_eth0",
            },
            RouteSpec {
                dst_addr: [0, 0, 0, 0, 0, 2],
                device: 1,
                counter: "via_eth1",
            },
            RouteSpec {
                dst_addr: [0, 0, 0, 0, 0, 3],
                device: 0,
                counter: "via_eth0_backup",
            },
        ],
        lists: vec![(0, 2), (2, 1)],
        indexes: vec![0, 2, 1],
        v6,
        v4: vec![
            LpmRange::prefix([10, 0, 0, 0], 8, 0),
            LpmRange::prefix([10, 1, 0, 0], 16, 1),
            LpmRange::prefix([10, 1, 2, 0], 24, 0),
        ],
    }
}

/// The parts of a FIB of `input`, registering each device once, copied into
/// the routes that use it, and a counter per route.
fn fib<'b, E: From<Failure>>(
    b: Scope<'b, '_, TestOwner, E>,
    input: &'b FibInput,
) -> Result<RouteFibParts<'b, TestOwner>, E> {
    let devices = input
        .devices
        .iter()
        .map(|&name| b.device(name))
        .collect::<Result<Vec<_>, E>>()?;
    Ok(RouteFibParts {
        lpm_v6: &input.v6,
        lpm_v4: &input.v4,
        routes: b.slice(input.routes.len(), |idx| {
            let route = &input.routes[idx];
            Ok(RouteParts {
                dst_addr: route.dst_addr,
                src_addr: [0xee; 6],
                pad: 0,
                device: devices[route.device],
                counter: b.counter(route.counter)?,
            })
        })?,
        route_lists: b.slice(input.lists.len(), |idx| {
            let (start, count) = input.lists[idx];
            Ok(RouteListParts { start, count })
        })?,
        route_indexes: b.slice(input.indexes.len(), |idx| Ok(input.indexes[idx]))?,
    })
}

/// The parts of a policy with the rules `(id, ports)` and a default rule
/// without ports.
fn policy<'b, E: From<Failure>>(
    b: Scope<'b, '_, TestOwner, E>,
    rules: &[(u64, &[u64])],
) -> Result<PolicyParts<'b, TestOwner>, E> {
    Ok(PolicyParts {
        rules: b.slice(rules.len(), |idx| {
            let (id, ports) = rules[idx];
            Ok(RuleParts {
                id,
                ports: b.slice(ports.len(), |port| Ok(ports[port]))?,
            })
        })?,
        default: b.boxed(RuleParts { id: 0, ports: b.slice(0, |_| Ok(0))? })?,
        flags: [1, 2, 3, 4],
        mask: AtomicU32::new(0xa5),
    })
}

/// Builds a configuration of `M` with the closure, failing with the SDK's
/// own errors.
fn build<'env, M: Module>(
    owner: &TestOwner,
    f: impl for<'b> FnOnce(Scope<'b, 'env, TestOwner>) -> Result<<M::Config as Build>::Parts<'b, TestOwner>, Failure>,
) -> Result<Built<M, TestOwner>, Failure> {
    Builder::<M>::build(owner, M::NAME, f)
}

/// Builds the FIB configuration of `input`.
fn build_fib(owner: &TestOwner, input: &FibInput) -> Result<Built<FibModule, TestOwner>, Failure> {
    build::<FibModule>(owner, |b| fib(b, input))
}

/// Builds the policy configuration with the rules, each an id and its ports.
fn build_policy(owner: &TestOwner, rules: &[(u64, &[u64])]) -> Result<Built<PolicyModule, TestOwner>, Failure> {
    build::<PolicyModule>(owner, |b| policy(b, rules))
}

/// The body of the configuration of `built`, resolved through a view of the
/// owner's arena.
///
/// The references live no longer than the borrow of the built configuration,
/// so releasing it while they live does not compile.
fn body<'a, M: Module>(owner: &'a TestOwner, built: &'a Built<M, TestOwner>) -> (&'a M::Config, View<'a>) {
    // SAFETY: a configuration the builder handed over, frozen for the test;
    // the view resolves through the root of the arena it lives in.
    unsafe {
        let config = &*owner.root().ptr::<ModuleConfig<M::Config>>(built.header().get());
        (config.body(), View::new(owner.root()))
    }
}

/// Releases a configuration of `M` built in `owner`.
fn release<M: Module>(owner: &TestOwner, built: Built<M, TestOwner>) {
    // SAFETY: handed over by the builder, unreferenced, released once.
    unsafe { built.destroy(owner) };
}

#[test]
fn test_build_writes_nested_slices_and_a_box() {
    let owner = TestOwner::new();

    let built = build_policy(&owner, &[(1, &[80, 443]), (2, &[]), (3, &[53])]).expect("built");

    let (config, view) = body::<PolicyModule>(&owner, &built);
    let rules: Vec<_> = config
        .rules
        .as_slice(&view)
        .iter()
        .map(|rule| (rule.id, rule.ports.as_slice(&view).to_vec()))
        .collect();
    let default = config.default.get(&view);
    assert_eq!(
        (
            vec![(1, vec![80, 443]), (2, vec![]), (3, vec![53])],
            (0, 0),
            [1, 2, 3, 4],
            0xa5
        ),
        (
            rules,
            (default.id, default.ports.len()),
            config.flags,
            config.mask.load(Ordering::Relaxed)
        )
    );
    release::<PolicyModule>(&owner, built);
    owner.assert_released();
}

#[test]
fn test_build_leaves_an_empty_slice_null_and_unallocated() {
    let owner = TestOwner::new();

    let built = build::<WordsModule>(&owner, |b| b.slice(0, |_| Ok(0))).expect("built");

    let (config, view) = body::<WordsModule>(&owner, &built);
    assert_eq!((0, 0, 0), (config.offset(), config.len(), config.as_slice(&view).len()));
    // Only the root block.
    assert_eq!(1, owner.live_blocks());
    release::<WordsModule>(&owner, built);
    owner.assert_released();
}

#[test]
fn test_destroy_releases_nested_blocks() {
    let owner = TestOwner::new();
    let built = build_policy(&owner, &[(1, &[80, 443]), (2, &[]), (3, &[53])]).expect("built");
    // The root block, the rules, the default rule, and the ports of two
    // rules.
    assert_eq!(5, owner.live_blocks());

    release::<PolicyModule>(&owner, built);

    owner.assert_released();
}

/// An owner whose allocations fail after `allowed` have succeeded.
fn owner_failing_alloc_after(allowed: usize) -> TestOwner {
    let owner = TestOwner::new();
    owner.fail_after(Call::Alloc, allowed);
    owner
}

/// Builds with a fresh owner for each limit in turn until a build fits.
///
/// Each build that fails must be refused for lack of memory and leave nothing
/// behind; the one that fits is released. Returns the limit that fits and
/// panics when none does.
fn sweep_limits<M: Module>(
    limits: impl IntoIterator<Item = usize>,
    owner_with: impl Fn(usize) -> TestOwner,
    build_with: impl Fn(&TestOwner) -> Result<Built<M, TestOwner>, Failure>,
) -> usize {
    for limit in limits {
        let owner = owner_with(limit);

        match build_with(&owner) {
            Ok(built) => {
                release::<M>(&owner, built);
                owner.assert_released();
                return limit;
            }
            Err(err) => assert_eq!(BuildError::Refused(Refusal::OutOfMemory), err),
        }

        owner.assert_released();
    }
    panic!("no limit let the build succeed");
}

#[test]
fn test_build_releases_everything_when_memory_runs_out_at_each_allocation() {
    let fits = sweep_limits(0..64, owner_failing_alloc_after, |owner| {
        build_policy(owner, &[(1, &[80, 443]), (2, &[]), (3, &[53])])
    });

    // The root block, the rules, the ports of two rules and the default rule.
    assert_eq!(5, fits);
}

#[test]
fn test_build_releases_everything_when_a_registration_fails_at_each_call() {
    let input = fib_input();
    // Two devices and a counter per route.
    for allowed in 0..5 {
        let owner = TestOwner::new();
        owner.fail_after(Call::Registration, allowed);

        let err = build_fib(&owner, &input).expect_err("refused registration");

        assert_eq!(BuildError::Owner(TestError::Registration), err);
        owner.assert_released();
    }
}

#[test]
fn test_build_frees_the_root_block_of_a_refused_header() {
    let owner = TestOwner::new();
    owner.fail_after(Call::Header, 0);

    let err = build_fib(&owner, &fib_input()).expect_err("refused header");

    assert!(matches!(err, BuildError::Owner(_)), "{err:?}");
    owner.assert_released();
}

#[test]
fn test_build_reports_exhausted_memory_for_the_root_block() {
    let owner = TestOwner::new();
    owner.fail_after(Call::Alloc, 0);

    let err = build_fib(&owner, &fib_input()).expect_err("no memory");

    assert_eq!(BuildError::Refused(Refusal::OutOfMemory), err);
    owner.assert_released();
}

#[test]
fn test_build_refuses_a_slice_whose_size_overflows() {
    let owner = TestOwner::new();

    let err = build::<WordsModule>(&owner, |b| b.slice(usize::MAX, |_| Ok(0))).expect_err("overflowing slice");

    assert_eq!(BuildError::Refused(Refusal::BlockTooLarge), err);
    owner.assert_released();
}

#[test]
fn test_build_refuses_a_slice_beyond_the_largest_block_without_asking_the_owner() {
    let owner = TestOwner::new();
    let len = yanet_sys::bindings::MEMORY_BLOCK_ALLOCATOR_MAX_SIZE as usize / size_of::<u64>() + 1;
    // The root block can be allocated; a request for any further block would
    // exhaust the memory.
    owner.fail_after(Call::Alloc, 1);

    let err = build::<WordsModule>(&owner, |b| b.slice(len, |_| Ok(0))).expect_err("slice beyond the largest block");

    assert_eq!(BuildError::Refused(Refusal::BlockTooLarge), err);
    owner.assert_released();
}

#[test]
fn test_build_returns_the_error_of_a_slice_item_and_releases() {
    let owner = TestOwner::new();

    let err = Builder::<PolicyModule>::build::<AuthorError, _>(&owner, "policy0", |b| {
        Ok(PolicyParts {
            rules: b.slice(3, |idx| {
                let id = idx as u64 + 1;
                if id == 3 {
                    return Err(AuthorError::Input("rule 3 is invalid"));
                }
                Ok(RuleParts { id, ports: b.slice(1, |_| Ok(80))? })
            })?,
            default: b.boxed(RuleParts { id: 0, ports: b.slice(0, |_| Ok(0))? })?,
            flags: [0; 4],
            mask: AtomicU32::new(0),
        })
    })
    .expect_err("invalid rule");

    assert_eq!(AuthorError::Input("rule 3 is invalid"), err);
    owner.assert_released();
}

// Unwinding is std-only.
#[allow(clippy::std_instead_of_core)]
#[test]
fn test_build_releases_everything_when_the_closure_panics() {
    use std::panic::{AssertUnwindSafe, catch_unwind};

    let owner = TestOwner::new();

    let result = catch_unwind(AssertUnwindSafe(|| {
        Builder::<PolicyModule>::build::<Failure, _>(&owner, "policy0", |b| {
            let _rule = b.boxed::<Rule>(RuleParts {
                id: 1,
                ports: b.slice(1, |_| Ok(80))?,
            })?;
            panic!("the closure of the author panics")
        })
    }));

    assert!(result.is_err());
    owner.assert_released();
}

#[test]
fn test_build_releases_everything_when_an_item_of_a_nested_slice_fails() {
    let owner = TestOwner::new();

    // Rule 1 owns a finished ports block when rule 2's ports fail halfway.
    let err = Builder::<PolicyModule>::build::<AuthorError, _>(&owner, "policy0", |b| {
        Ok(PolicyParts {
            rules: b.slice(2, |idx| {
                let id = idx as u64 + 1;
                Ok(RuleParts {
                    id,
                    ports: b.slice(2, |port| {
                        if id == 2 && port == 1 {
                            return Err(AuthorError::Input("port 81 is invalid"));
                        }
                        Ok(80 + port as u64)
                    })?,
                })
            })?,
            default: b.boxed(RuleParts { id: 0, ports: b.slice(0, |_| Ok(0))? })?,
            flags: [0; 4],
            mask: AtomicU32::new(0),
        })
    })
    .expect_err("invalid port");

    assert_eq!(AuthorError::Input("port 81 is invalid"), err);
    owner.assert_released();
}

// Unwinding is std-only.
#[allow(clippy::std_instead_of_core)]
#[test]
fn test_build_releases_everything_when_a_nested_fill_panics() {
    use std::panic::{AssertUnwindSafe, catch_unwind};

    let owner = TestOwner::new();

    let result = catch_unwind(AssertUnwindSafe(|| {
        Builder::<PolicyModule>::build::<Failure, _>(&owner, "policy0", |b| {
            Ok(PolicyParts {
                rules: b.slice(2, |idx| {
                    let id = idx as u64 + 1;
                    Ok(RuleParts {
                        id,
                        ports: b.slice(2, |port| {
                            assert!(!(id == 2 && port == 1), "the fill of the author panics");
                            Ok(80 + port as u64)
                        })?,
                    })
                })?,
                default: b.boxed(RuleParts { id: 0, ports: b.slice(0, |_| Ok(0))? })?,
                flags: [0; 4],
                mask: AtomicU32::new(0),
            })
        })
    }));

    assert!(result.is_err());
    owner.assert_released();
}

#[test]
fn test_build_frees_a_block_dropped_unused_at_once() {
    let owner = TestOwner::new();

    let built = build::<WordsModule>(&owner, |b| {
        let before = owner.live_blocks();
        let unused = b.boxed::<Rule>(RuleParts {
            id: 1,
            ports: b.slice(2, |idx| Ok([80, 443][idx]))?,
        })?;
        let held = owner.live_blocks();
        drop(unused);
        assert_eq!((before + 2, before), (held, owner.live_blocks()));
        b.slice(1, |_| Ok(5))
    })
    .expect("built");

    // The root block and the one slice the configuration owns.
    assert_eq!(2, owner.live_blocks());
    release::<WordsModule>(&owner, built);
    owner.assert_released();
}

#[test]
fn test_derive_expansion_is_hygienic_for_field_names() {
    let owner = TestOwner::new();

    let built = build::<HygieneModule>(&owner, |b| {
        Ok(HygieneParts {
            __addr: 1,
            __root: 2,
            __parts: 3,
            __f0: b.boxed(4)?,
        })
    })
    .expect("built");

    let (config, view) = body::<HygieneModule>(&owner, &built);
    assert_eq!(
        (1, 2, 3, 4),
        (config.__addr, config.__root, config.__parts, *config.__f0.get(&view))
    );
    release::<HygieneModule>(&owner, built);
    owner.assert_released();
}

/// verifies that the blocks, the scope and the derived parts are `Debug`
/// although the owner is not, so an author's `#[derive(Debug)]` can hold them.
#[test]
fn test_blocks_scope_and_parts_are_debug_for_any_owner() {
    fn is_debug<T: core::fmt::Debug>() {}

    is_debug::<yanet_sdk::Owned<'static, u64, TestOwner>>();
    is_debug::<yanet_sdk::OwnedSlice<'static, u64, TestOwner>>();
    is_debug::<yanet_sdk::Registered<'static, Counter>>();
    is_debug::<Scope<'static, 'static, TestOwner>>();
    is_debug::<PolicyParts<'static, TestOwner>>();
}

/// Parts of an entry whose word is number `position` of the build, which
/// fails when that is `fail_at`.
fn entry<'b>(
    b: Scope<'b, '_, TestOwner>,
    device: Registered<'b, Device>,
    fail_at: usize,
    position: usize,
) -> Result<EntryParts<'b, TestOwner>, Failure> {
    Ok(EntryParts {
        device,
        counter: b.counter("hits")?,
        ports: b.slice(1, |_| Ok(position as u64))?,
        word: FlakyParts {
            word: position as u64,
            fails: position == fail_at,
        },
    })
}

/// The parts of a configuration whose refusable words are numbered in the
/// order the closure makes them, and of which the one at `fail_at` fails.
///
/// The box and the slice are written inside the closure, the array and the
/// root words by the final write, so the numbering is also that of the writes.
fn faulty<'b>(b: Scope<'b, '_, TestOwner>, fail_at: usize) -> Result<FaultyParts<'b, TestOwner>, Failure> {
    let device = b.device("eth0")?;
    Ok(FaultyParts {
        boxed: b.boxed(entry(b, device, fail_at, 0)?)?,
        entries: b.slice(3, |idx| entry(b, device, fail_at, idx + 1))?,
        pair: [entry(b, device, fail_at, 4)?, entry(b, device, fail_at, 5)?],
        gate: FlakyParts { word: 6, fails: fail_at == 6 },
        tail: b.slice(1, |_| Ok(7))?,
    })
}

/// Number of refusable words in the configuration.
const FAULTY_WORDS: usize = 7;

#[test]
fn test_build_writes_an_array_of_edges() {
    let owner = TestOwner::new();

    let built = build::<LanesModule>(&owner, |b| {
        Ok(LanesParts {
            lanes: [b.slice(2, |idx| Ok(1 + idx as u64))?, b.slice(1, |_| Ok(3))?],
        })
    })
    .expect("built");

    let (config, view) = body::<LanesModule>(&owner, &built);
    assert_eq!(
        (vec![1, 2], vec![3]),
        (
            config.lanes[0].as_slice(&view).to_vec(),
            config.lanes[1].as_slice(&view).to_vec()
        )
    );
    release::<LanesModule>(&owner, built);
    owner.assert_released();
}

#[test]
fn test_build_returns_everything_when_a_write_is_refused_at_each_word() {
    for fail_at in 0..FAULTY_WORDS {
        let owner = TestOwner::new();

        let err = build::<FaultyModule>(&owner, |b| faulty(b, fail_at)).expect_err("refused write");

        assert_eq!(BuildError::Refused(Refusal::OutOfMemory), err);
        owner.assert_released();
    }
}

#[test]
fn test_build_writes_every_word_of_a_faulty_config_when_none_is_refused() {
    let owner = TestOwner::new();

    // One past the last word, which proves the sweep reached every one.
    let built = build::<FaultyModule>(&owner, |b| faulty(b, FAULTY_WORDS)).expect("built");

    let (config, view) = body::<FaultyModule>(&owner, &built);
    // The device takes the first id, then each entry's counter in the order
    // the closure makes them.
    assert_eq!(
        ((0, vec![1, 2, 3], vec![4, 5], 6), (1, vec![2, 3, 4], vec![5, 6])),
        (
            (
                config.boxed.get(&view).word.0,
                config
                    .entries
                    .as_slice(&view)
                    .iter()
                    .map(|entry| entry.word.0)
                    .collect::<Vec<_>>(),
                config.pair.iter().map(|entry| entry.word.0).collect::<Vec<_>>(),
                config.gate.0
            ),
            (
                config.boxed.get(&view).counter.id(),
                config
                    .entries
                    .as_slice(&view)
                    .iter()
                    .map(|entry| entry.counter.id())
                    .collect::<Vec<_>>(),
                config.pair.iter().map(|entry| entry.counter.id()).collect::<Vec<_>>()
            )
        )
    );
    release::<FaultyModule>(&owner, built);
    owner.assert_released();
}

/// Pages the C allocator gave the table, by the table's own count.
#[cfg(not(miri))]
fn c_page_count(lpm: &Lpm6) -> usize {
    // SAFETY: the table mirrors `struct lpm`, which C initialised, and nothing
    // writes it meanwhile.
    unsafe { (&raw const (*core::ptr::from_ref(lpm).cast::<yanet_sys::bindings::lpm>()).page_count).read() }
}

/// The routes a route list selects, each as its neighbour, device index and
/// counter id.
#[cfg(not(miri))]
fn resolve<'a>(config: &'a RouteFib, view: &View<'a>, list: u32) -> Vec<(u8, u64, u64)> {
    let list = &config.route_lists.as_slice(view)[list as usize];
    let routes = config.routes.as_slice(view);
    config.route_indexes.as_slice(view)[list.start as usize..][..list.count as usize]
        .iter()
        .map(|&index| {
            let route = &routes[index as usize];
            (route.dst_addr[5], route.device.index(), route.counter.id())
        })
        .collect()
}

#[cfg(not(miri))]
#[test]
fn test_build_writes_a_fib_whose_prefixes_resolve_to_routes() {
    let owner = TestOwner::new();
    let input = fib_input();

    let built = build_fib(&owner, &input).expect("built");

    let (config, view) = body::<FibModule>(&owner, &built);
    let v4 = |key: [u8; 4]| {
        config
            .lpm_v4
            .lookup(&view, &key)
            .map(|list| resolve(config, &view, list))
    };
    let v6 = |key: &str| {
        let key = key.parse::<Ipv6Addr>().expect("address").octets();
        config
            .lpm_v6
            .lookup(&view, &key)
            .map(|list| resolve(config, &view, list))
    };
    // The devices are registered first and shared by routes 0 and 2, then a
    // counter per route.
    let first_list = vec![(1, 0, 2), (3, 0, 4)];
    let second_list = vec![(2, 1, 3)];
    assert!(c_page_count(&config.lpm_v6) > 16);
    assert_eq!(
        [
            Some(first_list.clone()),
            Some(second_list.clone()),
            Some(first_list.clone()),
            None
        ],
        [
            v4([10, 9, 9, 9]),
            v4([10, 1, 9, 9]),
            v4([10, 1, 2, 3]),
            v4([11, 0, 0, 1])
        ],
    );
    assert_eq!(
        [Some(second_list), Some(first_list.clone()), Some(first_list), None],
        [
            v6("2001:db8:3::1"),
            v6("2001:db8:3:1::1"),
            v6("2001:db8:ffff::1"),
            v6("2002::1")
        ],
    );
    release::<FibModule>(&owner, built);
    owner.assert_released();
}

#[cfg(not(miri))]
#[test]
fn test_build_releases_a_fib_when_an_sdk_block_cannot_be_allocated() {
    let input = fib_input();

    let fits = sweep_limits(0..16, owner_failing_alloc_after, |owner| build_fib(owner, &input));

    // The root block and the three slices.
    assert_eq!(4, fits);
}

/// verifies that a build returns every C page when the C memory runs out at
/// any point: the first table, its growth, and the second table after the
/// first was written.
///
/// A buddy allocator fails monotonically with its capacity, so a sweep up in
/// steps well below a page chunk crosses each failure point in turn.
#[cfg(not(miri))]
#[test]
fn test_build_releases_a_fib_when_c_memory_runs_out_at_each_capacity() {
    let input = fib_input();

    let fits = sweep_limits((0..480 << 10).step_by(4 << 10), TestOwner::with_c_memory, |owner| {
        build_fib(owner, &input)
    });

    // The IPv6 table grows into two chunks of pages and the IPv4 table takes
    // one, so the sweep crossed the first table's init, its growth and the
    // second table's init after the first was written.
    let chunk = yanet_sys::bindings::LPM_CHUNK_SIZE as usize * size_of::<yanet_sys::bindings::lpm_page>();
    assert!(3 * chunk <= fits, "{fits} bytes fit, below three chunks of {chunk}");
}
