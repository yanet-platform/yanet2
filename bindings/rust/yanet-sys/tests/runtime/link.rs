//! Miri reach of the reads behind an object link: the module's link array,
//! the linked object's context, its configuration, its prepared value and its
//! counter storage, over a hand-laid context, from the handler and the commit
//! entries, after the object's own commit entry when the object has a prepared
//! value.

use core::{
    cell::Cell,
    mem::{offset_of, size_of},
    sync::atomic::{AtomicU64, Ordering},
};

use yanet_sdk_testing::BlockArena;
use yanet_shm::rel::{RelPtr, ShmSlice};
use yanet_sys::{
    bindings,
    dp::{
        Batch, CommitCx, CounterRef, Cx, Gauge, Module, Object, ObjectCommitCx, ObjectConfig, ObjectCounter,
        ObjectLink, Verdict,
    },
};

use crate::common::{Ectx, commit_object, numbered, write};

/// An object of atomic slots, which prepares their number.
struct Slots;

impl Object for Slots {
    const TYPE: &'static str = "slots";
    type Config = ShmSlice<AtomicU64>;
    type Prepared<'p> = u64;

    fn prepare(cx: &ObjectCommitCx<'_, Self>) -> u64 {
        cx.config().as_slice(cx).len() as u64
    }
}

/// An object of atomic slots without a prepared value.
struct Plain;

impl Object for Plain {
    const TYPE: &'static str = "plain";
    type Config = ShmSlice<AtomicU64>;
    type Prepared<'p> = ();

    fn prepare(_: &ObjectCommitCx<'_, Self>) {}
}

/// Adds the object's prepared value to its first slot per packet, and
/// prepares the number of slots plus the object's prepared value.
struct Linker;

impl Module for Linker {
    const NAME: &'static str = "linker";
    type Config = ObjectLink<Slots>;
    type Prepared<'p> = u64;

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let slots = cx.config().get(cx).as_slice(cx);
        let step = *cx.config().prepared(cx);
        batch.dispatch(|_| {
            slots[0].fetch_add(step, Ordering::Relaxed);
            Verdict::Pass
        });
    }

    fn prepare<'p>(cx: &CommitCx<'p, Self>) -> u64 {
        cx.config().get(cx).as_slice(cx).len() as u64 + *cx.config().prepared(cx)
    }
}

/// Adds one to the first slot of an object without a prepared value per
/// packet, reading its unit prepared value, and prepares the number of slots.
struct PlainLinker;

impl Module for PlainLinker {
    const NAME: &'static str = "plain_linker";
    type Config = ObjectLink<Plain>;
    type Prepared<'p> = u64;

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let slots = cx.config().get(cx).as_slice(cx);
        let () = *cx.config().prepared(cx);
        batch.dispatch(|_| {
            slots[0].fetch_add(1, Ordering::Relaxed);
            Verdict::Pass
        });
    }

    fn prepare<'p>(cx: &CommitCx<'p, Self>) -> u64 {
        let () = *cx.config().prepared(cx);
        cx.config().get(cx).as_slice(cx).len() as u64
    }
}

/// Lays out an object of type `O` of the slots 5 and 6 as the one link of the
/// module context, and returns the address of its first slot.
///
/// An object with a prepared value gets its buffer, filled by the object's
/// commit entry, as the dataplane commits object contexts before module
/// contexts; one without gets no buffer and no commit, as C does.
///
/// # Safety
///
/// The context is the one of a module whose configuration names link 0 of
/// type `O`.
unsafe fn link<O: Object<Config = ShmSlice<AtomicU64>>>(
    arena: &'static BlockArena,
    context: &mut bindings::module_ectx,
) -> usize {
    let root = arena.root();
    let alloc = |size: usize| arena.alloc(size).as_ptr().addr();
    let (slots, object, object_ectx, links) = (
        alloc(2 * size_of::<u64>()),
        alloc(size_of::<ObjectConfig<ShmSlice<AtomicU64>>>()),
        alloc(size_of::<bindings::object_ectx>()),
        alloc(size_of::<bindings::module_object_link_ectx>()),
    );
    let prepared_size = size_of::<O::Prepared<'static>>();
    // SAFETY: every write lands in a block just allocated for its value.
    unsafe {
        write(arena, slots, [5u64, 6]);
        let body = object + ObjectConfig::<ShmSlice<AtomicU64>>::BODY_OFFSET;
        // A self-relative slice stores the offset to its elements.
        write(arena, body, slots as isize - body as isize);
        write(arena, body + ShmSlice::<AtomicU64>::LEN_OFFSET, 2u64);
        let mut object_context = bindings::object_ectx {
            abs_cp_object: root.ptr(object),
            ..Default::default()
        };
        if prepared_size != 0 {
            object_context.abs_object_prepared = root.ptr(alloc(prepared_size));
        }
        write(arena, object_ectx, object_context);
        commit_object::<O>(arena, object_ectx, object);
        write(
            arena,
            links,
            bindings::module_object_link_ectx {
                abs_object_ectx: root.ptr(object_ectx),
                ..Default::default()
            },
        );
    }
    context.object_link_count = 1;
    context.abs_object_links = root.ptr(links);
    slots
}

