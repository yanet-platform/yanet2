//! Miri reach of per-worker state: a handler reads and writes its worker's
//! slot through the handle a module's or an object's prepare step made, across
//! calls and next to other workers' slots.

use core::{cell::Cell, convert::Infallible};

use yanet_sdk_testing::BlockArena;
use yanet_shm::shm::ShmLayout;
use yanet_sys::{
    bindings,
    cp::{Build, MemoryContext, Owner, Refusal},
    dp::{
        Batch, CommitCx, Cx, Module, Object, ObjectCommitCx, ObjectConfig, ObjectLink, PerWorker, Verdict, WorkerSlot,
        rt,
    },
};

use crate::common::{Ectx, numbered, write};

/// Workers the slots are laid out for.
const WORKERS: usize = 3;

/// What a packet of an odd first byte adds to its worker's slot.
const ODD: u64 = 1 << 32;

/// Configuration of the counting module: a slot per worker.
#[repr(C)]
struct Config {
    seen: PerWorker<u64>,
}

// SAFETY: a per-worker field, an offset and a count, which accept every bit
// pattern, with no padding and no drop.
unsafe impl ShmLayout for Config {}

// SAFETY: the configuration is laid out by hand and never built: its parts
// cannot exist, so nothing is ever written, and its slot block belongs to the
// test.
unsafe impl Build for Config {
    type Parts<'b, W: Owner> = Infallible;

    unsafe fn write<W: Owner>(parts: Infallible, _: &MemoryContext<'_, W>, _: usize) -> Result<(), Refusal> {
        match parts {}
    }

    unsafe fn free<W: Owner>(_: usize, _: &MemoryContext<'_, W>) {}
}

/// The committed context of a module of the counting configuration over a
/// slot block of [`WORKERS`] zeroed slots, and the block's address.
fn seen<M: Module<Config = Config>>() -> (Ectx<M>, usize) {
    let mut slots = 0;
    // SAFETY: the words are a null per-worker field the setup points at the
    // slot block before the commit; the module counts no counter and
    // redirects no packet.
    let ectx = unsafe {
        Ectx::<M>::new(&[0, WORKERS as u64], |arena, body, _| {
            let size = PerWorker::<u64>::block_size(WORKERS).expect("block size");
            slots = arena.alloc(size).as_ptr().addr();
            write(arena, body, slots as isize - body as isize);
        })
    };
    (ectx, slots)
}

/// The value in each slot of the block.
fn slots<M: Module>(ectx: &Ectx<M>, block: usize) -> [u64; WORKERS] {
    // SAFETY: each slot lies in the block, aligned, and was zeroed by the
    // arena or written by a handler that has returned.
    core::array::from_fn(|worker| unsafe {
        ectx.arena
            .root()
            .ptr::<u64>(PerWorker::<u64>::first_slot(block) + worker * PerWorker::<u64>::STRIDE)
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
/// when it made one, and passes them: one of an even first byte by an update,
/// in the low half, and the others by a read and a write, in the high half.
struct Held;

impl Module for Held {
    const NAME: &'static str = "held";
    type Config = Config;
    type Prepared<'p> = Option<WorkerSlot<'p, u64>>;

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let seen = cx.prepared();
        batch.dispatch(|packet| {
            match seen {
                Some(seen) if packet.data()[0] % 2 == 0 => {
                    seen.update(cx, |count| count + 1);
                }
                Some(seen) => seen.set(cx, seen.get(cx) + ODD),
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
    let (mut ectx, block) = seen::<Held>();
    ectx.set_worker(1);

    ectx.run(&mut numbered(2));
    ectx.run(&mut numbered(3));

    assert!(ectx.prepared().is_some());
    // First bytes 0, 1, then 0, 1, 2: three even and two odd.
    assert_eq!([0, 3 + 2 * ODD, 0], slots(&ectx, block));
}

/// verifies that a module's prepare step whose context is not among its
/// generation's worker contexts, here one of no generation, makes no handle.
#[test]
fn test_per_worker_handle_of_a_module_without_a_worker_is_none() {
    let (mut ectx, block) = seen::<Held>();

    ectx.run(&mut numbered(3));

    assert!(ectx.prepared().is_none());
    assert_eq!([0, 0, 0], slots(&ectx, block));
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
                seen.update(cx, |count| count + 1);
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

    assert_eq!([0, 3, 0], slots(&ectx, block.get()));
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
                seen.update(cx, |count| count + 1);
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

    assert_eq!([0, 0, 2], slots(&ectx, block.get()));
}
