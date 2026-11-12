//! The exact-match index built through the builder in a C block arena, as an
//! agent builds it: its lookups, the memory context it opens under the
//! header's, and what a release, a refusal and a panicking key leave.
//!
//! The index opens a C memory context, so these tests run natively only:
//! Miri does not run C.

extern crate alloc;

#[path = "support/arena.rs"]
mod arena;

use core::{
    assert_matches,
    cell::Cell,
    ffi::CStr,
    marker::PhantomData,
    net::{Ipv4Addr, Ipv6Addr},
    ptr,
};

use arena::ArenaOwner;
// The workspace denies unused crate dependencies, and this test names none of
// the crates it imports `as _`.
use netip as _;
use yanet_packet as _;
use yanet_sdk_testing as _;
use yanet_shm::{
    index::HashIndexKey,
    rel::{ShmBox, ShmSlice},
    shm::ShmLayout,
};
use yanet_sys::{
    bindings,
    common::index::HashIndex,
    cp::{Build, BuildError, Builder, Built, Owner, Refusal},
    dp::{Batch, CommitCx, Cx, Module},
};

type Error = BuildError<arena::Never>;

/// A key of the author's whose hash panics for the value 13.
#[derive(Clone, Copy, PartialEq, Eq)]
#[repr(C)]
struct Fragile(u32);

// SAFETY: a 32-bit word, valid for every bit pattern and needing no drop.
unsafe impl ShmLayout for Fragile {}

impl HashIndexKey for Fragile {
    const UNSPECIFIED: Self = Self(0);

    fn bits(self) -> u64 {
        assert_ne!(13, self.0, "the hash of the author's key panics");
        u64::from(self.0)
    }
}

/// A module whose configuration is one index, never run.
struct Index<K>(K);

macro_rules! index_module {
    ($($key:ty: $name:literal;)*) => {$(
        impl Module for Index<$key> {
            const NAME: &'static str = $name;
            type Config = HashIndex<$key>;
            type Prepared<'p> = ();

            fn handle<'id>(_: &mut Cx<'id, Self>, _: &mut Batch<'id>) {}

            fn prepare<'p>(_: &CommitCx<'p, Self>) {}
        }
    )*};
}

index_module! {
    Ipv4Addr: "hash-index-v4";
    Ipv6Addr: "hash-index-v6";
    Fragile: "hash-index-fragile";
}

/// A module with a container root, used only to exercise construction.
struct Container<C>(PhantomData<C>);

impl<C: Build + Sync> Module for Container<C> {
    const NAME: &'static str = "hash-index-container";
    type Config = C;
    type Prepared<'p> = ();

    fn handle<'id>(_: &mut Cx<'id, Self>, _: &mut Batch<'id>) {}

    fn prepare<'p>(_: &CommitCx<'p, Self>) {}
}

/// Records destruction of an iterator capture and can panic during it.
struct Capture<'a> {
    drops: &'a Cell<usize>,
    panics: bool,
}

impl Drop for Capture<'_> {
    fn drop(&mut self) {
        self.drops.set(self.drops.get() + 1);
        assert!(!self.panics, "the iterator capture panics on drop");
    }
}

/// Builds an index of the records over the owner.
fn build<K: HashIndexKey>(owner: &ArenaOwner, records: &[(K, u32)]) -> Result<Built<Index<K>, ArenaOwner>, Error>
where
    Index<K>: Module<Config = HashIndex<K>>,
{
    Builder::build(owner, "index", |_| Ok(records.into()))
}

/// The first memory context under the header of a configuration, if any.
fn first_child<'o, M: Module>(
    owner: &'o ArenaOwner,
    built: &Built<M, ArenaOwner>,
) -> Option<&'o bindings::memory_context> {
    let parent = owner
        .root()
        .ptr::<bindings::memory_context>(built.header().context().get());
    // SAFETY: the header's context, which the owner opened, frozen while the
    // test reads it. A link is an offset from its own field, zero for none.
    unsafe {
        let link = &raw const (*parent).first_child;
        let offset = link.read().addr();
        (offset != 0).then(|| {
            &*owner
                .root()
                .ptr::<bindings::memory_context>(link.addr().wrapping_add(offset))
        })
    }
}

