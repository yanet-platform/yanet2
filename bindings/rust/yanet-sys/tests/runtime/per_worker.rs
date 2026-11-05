//! Miri reach of per-worker state: a handler reads and writes its worker's
//! slot or region, borrowed shared or exclusive, through the handle a
//! module's or an object's prepare step made, across calls and next to other
//! workers' slots and regions.

use core::{cell::Cell, convert::Infallible};

use yanet_sdk_testing::BlockArena;
use yanet_shm::shm::ShmLayout;
use yanet_sys::{
    bindings,
    cp::{Build, MemoryContext, Owner, Refusal},
    dp::{
        Batch, CommitCx, Cx, Module, Object, ObjectCommitCx, ObjectConfig, ObjectLink, PerWorker, PerWorkerSlice,
        Verdict, WorkerSlice, WorkerSlot, rt,
    },
};

use crate::common::{Ectx, numbered, write};

/// Workers the slots are laid out for.
const WORKERS: usize = 3;

/// What a packet of an odd first byte adds to its worker's slot.
const ODD: u64 = 1 << 32;

/// Values in a slot of an array, wide enough that a copy of it costs.
#[cfg(miri)]
const VALUES: usize = 96;

/// Configuration of a counting module: a slot of `T` per worker.
#[repr(C)]
struct Config<T = u64> {
    seen: PerWorker<T>,
}

// SAFETY: a per-worker field, an offset and a count, which accept every bit
// pattern, with no padding and no drop.
unsafe impl<T: Copy + ShmLayout> ShmLayout for Config<T> {}

// SAFETY: the configuration is laid out by hand and never built: its parts
// cannot exist, so nothing is ever written, and its slot block belongs to the
// test.
unsafe impl<T: Copy + ShmLayout> Build for Config<T> {
    type Parts<'b, W: Owner> = Infallible;

    unsafe fn write<W: Owner>(parts: Infallible, _: &MemoryContext<'_, W>, _: usize) -> Result<(), Refusal> {
        match parts {}
    }

    unsafe fn free<W: Owner>(_: usize, _: &MemoryContext<'_, W>) {}
}

/// The committed context of a module of a counting configuration over a slot
/// block of [`WORKERS`] zeroed slots, and the block's address.
fn seen<T: Copy + ShmLayout, M: Module<Config = Config<T>>>() -> (Ectx<M>, usize) {
    let mut slots = 0;
    // SAFETY: the words are a null per-worker field the setup points at the
    // slot block before the commit; the module counts no counter and
    // redirects no packet.
    let ectx = unsafe {
        Ectx::<M>::new(&[0, WORKERS as u64], |arena, body, _| {
            let size = PerWorker::<T>::block_size(WORKERS).expect("block size");
            slots = arena.alloc(size).as_ptr().addr();
            write(arena, body, slots as isize - body as isize);
        })
    };
    (ectx, slots)
}

/// The value in each slot of a block of slots of `T`.
fn slots<T: Copy + ShmLayout, M: Module>(ectx: &Ectx<M>, block: usize) -> [T; WORKERS] {
    // SAFETY: each slot lies in the block, aligned, and was zeroed by the
    // arena, which `T` accepts, or written by a handler that has returned.
    core::array::from_fn(|worker| unsafe {
        ectx.arena
            .root()
            .ptr::<T>(PerWorker::<T>::first_slot(block) + worker * PerWorker::<T>::STRIDE)
            .read()
    })
}

#[cfg(miri)]
thread_local! {
    /// The worker the shim's lookup reports for a module context under Miri,
    /// none by default.
    ///
    /// Per thread: a test sets it on its own thread, where its commit runs,
    /// so tests running on other threads keep their own.
    static PREPARE_WORKER: Cell<u64> = const { Cell::new(bindings::YANET_SYS_DP_WORKER_IDX_NONE) };
}

/// The shim's lookup of a module context's worker, which Miri cannot run as
/// C: reports the worker the test chose, none unless it chose one.
#[cfg(miri)]
#[unsafe(no_mangle)]
extern "C" fn yanet_sys_dp_module_worker_idx(_: *mut bindings::module_ectx) -> u64 {
    PREPARE_WORKER.with(Cell::get)
}

/// Counts the packets of its worker through the handle its prepare step made,
/// when it made one, and passes them: one of an even first byte in place, in
/// the low half, and the others by a copy read out and written back, in the
/// high half.
struct Held;

impl Module for Held {
    const NAME: &'static str = "held";
    type Config = Config;
    type Prepared<'p> = Option<WorkerSlot<'p, u64>>;

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let seen = cx.prepared();
        batch.dispatch(|packet| {
            match seen {
                Some(seen) if packet.data()[0] % 2 == 0 => *seen.get_mut(cx) += 1,
                Some(seen) => {
                    let count = *seen.get(cx);
                    *seen.get_mut(cx) = count + ODD;
                }
                None => {}
            }
            Verdict::Pass
        });
    }

