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
use yanet_shm::{
    index::HashIndexKey,
    rel::{ShmBox, ShmSlice},
    shm::ShmLayout,
};
use yanet_sys::{
    bindings,
    common::index::HashIndex,
    cp::{Build, BuildError, Builder, Built, Init, MemoryContext, Owner, Place, Refusal},
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

/// An index or run of indexes interrupted only after it owns its contents.
#[repr(transparent)]
struct Interruptible<T>(T);

// SAFETY: the wrapper has exactly its field's shared-memory representation.
unsafe impl<T: ShmLayout> ShmLayout for Interruptible<T> {}

// SAFETY: the only field is built and released at its final offset. Once
// complete, its rollback belongs to the active transaction.
unsafe impl<T: Build> Build for Interruptible<T> {
    type Parts<'b, W: Owner> = (T::Parts<'b, W>, fn() -> Result<(), Refusal>);

    fn write<'s, 'b, W: Owner>(
        (parts, interrupt): Self::Parts<'b, W>,
        mut place: Place<'s, 'b, Self, W>,
    ) -> Result<Init<'s, Self>, Refusal> {
        // SAFETY: the field covers the whole wrapper and transfers to it on
        // success. No fallible operation separates the two transitions.
        let receipt = unsafe {
            place.write_field::<T>(0, parts)?;
            place.assume_init()
        };
        if let Err(err) = interrupt() {
            let _forgotten = core::mem::ManuallyDrop::new(receipt);
            return Err(err);
        }
        Ok(receipt)
    }

    unsafe fn free<W: Owner>(addr: usize, memory: &MemoryContext<'_, W>) {
        // SAFETY: the initialized field is the whole value at this address.
        unsafe { T::free(addr, memory) }
    }
}

/// Interrupts a completed element after an earlier element has committed.
#[allow(clippy::std_instead_of_core)]
fn interrupt_second<'b, T: Build, W: Owner>(
    scope: yanet_sys::cp::Scope<'b, '_, W>,
    unwind: bool,
    mut fill: impl FnMut(usize) -> Result<T::Parts<'b, W>, BuildError<W::Error>>,
) {
    use std::panic::{AssertUnwindSafe, catch_unwind};

    let result = catch_unwind(AssertUnwindSafe(|| {
        scope.slice::<Interruptible<T>>(3, |idx| {
            let interrupt: fn() -> Result<(), Refusal> = match (idx, unwind) {
                (0, _) => || Ok(()),
                (1, false) => || Err(Refusal::InvalidRange),
                (1, true) => || panic!("interrupted initialized index"),
                _ => panic!("no fill after the interrupted element"),
            };
            Ok((fill(idx)?, interrupt))
        })
    }));
    if unwind {
        let panic = result.expect_err("the completed write unwinds");
        assert_eq!(Some(&"interrupted initialized index"), panic.downcast_ref::<&str>());
    } else {
        assert_matches!(result.unwrap(), Err(BuildError::Refused(Refusal::InvalidRange)));
    }
}

#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_slice_interrupted_after_initialization_preserves_live_sibling_context() {
    struct Sibling;
    impl Module for Sibling {
        const NAME: &'static str = "sibling";
        type Config = ShmBox<HashIndex<Ipv4Addr>>;
        type Prepared<'p> = ();

        fn handle<'id>(_: &mut Cx<'id, Self>, _: &mut Batch<'id>) {}
        fn prepare<'p>(_: &CommitCx<'p, Self>) {}
    }

    let owner = ArenaOwner::new();
    let baseline = owner.free_bytes();
    let records = [(Ipv4Addr::LOCALHOST, 7)];
    for unwind in [false, true] {
        let built = Builder::<Sibling>::build::<Error, _>(&owner, "index", |b| {
            let sibling = b.boxed::<HashIndex<Ipv4Addr>>(records.as_slice().into())?;
            interrupt_second::<HashIndex<Ipv4Addr>, _>(b, unwind, |_| Ok(records.as_slice().into()));
            Ok(sibling)
        })
        .expect("publish the earlier index alone");

        let context = owner
            .root()
            .ptr::<bindings::memory_context>(built.header().context().get());
        let sibling = owner.body(&built).get(&owner);
        let expected = ptr::from_ref(sibling).cast::<bindings::memory_context>();
        // SAFETY: read only the known live header and index, never a possibly
        // stale link target. A relative link is measured from its own word.
        unsafe {
            let head = &raw const (*context).first_child;
            assert_eq!(expected.addr(), head.addr().wrapping_add(head.read().addr()));
            assert!((&raw const (*expected).next_sibling).read().is_null());
        }
        assert_eq!(Some(7), sibling.resolve(&owner).get(Ipv4Addr::LOCALHOST));
        // SAFETY: this test owns the unpublished root and has no live views.
        unsafe { built.destroy(&owner) };
        assert_eq!(baseline, owner.free_bytes());
    }
}

