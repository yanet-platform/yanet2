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
    net::{Ipv4Addr, Ipv6Addr},
    ptr,
};

use arena::ArenaOwner;
use yanet_shm::{index::HashIndexKey, shm::ShmLayout};
use yanet_sys::{
    bindings,
    common::index::HashIndex,
    cp::{BuildError, Builder, Built, Owner, Refusal},
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
        Builder::<Index<Ipv4Addr>>::build::<Error, _>(&owner, "index", |_| Ok(HashIndex::builder(&mut records)))
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
        Builder::<Index<Ipv4Addr>>::build::<Error, _>(&owner, "index", |_| Ok(HashIndex::builder(&mut records)))
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
        Builder::<Index<Ipv4Addr>>::build::<Error, _>(&owner, "index", |_| Ok(HashIndex::builder(&mut records)));

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
        Builder::<Index<Ipv4Addr>>::build::<Error, _>(&owner, "index", |_| Ok(HashIndex::builder(&mut records)));

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
        Builder::<Index<Ipv4Addr>>::build::<Error, _>(&owner, "index", |_| Ok(HashIndex::builder(&mut records)));

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
        Builder::<Index<Ipv4Addr>>::build::<Error, _>(&owner, "index", |_| Ok(HashIndex::builder(&mut records)))
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
            Builder::<Index<Ipv4Addr>>::build::<Error, _>(&owner, "index", |_| Ok(HashIndex::builder(&mut records)));

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
            Builder::<Index<Ipv4Addr>>::build::<Error, _>(&owner, "index", |_| Ok(HashIndex::builder(&mut records)))
        }));

        assert_matches!(result, Err(_), "{case}");
        assert_eq!(baseline, owner.free_bytes(), "{case}");
    }
}
