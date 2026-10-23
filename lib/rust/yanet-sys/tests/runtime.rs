//! The dataplane runtime over the invocation harness: a verdict puts a
//! packet in exactly one list, a packet the handler does not take stays on
//! the input list, and the contexts reach the worker, the prepared value,
//! the counters and the devices as a C module reaches them.
//!
//! Configurations are laid out by hand in the arena, as the control plane
//! would write them: the test writes the counter ids and device indices.

use core::mem::offset_of;

use yanet_sdk_testing::{
    BlockArena,
    dp::{Fate, Invocation, Outcome, TestPacket, alloc_config, write_body_word},
};
use yanet_shm::shm::ShmLayout;
use yanet_sys::{
    bindings,
    dp::{Batch, CommitCx, Counter, CounterRef, Cx, Device, Module, Verdict},
};

/// A configuration of one plain word.
type Word = u64;

/// Counters: three picked by value, one held by a handle.
#[repr(C)]
struct Tally {
    by_value: [Counter; 3],
    held: Counter,
}

// SAFETY: four registry indices, `repr(C)`, every bit pattern valid, no
// padding, aligned to 8.
unsafe impl ShmLayout for Tally {}

/// Devices a module resolves: three indices.
#[repr(C)]
struct Links {
    devices: [Device; 3],
}

// SAFETY: three device indices, `repr(C)`, every bit pattern valid, no
// padding, aligned to 8.
unsafe impl ShmLayout for Links {}

/// Runs `M` on `count` non-IP packets of 60 bytes, the first byte of packet
/// `n` being `n`, and returns the outcome.
fn run<M: Module>(count: usize) -> Outcome {
    let arena = BlockArena::new();
    let config = alloc_config::<M::Config>(arena);
    let mut invocation = Invocation::new::<M>(arena, config, 0, 0);
    invocation.run::<M>(packets(count))
}

fn packets(count: usize) -> Vec<TestPacket> {
    (0..count)
        .map(|idx| TestPacket::ether(0x0806, vec![idx as u8; 60]))
        .collect()
}

/// Forgets one packet, drops one value unused and passes the third.
struct Forgetful;

