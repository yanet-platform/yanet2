//! The configuration builder over the test owner: in-place construction of
//! nested blocks, its refusals, and the release of everything a finished or a
//! failed build allocated, for module configurations and objects alike.
//!
//! The owner sets up no C memory, so everything here runs under Miri and
//! natively alike. A configuration that embeds a C object, such as the route
//! FIB with its LPM tables, is built over the real agent in the C harness: see
//! the Go tests over `bindings/go/dataplane_ut`.

use core::{
    marker::PhantomData,
    sync::atomic::{AtomicU32, Ordering},
};

use yanet_packet as _;
use yanet_sdk::{
    Batch, Build, BuiltObject, CommitCx, Counter, Cx, Device, Kind, Lpm4, Module, Object, ObjectBuilder,
    ObjectCommitCx, ObjectLink, OwnedSlice, PerWorker, PerWorkerSlice, Refusal, Registered, ShmBox, ShmLayout,
    ShmOption, ShmSlice, Traffic, Verdict, WordLayout,
};
use yanet_sdk_derive as _;
use yanet_sdk_testing::{
    View,
    cp::{Call, LiveHeaders, TestError, TestOwner},
};
use yanet_shm as _;
use yanet_sys::{
    bindings::CP_OBJECT_NAME_LEN,
    cp::{BuildError, Builder, Built, Header, MemoryContext, Owner, Scope},
    dp::ModuleConfig,
};

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

// SAFETY: one word.
unsafe impl WordLayout for Flaky {}

// SAFETY: the word is the whole value, written or refused as a whole. It
// stands in, under Miri, for an embedded C object whose initialisation runs
// out of memory.
unsafe impl Build for Flaky {
    type Parts<'b, W: Owner> = FlakyParts;

