//! The cost per packet of the dataplane's counters and per-worker handles,
//! each bound or read before the packet loop and inside it.
//!
//! A figure is a handler's call over 32 packets through the runtime's entry,
//! over a context laid out as for the runtime's Miri tests, so it includes
//! that harness, whose floor the harness group gives. Code placement alone
//! moves small handlers by tens of percent: comparisons take medians of
//! repeated interleaved runs, in the default build and with `--profile plugin`,
//! which adds the plugins' thin LTO and keeps unwinding. The module-shaped
//! figures are the stable ones. Without a configuration generation a module's
//! prepare step finds no worker, so the worker's slot and region come from a
//! linked object.

extern crate alloc;

#[path = "../tests/runtime/common.rs"]
#[allow(dead_code, reason = "the bench uses only part of the runtime tests' fixture")]
mod common;

use core::{
    convert::Infallible,
    hint::black_box,
    mem::{offset_of, size_of, size_of_val},
    time::Duration,
};

use criterion::{BenchmarkGroup, Criterion, Throughput, criterion_group, criterion_main, measurement::WallTime};
use yanet_sdk_testing::BlockArena;
use yanet_shm::{rel::RelPtr, shm::ShmLayout};
use yanet_sys::{
    bindings,
    cp::{Build, MemoryContext, Owner, Refusal},
    dp::{
        Batch, CommitCx, Counter, CounterLayout, CounterRef, Cx, Gauge, Module, Object, ObjectCommitCx, ObjectConfig,
        ObjectCounter, ObjectLink, PerWorker, PerWorkerSlice, TaggedCounter, TaggedCounters, TaggedCountersRef,
        Traffic, Verdict, WorkerSlice, WorkerSlot,
    },
};

use crate::common::{Ectx, commit_object, numbered, write};

/// Packets of the batch each call handles.
const BATCH: usize = 32;

/// Workers the state object's slots and regions are laid out for.
const WORKERS: usize = 4;

/// The worker every call runs on, whose slot and region are not the first.
const WORKER: u64 = 1;

/// Values in each worker's region.
const LEN: usize = 8;

/// Counters picked by id, which the second tagged registry holds: the modulo
/// of every id a first byte selects.
const BY_ID: u64 = 4;

/// The configuration's words, in the order of its fields.
///
/// The word is 7, whose stamp of 14 passes the packets. The three counters
/// picked by value, the held one, the traffic, the counter by parity and the
/// running sum are counters 0 to 6. The two tagged counters by parity and the
/// held one are counters 0 to 2 of the first tagged registry, and the counters
/// picked by id are all of the second. The state object is the module's one
/// link, and the counters of what its slot and region saw are counters 7
/// and 8.
const WORDS: [u64; 19] = [7, 0, 1, 2, 3, 4, 5, 6, 0, 0, 0, 1, 0, 2, 1, BY_ID, 0, 7, 8];

// The words fill the whole configuration: a field added without its word
// fails to compile.
const _: () = assert!(size_of::<Config>() == size_of_val(&WORDS));

/// The word at the offset of a field of the configuration.
const fn word(offset: usize) -> u64 {
    WORDS[offset / size_of::<u64>()]
}

// Every counter id the words name is below the counters of its storage, so no
// handle or add reaches past the storage's table.
const _: () = {
    let by_value = offset_of!(Config, by_value);
    let counters = [
        by_value,
        by_value + size_of::<Counter>(),
        by_value + 2 * size_of::<Counter>(),
        offset_of!(Config, held),
        offset_of!(Config, traffic),
        offset_of!(Config, parity),
        offset_of!(Config, running),
        offset_of!(Config, seen),
        offset_of!(Config, filled),
    ];
    let mut idx = 0;
    while idx < counters.len() {
        assert!(
            word(counters[idx]) < COUNTERS.len() as u64,
            "a counter id past the module's storage"
        );
        idx += 1;
    }
    let by_parity = offset_of!(Config, by_parity);
    let tagged = [
        by_parity,
        by_parity + size_of::<TaggedCounter<Traffic>>(),
        offset_of!(Config, tagged_held),
    ];
    let mut idx = 0;
    while idx < tagged.len() {
        // A tagged counter is its registry's index, then its id there.
        let registry = word(tagged[idx]) as usize;
        assert!(
            registry < REGISTRIES.len() && word(tagged[idx] + size_of::<u64>()) < REGISTRIES[registry].len() as u64,
            "a tagged counter id past its registry"
        );
        idx += 1;
    }
};