/// verifies that Miri reaches the object a link resolves to from the handler
/// and the commit entries, and that both read the value the object's commit
/// entry prepared.
#[test]
fn test_miri_reach_of_an_object_link() {
    let slot = Cell::new(0);
    // SAFETY: the configuration names link 0, which the setup lays out; the
    // module redirects no packet.
    let mut ectx = unsafe { Ectx::<Linker>::new(&[0], |arena, _, context| slot.set(link::<Slots>(arena, context))) };
    let mut packets = numbered(3);

    ectx.run(&mut packets);

    // SAFETY: the slot lies in the arena, written by the handler only.
    let first = unsafe { ectx.arena.root().ptr::<u64>(slot.get()).read() };
    assert_eq!((11, 4), (first, *ectx.prepared()));
}

/// verifies that Miri reaches the object of a link whose type has no prepared
/// value, with no buffer and no object commit, from the handler and the
/// commit entries.
#[test]
fn test_miri_reach_of_an_object_link_without_a_prepared_value() {
    let slot = Cell::new(0);
    // SAFETY: as above.
    let mut ectx =
        unsafe { Ectx::<PlainLinker>::new(&[0], |arena, _, context| slot.set(link::<Plain>(arena, context))) };
    let mut packets = numbered(3);

    ectx.run(&mut packets);

    // SAFETY: as above.
    let first = unsafe { ectx.arena.root().ptr::<u64>(slot.get()).read() };
    assert_eq!((8, 2), (first, *ectx.prepared()));
}

/// An object of one gauge of its own, which prepares the worker's handle on
/// it.
struct Held;

impl Object for Held {
    const TYPE: &'static str = "held";
    type Config = ObjectCounter<Gauge>;
    type Prepared<'p> = CounterRef<'p, Gauge>;

    fn prepare<'p>(cx: &ObjectCommitCx<'p, Self>) -> CounterRef<'p, Gauge> {
        cx.config().resolve(cx)
    }
}

/// Keeps the linked object's gauge: holds a packet of an even first byte and
/// lets one go for a packet of an odd first byte while the worker holds any.
struct Holder;

impl Module for Holder {
    const NAME: &'static str = "holder";
    type Config = ObjectLink<Held>;
    type Prepared<'p> = ();

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let held = cx.config().prepared(cx).bind(cx);
        batch.dispatch(|packet| {
            match packet.data().first() {
                Some(byte) if byte % 2 == 0 => held.add(1),
                Some(..) if held.get() != 0 => held.sub(1),
                _ => {}
            }
            Verdict::Pass
        });
    }

    fn prepare(_: &CommitCx<'_, Self>) {}
}

/// Lays out an object of [`Held`], its counter of id 0, as the one link of the
/// module context, its context's counter storage holding the counter's one
/// value at the relative address C keeps it at, and returns the address of the
/// value.
///
/// # Safety
///
/// The context is the one of a module whose configuration names link 0 of
/// type [`Held`].
unsafe fn link_held(arena: &'static BlockArena, context: &mut bindings::module_ectx) -> usize {
    let root = arena.root();
    let alloc = |size: usize| arena.alloc(size).as_ptr().addr();
    let (value, table, storage, object, object_ectx, prepared, links) = (
        alloc(size_of::<u64>()),
        alloc(size_of::<RelPtr<u64>>()),
        alloc(size_of::<bindings::counter_storage>()),
        alloc(size_of::<ObjectConfig<ObjectCounter<Gauge>>>()),
        alloc(size_of::<bindings::object_ectx>()),
        alloc(size_of::<CounterRef<'static, Gauge>>()),
        alloc(size_of::<bindings::module_object_link_ectx>()),
    );
    let object_context = bindings::object_ectx {
        abs_cp_object: root.ptr(object),
        abs_object_prepared: root.ptr(prepared),
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
        write(arena, table, value as isize - table as isize);
        let handles = storage + offset_of!(bindings::counter_storage, counter_value_handles);
        write(arena, handles, table as isize - handles as isize);
        write(arena, object + ObjectConfig::<ObjectCounter<Gauge>>::BODY_OFFSET, 0u64);
        write(arena, object_ectx, object_context);
        let slot = object_ectx + offset_of!(bindings::object_ectx, counter_storage);
        write(arena, slot, storage as isize - slot as isize);
        commit_object::<Held>(arena, object_ectx, object);
        write(arena, links, link);
    }
    context.object_link_count = 1;
    context.abs_object_links = root.ptr(links);
    value
}

/// verifies that Miri reaches an object's counter, which the object's commit
/// entry resolves through the relative address of its context's counter
/// storage, from a handler that reads it, adds to it and takes from it as a
/// gauge.
#[test]
fn test_miri_reach_of_an_objects_counter_as_a_gauge() {
    let value = Cell::new(0);
    // SAFETY: the configuration names link 0, which the setup lays out; the
    // module redirects no packet.
    let mut ectx = unsafe { Ectx::<Holder>::new(&[0], |arena, _, context| value.set(link_held(arena, context))) };

    // Each round holds the packets 0 and 2 and lets go between them.
    ectx.run(&mut numbered(3));
    ectx.run(&mut numbered(3));

    // SAFETY: the value lies in the arena, written by the handler only.
    let held = unsafe { ectx.arena.root().ptr::<u64>(value.get()).read() };
    assert_eq!(2, held);
}
