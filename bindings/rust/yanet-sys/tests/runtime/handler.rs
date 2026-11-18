//! Miri reach of the runtime's unsafe code: the exported handler and commit
//! entries run over a hand-laid context, so that Miri executes the reads of
//! the absolute pointers they follow. No module behaviour is tested here.

use core::{
    convert::Infallible,
    mem::{offset_of, size_of},
};

use yanet_shm::{rel::RelPtr, shm::ShmLayout};
use yanet_sys::{
    bindings,
    cp::{Build, Init, MemoryContext, Owner, Place, Refusal},
    dp::{
        Batch, CommitCx, Counter, CounterRef, Cx, Device, Module, TaggedCounter, TaggedCounters, TaggedCountersRef,
        Traffic, Verdict,
    },
};

use crate::common::{Ectx, numbered, write};

/// Configuration of the reach module: a counter, a device, a pair of packet
/// and byte counts, a pair of a tagged registry, the pairs of that registry
/// picked by id and a nested counter of four pairs.
#[repr(C)]
struct Config {
    counter: Counter,
    device: Device,
    pair: Counter<Traffic>,
    tagged: TaggedCounter<Traffic>,
    by_id: TaggedCounters<Traffic>,
    pairs: Counter<[[Traffic; 2]; 2]>,
}

// SAFETY: seven registry indices and a number of counters of eight bytes,
// which accept every bit pattern, with no padding and no drop.
unsafe impl ShmLayout for Config {}

// SAFETY: the configuration is laid out by hand and never built: its parts
// cannot exist, so nothing is ever written, and it owns no block to free.
unsafe impl Build for Config {
    type Parts<'b, W: Owner> = Infallible;

    fn write<'s, 'b, W: Owner>(parts: Infallible, _: Place<'s, 'b, Self, W>) -> Result<Init<'s, Self>, Refusal> {
        match parts {}
    }

    unsafe fn free<W: Owner>(_: usize, _: &MemoryContext<'_, W>) {}
}

/// Reads everything the runtime hands a module: the configuration, prepared
/// counter handles, counters by value, those of a tagged registry included,
/// and a device target.
///
/// It adds to every counter per packet, through the handles and by value, to
/// the registry's pair of the id its first byte names and to the second pair
/// of the nested counter, reads the counters it holds handles on, and passes
/// the packets whose first byte is zero while the device is present.
struct Reach;

impl Module for Reach {
    const NAME: &'static str = "reach";
    type Config = Config;
    type Prepared<'p> = (
        CounterRef<'p>,
        CounterRef<'p, Traffic>,
        CounterRef<'p, Traffic>,
        TaggedCountersRef<'p, Traffic>,
        CounterRef<'p, [[Traffic; 2]; 2]>,
    );

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let config = cx.config();
        let (mut held, mut held_pair, mut held_tagged, by_id, pairs) = (
            cx.prepared().0.bind(cx),
            cx.prepared().1.bind(cx),
            cx.prepared().2.bind(cx),
            cx.prepared().3.bind(cx),
            cx.prepared().4.bind(cx),
        );
        let copied_pair = held_pair;
        let rebound_pair = cx.prepared().1.bind(cx);
        let copied_pairs = pairs;
        let present = config.device.resolve(cx).is_some();
        let _ = (cx.worker(), cx.now());
        batch.dispatch(|packet| {
            config.counter.add(cx, 1);
            held += 1;
            config.pair.add(cx, Traffic::packet(packet.data().len() as u64));
            let before = held_pair.get();
            let len = packet.data().len() as u64;
            held_pair += Traffic::packet(len);
            assert_eq!(
                Traffic {
                    packets: before.packets.wrapping_add(1),
                    bytes: before.bytes.wrapping_add(len),
                },
                rebound_pair.get(),
            );
            rebound_pair.add(Traffic { packets: 3, bytes: 5 });
            let expected = Traffic {
                packets: before.packets.wrapping_add(4),
                bytes: before.bytes.wrapping_add(len).wrapping_add(5),
            };
            assert_eq!(expected, held_pair.get());
            assert_eq!(expected, copied_pair.get());
            config.tagged.add(cx, Traffic::packet(packet.data().len() as u64));
            held_tagged += Traffic::packet(packet.data().len() as u64);
            by_id.add(u64::from(packet.data()[0]), Traffic::packet(packet.data().len() as u64));
            let before = pairs.get();
            let nested = pairs.at::<1>().at::<0>();
            nested.add(Traffic::packet(len));
            config.pairs.add(
                cx,
                [
                    [Traffic { packets: 1, bytes: 10 }, Traffic { packets: 2, bytes: 20 }],
                    [Traffic { packets: 4, bytes: 40 }, Traffic { packets: 8, bytes: 80 }],
                ],
            );
            let expected = [
                [
                    Traffic {
                        packets: before[0][0].packets.wrapping_add(1),
                        bytes: before[0][0].bytes.wrapping_add(10),
                    },
                    Traffic {
                        packets: before[0][1].packets.wrapping_add(2),
                        bytes: before[0][1].bytes.wrapping_add(20),
                    },
                ],
                [
                    Traffic {
                        packets: before[1][0].packets.wrapping_add(5),
                        bytes: before[1][0].bytes.wrapping_add(len).wrapping_add(40),
                    },
                    Traffic {
                        packets: before[1][1].packets.wrapping_add(8),
                        bytes: before[1][1].bytes.wrapping_add(80),
                    },
                ],
            ];
            assert_eq!(expected, copied_pairs.get());
            assert_eq!(expected[1][0], nested.get());
            let _ = (held.get(), held_pair.get(), held_tagged.get(), pairs.get());
            if present && packet.data()[0] == 0 {
                Verdict::Pass
            } else {
                Verdict::Drop
            }
        });
    }

    fn prepare<'p>(cx: &CommitCx<'p, Self>) -> Self::Prepared<'p> {
        let config = cx.config();
        (
            config.counter.resolve(cx),
            config.pair.resolve(cx),
            config.tagged.resolve(cx),
            config.by_id.resolve(cx),
            config.pairs.resolve(cx),
        )
    }
}