/// Values of each of the module's counters, in the order of their ids.
const COUNTERS: [usize; 9] = [
    u64::VALUES,
    u64::VALUES,
    u64::VALUES,
    u64::VALUES,
    Traffic::VALUES,
    <[Traffic; 2]>::VALUES,
    u64::VALUES,
    u64::VALUES,
    u64::VALUES,
];

/// Values of each counter of each tagged registry, in the order of their ids:
/// the two by parity and the held one, then the counters picked by id.
const REGISTRIES: [&[usize]; 2] = [
    &[Traffic::VALUES, Traffic::VALUES, u64::VALUES],
    &[Traffic::VALUES; BY_ID as usize],
];

/// Configuration of every module here: the fields of both module shapes.
///
/// A word the commit doubles into a stamp, three counters picked by value, a
/// counter held by a handle, the traffic, the traffic of packets of an even
/// and an odd first byte and the running sum of the worker's packets. Then, in
/// tagged registries, the traffic by parity picked by value, a counter held by
/// a handle and counters picked by id. Last, the state object and the counters
/// of what its slot and region saw.
#[repr(C)]
struct Config {
    word: u64,
    by_value: [Counter; 3],
    held: Counter,
    traffic: Counter<Traffic>,
    parity: Counter<[Traffic; 2]>,
    running: Counter,
    by_parity: [TaggedCounter<Traffic>; 2],
    tagged_held: TaggedCounter,
    by_id: TaggedCounters<Traffic>,
    state: ObjectLink<State>,
    seen: Counter,
    filled: Counter,
}

// SAFETY: a word, registry, counter and link indices and a number of counters,
// each of eight bytes, which accept every bit pattern, with no padding and no
// drop.
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

/// Configuration of the state object: a slot and a region of `u64`s per
/// worker, and a gauge of its own.
#[repr(C)]
struct StateConfig {
    workers: PerWorker<u64>,
    regions: PerWorkerSlice<u64>,
    gauge: ObjectCounter<Gauge>,
}

// SAFETY: a per-worker field, a per-worker slice field and a counter index,
// offsets and counts of eight bytes, which accept every bit pattern, with no
// padding and no drop.
unsafe impl ShmLayout for StateConfig {}

// SAFETY: as for the module's configuration: laid out by hand and never
// built, and its blocks belong to the bench.
unsafe impl Build for StateConfig {
    type Parts<'b, W: Owner> = Infallible;

    unsafe fn write<W: Owner>(parts: Infallible, _: &MemoryContext<'_, W>, _: usize) -> Result<(), Refusal> {
        match parts {}
    }

    unsafe fn free<W: Owner>(_: usize, _: &MemoryContext<'_, W>) {}
}

/// An object of per-worker state, which prepares the handles of its context's
/// worker.
struct State;

impl Object for State {
    const TYPE: &'static str = "state";
    type Config = StateConfig;
    type Prepared<'p> = StatePrepared<'p>;

    fn prepare<'p>(cx: &ObjectCommitCx<'p, Self>) -> StatePrepared<'p> {
        let config = cx.config();
        StatePrepared {
            slot: config.workers.handle(cx),
            region: config.regions.handle(cx),
            gauge: config.gauge.resolve(cx),
        }
    }
}

/// What the state object prepares for its context's worker: the handles of
/// the worker's slot, region and values of the gauge.
struct StatePrepared<'p> {
    slot: Option<WorkerSlot<'p, u64>>,
    region: Option<WorkerSlice<'p, u64>>,
    gauge: CounterRef<'p, Gauge>,
}