#[test]
fn test_slice_interrupted_after_initialization_returns_current_index_run_before_prefix() {
    use yanet_sdk_testing::cp::{TestError, TestOwner};

    type Table = HashIndex<Ipv4Addr>;
    type Run = ShmSlice<Table>;
    let owner = TestOwner::new().with_child_return_log();
    for unwind in [false, true] {
        let built = Builder::<Index<Ipv4Addr>>::build::<BuildError<TestError>, _>(&owner, "index", |b| {
            interrupt_second::<Run, _>(b, unwind, |idx| b.slice::<Table>(idx + 1, |_| Ok([].as_slice().into())));
            Ok([].as_slice().into())
        })
        .expect("empty index without C memory");
        assert_eq!(
            [2 * size_of::<Table>(), size_of::<Table>(), 3 * size_of::<Run>()],
            owner.take_child_returns().as_slice()
        );
        owner.release(built);
        owner.assert_released();
    }
}

// The lower-case const tests hygiene of the generated destination binding.
#[allow(non_upper_case_globals)]
mod derived {
    use super::{Interruptible, ShmSlice};

    #[derive(yanet_sdk::ShmLayout)]
    #[repr(C)]
    pub(super) struct Enclosing<const __place: usize> {
        pub(super) prior: [ShmSlice<u8>; __place],
        pub(super) current: Interruptible<ShmSlice<u32>>,
    }
}

#[allow(clippy::std_instead_of_core)]
#[test]
fn test_slice_interrupted_after_initialization_releases_nested_field_before_derived_guards() {
    use std::panic::{AssertUnwindSafe, catch_unwind};

    use derived::{Enclosing, EnclosingParts};
    use yanet_sdk_testing::cp::{TestError, TestOwner};

    let owner = TestOwner::new().with_child_return_log();
    for unwind in [false, true] {
        let built = Builder::<Index<Ipv4Addr>>::build::<BuildError<TestError>, _>(&owner, "index", |b| {
            let result = catch_unwind(AssertUnwindSafe(|| {
                b.slice::<Enclosing<1>>(2, |_| {
                    let interrupt: fn() -> Result<(), Refusal> = if unwind {
                        || panic!("initialized field")
                    } else {
                        || Err(Refusal::InvalidRange)
                    };
                    Ok(EnclosingParts {
                        prior: [b.slice(17, |_| Ok(0))?],
                        current: (b.slice(2, |_| Ok(0))?, interrupt),
                    })
                })
            }));
            if unwind {
                let panic = result.expect_err("initialized field unwinds");
                assert_eq!(Some(&"initialized field"), panic.downcast_ref::<&str>());
            } else {
                assert_matches!(result.unwrap(), Err(BuildError::Refused(Refusal::InvalidRange)));
            }
            assert_eq!(
                [8, 17, 2 * size_of::<Enclosing<1>>()],
                owner.take_child_returns().as_slice()
            );
            Ok([].as_slice().into())
        })
        .expect("same build survives interrupted field");
        owner.release(built);
        owner.assert_released();
    }
}

#[allow(clippy::std_instead_of_core)]
#[test]
fn test_boxed_write_unwind_after_initialization_releases_child_and_box_inside_build() {
    use std::panic::{AssertUnwindSafe, catch_unwind};

    use yanet_sdk_testing::cp::{TestError, TestOwner};

    type Ports = ShmSlice<u64>;
    let owner = TestOwner::new().with_child_return_log();
    let built = Builder::<Index<Ipv4Addr>>::build::<BuildError<TestError>, _>(&owner, "index", |b| {
        let ports = b.slice(3, |_| Ok(80))?;
        let panic = catch_unwind(AssertUnwindSafe(|| {
            b.boxed::<Interruptible<Ports>>((ports, || panic!("initialized box")))
        }))
        .expect_err("initialized box unwinds");
        assert_eq!(Some(&"initialized box"), panic.downcast_ref::<&str>());
        assert_eq!([24, size_of::<Ports>()], owner.take_child_returns().as_slice());
        assert_eq!(1, owner.live_blocks(), "only the root is still allocated");
        Ok([].as_slice().into())
    })
    .expect("same build survives interrupted box");
    owner.release(built);
    owner.assert_released();
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
