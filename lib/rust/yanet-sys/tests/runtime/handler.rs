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
    cp::{Build, MemoryContext, Owner, Refusal},
    dp::{Batch, CommitCx, Counter, CounterPair, CounterRef, Cx, Device, Module, Verdict},
};

use crate::common::{Ectx, numbered, write};

/// Configuration of the reach module: a counter, a device and a pair of
/// packet and byte counts.
#[repr(C)]
struct Config {
    counter: Counter,
    device: Device,
    pair: CounterPair,
}

// SAFETY: three registry indices of eight bytes, which accept every bit
// pattern, with no padding and no drop.
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

/// Reads everything the runtime hands a module: the configuration, prepared
/// counter handles, counters by value and a device target.
///
/// It adds to every counter per packet, through the handles and by value, and
/// passes the packets whose first byte is zero while the device is present.
struct Reach;

impl Module for Reach {
    const NAME: &'static str = "reach";
    type Config = Config;
    type Prepared<'p> = (CounterRef<'p>, CounterRef<'p, CounterPair>);

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let config = cx.config();
        let (held, held_pair) = (cx.prepared().0.bind(cx), cx.prepared().1.bind(cx));
        let present = config.device.resolve(cx).is_some();
        let _ = (cx.worker(), cx.now());
        batch.dispatch(|packet| {
            config.counter.add(cx, 1);
            held.add(1);
            config.pair.add(cx, 1, packet.data().len() as u64);
            held_pair.add(1, packet.data().len() as u64);
            if present && packet.data()[0] == 0 {
                Verdict::Pass
            } else {
                Verdict::Drop
            }
        });
    }

    fn prepare<'p>(cx: &CommitCx<'p, Self>) -> Self::Prepared<'p> {
        (cx.config().counter.resolve(cx), cx.config().pair.resolve(cx))
    }
}

/// verifies that Miri reaches the runtime's unsafe code, which is its only
/// purpose here and not module behaviour.
///
/// The commit entry resolves the counters into the prepared buffer; the
/// handler entry reads the configuration, the prepared buffer, the counter
/// storage, both values of the pair included, and the device targets from the
/// context and moves two packets over the front.
#[test]
fn test_miri_reach_of_the_handler_and_commit_entries() {
    // The configuration names counter slot 0, device 0 and the pair in
    // counter slot 1.
    //
    // SAFETY: the counter ids are below the two slots, the pair's of two
    // values; the module redirects no packet.
    let mut ectx = unsafe {
        Ectx::<Reach>::new(&[0, 0, 1], |arena, context| {
            let alloc = |size: usize| arena.alloc(size).as_ptr().addr();
            let (storage, table, word, pair) = (
                alloc(size_of::<bindings::counter_storage>()),
                alloc(2 * size_of::<RelPtr<u64>>()),
                alloc(size_of::<u64>()),
                alloc(2 * size_of::<u64>()),
            );
            // A self-relative slot stores the offset to its target.
            write(arena, table, word as isize - table as isize);
            let second = table + size_of::<RelPtr<u64>>();
            write(arena, second, pair as isize - second as isize);
            let handles = storage + offset_of!(bindings::counter_storage, counter_value_handles);
            write(arena, handles, table as isize - handles as isize);
            context.abs_counter_storage = arena.root().ptr(storage);

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