/// What every module here prepares: the handles of the counters it holds and
/// of the worker's slot and region, and twice the configuration's word.
///
/// The handles of the slot and the region are copies of the state object's.
struct Handles<'p> {
    traffic: CounterRef<'p, Traffic>,
    held: CounterRef<'p>,
    parity: CounterRef<'p, [Traffic; 2]>,
    stamp: u64,
    slot: Option<WorkerSlot<'p, u64>>,
    region: Option<WorkerSlice<'p, u64>>,
    tagged_held: CounterRef<'p>,
    by_id: TaggedCountersRef<'p, Traffic>,
}

impl<'p> Handles<'p> {
    /// Resolves the counters and copies the state object's handles of the
    /// worker's slot and region.
    fn new<M>(cx: &CommitCx<'p, M>) -> Self
    where
        M: Module<Config = Config>,
    {
        let config = cx.config();
        let linked = config.state.prepared(cx);
        Self {
            traffic: config.traffic.resolve(cx),
            held: config.held.resolve(cx),
            parity: config.parity.resolve(cx),
            stamp: config.word * 2,
            slot: linked.slot,
            region: linked.region,
            tagged_held: config.tagged_held.resolve(cx),
            by_id: config.by_id.resolve(cx),
        }
    }
}

/// Passes every packet and touches no handle: the floor of the harness under
/// every other figure.
struct PassOnly;

impl Module for PassOnly {
    const NAME: &'static str = "pass_only";
    type Config = Config;
    type Prepared<'p> = Handles<'p>;

    fn handle<'id>(_: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        batch.dispatch(|_| Verdict::Pass);
    }

    fn prepare<'p>(cx: &CommitCx<'p, Self>) -> Handles<'p> {
        Handles::new(cx)
    }
}

/// Adds each packet to the traffic through the handle, bound before the loop.
struct TrafficBoundBefore;

impl Module for TrafficBoundBefore {
    const NAME: &'static str = "traffic_bound_before";
    type Config = Config;
    type Prepared<'p> = Handles<'p>;

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let traffic = cx.prepared().traffic.bind(cx);
        batch.dispatch(|packet| {
            traffic.add(Traffic::packet(packet.data().len() as u64));
            Verdict::Pass
        });
    }

    fn prepare<'p>(cx: &CommitCx<'p, Self>) -> Handles<'p> {
        Handles::new(cx)
    }
}

/// Adds each packet to the traffic through the handle, bound per packet.
struct TrafficBoundPerPacket;

impl Module for TrafficBoundPerPacket {
    const NAME: &'static str = "traffic_bound_per_packet";
    type Config = Config;
    type Prepared<'p> = Handles<'p>;

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        batch.dispatch(|packet| {
            cx.prepared()
                .traffic
                .bind(cx)
                .add(Traffic::packet(packet.data().len() as u64));
            Verdict::Pass
        });
    }

    fn prepare<'p>(cx: &CommitCx<'p, Self>) -> Handles<'p> {
        Handles::new(cx)
    }
}

/// Adds each packet to the traffic by value, which resolves the counter per
/// packet.
struct TrafficByValue;

impl Module for TrafficByValue {
    const NAME: &'static str = "traffic_by_value";
    type Config = Config;
    type Prepared<'p> = Handles<'p>;

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let traffic = &cx.config().traffic;
        batch.dispatch(|packet| {
            traffic.add(cx, Traffic::packet(packet.data().len() as u64));
            Verdict::Pass
        });
    }

    fn prepare<'p>(cx: &CommitCx<'p, Self>) -> Handles<'p> {
        Handles::new(cx)
    }
}

/// Adds each packet to the counter of the id its first byte selects through
/// the handle of the counters by id, bound before the loop.
struct ByIdBoundBefore;

impl Module for ByIdBoundBefore {
    const NAME: &'static str = "by_id_bound_before";
    type Config = Config;
    type Prepared<'p> = Handles<'p>;

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let by_id = cx.prepared().by_id.bind(cx);
        batch.dispatch(|packet| {
            let id = packet.data().first().map_or(0, |&byte| u64::from(byte) % BY_ID);
            by_id.add(id, Traffic::packet(packet.data().len() as u64));
            Verdict::Pass
        });
    }

    fn prepare<'p>(cx: &CommitCx<'p, Self>) -> Handles<'p> {
        Handles::new(cx)
    }
}

