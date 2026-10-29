//! The configuration builder over the test owner: in-place construction of
//! nested blocks, its refusals, and the release of everything a finished or a
//! failed build allocated.
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
    Batch, Build, CommitCx, Counter, Cx, Device, Module, OwnedSlice, Refusal, Registered, ShmBox, ShmLayout, ShmSlice,
    Verdict,
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

/// An owner of an empty arena.
fn owner() -> TestOwner {
    // SAFETY: no configuration of this file embeds a C object.
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
