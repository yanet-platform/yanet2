//! Miri reach of the runtime's unsafe code: the exported handler and commit
//! entries run over a hand-laid context, so that Miri executes the reads of
//! the absolute pointers they follow. No module behaviour is tested here.

use core::mem::{offset_of, size_of};

use yanet_shm::{rel::RelPtr, shm::ShmLayout};
use yanet_sys::{
    bindings,
    dp::{Batch, CommitCx, Counter, CounterRef, Cx, Device, Module, Verdict},
};

use crate::common::{Ectx, numbered, write};

/// Configuration of the reach module: a counter and a device.
#[repr(C)]
struct Config {
    counter: Counter,
    device: Device,
}

// SAFETY: two registry indices of eight bytes, which accept every bit
// pattern, with no padding and no drop.
unsafe impl ShmLayout for Config {}

/// Reads everything the runtime hands a module: the configuration, a prepared
/// counter handle, a counter by value and a device target.
///
/// It adds to both counters per packet and passes the packets whose first
/// byte is zero while the device is present.
struct Reach;

impl Module for Reach {
    const NAME: &'static str = "reach";
    type Config = Config;
    type Prepared<'p> = CounterRef<'p>;

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let config = cx.config();
        let held = cx.prepared().bind(cx);
        let present = config.device.resolve(cx).is_some();
        let _ = (cx.worker(), cx.now());
        batch.dispatch(|packet| {
            config.counter.add(cx, 1);
            held.add(1);
            if present && packet.data()[0] == 0 {
                Verdict::Pass
            } else {
                Verdict::Drop
            }
        });
    }

    fn prepare<'p>(cx: &CommitCx<'p, Self>) -> CounterRef<'p> {
        cx.config().counter.resolve(cx)
    }
}

/// verifies that Miri reaches the runtime's unsafe code, which is its only
/// purpose here and not module behaviour.
///
/// The commit entry resolves a counter into the prepared buffer; the handler
/// entry reads the configuration, the prepared buffer, the counter storage
/// and the device targets from the context and moves two packets over the
/// front.
#[test]
fn test_miri_reach_of_the_handler_and_commit_entries() {
    let mut value = 0;
    // The configuration names counter slot 0 and device 0.
    //
    // SAFETY: the counter id is below the one slot; the module redirects no
    // packet.
    let mut ectx = unsafe {
        Ectx::<Reach>::new(&[0, 0], |arena, context| {
            let alloc = |size: usize| arena.alloc(size).as_ptr().addr();
            let (storage, table, word) = (
                alloc(size_of::<bindings::counter_storage>()),
                alloc(size_of::<RelPtr<u64>>()),
                alloc(size_of::<u64>()),
            );
            // A self-relative slot stores the offset to its target.
            write(arena, table, word as isize - table as isize);
            let handles = storage + offset_of!(bindings::counter_storage, counter_value_handles);
            write(arena, handles, table as isize - handles as isize);
            context.abs_counter_storage = arena.root().ptr(storage);
            value = word;

            let target = alloc(size_of::<bindings::module_device_target>());
            write(arena, target, bindings::module_device_target::default());
            context.device_target_count = 1;
            context.abs_device_targets = arena.root().ptr(target);
        })
    };
    let mut packets = numbered(2);

    let front = ectx.run(&mut packets);

    assert_eq!((vec![0], vec![1]), (front.output(), front.dropped()));
    // SAFETY: a live word of the arena that no handler is writing now.
    assert_eq!(4, unsafe { ectx.arena.root().ptr::<u64>(value).read() });
}