/// Adds each packet to the counter of the id its first byte selects through
/// the handle of the counters by id, bound per packet.
struct ByIdBoundPerPacket;

impl Module for ByIdBoundPerPacket {
    const NAME: &'static str = "by_id_bound_per_packet";
    type Config = Config;
    type Prepared<'p> = Handles<'p>;

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        batch.dispatch(|packet| {
            let id = packet.data().first().map_or(0, |&byte| u64::from(byte) % BY_ID);
            cx.prepared()
                .by_id
                .bind(cx)
                .add(id, Traffic::packet(packet.data().len() as u64));
            Verdict::Pass
        });
    }

    fn prepare<'p>(cx: &CommitCx<'p, Self>) -> Handles<'p> {
        Handles::new(cx)
    }
}

/// Counts each packet in the worker's slot through the handle, read from the
/// prepared value per packet.
struct SlotReadInLoop;

impl Module for SlotReadInLoop {
    const NAME: &'static str = "slot_read_in_loop";
    type Config = Config;
    type Prepared<'p> = Handles<'p>;

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let prepared = cx.prepared();
        batch.dispatch(|_| {
            if let Some(slot) = &prepared.slot {
                *slot.get_mut(cx) += 1;
            }
            Verdict::Pass
        });
    }

    fn prepare<'p>(cx: &CommitCx<'p, Self>) -> Handles<'p> {
        Handles::new(cx)
    }
}

/// Counts each packet in the worker's slot through the handle, copied before
/// the loop.
struct SlotCopiedBefore;

impl Module for SlotCopiedBefore {
    const NAME: &'static str = "slot_copied_before";
    type Config = Config;
    type Prepared<'p> = Handles<'p>;

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let slot = cx.prepared().slot;
        batch.dispatch(|_| {
            if let Some(slot) = slot {
                *slot.get_mut(cx) += 1;
            }
            Verdict::Pass
        });
    }

    fn prepare<'p>(cx: &CommitCx<'p, Self>) -> Handles<'p> {
        Handles::new(cx)
    }
}

/// Counts each packet in the value of the worker's region its first byte
/// selects, through the handle, read from the prepared value per packet.
struct RegionReadInLoop;

impl Module for RegionReadInLoop {
    const NAME: &'static str = "region_read_in_loop";
    type Config = Config;
    type Prepared<'p> = Handles<'p>;

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let prepared = cx.prepared();
        batch.dispatch(|packet| {
            if let Some(region) = &prepared.region {
                let idx = packet.data().first().map_or(0, |&byte| usize::from(byte) % LEN);
                if let Some(value) = region.get_mut(cx).get_mut(idx) {
                    *value += 1;
                }
            }
            Verdict::Pass
        });
    }

    fn prepare<'p>(cx: &CommitCx<'p, Self>) -> Handles<'p> {
        Handles::new(cx)
    }
}

/// Counts each packet in the value of the worker's region its first byte
/// selects, through the handle, copied before the loop.
struct RegionCopiedBefore;

impl Module for RegionCopiedBefore {
    const NAME: &'static str = "region_copied_before";
    type Config = Config;
    type Prepared<'p> = Handles<'p>;

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let region = cx.prepared().region;
        batch.dispatch(|packet| {
            if let Some(region) = region {
                let idx = packet.data().first().map_or(0, |&byte| usize::from(byte) % LEN);
                if let Some(value) = region.get_mut(cx).get_mut(idx) {
                    *value += 1;
                }
            }
            Verdict::Pass
        });
    }

    fn prepare<'p>(cx: &CommitCx<'p, Self>) -> Handles<'p> {
        Handles::new(cx)
    }
}

// The indices of the traffic of packets whose first byte is even and odd in
// the counter by parity.
const EVEN: usize = 0;
const ODD: usize = 1;