/// verifies that a built index names the position of every key it was given
/// and misses another.
#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_hash_index_build_finds_every_key_at_its_position() {
    let owner = ArenaOwner::new();
    let records = [
        (Ipv4Addr::new(10, 0, 0, 1), 3),
        (Ipv4Addr::new(10, 0, 0, 2), 0),
        (Ipv4Addr::UNSPECIFIED, 7),
        (Ipv4Addr::BROADCAST, 1),
    ];

    let built = build(&owner, &records).expect("built");

    let index = owner.body(&built).resolve(&owner);
    for (key, position) in records {
        assert_eq!(Some(position), index.get(key));
    }
    assert_eq!(None, index.get(Ipv4Addr::new(10, 0, 0, 3)));
}

/// verifies that an index of IPv6 keys names their positions and misses an
/// address of their network it was not given.
#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_hash_index_build_finds_ipv6_keys() {
    let owner = ArenaOwner::new();
    let records = [
        (Ipv6Addr::new(0x2001, 0xdb8, 0, 0, 0, 0, 0, 1), 0),
        (Ipv6Addr::new(0x2001, 0xdb8, 0, 0, 0, 0, 0, 2), 1),
        (Ipv6Addr::LOCALHOST, 2),
    ];

    let built = build(&owner, &records).expect("built");

    let index = owner.body(&built).resolve(&owner);
    for (key, position) in records {
        assert_eq!(Some(position), index.get(key));
    }
    assert_eq!(None, index.get(Ipv6Addr::new(0x2001, 0xdb8, 0, 0, 0, 0, 0, 3)));
}

/// verifies that the slots of an index lie in a memory context of its own,
/// named for the index under the header's, which a release returns with
/// every block.
#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_hash_index_build_holds_its_slots_in_a_context_under_the_header() {
    let owner = ArenaOwner::new();
    let baseline = owner.free_bytes();

    let built = build(
        &owner,
        &[(Ipv4Addr::new(10, 0, 0, 1), 0), (Ipv4Addr::new(10, 0, 0, 2), 1)],
    )
    .expect("built");

    let child = first_child(&owner, &built).expect("a context under the header");
    // SAFETY: a name C wrote with its terminator.
    let name = unsafe { CStr::from_ptr(child.name.as_ptr()) };
    assert_eq!(c"hash_index", name);
    // Four slots of eight bytes, in one block.
    assert_eq!(
        (1, 32),
        (
            child.balloc_count - child.bfree_count,
            child.balloc_size - child.bfree_size
        )
    );
    assert_eq!(ptr::from_ref(owner.body(&built)).addr(), ptr::from_ref(child).addr());
    // SAFETY: built by this owner and referenced by nothing.
    unsafe { built.destroy(&owner) };
    assert_eq!(baseline, owner.free_bytes());
}

#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_hash_index_build_of_no_records_opens_no_context() {
    let owner = ArenaOwner::new();

    let built = build::<Ipv4Addr>(&owner, &[]).expect("built");

    assert!(first_child(&owner, &built).is_none());
    assert_eq!(None, owner.body(&built).resolve(&owner).get(Ipv4Addr::UNSPECIFIED));
}

/// verifies that a key given twice refuses the build with the positions of
/// both records and returns everything the build took.
#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_hash_index_build_refuses_a_key_given_twice_with_both_positions() {
    let owner = ArenaOwner::new();
    let baseline = owner.free_bytes();
    let key = Ipv6Addr::new(0x2001, 0xdb8, 0, 0, 0, 0, 0, 1);

    let refused = build(&owner, &[(key, 3), (Ipv6Addr::LOCALHOST, 5), (key, 7)]);

    assert!(matches!(
        refused,
        Err(BuildError::Refused(Refusal::DuplicateKey { first: 3, second: 7 }))
    ));
    assert_eq!(baseline, owner.free_bytes());
}