impl Module for Forgetful {
    const NAME: &'static str = "forgetful";
    type Config = Word;
    type Prepared<'p> = ();
    fn prepare(_: &CommitCx<'_, Self>) {}

    // Forgetting is the point: it is safe and touches no list.
    #[allow(clippy::forget_non_drop)]
    fn handle(_: &mut Cx<Self>, batch: &mut Batch) {
        if let Some(packet) = batch.next() {
            core::mem::forget(packet);
        }
        #[allow(unused_must_use)]
        {
            batch.next();
        }
        if let Some(packet) = batch.next() {
            batch.pass(packet);
        }
    }
}

/// Takes every packet first, then disposes of them out of order.
struct Shuffled;

impl Module for Shuffled {
    const NAME: &'static str = "shuffled";
    type Config = Word;
    type Prepared<'p> = ();
    fn prepare(_: &CommitCx<'_, Self>) {}

    fn handle(_: &mut Cx<Self>, batch: &mut Batch) {
        let (Some(first), Some(second), Some(third)) = (batch.next(), batch.next(), batch.next()) else {
            return;
        };
        batch.pass(second);
        batch.drop(first);
        batch.pass(third);
    }
}

/// Passes even-valued frames and drops odd-valued ones through dispatch.
struct Dispatched;

impl Module for Dispatched {
    const NAME: &'static str = "dispatched";
    type Config = Word;
    type Prepared<'p> = ();
    fn prepare(_: &CommitCx<'_, Self>) {}

    fn handle(_: &mut Cx<Self>, batch: &mut Batch) {
        batch.dispatch(|packet| {
            if packet.data()[0] % 2 == 0 {
                Verdict::Pass
            } else {
                Verdict::Drop
            }
        });
    }
}

/// Takes nothing.
struct Idle;

impl Module for Idle {
    const NAME: &'static str = "idle";
    type Config = Word;
    type Prepared<'p> = ();
    fn prepare(_: &CommitCx<'_, Self>) {}
    fn handle(_: &mut Cx<Self>, _: &mut Batch) {}
}

/// Takes one packet and passes it, leaving the rest on the input list.
struct TakesOne;

impl Module for TakesOne {
    const NAME: &'static str = "takes_one";
    type Config = Word;
    type Prepared<'p> = ();
    fn prepare(_: &CommitCx<'_, Self>) {}

    fn handle(_: &mut Cx<Self>, batch: &mut Batch) {
        if let Some(packet) = batch.next() {
            batch.pass(packet);
        }
    }
}

/// verifies that a packet the handler takes and forgets is on no list, with
/// the front counting only the passed one.
#[test]
fn test_batch_forgotten_packets_are_on_no_list() {
    let outcome = run::<Forgetful>(3);

    assert_eq!(vec![Fate::Lost, Fate::Lost, Fate::Output], outcome.fates);
    assert_eq!(
        (3, 180, 1, 60, 0, 0),
        (
            outcome.front.input_count,
            outcome.front.input_bytes,
            outcome.front.output_count,
            outcome.front.output_bytes,
            outcome.front.drop_count,
            outcome.front.drop_bytes
        )
    );
}

/// verifies that packets taken together are disposed of in any order, each
/// ending in the list its verdict names.
#[test]
fn test_batch_disposes_out_of_order() {
    let outcome = run::<Shuffled>(3);

    assert_eq!(vec![Fate::Drop, Fate::Output, Fate::Output], outcome.fates);
    assert_eq!(
        (2, 120, 1, 60),
        (
            outcome.front.output_count,
            outcome.front.output_bytes,
            outcome.front.drop_count,
            outcome.front.drop_bytes
        )
    );
}

/// verifies that dispatch applies the verdict of every packet and the front
/// counts and bytes follow the lists.
#[test]
fn test_batch_dispatch_applies_every_verdict() {
    let outcome = run::<Dispatched>(4);

    assert_eq!(vec![Fate::Output, Fate::Drop, Fate::Output, Fate::Drop], outcome.fates);
    assert_eq!(
        (2, 120, 2, 120),
        (
            outcome.front.output_count,
            outcome.front.output_bytes,
            outcome.front.drop_count,
            outcome.front.drop_bytes
        )
    );
}

/// verifies that a handler that takes nothing leaves every packet on the
/// input list with its counters.
#[test]
fn test_batch_untaken_packets_stay_on_input() {
    let outcome = run::<Idle>(2);

    assert_eq!(vec![Fate::Input, Fate::Input], outcome.fates);
    assert_eq!(
        (2, 120, 0, 0),
        (
            outcome.front.input_count,
            outcome.front.input_bytes,
            outcome.front.output_count,
            outcome.front.drop_count
        )
    );
}

/// verifies that taking one packet leaves the others on the input list, in
/// order, and the handler's own packet in the output.
#[test]
fn test_batch_partial_take_leaves_the_rest_on_input() {
    let outcome = run::<TakesOne>(3);

    assert_eq!(vec![Fate::Output, Fate::Input, Fate::Input], outcome.fates);
    assert_eq!(1, outcome.front.output_count);
}

/// Drops the packets of worker 3 and the packets of any worker once the
/// round time has passed 1000, and passes the rest.
struct WorkerAware;

impl Module for WorkerAware {
    const NAME: &'static str = "worker_aware";
    type Config = Word;
    type Prepared<'p> = ();
    fn prepare(_: &CommitCx<'_, Self>) {}

    fn handle(cx: &mut Cx<Self>, batch: &mut Batch) {
        let drop_all = cx.worker() == 3 || cx.now() > 1000;
        batch.dispatch(|_| if drop_all { Verdict::Drop } else { Verdict::Pass });
    }
}

/// Runs `WorkerAware` as `worker` at round time `now`.
fn run_as(worker: u64, now: u64) -> Vec<Fate> {
    let arena = BlockArena::new();
    let config = alloc_config::<Word>(arena);
    let mut invocation = Invocation::new::<WorkerAware>(arena, config, 0, 0);
    invocation.set_worker(worker, now);
    invocation.run::<WorkerAware>(packets(1)).fates
}

/// verifies that the context reports the worker index and the round time
/// the dataplane stored in the worker.
#[test]
fn test_cx_worker_and_now_read_the_worker() {
    assert_eq!(vec![Fate::Output], run_as(2, 1000));
    assert_eq!(vec![Fate::Drop], run_as(3, 0));
    assert_eq!(vec![Fate::Drop], run_as(0, 1001));
}

/// Passes a packet when the prepared value is 14, drops it otherwise.
struct Stamped;

impl Module for Stamped {
    const NAME: &'static str = "stamped";
    type Config = Word;
    type Prepared<'p> = u64;

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let verdict = if *cx.prepared() == 14 {
            Verdict::Pass
        } else {
            Verdict::Drop
        };
        batch.dispatch(|_| match verdict {
            Verdict::Pass => Verdict::Pass,
            _ => Verdict::Drop,
        });
    }