/// A module-shaped handler over counters of every kind and the worker's slot,
/// as the fixture plugin's module of the same name.
///
/// Per packet it counts one in the counter its first byte selects, and a
/// packet and its bytes in the traffic, in the side of the counter by parity
/// and in the tagged counter of that byte's parity and the counter of its id.
/// It adds 2 and 3 through its held handles, counts the packet in the worker's
/// slot and adds the new count to the running counter. It passes the packets
/// when the stamp is 14 and drops them otherwise. Its slot is the linked
/// object's, where the fixture's module has a per-worker field of its own.
struct Tally;

impl Module for Tally {
    const NAME: &'static str = "tally";
    type Config = Config;
    type Prepared<'p> = Handles<'p>;

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let config = cx.config();
        let prepared = cx.prepared();
        let held = prepared.held.bind(cx);
        let parity = prepared.parity.bind(cx);
        let tagged_held = prepared.tagged_held.bind(cx);
        let by_id = prepared.by_id.bind(cx);
        let pass = prepared.stamp == 14;
        batch.dispatch(|packet| {
            if let Some(byte) = packet.data().first() {
                config.by_value[usize::from(byte % 3)].add(cx, 1);
                let side = if byte % 2 == 0 {
                    parity.at::<EVEN>()
                } else {
                    parity.at::<ODD>()
                };
                side.add(Traffic::packet(packet.data().len() as u64));
                config.by_parity[usize::from(byte % 2)].add(cx, Traffic::packet(packet.data().len() as u64));
                by_id.add(u64::from(*byte) % BY_ID, Traffic::packet(packet.data().len() as u64));
            }
            held.add(2);
            tagged_held.add(3);
            config.traffic.add(cx, Traffic::packet(packet.data().len() as u64));
            if let Some(slot) = &prepared.slot {
                let count = slot.get_mut(cx);
                *count += 1;
                let count = *count;
                config.running.add(cx, count);
            }
            if pass { Verdict::Pass } else { Verdict::Drop }
        });
    }

    fn prepare<'p>(cx: &CommitCx<'p, Self>) -> Handles<'p> {
        Handles::new(cx)
    }
}

/// A module-shaped handler over a linked object's per-worker state, as the
/// fixture plugin's module of the same name without its shared atomic slots.
///
/// Per packet it counts in the object's slot of the worker and adds the new
/// count to the seen counter, and adds one to every value of the worker's
/// region and their sum to the filled counter. It holds a packet of an even
/// first byte in the object's gauge of the worker and lets one go for a packet
/// of an odd first byte while the worker holds any. It passes every packet.
struct Stash;

impl Module for Stash {
    const NAME: &'static str = "stash";
    type Config = Config;
    type Prepared<'p> = Handles<'p>;

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let linked = cx.config().state.prepared(cx);
        let seen = &cx.config().seen;
        let filled = &cx.config().filled;
        let gauge = linked.gauge.bind(cx);
        batch.dispatch(|packet| {
            if let Some(slot) = &linked.slot {
                let count = slot.get_mut(cx);
                *count += 1;
                let count = *count;
                seen.add(cx, count);
            }
            if let Some(region) = linked.region {
                region
                    .get_mut(cx)
                    .iter_mut()
                    .for_each(|value| *value = value.wrapping_add(1));
                filled.add(cx, region.get(cx).iter().copied().fold(0, u64::wrapping_add));
            }
            match packet.data().first() {
                Some(byte) if byte % 2 == 0 => gauge.add(1),
                Some(..) if gauge.get() != 0 => gauge.sub(1),
                _ => {}
            }
            Verdict::Pass
        });
    }

    fn prepare<'p>(cx: &CommitCx<'p, Self>) -> Handles<'p> {
        Handles::new(cx)
    }
}