    fn prepare<'p>(cx: &CommitCx<'p, Self>) -> Self::Prepared<'p> {
        cx.config().seen.handle(cx)
    }
}

/// verifies that a module's prepare step finds its worker and makes the
/// handle of that worker's slot, in which the handler finds what its earlier
/// calls left, and that no other slot changes.
#[cfg(miri)]
#[test]
fn test_per_worker_handle_of_a_module_carries_over_between_calls_of_its_worker() {
    PREPARE_WORKER.with(|worker| worker.set(1));
    let (mut ectx, block) = seen::<u64, Held>();
    ectx.set_worker(1);

    ectx.run(&mut numbered(2));
    ectx.run(&mut numbered(3));

    assert!(ectx.prepared().is_some());
    // First bytes 0, 1, then 0, 1, 2: three even and two odd.
    assert_eq!([0, 3 + 2 * ODD, 0], slots::<u64, _>(&ectx, block));
}

/// verifies that a module's prepare step whose context is not among its
/// generation's worker contexts, here one of no generation, makes no handle.
#[test]
fn test_per_worker_handle_of_a_module_without_a_worker_is_none() {
    let (mut ectx, block) = seen::<u64, Held>();

    ectx.run(&mut numbered(3));

    assert!(ectx.prepared().is_none());
    assert_eq!([0, 0, 0], slots::<u64, _>(&ectx, block));
}

/// Counts the packets of its worker in place, in the value of its worker's
/// array slot that the first byte selects, through the handle its prepare
/// step made, and passes them.
#[cfg(miri)]
struct InPlace;

#[cfg(miri)]
impl Module for InPlace {
    const NAME: &'static str = "in_place";
    type Config = Config<[u64; VALUES]>;
    type Prepared<'p> = Option<WorkerSlot<'p, [u64; VALUES]>>;

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let seen = cx.prepared();
        batch.dispatch(|packet| {
            let byte = usize::from(packet.data()[0]);
            if let Some(seen) = seen {
                seen.get_mut(cx)[byte] += 1;
            }
            Verdict::Pass
        });
    }

    fn prepare<'p>(cx: &CommitCx<'p, Self>) -> Self::Prepared<'p> {
        cx.config().seen.handle(cx)
    }
}

/// verifies that a handler changing its worker's array slot in place finds
/// what its earlier call left there, and that no other slot changes.
#[cfg(miri)]
#[test]
fn test_per_worker_get_mut_changes_a_slot_in_place_across_calls() {
    PREPARE_WORKER.with(|worker| worker.set(1));
    let (mut ectx, block) = seen::<[u64; VALUES], InPlace>();
    ectx.set_worker(1);

    ectx.run(&mut numbered(2));
    ectx.run(&mut numbered(3));

    let mut counted = [0; VALUES];
    counted[..3].copy_from_slice(&[2, 2, 1]);
    assert_eq!(
        [[0; VALUES], counted, [0; VALUES]],
        slots::<[u64; VALUES], _>(&ectx, block)
    );
}

/// Per packet, counts in the first value of its worker's array slot through an
/// exclusive borrow, sets the second to ten times the first as a shared borrow
/// reads it, and the third to one more than the second, then passes it.
#[cfg(miri)]
struct Mixed;

#[cfg(miri)]
impl Module for Mixed {
    const NAME: &'static str = "mixed";
    type Config = Config<[u64; VALUES]>;
    type Prepared<'p> = Option<WorkerSlot<'p, [u64; VALUES]>>;

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let seen = cx.prepared();
        batch.dispatch(|_| {
            if let Some(seen) = seen {
                seen.get_mut(cx)[0] += 1;
                let first = seen.get(cx)[0];
                seen.get_mut(cx)[1] = first * 10;
                let second = seen.get(cx)[1];
                seen.get_mut(cx)[2] = second + 1;
            }
            Verdict::Pass
        });
    }

    fn prepare<'p>(cx: &CommitCx<'p, Self>) -> Self::Prepared<'p> {
        cx.config().seen.handle(cx)
    }
}