    fn prepare<'p>(cx: &CommitCx<'p, Self>) -> u64 {
        *cx.config() * 2
    }
}

/// verifies that the commit pass writes the prepared value the handler
/// reads, from the configuration, and that the buffer is zero before it.
#[test]
fn test_commit_writes_the_prepared_value_the_handler_reads() {
    let arena = BlockArena::new();
    let config = alloc_config::<Word>(arena);
    write_body_word::<Word>(arena, config, 0, 7);
    let mut invocation = Invocation::new::<Stamped>(arena, config, 0, 0);

    let before = invocation.run::<Stamped>(packets(1)).fates;
    invocation.commit::<Stamped>();
    let after = invocation.run::<Stamped>(packets(1)).fates;

    assert_eq!((vec![Fate::Drop], vec![Fate::Output]), (before, after));
}

/// A prepared value of no size: the runtime needs no buffer for it.
struct Unprepared;

impl Module for Unprepared {
    const NAME: &'static str = "unprepared";
    type Config = Word;
    type Prepared<'p> = ();

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let () = *cx.prepared();
        batch.dispatch(|_| Verdict::Pass);
    }

    fn prepare(_: &CommitCx<'_, Self>) {}
}

/// verifies that a module with a zero-sized prepared value commits and runs
/// without a buffer.
#[test]
fn test_commit_of_a_zero_sized_prepared_value_needs_no_buffer() {
    let arena = BlockArena::new();
    let config = alloc_config::<Word>(arena);
    let mut invocation = Invocation::new::<Unprepared>(arena, config, 0, 0);

    invocation.commit::<Unprepared>();
    let outcome = invocation.run::<Unprepared>(packets(2));

    assert_eq!(vec![Fate::Output, Fate::Output], outcome.fates);
}

/// Counts each packet in the counter its first byte selects, by value, and
/// in the held counter, through the handle resolved at commit.
struct Tallier;

impl Module for Tallier {
    const NAME: &'static str = "tallier";
    type Config = Tally;
    type Prepared<'p> = CounterRef<'p>;

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let config = cx.config();
        let held = cx.prepared();
        batch.dispatch(|packet| {
            config.by_value[usize::from(packet.data()[0] % 3)].add(cx, 1);
            held.add(2);
            Verdict::Pass
        });
    }