    unsafe fn write<W: Owner>(parts: FlakyParts, memory: &MemoryContext<'_, W>, addr: usize) -> Result<(), Refusal> {
        if parts.fails {
            return Err(Refusal::OutOfMemory);
        }
        // SAFETY: the address is aligned and exclusive (caller contract).
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

/// A counter of hits and one of the packets and bytes of the traffic.
#[derive(ShmLayout)]
#[repr(C)]
struct Counted {
    hits: Counter,
    traffic: Counter<Traffic>,
}

/// An optional run of ports, an optional word whose write may be refused and
/// an optional LPM, which builds here always leave absent: building one opens
/// C memory, which the test owner does not set up.
#[derive(ShmLayout)]
#[repr(C)]
struct Optional {
    ports: ShmOption<ShmSlice<u64>>,
    word: ShmOption<Flaky>,
    routes: ShmOption<Lpm4>,
}

/// A module with a given configuration type: the builder reads only its name
/// and that type.
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

type PolicyModule = Fixture<Policy>;
type HygieneModule = Fixture<Hygiene>;
type WordsModule = Fixture<ShmSlice<u64>>;
type LanesModule = Fixture<Lanes>;
type FaultyModule = Fixture<Faulty>;
type EntriesModule = Fixture<ShmSlice<Entry>>;
type CountedModule = Fixture<Counted>;
type OptionalModule = Fixture<Optional>;

/// An owner of an empty arena.
fn owner() -> TestOwner {
    // SAFETY: no build of this file writes a C object: the only one, an
    // optional LPM, is always left absent.
    unsafe { TestOwner::new() }
}

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

/// The parts of a policy with rules, each an id and its ports, and a default
/// rule without ports.
fn policy<'b, E: From<Failure>, K: Kind>(
    b: Scope<'b, '_, TestOwner, E, K>,
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

/// Entries over two devices, registered once each, and a counter per entry:
/// five registrations.
fn entries<'b>(b: Scope<'b, '_, TestOwner>) -> Result<OwnedSlice<'b, Entry, TestOwner>, Failure> {
    let devices = [b.device("eth0")?, b.device("eth1")?];
    b.slice(3, |idx| {
        Ok(EntryParts {
            device: devices[idx % 2],
            counter: b.counter(["a", "b", "c"][idx])?,
            ports: b.slice(0, |_| Ok(0))?,
            word: FlakyParts { word: 0, fails: false },
        })
    })
}

/// Builds a configuration of a module with the closure, failing with the SDK's
/// own errors.
fn build<'env, M: Module>(
    owner: &TestOwner,
    f: impl for<'b> FnOnce(Scope<'b, 'env, TestOwner>) -> Result<<M::Config as Build>::Parts<'b, TestOwner>, Failure>,
) -> Result<Built<M, TestOwner>, Failure> {
    Builder::<M>::build(owner, M::NAME, f)
}

/// Builds the policy configuration with the rules, each an id and its ports.
fn build_policy(owner: &TestOwner, rules: &[(u64, &[u64])]) -> Result<Built<PolicyModule, TestOwner>, Failure> {
    build::<PolicyModule>(owner, |b| policy(b, rules))
}

/// The body of a built configuration, resolved through a view of the owner's
/// arena.
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

/// Releases a configuration of a module built in the given owner.
fn release<M: Module>(owner: &TestOwner, built: Built<M, TestOwner>) {
    // SAFETY: handed over by the builder, unreferenced, released once.
    unsafe { built.destroy(owner) };
}

#[test]
fn test_build_writes_nested_slices_and_a_box() {
    let owner = owner();

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
    let owner = owner();

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
    let owner = owner();
    let built = build_policy(&owner, &[(1, &[80, 443]), (2, &[]), (3, &[53])]).expect("built");
    // The root block, the rules, the default rule, and the ports of two
    // rules.
    assert_eq!(5, owner.live_blocks());

    release::<PolicyModule>(&owner, built);

    owner.assert_released();
}

/// An owner whose allocations fail after a given number have succeeded.
fn owner_failing_alloc_after(allowed: usize) -> TestOwner {
    let owner = owner();
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
    for allowed in 0..5 {
        let owner = owner();
        owner.fail_after(Call::Registration, allowed);

        let err = build::<EntriesModule>(&owner, entries).expect_err("refused registration");

        assert_eq!(BuildError::Owner(TestError::Registration), err);
        owner.assert_released();
    }
}

#[test]
fn test_build_frees_the_root_block_of_a_refused_header() {
    let owner = owner();
    owner.fail_after(Call::Header, 0);

    let err = build::<EntriesModule>(&owner, entries).expect_err("refused header");

    assert!(matches!(err, BuildError::Owner(_)), "{err:?}");
    owner.assert_released();
}

#[test]
fn test_build_reports_exhausted_memory_for_the_root_block() {
    let owner = owner();
    owner.fail_after(Call::Alloc, 0);

    let err = build::<EntriesModule>(&owner, entries).expect_err("no memory");

    assert_eq!(BuildError::Refused(Refusal::OutOfMemory), err);
    owner.assert_released();
}

#[test]
fn test_build_refuses_a_slice_whose_size_overflows() {
    let owner = owner();

    let err = build::<WordsModule>(&owner, |b| b.slice(usize::MAX, |_| Ok(0))).expect_err("overflowing slice");

    assert_eq!(BuildError::Refused(Refusal::BlockTooLarge), err);
    owner.assert_released();
}

#[test]
fn test_build_refuses_a_slice_beyond_the_largest_block_without_asking_the_owner() {
    let owner = owner();
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
    let owner = owner();

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

    let owner = owner();

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
    let owner = owner();

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

    let owner = owner();

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
    let owner = owner();

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
    let owner = owner();

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

/// Parts of the entry at a given position in the build, which fails when that
/// is the failing position.
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
/// order the closure makes them, and of which the one chosen to fail fails.
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

/// verifies that each counter is registered with as many values as its
/// layout holds and written with the id the registry assigned.
#[test]
fn test_build_registers_each_counter_with_the_size_of_its_layout() {
    let owner = owner();

    let built = build::<CountedModule>(&owner, |b| {
        Ok(CountedParts {
            hits: b.counter("hits")?,
            traffic: b.counter("traffic")?,
        })
    })
    .expect("built");

    let (config, _) = body::<CountedModule>(&owner, &built);
    assert_eq!(
        ((0, 1), vec![1, 2]),
        ((config.hits.id(), config.traffic.id()), owner.counter_sizes())
    );
    release::<CountedModule>(&owner, built);
    owner.assert_released();
}

#[test]
fn test_build_writes_an_array_of_edges() {
    let owner = owner();

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
        let owner = owner();

        let err = build::<FaultyModule>(&owner, |b| faulty(b, fail_at)).expect_err("refused write");

        assert_eq!(BuildError::Refused(Refusal::OutOfMemory), err);
        owner.assert_released();
    }
}

#[test]
fn test_build_writes_every_word_of_a_faulty_config_when_none_is_refused() {
    let owner = owner();

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

/// Builds the optional configuration with the ports and the word present when
/// given, and the LPM absent.
fn build_optional(
    owner: &TestOwner,
    ports: Option<&[u64]>,
    word: Option<FlakyParts>,
) -> Result<Built<OptionalModule, TestOwner>, Failure> {
    build::<OptionalModule>(owner, |b| {
        Ok(OptionalParts {
            ports: ports
                .map(|ports| b.slice(ports.len(), |idx| Ok(ports[idx])))
                .transpose()?,
            word,
            routes: None,
        })
    })
}

#[test]
fn test_build_leaves_absent_options_empty_and_unallocated() {
    let owner = owner();

    let built = build_optional(&owner, None, None).expect("built");

    let (config, _) = body::<OptionalModule>(&owner, &built);
    assert_eq!(
        (false, false, false),
        (config.ports.is_some(), config.word.is_some(), config.routes.is_some())
    );
    // Only the root block: an absent LPM allocates no page.
    assert_eq!(1, owner.live_blocks());
    release::<OptionalModule>(&owner, built);
    owner.assert_released();
}

#[test]
fn test_build_writes_present_options_readable_in_place() {
    let owner = owner();

    let built = build_optional(&owner, Some(&[80, 443]), Some(FlakyParts { word: 7, fails: false })).expect("built");

    let (config, view) = body::<OptionalModule>(&owner, &built);
    assert_eq!(
        (Some(vec![80, 443]), Some(7)),
        (
            config.ports.get().map(|ports| ports.as_slice(&view).to_vec()),
            config.word.get().map(|word| word.0)
        )
    );
    // The root block and the ports.
    assert_eq!(2, owner.live_blocks());
    release::<OptionalModule>(&owner, built);
    owner.assert_released();
}

#[test]
fn test_build_releases_present_options_when_memory_runs_out_at_each_allocation() {
    let fits = sweep_limits(0..8, owner_failing_alloc_after, |owner| {
        build_optional(owner, Some(&[80, 443]), Some(FlakyParts { word: 7, fails: false }))
    });

    // The root block and the ports.
    assert_eq!(2, fits);
}

/// verifies that a refused present value leaves its option owning nothing and
/// the option written before it is returned.
#[test]
fn test_build_returns_everything_when_a_present_option_is_refused() {
    let owner = owner();

    let err = build_optional(&owner, Some(&[80, 443]), Some(FlakyParts { word: 7, fails: true })).expect_err("refused");

    assert_eq!(BuildError::Refused(Refusal::OutOfMemory), err);
    owner.assert_released();
}

/// An object of policy state: the builder reads only its type name and its
/// configuration type.
struct PolicyState;

impl Object for PolicyState {
    const TYPE: &'static str = "policy_state";
    type Config = Policy;
    type Prepared<'p> = ();

    fn prepare(_: &ObjectCommitCx<'_, Self>) {}
}

/// Builds the policy object with the rules, each an id and its ports.
fn build_policy_object(
    owner: &TestOwner,
    rules: &[(u64, &[u64])],
) -> Result<BuiltObject<PolicyState, TestOwner>, Failure> {
    ObjectBuilder::<PolicyState>::build(owner, "state0", |b| policy(b, rules))
}

/// Releases an object built in the given owner.
fn release_object(owner: &TestOwner, built: BuiltObject<PolicyState, TestOwner>) {
    // SAFETY: handed over by the builder, unreferenced, released once.
    unsafe { built.destroy(owner) };
}

#[test]
fn test_object_build_writes_nested_slices_and_a_box() {
    let owner = owner();

    let built = build_policy_object(&owner, &[(1, &[80, 443]), (2, &[])]).expect("built");

    // SAFETY: the object was built in this owner's memory; the view resolves
    // through the root of the arena it lives in, frozen for the test.
    let (config, view) = unsafe { (built.config(&owner), View::new(owner.root())) };
    let rules: Vec<_> = config
        .rules
        .as_slice(&view)
        .iter()
        .map(|rule| (rule.id, rule.ports.as_slice(&view).to_vec()))
        .collect();
    assert_eq!(
        (vec![(1, vec![80, 443]), (2, vec![])], 0, 0xa5),
        (rules, config.default.get(&view).id, config.mask.load(Ordering::Relaxed))
    );
    release_object(&owner, built);
    owner.assert_released();
}

#[test]
fn test_object_build_initialises_an_object_header() {
    let owner = owner();

    let built = build_policy_object(&owner, &[(1, &[80])]).expect("built");

    assert_eq!(LiveHeaders { modules: 0, objects: 1 }, owner.live_headers());
    release_object(&owner, built);
    owner.assert_released();
}

#[test]
fn test_object_destroy_releases_nested_blocks_and_the_object_header() {
    let owner = owner();
    let built = build_policy_object(&owner, &[(1, &[80, 443]), (2, &[]), (3, &[53])]).expect("built");

    release_object(&owner, built);

    owner.assert_released();
}

#[test]
fn test_object_build_releases_everything_when_memory_runs_out_at_each_allocation() {
    let fits = (0..64)
        .find(|&limit| {
            let owner = owner_failing_alloc_after(limit);
            let result = build_policy_object(&owner, &[(1, &[80, 443]), (2, &[]), (3, &[53])]);
            let fits = match result {
                Ok(built) => {
                    release_object(&owner, built);
                    true
                }
                Err(err) => {
                    assert_eq!(BuildError::Refused(Refusal::OutOfMemory), err);
                    false
                }
            };
            owner.assert_released();
            fits
        })
        .expect("a limit lets the build succeed");

    // The root block, the rules, the ports of two rules and the default rule.
    assert_eq!(5, fits);
}

#[test]
fn test_object_build_frees_the_root_block_of_a_refused_header() {
    let owner = owner();
    owner.fail_after(Call::Header, 0);

    let err = build_policy_object(&owner, &[(1, &[80])]).expect_err("refused header");

    assert_eq!(BuildError::Owner(TestError::Header), err);
    owner.assert_released();
}

#[test]
fn test_object_build_returns_the_error_of_the_closure_and_releases() {
    let owner = owner();

    let err = ObjectBuilder::<PolicyState>::build::<AuthorError, _>(&owner, "state0", |b| {
        let _rules = b.slice::<Rule>(1, |_| {
            Ok(RuleParts {
                id: 1,
                ports: b.slice(1, |_| Ok(80))?,
            })
        })?;
        Err(AuthorError::Input("the state is invalid"))
    })
    .expect_err("invalid state");

    assert_eq!(AuthorError::Input("the state is invalid"), err);
    owner.assert_released();
}

// Unwinding is std-only.
#[allow(clippy::std_instead_of_core)]
#[test]
fn test_object_build_releases_everything_when_the_closure_panics() {
    use std::panic::{AssertUnwindSafe, catch_unwind};

    let owner = owner();

    let result = catch_unwind(AssertUnwindSafe(|| {
        ObjectBuilder::<PolicyState>::build::<Failure, _>(&owner, "state0", |b| {
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
fn test_object_build_returns_everything_when_a_write_is_refused() {
    struct FlakyState;

    impl Object for FlakyState {
        const TYPE: &'static str = "flaky_state";
        type Config = [Flaky; 2];
        type Prepared<'p> = ();

        fn prepare(_: &ObjectCommitCx<'_, Self>) {}
    }

    let owner = owner();

    let err = ObjectBuilder::<FlakyState>::build::<Failure, _>(&owner, "state0", |_| {
        Ok([
            FlakyParts { word: 1, fails: false },
            FlakyParts { word: 2, fails: true },
        ])
    })
    .expect_err("refused write");

    assert_eq!(BuildError::Refused(Refusal::OutOfMemory), err);
    owner.assert_released();
}

/// verifies that the test owner records a header finalised as the other kind
/// than it was initialised as, which a builder mixing the kinds would do.
#[test]
fn test_test_owner_records_a_header_finalised_as_the_other_kind() {
    let owner = owner();
    let block = owner.alloc(64).expect("block");
    // SAFETY: the block is the owner's and large enough for the test owner,
    // which reads no header bytes.
    let header = unsafe { Header::at(block) };
    // SAFETY: as above.
    unsafe { owner.init_header(header, "fixture", "config0") }.expect("header");

    // SAFETY: the header is the owner's and unreferenced.
    unsafe { owner.fini_object_header(header) };

    assert_eq!(
        vec!["a header finalised that is unknown, of the other kind or has live blocks"],
        owner.violations()
    );
}

/// A block of ports, then links to two policy objects: a refused link leaves
/// the block before it to be returned.
#[derive(ShmLayout)]
#[repr(C)]
struct Linked {
    ports: ShmSlice<u64>,
    primary: ObjectLink<PolicyState>,
    backup: ObjectLink<PolicyState>,
}

type LinkedModule = Fixture<Linked>;

/// The parts of a configuration linking the policy objects of the two names.
fn linked<'b>(b: Scope<'b, '_, TestOwner>, names: [&str; 2]) -> Result<LinkedParts<'b, TestOwner>, Failure> {
    Ok(LinkedParts {
        ports: b.slice(2, |idx| Ok(80 + idx as u64))?,
        primary: b.object(names[0])?,
        backup: b.object(names[1])?,
    })
}

/// verifies that a module build links each object by its type and name and
/// writes the index the owner assigned.
#[test]
fn test_build_links_each_object_by_type_and_name() {
    let owner = owner();

    let built = build::<LinkedModule>(&owner, |b| linked(b, ["state0", "state1"])).expect("built");

    let (config, _) = body::<LinkedModule>(&owner, &built);
    assert_eq!(
        (
            (0, 1),
            vec![
                ("policy_state".to_string(), "state0".to_string()),
                ("policy_state".to_string(), "state1".to_string())
            ]
        ),
        ((config.primary.index(), config.backup.index()), owner.object_links())
    );
    release::<LinkedModule>(&owner, built);
    owner.assert_released();
}

/// verifies that a build whose link the owner refuses, at each link, returns
/// everything it took.
#[test]
fn test_build_releases_everything_when_a_link_fails_at_each_call() {
    for allowed in 0..2 {
        let owner = owner();
        owner.fail_after(Call::Registration, allowed);

        let err = build::<LinkedModule>(&owner, |b| linked(b, ["state0", "state1"])).expect_err("refused link");

        assert_eq!(BuildError::Owner(TestError::Registration), err);
        owner.assert_released();
    }
}

/// verifies that an object name C would cut short is refused before the owner
/// is asked, and the build returns everything it took.
#[test]
fn test_build_refuses_an_object_name_that_does_not_fit_its_c_field() {
    let overlong = "s".repeat(CP_OBJECT_NAME_LEN as usize);
    for name in [overlong.as_str(), "sta\0te", ""] {
        let owner = owner();

        let err = build::<LinkedModule>(&owner, |b| linked(b, ["state0", name])).expect_err("refused name");

        assert_eq!(
            (BuildError::Refused(Refusal::InvalidName), 1),
            (err, owner.object_links().len())
        );
        owner.assert_released();
    }
}

/// verifies that an object name filling its C field with the terminator is
/// linked whole.
#[test]
fn test_build_links_an_object_name_that_fills_its_c_field() {
    let owner = owner();
    let name = "s".repeat(CP_OBJECT_NAME_LEN as usize - 1);

    let built = build::<LinkedModule>(&owner, |b| linked(b, ["state0", &name])).expect("built");

    assert_eq!(Some(name), owner.object_links().pop().map(|(_, name)| name));
    release::<LinkedModule>(&owner, built);
    owner.assert_released();
}

/// verifies that a pair linked twice is linked once, as C does: both fields
/// hold the same index.
#[test]
fn test_build_links_the_same_object_twice_at_one_index() {
    let owner = owner();

    let built = build::<LinkedModule>(&owner, |b| linked(b, ["state0", "state0"])).expect("built");

    let (config, _) = body::<LinkedModule>(&owner, &built);
    assert_eq!(
        ((0, 0), 1),
        (
            (config.primary.index(), config.backup.index()),
            owner.object_links().len()
        )
    );
    release::<LinkedModule>(&owner, built);
    owner.assert_released();
}

/// An object type other than [`PolicyState`] of the same configuration.
struct OtherState;

impl Object for OtherState {
    const TYPE: &'static str = "other_state";
    type Config = Policy;
    type Prepared<'p> = ();

    fn prepare(_: &ObjectCommitCx<'_, Self>) {}
}

/// A counter, then links to one name under two object types.
#[derive(ShmLayout)]
#[repr(C)]
struct Mixed {
    hits: Counter,
    policy: ObjectLink<PolicyState>,
    other: ObjectLink<OtherState>,
}

type MixedModule = Fixture<Mixed>;

/// Builds the mixed configuration, linking `state0` under both types.
fn build_mixed(owner: &TestOwner) -> Built<MixedModule, TestOwner> {
    build::<MixedModule>(owner, |b| {
        Ok(MixedParts {
            hits: b.counter("hits")?,
            policy: b.object("state0")?,
            other: b.object("state0")?,
        })
    })
    .expect("built")
}

#[test]
fn test_build_links_one_name_under_two_types_at_distinct_indexes() {
    let owner = owner();

    let built = build_mixed(&owner);

    let (config, _) = body::<MixedModule>(&owner, &built);
    assert_eq!((0, 1), (config.policy.index(), config.other.index()));
    release::<MixedModule>(&owner, built);
    owner.assert_released();
}

/// verifies that object links are numbered apart from counters: a counter
/// registered first leaves the first link at index zero.
#[test]
fn test_build_numbers_object_links_apart_from_counters() {
    let owner = owner();

    let built = build_mixed(&owner);

    let (config, _) = body::<MixedModule>(&owner, &built);
    assert_eq!((0, 0), (config.hits.id(), config.policy.index()));
    release::<MixedModule>(&owner, built);
    owner.assert_released();
}

/// A worker's budget, the shape of per-worker state: three plain words.
#[derive(Clone, Copy, Debug, PartialEq, ShmLayout)]
#[repr(C)]
struct Budget {
    tokens: u64,
    last: u64,
    spent: u64,
}

/// A value wider than a 64-byte line.
#[derive(Clone, Copy, Debug, PartialEq, ShmLayout)]
#[repr(C)]
struct Wide {
    words: [u64; 9],
}

/// A per-worker budget beside a plain word.
#[derive(ShmLayout)]
#[repr(C)]
struct Budgets {
    word: u64,
    budgets: PerWorker<Budget>,
}

type BudgetsModule = Fixture<Budgets>;
type WideModule = Fixture<PerWorker<Wide>>;

/// An object of per-worker budgets.
struct BudgetsState;

impl Object for BudgetsState {
    const TYPE: &'static str = "budgets_state";
    type Config = Budgets;
    type Prepared<'p> = ();

    fn prepare(_: &ObjectCommitCx<'_, Self>) {}
}

const BUDGET: Budget = Budget { tokens: 7, last: 11, spent: 13 };

/// An owner of an empty arena for a dataplane of the given workers.
fn owner_of_workers(workers: u64) -> TestOwner {
    owner().with_workers(workers)
}

/// Builds per-worker budgets, every slot from the same budget.
fn build_budgets(owner: &TestOwner) -> Result<Built<BudgetsModule, TestOwner>, Failure> {
    build::<BudgetsModule>(owner, |b| {
        Ok(BudgetsParts {
            word: 3,
            budgets: b.per_worker(BUDGET)?,
        })
    })
}

/// Address of the slot block of a built per-worker field.
fn slot_block<T: Copy + ShmLayout>(field: &PerWorker<T>) -> usize {
    core::ptr::from_ref(field).addr().wrapping_add_signed(field.offset())
}

/// Addresses of the slots of the given workers in a slot block at an address.
fn slots_in(block: usize, workers: usize, stride: usize) -> Vec<usize> {
    let first = PerWorker::<u8>::first_slot(block);
    (0..workers).map(|worker| first + worker * stride).collect()
}

/// Addresses of the slots of a built per-worker field.
fn slot_addrs<T: Copy + ShmLayout>(field: &PerWorker<T>) -> Vec<usize> {
    slots_in(slot_block(field), field.worker_count() as usize, PerWorker::<T>::STRIDE)
}

/// The value in each slot of a built per-worker field of the owner.
fn slot_values<T: Copy + ShmLayout>(owner: &TestOwner, field: &PerWorker<T>) -> Vec<T> {
    slot_addrs(field)
        .into_iter()
        // SAFETY: each slot of the field holds a `T` the build wrote, aligned,
        // and nothing writes it while the test reads.
        .map(|addr| unsafe { owner.root().ptr::<T>(addr).read() })
        .collect()
}

/// The 128-byte lines a value of the size at the address touches.
fn lines(addr: usize, size: usize) -> core::ops::RangeInclusive<usize> {
    addr / 128..=(addr + size - 1) / 128
}

/// Reports whether the slots of `T` of the given workers in a slot block at
/// an address each touch 128-byte lines of their own, which hold no byte
/// outside the block.
fn own_lines<T: Copy + ShmLayout>(block: usize, workers: usize) -> bool {
    let size = size_of::<T>();
    let end = block + PerWorker::<T>::block_size(workers).expect("block size");
    let addrs = slots_in(block, workers, PerWorker::<T>::STRIDE);
    addrs.iter().enumerate().all(|(idx, &addr)| {
        let mine = lines(addr, size);
        *mine.start() * 128 >= block
            && (*mine.end() + 1) * 128 <= end
            && addrs[idx + 1..]
                .iter()
                .all(|&other| mine.end() < lines(other, size).start())
    })
}

#[test]
fn test_per_worker_build_writes_the_initial_value_into_a_slot_per_worker() {
    let owner = owner_of_workers(3);

    let built = build_budgets(&owner).expect("built");

    let (config, _) = body::<BudgetsModule>(&owner, &built);
    assert_eq!(
        (3, 3, vec![BUDGET; 3]),
        (
            config.word,
            config.budgets.worker_count(),
            slot_values(&owner, &config.budgets)
        )
    );
    release::<BudgetsModule>(&owner, built);
    owner.assert_released();
}

#[test]
fn test_per_worker_slots_start_on_a_128_byte_line_with_a_stride_of_128_bytes() {
    let owner = owner_of_workers(4);

    let built = build_budgets(&owner).expect("built");

    let (config, _) = body::<BudgetsModule>(&owner, &built);
    let addrs = slot_addrs(&config.budgets);
    assert_eq!((0, 128), (addrs[0] % 128, PerWorker::<Budget>::STRIDE));
    release::<BudgetsModule>(&owner, built);
    owner.assert_released();
}

#[test]
fn test_per_worker_slots_share_no_128_byte_line_with_each_other_or_outside() {
    let owner = owner_of_workers(4);

    let built = build::<WideModule>(&owner, |b| b.per_worker(Wide { words: [5; 9] })).expect("built");

    let (config, _) = body::<WideModule>(&owner, &built);
    assert!(own_lines::<Wide>(slot_block(config), 4));
    release::<WideModule>(&owner, built);
    owner.assert_released();
}

/// verifies that every worker's slot keeps 128-byte lines of its own, holding
/// no byte outside the block, whether the block starts on a 128-byte line or
/// 64 bytes past one, as a sanitizer build shifts it.
#[test]
fn test_per_worker_layout_keeps_slots_on_own_128_byte_lines_at_either_start() {
    fn check<T: Copy + ShmLayout>() {
        for block in [4096, 4096 + 64] {
            assert!(own_lines::<T>(block, 4), "{} bytes from {block}", size_of::<T>());
        }
    }

    check::<u8>();
    check::<Budget>();
    check::<[u64; 8]>();
    check::<Wide>();
    check::<[u64; 16]>();
    check::<[u64; 17]>();
}

#[test]
fn test_per_worker_stride_is_the_value_rounded_up_to_128_bytes() {
    assert_eq!(
        (128, 128, 128, 256),
        (
            PerWorker::<Budget>::STRIDE,
            PerWorker::<Wide>::STRIDE,
            PerWorker::<[u64; 16]>::STRIDE,
            PerWorker::<[u64; 17]>::STRIDE,
        )
    );
}

/// Lanes, each with per-worker budgets, in a slice and a box.
#[derive(ShmLayout)]
#[repr(C)]
struct Lane {
    id: u64,
    budgets: PerWorker<Budget>,
}

#[derive(ShmLayout)]
#[repr(C)]
struct LaneSet {
    lanes: ShmSlice<Lane>,
    spare: ShmBox<Lane>,
}

type LaneSetModule = Fixture<LaneSet>;

#[test]
fn test_per_worker_nested_in_owned_blocks_is_released_through_its_parents() {
    let owner = owner_of_workers(3);

    let built = build::<LaneSetModule>(&owner, |b| {
        Ok(LaneSetParts {
            lanes: b.slice(2, |idx| {
                Ok(LaneParts {
                    id: idx as u64,
                    budgets: b.per_worker(BUDGET)?,
                })
            })?,
            spare: b.boxed(LaneParts {
                id: 9,
                budgets: b.per_worker(BUDGET)?,
            })?,
        })
    })
    .expect("built");

    let (config, view) = body::<LaneSetModule>(&owner, &built);
    let lanes = config.lanes.as_slice(&view);
    // The root block, the lanes, the spare lane and three slot blocks.
    assert_eq!(
        (6, vec![BUDGET; 3], vec![BUDGET; 3]),
        (
            owner.live_blocks(),
            slot_values(&owner, &lanes[1].budgets),
            slot_values(&owner, &config.spare.get(&view).budgets)
        )
    );
    release::<LaneSetModule>(&owner, built);
    owner.assert_released();
}

#[test]
fn test_per_worker_build_without_workers_leaves_the_field_null_and_unallocated() {
    let owner = owner_of_workers(0);

    let built = build_budgets(&owner).expect("built");

    let (config, _) = body::<BudgetsModule>(&owner, &built);
    assert_eq!(
        (0, 0, 1),
        (
            config.budgets.offset(),
            config.budgets.worker_count(),
            owner.live_blocks()
        )
    );
    release::<BudgetsModule>(&owner, built);
    owner.assert_released();
}

/// verifies that a module build sizes the slot block by the owner's workers.
#[test]
fn test_per_worker_module_build_sizes_the_slots_by_the_owners_workers() {
    let owner = owner_of_workers(5);

    let built = build_budgets(&owner).expect("built");

    let size = PerWorker::<Budget>::block_size(5).expect("block size");
    assert!(owner.live_block_sizes().contains(&size));
    release::<BudgetsModule>(&owner, built);
    owner.assert_released();
}

/// verifies that an object build sizes the slot block by the owner's workers.
#[test]
fn test_per_worker_object_build_sizes_the_slots_by_the_owners_workers() {
    let owner = owner_of_workers(5);

    let built = ObjectBuilder::<BudgetsState>::build::<Failure, _>(&owner, "state0", |b| {
        Ok(BudgetsParts {
            word: 0,
            budgets: b.per_worker(BUDGET)?,
        })
    })
    .expect("built");

    let size = PerWorker::<Budget>::block_size(5).expect("block size");
    assert!(owner.live_block_sizes().contains(&size));
    // SAFETY: handed over by the builder, unreferenced, released once.
    unsafe { built.destroy(&owner) };
    owner.assert_released();
}

#[test]
fn test_per_worker_build_refuses_slots_beyond_the_largest_block_without_asking_the_owner() {
    for workers in [
        yanet_sys::bindings::MEMORY_BLOCK_ALLOCATOR_MAX_SIZE as u64 / PerWorker::<Budget>::STRIDE as u64 + 1,
        u64::MAX,
    ] {
        let owner = owner_of_workers(workers);
        // The root block can be allocated; a request for any further block
        // would exhaust the memory.
        owner.fail_after(Call::Alloc, 1);

        let err = build_budgets(&owner).expect_err("slots beyond the largest block");

        assert_eq!(BuildError::Refused(Refusal::BlockTooLarge), err, "{workers} workers");
        owner.assert_released();
    }
}

#[test]
fn test_per_worker_build_releases_everything_when_the_slot_block_fails() {
    let owner = owner_of_workers(4);
    owner.fail_after(Call::Alloc, 1);

    let err = build_budgets(&owner).expect_err("no memory for the slots");

    assert_eq!(BuildError::Refused(Refusal::OutOfMemory), err);
    owner.assert_released();
}

#[test]
fn test_per_worker_build_returns_the_slots_of_a_failed_closure() {
    let owner = owner_of_workers(4);

    let err = Builder::<BudgetsModule>::build::<AuthorError, _>(&owner, "budgets0", |b| {
        let _budgets = b.per_worker(BUDGET)?;
        Err(AuthorError::Input("the budgets are invalid"))
    })
    .expect_err("invalid budgets");

    assert_eq!(AuthorError::Input("the budgets are invalid"), err);
    owner.assert_released();
}

#[test]
fn test_per_worker_object_build_writes_the_slots_and_releases_them() {
    let owner = owner_of_workers(2);

    let built = ObjectBuilder::<BudgetsState>::build::<Failure, _>(&owner, "state0", |b| {
        Ok(BudgetsParts {
            word: 1,
            budgets: b.per_worker(BUDGET)?,
        })
    })
    .expect("built");

    // SAFETY: the object was built in this owner's memory.
    let config = unsafe { built.config(&owner) };
    assert_eq!(vec![BUDGET; 2], slot_values(&owner, &config.budgets));
    // SAFETY: handed over by the builder, unreferenced, released once.
    unsafe { built.destroy(&owner) };
    owner.assert_released();
}

/// A region of budgets per worker beside a plain word.
#[derive(ShmLayout)]
#[repr(C)]
struct Flows {
    word: u64,
    table: PerWorkerSlice<Budget>,
}

type FlowsModule = Fixture<Flows>;

/// An object of a region of budgets per worker.
struct FlowsState;

impl Object for FlowsState {
    const TYPE: &'static str = "flows_state";
    type Config = Flows;
    type Prepared<'p> = ();

    fn prepare(_: &ObjectCommitCx<'_, Self>) {}
}

/// Builds a region of `len` budgets per worker, every one the same budget.
fn build_flows(owner: &TestOwner, len: usize) -> Result<Built<FlowsModule, TestOwner>, Failure> {
    build::<FlowsModule>(owner, |b| {
        Ok(FlowsParts {
            word: 3,
            table: b.per_worker_slice(len, BUDGET)?,
        })
    })
}

/// Address of the region block of each worker of a built per-worker slice
/// field of the owner, read from its table.
fn region_blocks<T: Copy + ShmLayout>(owner: &TestOwner, field: &PerWorkerSlice<T>) -> Vec<usize> {
    let table = core::ptr::from_ref(field).addr().wrapping_add_signed(field.offset());
    (0..field.worker_count() as usize)
        .map(|worker| {
            let entry = table + worker * size_of::<isize>();
            // SAFETY: the table holds a word per worker the build wrote,
            // aligned, and nothing writes it while the test reads.
            entry.wrapping_add_signed(unsafe { owner.root().ptr::<isize>(entry).read() })
        })
        .collect()
}

/// The values of the region of each worker of a built per-worker slice field
/// of the owner.
fn region_values<T: Copy + ShmLayout>(owner: &TestOwner, field: &PerWorkerSlice<T>) -> Vec<Vec<T>> {
    region_blocks(owner, field)
        .into_iter()
        .map(|block| {
            let first = PerWorkerSlice::<T>::first_element(block);
            (0..field.len())
                // SAFETY: each value of the region holds a `T` the build
                // wrote, aligned, and nothing writes it while the test reads.
                .map(|idx| unsafe { owner.root().ptr::<T>(first + idx * size_of::<T>()).read() })
                .collect()
        })
        .collect()
}

/// Reports whether the region of `len` values of `T` in a block at an address
/// starts on a 128-byte line and touches 128-byte lines that hold no byte
/// outside the block.
fn region_owns_its_lines<T: Copy + ShmLayout>(block: usize, len: usize) -> bool {
    let first = PerWorkerSlice::<T>::first_element(block);
    let end = block + PerWorkerSlice::<T>::region_block(len).expect("region size");
    let mine = lines(first, len * size_of::<T>());
    first.is_multiple_of(128) && *mine.start() * 128 >= block && (*mine.end() + 1) * 128 <= end
}

#[test]
fn test_per_worker_slice_build_writes_the_initial_value_into_a_region_per_worker() {
    let owner = owner_of_workers(3);

    let built = build_flows(&owner, 5).expect("built");

    let (config, _) = body::<FlowsModule>(&owner, &built);
    assert_eq!(
        (3, 3, 5, vec![vec![BUDGET; 5]; 3]),
        (
            config.word,
            config.table.worker_count(),
            config.table.len(),
            region_values(&owner, &config.table)
        )
    );
    release::<FlowsModule>(&owner, built);
    owner.assert_released();
}

/// verifies that every worker's region is a block of its own, sized for its
/// values padded to whole 128-byte lines, beside the table of the regions.
#[test]
fn test_per_worker_slice_build_allocates_a_block_per_region_and_the_table() {
    let owner = owner_of_workers(3);

    let built = build_flows(&owner, 5).expect("built");

    let region = PerWorkerSlice::<Budget>::region_block(5).expect("region size");
    let mut sizes = owner.live_block_sizes();
    sizes.sort_unstable();
    let mut expected = vec![region; 3];
    expected.extend([3 * size_of::<isize>(), size_of::<ModuleConfig<Flows>>()]);
    expected.sort_unstable();
    // Five budgets of 24 bytes fill one 128-byte line, and the slack.
    assert_eq!((128 + 64, expected), (region, sizes));
    release::<FlowsModule>(&owner, built);
    owner.assert_released();
}

#[test]
fn test_per_worker_slice_regions_start_on_and_keep_to_their_own_128_byte_lines() {
    let owner = owner_of_workers(4);

    let built = build_flows(&owner, 11).expect("built");

    let (config, _) = body::<FlowsModule>(&owner, &built);
    let blocks = region_blocks(&owner, &config.table);
    assert!(blocks.iter().all(|&block| region_owns_its_lines::<Budget>(block, 11)));
    release::<FlowsModule>(&owner, built);
    owner.assert_released();
}

#[test]
fn test_per_worker_slice_region_blocks_do_not_overlap() {
    let owner = owner_of_workers(4);

    let built = build_flows(&owner, 11).expect("built");

    let (config, _) = body::<FlowsModule>(&owner, &built);
    let size = PerWorkerSlice::<Budget>::region_block(11).expect("region size");
    let mut blocks = region_blocks(&owner, &config.table);
    blocks.sort_unstable();
    assert!(blocks.windows(2).all(|pair| pair[0] + size <= pair[1]), "{blocks:x?}");
    release::<FlowsModule>(&owner, built);
    owner.assert_released();
}

#[test]
fn test_per_worker_slice_write_to_one_region_leaves_the_others_unchanged() {
    let owner = owner_of_workers(3);
    let built = build_flows(&owner, 5).expect("built");
    let (config, _) = body::<FlowsModule>(&owner, &built);
    let first = PerWorkerSlice::<Budget>::first_element(region_blocks(&owner, &config.table)[1]);
    let changed = Budget { tokens: 1, last: 2, spent: 3 };

    for idx in 0..5 {
        // SAFETY: the values of worker 1's region, aligned, which nothing
        // else reads or writes while the test writes.
        unsafe {
            owner
                .root()
                .ptr::<Budget>(first + idx * size_of::<Budget>())
                .write(changed)
        };
    }

    assert_eq!(
        vec![vec![BUDGET; 5], vec![changed; 5], vec![BUDGET; 5]],
        region_values(&owner, &config.table)
    );
    release::<FlowsModule>(&owner, built);
    owner.assert_released();
}

/// verifies that a region keeps 128-byte lines of its own, holding no byte
/// outside its block, whether the block starts on a 128-byte line or 64 bytes
/// past one, as a sanitizer build shifts it.
#[test]
fn test_per_worker_slice_layout_keeps_a_region_on_own_128_byte_lines_at_either_start() {
    fn check<T: Copy + ShmLayout>(len: usize) {
        for block in [4096, 4096 + 64] {
            assert!(
                region_owns_its_lines::<T>(block, len),
                "{len} of {} bytes from {block}",
                size_of::<T>()
            );
        }
    }

    for len in [1, 5, 6, 16, 1000] {
        check::<u8>(len);
        check::<Budget>(len);
        check::<Wide>(len);
        check::<[u64; 16]>(len);
    }
}

#[test]
fn test_per_worker_slice_build_of_no_values_leaves_the_field_null_and_unallocated() {
    let owner = owner_of_workers(3);

    let built = build_flows(&owner, 0).expect("built");

    let (config, _) = body::<FlowsModule>(&owner, &built);
    assert_eq!(
        (0, 3, 0, 1),
        (
            config.table.offset(),
            config.table.worker_count(),
            config.table.len(),
            owner.live_blocks()
        )
    );
    release::<FlowsModule>(&owner, built);
    owner.assert_released();
}

/// verifies that a build for an owner without workers allocates nothing and
/// leaves the field without a table and with a length of zero, so no handle
/// pairs an empty region with a length.
#[test]
fn test_per_worker_slice_build_without_workers_leaves_the_field_null_and_unallocated() {
    let owner = owner_of_workers(0);

    let built = build_flows(&owner, 5).expect("built");

    let (config, _) = body::<FlowsModule>(&owner, &built);
    assert_eq!(
        (0, 0, 0, 1),
        (
            config.table.offset(),
            config.table.worker_count(),
            config.table.len(),
            owner.live_blocks()
        )
    );
    release::<FlowsModule>(&owner, built);
    owner.assert_released();
}

/// The most `u64`s a region the largest block holds takes: whole 128-byte
/// lines after the slack.
fn largest_region_len() -> usize {
    let max = yanet_sys::bindings::MEMORY_BLOCK_ALLOCATOR_MAX_SIZE as usize;
    (max - 64) / 128 * 128 / size_of::<u64>()
}

/// verifies that the largest region the largest block holds passes the size
/// check: the owner is asked, and its exhausted memory is what fails.
#[test]
fn test_per_worker_slice_build_of_the_largest_region_asks_the_owner() {
    let owner = owner_of_workers(2);
    // The root block can be allocated; the table cannot.
    owner.fail_after(Call::Alloc, 1);

    let err = build::<Fixture<PerWorkerSlice<u64>>>(&owner, |b| b.per_worker_slice(largest_region_len(), 0))
        .expect_err("no memory for the table");

    assert_eq!(BuildError::Refused(Refusal::OutOfMemory), err);
    owner.assert_released();
}

/// verifies that a region of one value more than the largest block holds, a
/// length whose bytes overflow, and the longest length are refused before
/// anything is allocated.
#[test]
fn test_per_worker_slice_build_refuses_a_region_beyond_the_largest_block_without_asking_the_owner() {
    for len in [largest_region_len() + 1, usize::MAX / size_of::<u64>() + 1, usize::MAX] {
        let owner = owner_of_workers(2);
        // The root block can be allocated; a request for any further block
        // would exhaust the memory.
        owner.fail_after(Call::Alloc, 1);

        let err = build::<Fixture<PerWorkerSlice<u64>>>(&owner, |b| b.per_worker_slice(len, 0))
            .expect_err("a region beyond the largest block");

        assert_eq!(BuildError::Refused(Refusal::BlockTooLarge), err, "{len} values");
        owner.assert_released();
    }
}

/// verifies that a table of more workers than the largest block holds a word
/// for, or of a count that overflows, is refused before anything is
/// allocated.
#[test]
fn test_per_worker_slice_build_refuses_a_table_beyond_the_largest_block_without_asking_the_owner() {
    let max = yanet_sys::bindings::MEMORY_BLOCK_ALLOCATOR_MAX_SIZE as u64;
    for workers in [max / size_of::<isize>() as u64 + 1, u64::MAX] {
        let owner = owner_of_workers(workers);
        // The root block can be allocated; a request for any further block
        // would exhaust the memory.
        owner.fail_after(Call::Alloc, 1);

        let err = build_flows(&owner, 1).expect_err("a table beyond the largest block");

        assert_eq!(BuildError::Refused(Refusal::BlockTooLarge), err, "{workers} workers");
        owner.assert_released();
    }
}

#[test]
fn test_per_worker_slice_build_releases_everything_when_memory_runs_out_at_each_block() {
    // The root block, the table and three regions.
    for allowed in 1..5 {
        let owner = owner_of_workers(3);
        owner.fail_after(Call::Alloc, allowed);

        let err = build_flows(&owner, 5).expect_err("no memory for a block");

        assert_eq!(BuildError::Refused(Refusal::OutOfMemory), err, "{allowed} blocks");
        owner.assert_released();
    }
}

#[test]
fn test_per_worker_slice_build_returns_the_regions_of_a_failed_closure() {
    let owner = owner_of_workers(4);

    let err = Builder::<FlowsModule>::build::<AuthorError, _>(&owner, "flows0", |b| {
        let _table = b.per_worker_slice(5, BUDGET)?;
        Err(AuthorError::Input("the flows are invalid"))
    })
    .expect_err("invalid flows");

    assert_eq!(AuthorError::Input("the flows are invalid"), err);
    owner.assert_released();
}

#[test]
fn test_per_worker_slice_object_build_writes_the_regions_and_releases_them() {
    let owner = owner_of_workers(2);

    let built = ObjectBuilder::<FlowsState>::build::<Failure, _>(&owner, "flows0", |b| {
        Ok(FlowsParts {
            word: 1,
            table: b.per_worker_slice(3, BUDGET)?,
        })
    })
    .expect("built");

    // SAFETY: the object was built in this owner's memory.
    let config = unsafe { built.config(&owner) };
    assert_eq!(vec![vec![BUDGET; 3]; 2], region_values(&owner, &config.table));
    // SAFETY: handed over by the builder, unreferenced, released once.
    unsafe { built.destroy(&owner) };
    owner.assert_released();
}