#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_hash_index_build_refuses_a_record_at_the_position_of_a_free_slot() {
    let owner = ArenaOwner::new();
    let baseline = owner.free_bytes();

    let refused = build(&owner, &[(Ipv4Addr::new(10, 0, 0, 1), u32::MAX)]);

    assert!(matches!(refused, Err(BuildError::Refused(Refusal::InvalidPosition))));
    assert_eq!(baseline, owner.free_bytes());
}

// Unwinding is std-only.
#[allow(clippy::std_instead_of_core)]
#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_hash_index_build_returns_everything_when_the_hash_of_a_key_panics() {
    use std::panic::{AssertUnwindSafe, catch_unwind};

    let owner = ArenaOwner::new();
    let baseline = owner.free_bytes();

    let result = catch_unwind(AssertUnwindSafe(|| build(&owner, &[(Fragile(1), 0), (Fragile(13), 1)])));

    assert!(result.is_err());
    assert_eq!(baseline, owner.free_bytes());
}

#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_hash_index_iterator_reads_borrowed_records_once_in_order() {
    let owner = ArenaOwner::new();
    let baseline = owner.free_bytes();
    let input = [[10, 0, 0, 1], [10, 0, 0, 2], [10, 0, 0, 3]];
    let calls = Cell::new(0);
    let mut records = input.iter().enumerate().map(|(idx, address)| {
        assert_eq!(calls.get(), idx);
        calls.set(idx + 1);
        (Ipv4Addr::from(*address), (idx as u32) * 2)
    });

    let built =
        Builder::<Index<Ipv4Addr>>::build::<Error, _, _>(&owner, "index", |_| Ok(HashIndex::builder(&mut records)))
            .expect("built");

    assert_eq!(input.len(), calls.get());
    for (idx, address) in input.into_iter().enumerate() {
        assert_eq!(
            Some((idx as u32) * 2),
            owner.body(&built).resolve(&owner).get(address.into())
        );
    }
    // SAFETY: the index is owned by this test and has no remaining readers.
    unsafe { built.destroy(&owner) };
    assert_eq!(baseline, owner.free_bytes());
}

#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_hash_index_iterator_of_no_records_opens_no_context_and_skips_projection() {
    let owner = ArenaOwner::new();
    let baseline = owner.free_bytes();
    let mut records = (0..0).map(|_| -> (Ipv4Addr, u32) { panic!("an empty index has no records") });

    let built =
        Builder::<Index<Ipv4Addr>>::build::<Error, _, _>(&owner, "index", |_| Ok(HashIndex::builder(&mut records)))
            .expect("built");

    assert_matches!(first_child(&owner, &built).map(ptr::from_ref), None);
    // SAFETY: the index is owned by this test and has no remaining readers.
    unsafe { built.destroy(&owner) };
    assert_eq!(baseline, owner.free_bytes());
}

#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_hash_index_iterator_stops_at_duplicate_and_returns_allocations() {
    let owner = ArenaOwner::new();
    let baseline = owner.free_bytes();
    let addresses = [Ipv4Addr::LOCALHOST, Ipv4Addr::BROADCAST, Ipv4Addr::LOCALHOST];
    let calls = Cell::new(0);
    let mut records = (0..4).map(|idx| {
        calls.set(calls.get() + 1);
        (addresses[idx], (idx as u32) + 3)
    });

    let refused =
        Builder::<Index<Ipv4Addr>>::build::<Error, _, _>(&owner, "index", |_| Ok(HashIndex::builder(&mut records)));

    assert_matches!(
        refused,
        Err(BuildError::Refused(Refusal::DuplicateKey { first: 3, second: 5 }))
    );
    assert_eq!(3, calls.get());
    assert_eq!(baseline, owner.free_bytes());
}

#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_hash_index_iterator_refuses_invalid_position_and_returns_allocations() {
    let owner = ArenaOwner::new();
    let baseline = owner.free_bytes();
    let mut records = core::iter::once((Ipv4Addr::LOCALHOST, u32::MAX));

    let refused =
        Builder::<Index<Ipv4Addr>>::build::<Error, _, _>(&owner, "index", |_| Ok(HashIndex::builder(&mut records)));

    assert_matches!(refused, Err(BuildError::Refused(Refusal::InvalidPosition)));
    assert_eq!(baseline, owner.free_bytes());
}