    fn prepare<'p>(cx: &CommitCx<'p, Self>) -> CounterRef<'p> {
        cx.config().held.resolve(cx)
    }
}

/// verifies that a counter picked by value and a counter held by a handle
/// each add to the worker's own copy, at the registry index the
/// configuration stores.
#[test]
fn test_cx_counters_add_by_value_and_by_handle() {
    let arena = BlockArena::new();
    let config = alloc_config::<Tally>(arena);
    // The control plane writes the registry indices; here, in a different
    // order than the slots.
    for (field, id) in [(0, 3), (1, 1), (2, 2), (3, 0)] {
        write_body_word::<Tally>(arena, config, offset_of!(Tally, by_value) + field * 8, id);
    }
    let mut invocation = Invocation::new::<Tallier>(arena, config, 4, 0);
    invocation.commit::<Tallier>();

    invocation.run::<Tallier>((0..5).map(|idx| TestPacket::ether(0x0806, vec![idx; 60])).collect());

    // Packets 0..5 select the first three fields 0, 1, 2, 0, 1, which name
    // slots 3, 1, 2; the held field names slot 0 and takes two per packet.
    assert_eq!(
        vec![10, 2, 1, 2],
        (0..4).map(|idx| invocation.counter(idx)).collect::<Vec<_>>()
    );
}

/// Passes a packet whose device resolves, drops one whose does not; the
/// first packets ask for the configured devices in turn as output targets,
/// the following ones as input targets.
struct Resolver;

impl Module for Resolver {
    const NAME: &'static str = "resolver";
    type Config = Links;
    type Prepared<'p> = ();
    fn prepare(_: &CommitCx<'_, Self>) {}

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let devices = &cx.config().devices;
        let mut next = 0;
        batch.dispatch(|_| {
            let device = &devices[next % devices.len()];
            let target = if next < devices.len() {
                device.resolve(cx)
            } else {
                device.resolve_input(cx)
            };
            next += 1;
            if target.is_some() { Verdict::Pass } else { Verdict::Drop }
        });
    }
}

/// verifies that a device resolves when it is a present target of the
/// generation, and does not when the target is absent or the index is past
/// the table, for the output and the input route alike.
#[test]
fn test_device_resolve_present_absent_and_out_of_range() {
    let arena = BlockArena::new();
    let config = alloc_config::<Links>(arena);
    // Device 0 is present, device 1 absent, index 2 the first past the two
    // targets.
    for (field, index) in [(0, 0), (1, 1), (2, 2)] {
        write_body_word::<Links>(arena, config, field * 8, index);
    }
    let mut invocation = Invocation::new::<Resolver>(arena, config, 0, 2);
    invocation.set_device_absent(1);

    let outcome = invocation.run::<Resolver>(packets(6));

    assert_eq!(
        vec![
            Fate::Output,
            Fate::Drop,
            Fate::Drop,
            Fate::Output,
            Fate::Drop,
            Fate::Drop
        ],
        outcome.fates
    );
}

/// Passes a packet whose transport protocol is UDP behind the unavailable
/// mark, drops every other.
struct Udp;

impl Module for Udp {
    const NAME: &'static str = "udp";
    type Config = Word;
    type Prepared<'p> = ();
    fn prepare(_: &CommitCx<'_, Self>) {}

    fn handle(_: &mut Cx<Self>, batch: &mut Batch) {
        batch.dispatch(|packet| {
            if packet.transport_protocol() == 17 && !packet.transport_header_available() {
                Verdict::Pass
            } else {
                Verdict::Drop
            }
        });
    }
}

/// verifies that the declared protocol is the low byte of the transport tag
/// and that the unavailable mark, bit 8, is not part of it.
#[test]
fn test_batch_transport_protocol_masks_the_unavailable_mark() {
    let mark = bindings::PACKET_TRANSPORT_HEADER_UNAVAILABLE as u16;
    let arena = BlockArena::new();
    let config = alloc_config::<Word>(arena);
    let mut invocation = Invocation::new::<Udp>(arena, config, 0, 0);
    let tagged = |transport_type| TestPacket {
        transport_type,
        ..TestPacket::ether(0x0800, vec![0; 60])
    };

    let outcome = invocation.run::<Udp>(vec![tagged(mark | 17), tagged(17), tagged(mark | 6), tagged(6)]);

    assert_eq!(vec![Fate::Output, Fate::Drop, Fate::Drop, Fate::Drop], outcome.fates);
}

/// Writes the first byte of every frame and passes the packet.
struct Marker;

impl Module for Marker {
    const NAME: &'static str = "marker";
    type Config = Word;
    type Prepared<'p> = ();
    fn prepare(_: &CommitCx<'_, Self>) {}

    fn handle(_: &mut Cx<Self>, batch: &mut Batch) {
        batch.dispatch(|packet| {
            packet.data_mut()[0] = 0xee;
            Verdict::Pass
        });
    }
}

/// verifies that a write through the frame a packet exposes reaches the
/// frame the mbuf describes.
#[test]
fn test_packet_data_mut_writes_the_frame() {
    let outcome = run::<Marker>(2);

    assert_eq!(
        vec![0xee, 0xee],
        outcome.frames.iter().map(|frame| frame[0]).collect::<Vec<_>>()
    );
}

/// Passes a packet whose parsed headers are the IPv4 and UDP ones of the
/// test frame, drops every other.
struct Offsets;

impl Module for Offsets {
    const NAME: &'static str = "offsets";
    type Config = Word;
    type Prepared<'p> = ();
    fn prepare(_: &CommitCx<'_, Self>) {}

    fn handle(_: &mut Cx<Self>, batch: &mut Batch) {
        batch.dispatch(|packet| {
            let seen = (
                packet.network_type(),
                packet.network_offset(),
                packet.transport_offset(),
            );
            if seen == (0x0800, 14, 34) && packet.data().len() == 42 {
                Verdict::Pass
            } else {
                Verdict::Drop
            }
        });
    }
}

/// verifies that the header types and offsets a packet reports are the
/// parser's, the network type in host order, and the frame has the length of
/// the first segment.
#[test]
fn test_packet_reports_the_parsers_headers() {
    let arena = BlockArena::new();
    let config = alloc_config::<Word>(arena);
    let mut invocation = Invocation::new::<Offsets>(arena, config, 0, 0);
    let mut parsed = TestPacket::ether(0x0800, vec![0; 42]);
    parsed.transport_offset = 34;

    let outcome = invocation.run::<Offsets>(vec![parsed, TestPacket::ether(0x0806, vec![0; 42])]);

    assert_eq!(vec![Fate::Output, Fate::Drop], outcome.fates);
}