#[cfg(miri)]
#[test]
fn test_per_worker_get_mut_and_get_see_the_same_value() {
    PREPARE_WORKER.with(|worker| worker.set(0));
    let (mut ectx, block) = seen::<[u64; VALUES], Mixed>();
    ectx.set_worker(0);

    ectx.run(&mut numbered(3));

    let mut mixed = [0; VALUES];
    mixed[..3].copy_from_slice(&[3, 30, 31]);
    assert_eq!(
        [mixed, [0; VALUES], [0; VALUES]],
        slots::<[u64; VALUES], _>(&ectx, block)
    );
}

/// An object with a slot per worker, which prepares the handle of its
/// context's worker.
struct Budgets;

impl Object for Budgets {
    const TYPE: &'static str = "budgets";
    type Config = PerWorker<u64>;
    type Prepared<'p> = Option<WorkerSlot<'p, u64>>;

    fn prepare<'p>(cx: &ObjectCommitCx<'p, Self>) -> Self::Prepared<'p> {
        cx.config().handle(cx)
    }
}

/// Counts the packets of its worker through the handle the linked object
/// prepared, and passes them.
struct Linked;

impl Module for Linked {
    const NAME: &'static str = "linked";
    type Config = ObjectLink<Budgets>;
    type Prepared<'p> = ();

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let seen = cx.config().prepared(cx);
        batch.dispatch(|_| {
            if let Some(seen) = seen {
                *seen.get_mut(cx) += 1;
            }
            Verdict::Pass
        });
    }

    fn prepare(_: &CommitCx<'_, Self>) {}
}

/// Lays out an object of per-worker budgets, committed for the given worker,
/// as the one link of the module context, and returns its slot block.
///
/// # Safety
///
/// The context is the one of a module whose configuration names link 0 of
/// the budgets type.
unsafe fn link_budgets(arena: &'static BlockArena, context: &mut bindings::module_ectx, worker: u64) -> usize {
    let root = arena.root();
    let alloc = |size: usize| arena.alloc(size).as_ptr().addr();
    let (slots, object, object_ectx, prepared, links) = (
        alloc(PerWorker::<u64>::block_size(WORKERS).expect("block size")),
        alloc(size_of::<ObjectConfig<PerWorker<u64>>>()),
        alloc(size_of::<bindings::object_ectx>()),
        alloc(size_of::<Option<WorkerSlot<'static, u64>>>()),
        alloc(size_of::<bindings::module_object_link_ectx>()),
    );
    let body = object + ObjectConfig::<PerWorker<u64>>::BODY_OFFSET;
    let object_context = bindings::object_ectx {
        abs_cp_object: root.ptr(object),
        abs_object_prepared: root.ptr(prepared),
        worker_idx: worker,
        ..Default::default()
    };
    let link = bindings::module_object_link_ectx {
        abs_object_ectx: root.ptr(object_ectx),
        ..Default::default()
    };
    // SAFETY: every write lands in a block just allocated for its value, and
    // the object's commit entry runs over the context laid out for it.
    unsafe {
        write(arena, body, [slots as isize - body as isize, WORKERS as isize]);
        write(arena, object_ectx, object_context);
        rt::commit_object_ectx::<Budgets>(root.ptr(object_ectx), root.ptr(object));
        write(arena, links, link);
    }
    context.object_link_count = 1;
    context.abs_object_links = root.ptr(links);
    slots
}

/// verifies that an object's prepare step makes the handle of the worker its
/// context records, and that a module linking the object counts in that slot
/// through the object's prepared value.
#[test]
fn test_per_worker_handle_of_an_object_is_read_through_a_link() {
    let block = Cell::new(0);
    // SAFETY: the configuration names link 0, which the setup lays out; the
    // module redirects no packet.
    let mut ectx = unsafe { Ectx::<Linked>::new(&[0], |arena, _, context| block.set(link_budgets(arena, context, 1))) };
    ectx.set_worker(1);

    ectx.run(&mut numbered(2));
    ectx.run(&mut numbered(1));

    assert_eq!([0, 3, 0], slots::<u64, _>(&ectx, block.get()));
}

/// Counts the packets of its worker through the handle its prepare step made
/// on the linked object's slots, when it made one, and passes them.
#[cfg(miri)]
struct LinkHeld;

#[cfg(miri)]
impl Module for LinkHeld {
    const NAME: &'static str = "link_held";
    type Config = ObjectLink<Budgets>;
    type Prepared<'p> = Option<WorkerSlot<'p, u64>>;

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let seen = cx.prepared();
        batch.dispatch(|_| {
            if let Some(seen) = seen {
                *seen.get_mut(cx) += 1;
            }
            Verdict::Pass
        });
    }

    fn prepare<'p>(cx: &CommitCx<'p, Self>) -> Self::Prepared<'p> {
        cx.config().get(cx).handle(cx)
    }
}