#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_hash_index_iterator_refuses_overflow_before_consuming_records() {
    let owner = ArenaOwner::new();
    let baseline = owner.free_bytes();
    let mut records = (0..usize::MAX).map(|_| -> (Ipv4Addr, u32) { panic!("an oversized index cannot be written") });

    let refused =
        Builder::<Index<Ipv4Addr>>::build::<Error, _, _>(&owner, "index", |_| Ok(HashIndex::builder(&mut records)));

    assert_matches!(refused, Err(BuildError::Refused(Refusal::BlockTooLarge)));
    assert_eq!(baseline, owner.free_bytes());
}

#[allow(clippy::std_instead_of_core)]
#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_hash_index_iterator_returns_allocations_when_projection_panics() {
    use std::panic::{AssertUnwindSafe, catch_unwind};

    let owner = ArenaOwner::new();
    let baseline = owner.free_bytes();
    let mut records = (0..2).map(|idx| {
        assert_ne!(1, idx, "the projection panics after the first record");
        (Ipv4Addr::from_bits(idx as u32), idx as u32)
    });

    let result = catch_unwind(AssertUnwindSafe(|| {
        Builder::<Index<Ipv4Addr>>::build::<Error, _, _>(&owner, "index", |_| Ok(HashIndex::builder(&mut records)))
    }));

    assert_matches!(result, Err(_));
    assert_eq!(baseline, owner.free_bytes());
}

/// An iterator that deliberately reports a chosen, possibly false length.
struct DeclaredLen<I> {
    records: I,
    len: usize,
}

impl<I: Iterator> Iterator for DeclaredLen<I> {
    type Item = I::Item;

    fn next(&mut self) -> Option<Self::Item> {
        self.records.next()
    }

    fn size_hint(&self) -> (usize, Option<usize>) {
        (self.len, Some(self.len))
    }
}

impl<I: Iterator> ExactSizeIterator for DeclaredLen<I> {}

#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_hash_index_iterator_refuses_incorrect_lengths_and_returns_allocations() {
    for (case, declared, actual) in [
        ("nonempty input declared empty", 0, 1),
        ("empty input declared nonempty", 1, 0),
        ("more records than declared", 1, 2),
        ("fewer records than declared", 2, 1),
    ] {
        let owner = ArenaOwner::new();
        let baseline = owner.free_bytes();
        let mut records = DeclaredLen {
            records: (0..actual).map(|idx| (Ipv4Addr::from_bits(idx), idx)),
            len: declared,
        };

        let refused =
            Builder::<Index<Ipv4Addr>>::build::<Error, _, _>(&owner, "index", |_| Ok(HashIndex::builder(&mut records)));

        assert_matches!(
            refused,
            Err(BuildError::Refused(Refusal::IndexLengthMismatch)),
            "{case}"
        );
        assert_eq!(baseline, owner.free_bytes(), "{case}");
    }
}

#[allow(clippy::std_instead_of_core)]
#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_hash_index_iterator_returns_allocations_when_end_check_panics() {
    use std::panic::{AssertUnwindSafe, catch_unwind};

    for (case, len) in [("empty input", 0), ("one record", 1)] {
        let owner = ArenaOwner::new();
        let baseline = owner.free_bytes();
        let mut records = DeclaredLen {
            records: (0..=len).map(|idx| {
                assert_ne!(len, idx, "the end of input check panics");
                (Ipv4Addr::from_bits(idx as u32), idx as u32)
            }),
            len,
        };

        let result = catch_unwind(AssertUnwindSafe(|| {
            Builder::<Index<Ipv4Addr>>::build::<Error, _, _>(&owner, "index", |_| Ok(HashIndex::builder(&mut records)))
        }));

        assert_matches!(result, Err(_), "{case}");
        assert_eq!(baseline, owner.free_bytes(), "{case}");
    }
}