/// Lays out a counter storage of counters of the given numbers of values, and
/// returns the storage's address.
///
/// The values of all the counters lie next to each other in one zeroed block.
fn storage(arena: &'static BlockArena, sizes: &[usize]) -> usize {
    let alloc = |size: usize| arena.alloc(size).as_ptr().addr();
    let (storage, table, values) = (
        alloc(size_of::<bindings::counter_storage>()),
        alloc(sizes.len() * size_of::<RelPtr<u64>>()),
        alloc(sizes.iter().sum::<usize>() * size_of::<u64>()),
    );
    let mut value = values;
    for (id, size) in sizes.iter().enumerate() {
        let entry = table + id * size_of::<RelPtr<u64>>();
        // SAFETY: an entry of the table just allocated, one per counter. A
        // self-relative slot stores the offset to its target.
        unsafe { write(arena, entry, value as isize - entry as isize) };
        value += size * size_of::<u64>();
    }
    let field = storage + offset_of!(bindings::counter_storage, counter_value_handles);
    // SAFETY: a field of the storage just allocated.
    unsafe { write(arena, field, table as isize - field as isize) };
    storage
}

/// Lays out the state object as the one link of the module context, committed
/// for the worker every call runs on.
///
/// The object has zeroed slots and regions of all workers, and its gauge in a
/// counter storage of its own.
fn link_state(arena: &'static BlockArena, context: &mut bindings::module_ectx) {
    let root = arena.root();
    let alloc = |size: usize| arena.alloc(size).as_ptr().addr();
    let (slot_block, region_table, object, object_ectx, object_prepared, links) = (
        alloc(PerWorker::<u64>::block_size(WORKERS).expect("slot block size")),
        alloc(WORKERS * size_of::<isize>()),
        alloc(size_of::<ObjectConfig<StateConfig>>()),
        alloc(size_of::<bindings::object_ectx>()),
        alloc(size_of::<StatePrepared<'static>>()),
        alloc(size_of::<bindings::module_object_link_ectx>()),
    );
    let region_blocks: [usize; WORKERS] =
        core::array::from_fn(|_| alloc(PerWorkerSlice::<u64>::region_block(LEN).expect("region block size")));
    let storage = storage(arena, &[Gauge::VALUES]);
    let body = object + ObjectConfig::<StateConfig>::BODY_OFFSET;
    let (workers, regions, gauge) = (
        body + offset_of!(StateConfig, workers),
        body + offset_of!(StateConfig, regions),
        body + offset_of!(StateConfig, gauge),
    );
    let object_context = bindings::object_ectx {
        abs_cp_object: root.ptr(object),
        abs_object_prepared: root.ptr(object_prepared),
        worker_idx: WORKER,
        ..Default::default()
    };
    let link = bindings::module_object_link_ectx {
        abs_object_ectx: root.ptr(object_ectx),
        ..Default::default()
    };
    // SAFETY: every write lands in a block just allocated for its value, and
    // the object's commit entry runs over the context laid out for it. A
    // self-relative slot stores the offset to its target.
    unsafe {
        write(
            arena,
            workers,
            [slot_block as isize - workers as isize, WORKERS as isize],
        );
        for (worker, block) in region_blocks.iter().enumerate() {
            let entry = region_table + worker * size_of::<isize>();
            write(arena, entry, *block as isize - entry as isize);
        }
        write(
            arena,
            regions,
            [region_table as isize - regions as isize, WORKERS as isize, LEN as isize],
        );
        write(arena, gauge, 0u64);
        write(arena, object_ectx, object_context);
        let field = object_ectx + offset_of!(bindings::object_ectx, counter_storage);
        write(arena, field, storage as isize - field as isize);
        commit_object::<State>(arena, object_ectx, object);
        write(arena, links, link);
    }
    context.object_link_count = 1;
    context.abs_object_links = root.ptr(links);
}

/// Lays out what the configuration names into the module context.
///
/// That is the module's counter storage, the storages of its tagged registries
/// and the state object.
fn lay_out(arena: &'static BlockArena, context: &mut bindings::module_ectx) {
    let root = arena.root();
    context.abs_counter_storage = root.ptr(storage(arena, &COUNTERS));
    let registries = REGISTRIES.map(|sizes| root.ptr::<bindings::counter_storage>(storage(arena, sizes)));
    let storages = arena.alloc(size_of_val(&registries)).as_ptr().addr();
    // SAFETY: the array of the registries' storages, just allocated.
    unsafe { write(arena, storages, registries) };
    context.runtime_counter_storage_count = registries.len() as u64;
    context.abs_runtime_counter_storages_base = root.ptr(storages);
    link_state(arena, context);
}

/// The committed context of a module over the layout every module here
/// shares, called on the worker the state object was committed for.
///
/// Panics when the words name counters by id the layout gives no registry
/// for, or when it gives the worker no slot or no region of all its values, so
/// that no figure is of a handler that skips a handle.
fn committed<M>() -> Ectx<M>
where
    M: Module<Config = Config, Prepared<'static> = Handles<'static>>,
{
    let by_id = offset_of!(Config, by_id) / size_of::<u64>();
    let (registry, count) = (WORDS[by_id] as usize, WORDS[by_id + 1]);
    assert_eq!(
        BY_ID, count,
        "the words name other counters by id than the handlers pick"
    );
    assert!(
        REGISTRIES
            .get(registry)
            .is_some_and(|sizes| count as usize <= sizes.len()),
        "the layout gives the counters by id no registry"
    );
    // SAFETY: the words form the configuration, whose counter ids are below
    // the counters the setup lays out in the module's storage and in its two
    // tagged registries, and whose link is the one the setup lays out. No
    // module redirects a packet.
    let mut ectx = unsafe { Ectx::<M>::new(&WORDS, |arena, _, context| lay_out(arena, context)) };
    ectx.set_worker(WORKER);
    let prepared = ectx.prepared();
    assert!(prepared.slot.is_some(), "the layout gives the worker no slot");
    assert!(
        prepared.region.is_some_and(|region| region.len() == LEN),
        "the layout gives the worker no region of all its values"
    );
    ectx
}

/// Opens a group of figures per packet.
fn per_packet<'c>(c: &'c mut Criterion, name: &str) -> BenchmarkGroup<'c, WallTime> {
    let mut group = c.benchmark_group(name);
    group.throughput(Throughput::Elements(BATCH as u64));
    group
}