/// verifies that a module's prepare step makes a handle on its worker's slot
/// of a linked object, which the handler counts through.
#[cfg(miri)]
#[test]
fn test_per_worker_handle_of_a_module_on_a_linked_object_counts_in_its_workers_slot() {
    PREPARE_WORKER.with(|worker| worker.set(2));
    let block = Cell::new(0);
    // SAFETY: as above.
    let mut ectx =
        unsafe { Ectx::<LinkHeld>::new(&[0], |arena, _, context| block.set(link_budgets(arena, context, 2))) };
    ectx.set_worker(2);

    ectx.run(&mut numbered(2));

    assert_eq!([0, 0, 2], slots::<u64, _>(&ectx, block.get()));
}

/// Values in a worker's region.
const LEN: usize = 5;

/// Configuration of a region-counting module: a region of `u64`s per worker.
#[repr(C)]
struct Regions {
    table: PerWorkerSlice<u64>,
}

// SAFETY: a per-worker slice field, an offset and two counts, which accept
// every bit pattern, with no padding and no drop.
unsafe impl ShmLayout for Regions {}

// SAFETY: as for `Config`: laid out by hand and never built.
unsafe impl Build for Regions {
    type Parts<'b, W: Owner> = Infallible;

    unsafe fn write<W: Owner>(parts: Infallible, _: &MemoryContext<'_, W>, _: usize) -> Result<(), Refusal> {
        match parts {}
    }

    unsafe fn free<W: Owner>(_: usize, _: &MemoryContext<'_, W>) {}
}

/// Lays out the table and the [`WORKERS`] zeroed regions of `len` values of a
/// per-worker slice field at an address, and returns the region blocks.
///
/// # Safety
///
/// The address is a per-worker slice field in a block of the arena that
/// nothing references.
unsafe fn lay_out_regions(arena: &'static BlockArena, field: usize, len: usize) -> [usize; WORKERS] {
    let table = arena.alloc(WORKERS * size_of::<isize>()).as_ptr().addr();
    let size = PerWorkerSlice::<u64>::region_block(len).expect("region size");
    let blocks = core::array::from_fn(|_| arena.alloc(size).as_ptr().addr());
    // SAFETY: the table holds a word per worker, just allocated, and the
    // field its three words (caller contract).
    unsafe {
        for (worker, block) in blocks.iter().enumerate() {
            let entry = table + worker * size_of::<isize>();
            write(arena, entry, *block as isize - entry as isize);
        }
        write(
            arena,
            field,
            [table as isize - field as isize, WORKERS as isize, len as isize],
        );
    }
    blocks
}

/// The values in each region of the blocks.
fn regions<M: Module>(ectx: &Ectx<M>, blocks: [usize; WORKERS], len: usize) -> [Vec<u64>; WORKERS] {
    blocks.map(|block| {
        let first = PerWorkerSlice::<u64>::first_element(block);
        (0..len)
            // SAFETY: each value lies in its region, aligned, and was zeroed
            // by the arena or written by a handler that has returned.
            .map(|idx| unsafe { ectx.arena.root().ptr::<u64>(first + idx * size_of::<u64>()).read() })
            .collect()
    })
}

/// Counts each packet of its worker in place, in the value of its worker's
/// region that the first byte selects modulo the number of values before the
/// last, and keeps in the last one more than the sum of those values it read
/// before the count, through the handle its prepare step made, and passes the
/// packets.
#[cfg(miri)]
struct Spread;

#[cfg(miri)]
impl Module for Spread {
    const NAME: &'static str = "spread";
    type Config = Regions;
    type Prepared<'p> = Option<WorkerSlice<'p, u64>>;

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let table = *cx.prepared();
        batch.dispatch(|packet| {
            let Some(table) = table else {
                return Verdict::Pass;
            };
            let Some(last) = table.len().checked_sub(1) else {
                return Verdict::Pass;
            };
            if let Some(idx) = usize::from(packet.data()[0]).checked_rem(last) {
                let before: u64 = table.get(cx)[..last].iter().sum();
                let values = table.get_mut(cx);
                values[idx] += 1;
                values[last] = before + 1;
            }
            Verdict::Pass
        });
    }

    fn prepare<'p>(cx: &CommitCx<'p, Self>) -> Self::Prepared<'p> {
        cx.config().table.handle(cx)
    }
}