#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_hash_index_owned_sources_build_inside_boxes_and_slices() {
    let owner = ArenaOwner::new();
    let baseline = owner.free_bytes();
    let input = [(Ipv4Addr::LOCALHOST, 3), (Ipv4Addr::BROADCAST, 7)];

    let boxed = Builder::<Container<ShmBox<HashIndex<Ipv4Addr>>>>::build::<Error, _, _>(&owner, "boxed", |scope| {
        scope.boxed(HashIndex::builder(input.into_iter()))
    })
    .expect("built box");
    let indexes = Builder::<Container<ShmSlice<HashIndex<Ipv4Addr>>>>::build::<Error, _, _>(&owner, "slice", |scope| {
        scope.slice(2, |idx| Ok(HashIndex::builder(core::iter::once(input[idx]))))
    })
    .expect("built slice");

    for (idx, (key, position)) in input.into_iter().enumerate() {
        assert_eq!(Some(position), owner.body(&boxed).get(&owner).resolve(&owner).get(key));
        assert_eq!(
            Some(position),
            owner.body(&indexes).as_slice(&owner)[idx].resolve(&owner).get(key)
        );
    }
    // SAFETY: both configurations are owned by the test without readers.
    unsafe {
        boxed.destroy(&owner);
        indexes.destroy(&owner);
    }
    assert_eq!(baseline, owner.free_bytes());
}

#[allow(clippy::std_instead_of_core)]
#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_hash_index_owned_source_panic_releases_the_box_and_index_blocks() {
    use std::panic::{AssertUnwindSafe, catch_unwind};

    let owner = ArenaOwner::new();
    let baseline = owner.free_bytes();
    let result = catch_unwind(AssertUnwindSafe(|| {
        Builder::<Container<ShmBox<HashIndex<Ipv4Addr>>>>::build::<Error, _, _>(&owner, "boxed", |scope| {
            scope.boxed(HashIndex::builder((0..2).map(|idx| {
                assert_ne!(1, idx, "the second record panics");
                (Ipv4Addr::LOCALHOST, idx)
            })))
        })
    }));

    assert_matches!(result, Err(_));
    assert_eq!(baseline, owner.free_bytes());
}

#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_hash_index_drops_owned_source_on_success_and_refusal() {
    let owner = ArenaOwner::new();
    let baseline = owner.free_bytes();
    for (case, duplicate) in [("distinct keys", false), ("duplicate key", true)] {
        let drops = Cell::new(0);
        let capture = Capture { drops: &drops, panics: false };
        let built = Builder::<Index<Ipv4Addr>>::build::<Error, _, _>(&owner, "index", |_| {
            Ok(HashIndex::builder((0..2).map(move |idx| {
                let _capture = &capture;
                (Ipv4Addr::from_bits(if duplicate { 0 } else { idx }), idx)
            })))
        });

        assert_eq!(1, drops.get(), "{case}");
        if duplicate {
            assert_matches!(
                built,
                Err(BuildError::Refused(Refusal::DuplicateKey { first: 0, second: 1 })),
                "{case}"
            );
        } else {
            // SAFETY: this configuration is owned by the test without readers.
            unsafe { built.expect("built").destroy(&owner) };
        }
        assert_eq!(baseline, owner.free_bytes(), "{case}");
    }
}

#[allow(clippy::std_instead_of_core)]
#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_hash_index_returns_allocations_when_owned_source_drop_panics() {
    use std::panic::{AssertUnwindSafe, catch_unwind};

    let owner = ArenaOwner::new();
    let baseline = owner.free_bytes();
    for (case, len) in [("empty input", 0), ("one record", 1)] {
        let drops = Cell::new(0);
        let capture = Capture { drops: &drops, panics: true };
        let result = catch_unwind(AssertUnwindSafe(|| {
            Builder::<Index<Ipv4Addr>>::build::<Error, _, _>(&owner, "index", |_| {
                Ok(HashIndex::builder((0..len).map(move |idx| {
                    let _capture = &capture;
                    (Ipv4Addr::LOCALHOST, idx)
                })))
            })
        }));

        assert_matches!(result, Err(_), "{case}");
        assert_eq!(1, drops.get(), "{case}");
        assert_eq!(baseline, owner.free_bytes(), "{case}");
    }
}
