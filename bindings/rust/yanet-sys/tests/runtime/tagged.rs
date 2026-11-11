//! Counters of a tagged registry picked by id: the handle a module's prepare
//! step resolves for them over a hand-laid context, and the adds through it.

use core::{
    convert::Infallible,
    mem::{offset_of, size_of},
};

use yanet_shm::{rel::RelPtr, shm::ShmLayout};
use yanet_sys::{
    bindings,
    cp::{Build, MemoryContext, Owner, Refusal},
    dp::{Batch, CommitCx, Cx, Module, TaggedCounters, TaggedCountersRef, Traffic, Verdict},
};

use crate::common::{Ectx, numbered, write};

/// Configuration of the tallying module: counters picked by id in one tagged
/// registry.
#[repr(C)]
struct Config {
    by_id: TaggedCounters<Traffic>,
}

// SAFETY: a registry index and a number of counters of eight bytes, which
// accept every bit pattern, with no padding and no drop.
unsafe impl ShmLayout for Config {}

// SAFETY: the configuration is laid out by hand and never built: its parts
// cannot exist, so nothing is ever written, and it owns no block to free.
unsafe impl Build for Config {
    type Parts<'b, W: Owner> = Infallible;

    unsafe fn write<W: Owner>(parts: Infallible, _: &MemoryContext<'_, W>, _: usize) -> Result<(), Refusal> {
        match parts {}
    }

    unsafe fn free<W: Owner>(_: usize, _: &MemoryContext<'_, W>) {}
}

/// Adds each packet to the counter of id 0 through the handle its prepare
/// step resolved, and passes the packets.
struct Tally;

impl Module for Tally {
    const NAME: &'static str = "tally";
    type Config = Config;
    type Prepared<'p> = TaggedCountersRef<'p, Traffic>;

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let by_id = cx.prepared().bind(cx);
        batch.dispatch(|packet| {
            by_id.add(0, Traffic::packet(packet.data().len() as u64));
            Verdict::Pass
        });
    }

    fn prepare<'p>(cx: &CommitCx<'p, Self>) -> Self::Prepared<'p> {
        cx.config().by_id.resolve(cx)
    }
}

/// verifies that counters of a tagged registry the context holds no storage
/// for resolve to an empty handle, through which the handler's adds count
/// nowhere, not in a storage past the context's registries.
#[test]
fn test_tagged_counters_of_a_registry_past_the_context_count_nowhere() {
    let mut pair = 0;
    // The configuration names one counter of the second tagged registry. The
    // context holds one registry, though its array of storage addresses has a
    // second entry: a storage of one pair, which is the test's.
    //
    // SAFETY: the words are the field's registry and number of counters; the
    // module counts no other counter and redirects no packet.
    let mut ectx = unsafe {
        Ectx::<Tally>::new(&[1, 1], |arena, _, context| {
            let alloc = |size: usize| arena.alloc(size).as_ptr().addr();
            let storages = alloc(2 * size_of::<*mut bindings::counter_storage>());
            for idx in 0..2 {
                let (storage, table, values) = (
                    alloc(size_of::<bindings::counter_storage>()),
                    alloc(size_of::<RelPtr<u64>>()),
                    alloc(2 * size_of::<u64>()),
                );
                // A self-relative slot stores the offset to its target.
                write(arena, table, values as isize - table as isize);
                let handles = storage + offset_of!(bindings::counter_storage, counter_value_handles);
                write(arena, handles, table as isize - handles as isize);
                let entry = storages + idx * size_of::<*mut bindings::counter_storage>();
                write(arena, entry, arena.root().ptr::<bindings::counter_storage>(storage));
                pair = values;
            }
            context.runtime_counter_storage_count = 1;
            context.abs_runtime_counter_storages_base = arena.root().ptr(storages);
        })
    };

    ectx.run(&mut numbered(2));

    // SAFETY: the pair lies in its block, aligned, and was zeroed by the
    // arena or written by a handler that has returned.
    let values = [0, 1].map(|idx| unsafe { ectx.arena.root().ptr::<u64>(pair + idx * size_of::<u64>()).read() });
    assert_eq!([0, 0], values);
}