/// verifies that a handler changing its worker's region in place finds what
/// its earlier call left there, shared and mutably, and that no other region
/// changes.
#[cfg(miri)]
#[test]
fn test_per_worker_slice_get_mut_changes_a_region_in_place_across_calls() {
    PREPARE_WORKER.with(|worker| worker.set(1));
    let mut blocks = [0; WORKERS];
    // SAFETY: the setup lays out the field the configuration starts with; the
    // module counts no counter and redirects no packet.
    let mut ectx = unsafe { Ectx::<Spread>::new(&[], |arena, body, _| blocks = lay_out_regions(arena, body, LEN)) };
    ectx.set_worker(1);

    ectx.run(&mut numbered(7));
    ectx.run(&mut numbered(3));

    // First bytes 0 to 6, then 0 to 2, modulo four: ten packets in all.
    assert_eq!(
        [vec![0; LEN], vec![3, 3, 3, 1, 10], vec![0; LEN]],
        regions(&ectx, blocks, LEN)
    );
}

/// verifies that a field of no values makes the handle of an empty region,
/// which the handler borrows without touching memory.
#[cfg(miri)]
#[test]
fn test_per_worker_slice_handle_of_no_values_borrows_an_empty_region() {
    PREPARE_WORKER.with(|worker| worker.set(2));
    // SAFETY: the words are a field of no table, three workers and no values;
    // the module counts no counter and redirects no packet.
    let mut ectx = unsafe { Ectx::<Spread>::new(&[0, WORKERS as u64, 0], |_, _, _| {}) };
    ectx.set_worker(2);

    ectx.run(&mut numbered(3));

    assert_eq!(Some(0), ectx.prepared().map(|table| table.len()));
}

/// An object with a region per worker, which prepares the handle of its
/// context's worker.
struct Flows;

impl Object for Flows {
    const TYPE: &'static str = "flows";
    type Config = Regions;
    type Prepared<'p> = Option<WorkerSlice<'p, u64>>;

    fn prepare<'p>(cx: &ObjectCommitCx<'p, Self>) -> Self::Prepared<'p> {
        cx.config().table.handle(cx)
    }
}

/// Counts each packet of its worker in the first value of the region the
/// linked object prepared, and passes them.
struct LinkedFlows;

impl Module for LinkedFlows {
    const NAME: &'static str = "linked_flows";
    type Config = ObjectLink<Flows>;
    type Prepared<'p> = ();

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let table = *cx.config().prepared(cx);
        batch.dispatch(|_| {
            if let Some(table) = table {
                table.get_mut(cx)[0] += 1;
            }
            Verdict::Pass
        });
    }

    fn prepare(_: &CommitCx<'_, Self>) {}
}

/// verifies that an object's prepare step makes the handle of the region of
/// the worker its context records, and that a module linking the object
/// counts in that region through the object's prepared value.
#[test]
fn test_per_worker_slice_handle_of_an_object_is_read_through_a_link() {
    let blocks = Cell::new([0; WORKERS]);
    // SAFETY: the configuration names link 0, which the setup lays out; the
    // module redirects no packet.
    let mut ectx = unsafe {
        Ectx::<LinkedFlows>::new(&[0], |arena, _, context| {
            let root = arena.root();
            let alloc = |size: usize| arena.alloc(size).as_ptr().addr();
            let (object, object_ectx, prepared, links) = (
                alloc(size_of::<ObjectConfig<Regions>>()),
                alloc(size_of::<bindings::object_ectx>()),
                alloc(size_of::<Option<WorkerSlice<'static, u64>>>()),
                alloc(size_of::<bindings::module_object_link_ectx>()),
            );
            blocks.set(lay_out_regions(
                arena,
                object + ObjectConfig::<Regions>::BODY_OFFSET,
                LEN,
            ));
            let object_context = bindings::object_ectx {
                abs_cp_object: root.ptr(object),
                abs_object_prepared: root.ptr(prepared),
                worker_idx: 1,
                ..Default::default()
            };
            let link = bindings::module_object_link_ectx {
                abs_object_ectx: root.ptr(object_ectx),
                ..Default::default()
            };
            write(arena, object_ectx, object_context);
            rt::commit_object_ectx::<Flows>(root.ptr(object_ectx), root.ptr(object));
            write(arena, links, link);
            context.object_link_count = 1;
            context.abs_object_links = root.ptr(links);
        })
    };
    ectx.set_worker(1);

    ectx.run(&mut numbered(2));
    ectx.run(&mut numbered(1));

    let mut counted = vec![0; LEN];
    counted[0] = 3;
    assert_eq!([vec![0; LEN], counted, vec![0; LEN]], regions(&ectx, blocks.get(), LEN));
}