/// verifies that Miri reaches the runtime's unsafe code, which is its only
/// purpose here and not module behaviour.
///
/// The commit entry resolves the counters into the prepared buffer; the
/// handler entry reads the configuration, the prepared buffer, the counter
/// storage, both values of the pair and the eight of the nested counter of four
/// pairs included, the runtime storage of the tagged registry, by id below the
/// registry's one counter and past it, and the device targets from the context
/// and moves two packets over the front.
#[test]
fn test_miri_reach_of_the_handler_and_commit_entries() {
    // The configuration names counter slot 0, device 0, the pair in counter
    // slot 1, the tagged pair in slot 0 of the first tagged registry, the one
    // pair the configuration registered in that registry and the two pairs in
    // counter slot 2.
    //
    // SAFETY: the counter ids are below the three slots of the counter storage
    // and the one of the runtime storage, the pairs' of two values, which the
    // registry's one counter is, and the nested pairs' of eight; the module
    // redirects no packet.
    let mut ectx = unsafe {
        Ectx::<Reach>::new(&[0, 0, 1, 0, 0, 0, 1, 2], |arena, _, context| {
            let alloc = |size: usize| arena.alloc(size).as_ptr().addr();
            let (storage, table, word, pair, pairs) = (
                alloc(size_of::<bindings::counter_storage>()),
                alloc(3 * size_of::<RelPtr<u64>>()),
                alloc(size_of::<u64>()),
                alloc(2 * size_of::<u64>()),
                alloc(size_of::<[u64; 8]>()),
            );
            // A self-relative slot stores the offset to its target.
            write(arena, table, word as isize - table as isize);
            let second = table + size_of::<RelPtr<u64>>();
            write(arena, second, pair as isize - second as isize);
            let third = second + size_of::<RelPtr<u64>>();
            write(arena, third, pairs as isize - third as isize);
            let handles = storage + offset_of!(bindings::counter_storage, counter_value_handles);
            write(arena, handles, table as isize - handles as isize);
            context.abs_counter_storage = arena.root().ptr(storage);

            // The runtime storage of the one tagged registry, reached through
            // the array of their absolute addresses.
            let (runtime, runtime_table, tagged, storages) = (
                alloc(size_of::<bindings::counter_storage>()),
                alloc(size_of::<RelPtr<u64>>()),
                alloc(2 * size_of::<u64>()),
                alloc(size_of::<*mut bindings::counter_storage>()),
            );
            write(arena, runtime_table, tagged as isize - runtime_table as isize);
            let handles = runtime + offset_of!(bindings::counter_storage, counter_value_handles);
            write(arena, handles, runtime_table as isize - handles as isize);
            write(arena, storages, arena.root().ptr::<bindings::counter_storage>(runtime));
            context.runtime_counter_storage_count = 1;
            context.abs_runtime_counter_storages_base = arena.root().ptr(storages);

            let target = alloc(size_of::<bindings::module_device_target>());
            write(arena, target, bindings::module_device_target::default());
            context.device_target_count = 1;
            context.abs_device_targets = arena.root().ptr(target);
        })
    };
    let mut packets = numbered(2);

    // Passing means Miri found no undefined behaviour on the way; what the
    // module decided is not checked here.
    ectx.run(&mut packets);
}

/// Reads the attached worker values on every callback, including a later round.
struct WorkerValues;

impl Module for WorkerValues {
    const NAME: &'static str = "worker_values";
    type Config = u64;
    type Prepared<'p> = ();

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        assert_eq!(cx.worker() * 100 + cx.config(), cx.now());
        batch.pass_all();
    }

    fn prepare(_: &CommitCx<'_, Self>) {}
}

#[test]
fn test_worker_values_belong_to_each_callback() {
    // SAFETY: the one word is the whole configuration. This module uses no
    // storage, links, pool or routing generation.
    let mut ectx = unsafe { Ectx::<WorkerValues>::new(&[7], |_, _, _| {}) };
    let mut packets = numbered(2);
    for worker in [1, 3] {
        ectx.set_worker(worker);
        ectx.set_time(worker * 100 + 7);
        ectx.run(&mut packets);
    }
}
