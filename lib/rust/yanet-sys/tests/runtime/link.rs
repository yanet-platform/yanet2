//! Miri reach of the reads behind an object link: the module's link array,
//! the linked object's context and its configuration, over a hand-laid
//! context, from the handler and the commit entries.

use core::{
    cell::Cell,
    mem::size_of,
    sync::atomic::{AtomicU64, Ordering},
};

use yanet_shm::rel::ShmSlice;
use yanet_sys::{
    bindings,
    dp::{Batch, CommitCx, Cx, Module, Object, ObjectConfig, ObjectLink, Verdict},
};

use crate::common::{Ectx, numbered, write};

/// An object of atomic slots.
struct Slots;

impl Object for Slots {
    const TYPE: &'static str = "slots";
    type Config = ShmSlice<AtomicU64>;
}

/// Adds one to the first slot of the linked object per packet, and prepares
/// the number of slots.
struct Linker;

impl Module for Linker {
    const NAME: &'static str = "linker";
    type Config = ObjectLink<Slots>;
    type Prepared<'p> = u64;

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let slots = cx.config().get(cx).as_slice(cx);
        batch.dispatch(|_| {
            slots[0].fetch_add(1, Ordering::Relaxed);
            Verdict::Pass
        });
    }

    fn prepare<'p>(cx: &CommitCx<'p, Self>) -> u64 {
        cx.config().get(cx).as_slice(cx).len() as u64
    }
}

/// verifies that Miri reaches the object a link resolves to from the handler
/// and the commit entries, and that the handler writes the linked slots.
#[test]
fn test_miri_reach_of_an_object_link() {
    let slot = Cell::new(0);
    // The configuration names link 0, which the setup resolves to an object
    // of two slots.
    //
    // SAFETY: the link index is below the one link the setup lays out; the
    // module redirects no packet.
    let mut ectx = unsafe {
        Ectx::<Linker>::new(&[0], |arena, context| {
            let root = arena.root();
            let alloc = |size: usize| arena.alloc(size).as_ptr().addr();
            let (slots, object, object_ectx, links) = (
                alloc(2 * size_of::<u64>()),
                alloc(size_of::<ObjectConfig<ShmSlice<AtomicU64>>>()),
                alloc(size_of::<bindings::object_ectx>()),
                alloc(size_of::<bindings::module_object_link_ectx>()),
            );
            write(arena, slots, [5u64, 6]);
            let body = object + ObjectConfig::<ShmSlice<AtomicU64>>::BODY_OFFSET;
            // A self-relative slice stores the offset to its elements.
            write(arena, body, slots as isize - body as isize);
            write(arena, body + ShmSlice::<AtomicU64>::LEN_OFFSET, 2u64);
            write(
                arena,
                object_ectx,
                bindings::object_ectx {
                    abs_cp_object: root.ptr(object),
                    ..Default::default()
                },
            );
            write(
                arena,
                links,
                bindings::module_object_link_ectx {
                    abs_object_ectx: root.ptr(object_ectx),
                    ..Default::default()
                },
            );
            context.object_link_count = 1;
            context.abs_object_links = root.ptr(links);
            slot.set(slots);
        })
    };
    let mut packets = numbered(3);

    ectx.run(&mut packets);

    // SAFETY: the slot lies in the arena, written by the handler only.
    let first = unsafe { ectx.arena.root().ptr::<u64>(slot.get()).read() };
    assert_eq!((8, 2), (first, *ectx.prepared()));
}