/// Measures the calls of a module's handler over the batch, as the figure of
/// the module's name.
///
/// The context passes an opaque barrier on every call, so the handler is
/// called through its entry's pointer, as the dataplane calls it, and never
/// inlined into the measuring loop.
fn handler<M>(group: &mut BenchmarkGroup<'_, WallTime>)
where
    M: Module<Config = Config, Prepared<'static> = Handles<'static>>,
{
    let mut ectx = committed::<M>();
    let mut packets = numbered(BATCH);
    group.bench_function(M::NAME, |b| b.iter(|| black_box(&mut ectx).run(&mut packets)));
}

fn harness(c: &mut Criterion) {
    let mut group = per_packet(c, "harness");
    handler::<PassOnly>(&mut group);
    group.finish();
}

fn counters(c: &mut Criterion) {
    let mut group = per_packet(c, "counter");
    handler::<TrafficBoundBefore>(&mut group);
    handler::<TrafficBoundPerPacket>(&mut group);
    handler::<TrafficByValue>(&mut group);
    handler::<ByIdBoundBefore>(&mut group);
    handler::<ByIdBoundPerPacket>(&mut group);
    group.finish();
}

fn per_worker(c: &mut Criterion) {
    let mut group = per_packet(c, "per_worker");
    handler::<SlotReadInLoop>(&mut group);
    handler::<SlotCopiedBefore>(&mut group);
    handler::<RegionReadInLoop>(&mut group);
    handler::<RegionCopiedBefore>(&mut group);
    group.finish();
}

fn modules(c: &mut Criterion) {
    let mut group = per_packet(c, "module");
    handler::<Tally>(&mut group);
    handler::<Stash>(&mut group);
    group.finish();
}

/// Rounds of a second of warm-up and three of measurement per figure, with a
/// change within 2% taken as noise.
///
/// The verdict of one run against a saved baseline is a hint only: one build
/// moves by more than 2% between its own runs, so a comparison follows the
/// method the bench's doc gives.
fn config() -> Criterion {
    Criterion::default()
        .warm_up_time(Duration::from_secs(1))
        .measurement_time(Duration::from_secs(3))
        .noise_threshold(0.02)
}

criterion_group! {
    name = benches;
    config = config();
    targets = harness, counters, per_worker, modules
}
criterion_main!(benches);
