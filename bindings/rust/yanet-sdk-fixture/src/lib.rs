//! Modules for the tests that load them as plugins into the C dataplane
//! harness and build their configurations through the fixture api crate.
//!
//! [`Fixture`] is a test program in module form: it has no prepared value, and
//! its configuration's mode word selects what it does with a batch. [`Tally`]
//! has a prepared value made at commit and a slot per worker, and counts.
//! [`Fib`] forwards by a route FIB. [`assign::Assign`] passes or drops by the
//! first rule a packet matches, through the classify bindings. [`State`] is an
//! object type: state that outlives the configurations, changed by the control
//! plane and by [`Stash`], which links it.

#![no_std]
#![forbid(unsafe_code)]

pub mod assign;

use core::sync::atomic::{AtomicU64, Ordering};

use yanet_sdk::{
    Batch, CommitCx, Counter, CounterRef, Cx, Device, Lpm4, Lpm6, Module, Object, ObjectCommitCx, ObjectLink,
    PerWorker, ShmLayout, ShmSlice, Traffic, Verdict, WorkerSlot,
};
// The plugin examples use it.
#[cfg(feature = "plugin")]
use yanet_sdk_plugin as _;

// What the fixture module does with a batch, as values of its mode word. The
// names are prefixed because the api crate's C header carries them as macros.

/// Passes every packet.
pub const FIXTURE_MODE_PASS: u64 = 0;
/// Forgets the first packet, drops the second value unused and passes the
/// third: a module that breaks the drain obligation on purpose, which the
/// safe API survives.
pub const FIXTURE_MODE_FORGET: u64 = 1;
/// Takes three packets first, disposes of them out of order and passes the
/// rest.
pub const FIXTURE_MODE_SHUFFLE: u64 = 2;
/// Passes the packets whose first byte is even and drops the others, an empty
/// one included.
pub const FIXTURE_MODE_PARITY: u64 = 3;
/// Drops everything of worker 3 and everything once the round time has
/// passed 1000, and passes the rest.
pub const FIXTURE_MODE_WORKER: u64 = 4;
/// Redirects every packet to the output of its configured device, so
/// that it reaches the transmit list.
pub const FIXTURE_MODE_FORWARD: u64 = 5;
/// Passes a packet when the device resolves as an output target of the
/// generation, and drops it otherwise.
pub const FIXTURE_MODE_RESOLVE: u64 = 6;
/// As [`FIXTURE_MODE_RESOLVE`], for an input target.
pub const FIXTURE_MODE_RESOLVE_INPUT: u64 = 7;
/// Redirects a packet to the input of its configured device once, marking
/// it by the top bit of its first byte, passes a marked one and drops an
/// empty one.
pub const FIXTURE_MODE_REENTER: u64 = 8;
/// Passes the input, makes a packet of one byte 0x5a and passes it, and
/// makes another and drops it.
pub const FIXTURE_MODE_EMIT: u64 = 9;
/// Passes every packet and a copy of it, whose first byte, if any, is 0xff.
pub const FIXTURE_MODE_CLONE: u64 = 10;
/// Passes the input, and makes a packet and prepends as many bytes as the
/// configuration's argument says, filled with 0xbb; passes it when
/// accepted and drops it when refused.
pub const FIXTURE_MODE_ALLOC_PREPEND: u64 = 11;

/// The configuration of [`Fixture`]: a mode, and one of each kind of resource
/// a build registers or allocates.
#[derive(ShmLayout)]
#[repr(C)]
pub struct Config {
    pub mode: u64,
    pub arg: u64,
    pub counter: Counter,
    pub device: Device,
    pub routes: Lpm4,
    pub weights: ShmSlice<u64>,
}

pub struct Fixture;

impl Module for Fixture {
    const NAME: &'static str = "fixture";
    type Config = Config;
    type Prepared<'p> = ();

    // Forgetting is the point: it is safe and touches no list.
    #[allow(clippy::forget_non_drop)]
    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        match cx.config().mode {
            FIXTURE_MODE_FORGET => {
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
            FIXTURE_MODE_SHUFFLE => {
                let (first, second, third) = (batch.next(), batch.next(), batch.next());
                if let Some(packet) = second {
                    batch.pass(packet);
                }
                if let Some(packet) = first {
                    batch.drop(packet);
                }
                if let Some(packet) = third {
                    batch.pass(packet);
                }
                batch.dispatch(|_| Verdict::Pass);
            }
            FIXTURE_MODE_PARITY => batch.dispatch(|packet| match packet.data().first() {
                Some(byte) if byte % 2 == 0 => Verdict::Pass,
                _ => Verdict::Drop,
            }),
            FIXTURE_MODE_WORKER => {
                let drop_all = cx.worker() == 3 || cx.now() > 1000;
                batch.dispatch(|_| if drop_all { Verdict::Drop } else { Verdict::Pass });
            }
            FIXTURE_MODE_FORWARD => {
                let device = &cx.config().device;
                batch.dispatch(|_| device.resolve(cx).map_or(Verdict::Drop, Verdict::Redirect));
            }
            FIXTURE_MODE_RESOLVE => {
                let device = &cx.config().device;
                batch.dispatch(|_| {
                    if device.resolve(cx).is_some() {
                        Verdict::Pass
                    } else {
                        Verdict::Drop
                    }
                });
            }
            FIXTURE_MODE_RESOLVE_INPUT => {
                let device = &cx.config().device;
                batch.dispatch(|_| {
                    if device.resolve_input(cx).is_some() {
                        Verdict::Pass
                    } else {
                        Verdict::Drop
                    }
                });
            }
            FIXTURE_MODE_REENTER => {
                let device = &cx.config().device;
                batch.dispatch(|packet| match packet.data_mut().first_mut() {
                    Some(byte) if *byte & 0x80 != 0 => Verdict::Pass,
                    Some(byte) => {
                        *byte |= 0x80;
                        device.resolve_input(cx).map_or(Verdict::Drop, Verdict::Redirect)
                    }
                    None => Verdict::Drop,
                });
            }
            FIXTURE_MODE_EMIT => {
                batch.pass_all();
                let Some(mut packet) = batch.alloc() else {
                    return;
                };
                if let Ok(byte) = packet.append(1) {
                    byte[0] = 0x5a;
                }
                if let Some(other) = batch.alloc() {
                    batch.drop(other);
                }
                batch.pass(packet);
            }
            FIXTURE_MODE_CLONE => {
                while let Some(packet) = batch.next() {
                    if let Some(mut copy) = batch.clone(&packet) {
                        if let Some(byte) = copy.data_mut().first_mut() {
                            *byte = 0xff;
                        }
                        batch.pass(copy);
                    }
                    batch.pass(packet);
                }
            }
            FIXTURE_MODE_ALLOC_PREPEND => {
                batch.pass_all();
                if let Some(mut packet) = batch.alloc() {
                    match usize::try_from(cx.config().arg).map(|len| packet.prepend(len)) {
                        Ok(Ok(room)) => {
                            room.fill(0xbb);
                            batch.pass(packet);
                        }
                        _ => batch.drop(packet),
                    }
                }
            }
            _ => batch.dispatch(|_| Verdict::Pass),
        }
    }

    fn prepare(_: &CommitCx<'_, Self>) {}
}

/// The configuration of [`Tally`]: a word the commit derives from, three
/// counters picked by value, one held by a handle, one of the packets and
/// bytes, the packets each worker has seen and a counter of running sums of
/// them.
#[derive(ShmLayout)]
#[repr(C)]
pub struct TallyConfig {
    pub word: u64,
    pub by_value: [Counter; 3],
    pub held: Counter,
    pub traffic: Counter<Traffic>,
    pub seen: PerWorker<u64>,
    pub running: Counter,
}

/// What [`Tally`] makes at commit: the handle of the held counter, twice the
/// configuration's word and the handle of the worker's slot of packets seen.
pub struct Stamp<'p> {
    held: CounterRef<'p>,
    stamp: u64,
    seen: Option<WorkerSlot<'p, u64>>,
}

/// Counts each packet in the counter its first byte selects, by value, unless
/// it is empty, in the held counter, through the handle made at commit, and
/// its bytes in the traffic; counts it in the worker's slot of packets seen,
/// through the handle made at commit, and adds the new count to the running
/// counter, unless the commit found no worker.
/// Passes the packets when the stamp is 14 and drops them otherwise.
pub struct Tally;

impl Module for Tally {
    const NAME: &'static str = "tally";
    type Config = TallyConfig;
    type Prepared<'p> = Stamp<'p>;

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let config = cx.config();
        let prepared = cx.prepared();
        let held = prepared.held.bind(cx);
        let pass = prepared.stamp == 14;
        batch.dispatch(|packet| {
            if let Some(byte) = packet.data().first() {
                config.by_value[usize::from(byte % 3)].add(cx, 1);
            }
            held.add(2);
            config.traffic.add(cx, Traffic::packet(packet.data().len() as u64));
            if let Some(seen) = &prepared.seen {
                config.running.add(cx, seen.update(cx, |seen| seen + 1));
            }
            if pass { Verdict::Pass } else { Verdict::Drop }
        });
    }

    fn prepare<'p>(cx: &CommitCx<'p, Self>) -> Stamp<'p> {
        let config = cx.config();
        Stamp {
            held: config.held.resolve(cx),
            stamp: config.word * 2,
            seen: config.seen.handle(cx),
        }
    }
}

/// A route as the C FIB keeps it: the neighbour's addresses, where it goes
/// and what counts it.
#[derive(ShmLayout)]
#[repr(C)]
pub struct Route {
    pub dst_addr: [u8; 6],
    pub src_addr: [u8; 6],
    pub pad: u32,
    pub device: Device,
    pub counter: Counter,
}

/// A run of `count` entries of the route index, from `start`.
#[derive(ShmLayout)]
#[repr(C)]
pub struct RouteList {
    pub start: u64,
    pub count: u64,
}

/// A FIB with the fields of C's `route_fib`, in its order: two LPM tables
/// mapping a destination to a route list, and the routes the lists select from.
///
/// The bytes differ from C's: a `ShmSlice` is a pointer then a length, where
/// `route_fib` keeps a count then a pointer.
#[derive(ShmLayout)]
#[repr(C)]
pub struct RouteFib {
    pub lpm_v6: Lpm6,
    pub lpm_v4: Lpm4,
    pub routes: ShmSlice<Route>,
    pub route_lists: ShmSlice<RouteList>,
    pub route_indexes: ShmSlice<u64>,
}

/// Forwards an untagged Ethernet frame by its destination address: counts it
/// on every route of the selected list, addresses it from the first route and
/// redirects it to that route's device. Drops what has no route, another
/// ethertype and a device that does not resolve.
pub struct Fib;

impl Module for Fib {
    const NAME: &'static str = "fib";
    type Config = RouteFib;
    type Prepared<'p> = ();

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let fib = cx.config();
        batch.dispatch(|packet| {
            let Some(indexes) = route_indexes(cx, fib, packet.data()) else {
                return Verdict::Drop;
            };
            let table = fib.routes.as_slice(cx);
            let routes = indexes
                .iter()
                .filter_map(|&index| table.get(usize::try_from(index).ok()?));
            for route in routes.clone() {
                route.counter.add(cx, 1);
            }
            let Some(first) = routes.clone().next() else {
                return Verdict::Drop;
            };
            let frame = packet.data_mut();
            frame[..6].copy_from_slice(&first.dst_addr);
            frame[6..12].copy_from_slice(&first.src_addr);
            first.device.resolve(cx).map_or(Verdict::Drop, Verdict::Redirect)
        });
    }

    fn prepare(_: &CommitCx<'_, Self>) {}
}

/// The entries of the route index that the destination of an IPv4 or IPv6
/// frame selects.
fn route_indexes<'id>(cx: &Cx<'id, Fib>, fib: &'id RouteFib, frame: &[u8]) -> Option<&'id [u64]> {
    let list = match frame.get(12..14)? {
        [0x08, 0x00] => fib.lpm_v4.lookup(cx, frame.get(30..34)?.try_into().ok()?)?,
        [0x86, 0xdd] => fib.lpm_v6.lookup(cx, frame.get(38..54)?.try_into().ok()?)?,
        _ => return None,
    };
    let list = fib.route_lists.as_slice(cx).get(usize::try_from(list).ok()?)?;
    fib.route_indexes
        .as_slice(cx)
        .get(usize::try_from(list.start).ok()?..)?
        .get(..usize::try_from(list.count).ok()?)
}

/// The configuration of [`State`]: the seed it was built from, a slot per
/// item, which the control plane changes while the object is published, and a
/// slot per worker.
#[derive(ShmLayout)]
#[repr(C)]
pub struct StateConfig {
    pub seed: u64,
    pub slots: ShmSlice<AtomicU64>,
    pub workers: PerWorker<u64>,
}

/// An object of atomic slots, the shape of state kept across configuration
/// updates; prepares its seed, which a module linking it reads per packet.
pub struct State;

impl Object for State {
    const TYPE: &'static str = "fixture_state";
    type Config = StateConfig;
    type Prepared<'p> = StatePrepared<'p>;

    fn prepare<'p>(cx: &ObjectCommitCx<'p, Self>) -> StatePrepared<'p> {
        let config = cx.config();
        StatePrepared {
            seed: config.seed,
            worker: config.workers.handle(cx),
        }
    }
}

/// What [`State`] prepares for each worker: its seed and the handle of the
/// worker's slot.
pub struct StatePrepared<'p> {
    pub seed: u64,
    pub worker: Option<WorkerSlot<'p, u64>>,
}

/// The configuration of [`Stash`]: the state object it writes, linked by
/// name.
#[derive(ShmLayout)]
#[repr(C)]
pub struct StashConfig {
    pub state: ObjectLink<State>,
    pub seen: Counter,
}

/// Adds the seed the linked state object prepared to the slot that the first
/// byte of a packet selects, modulo the slot count, and passes every packet;
/// an empty packet or an object without slots changes nothing. Counts each
/// packet in the object's slot of the worker and adds the new count to the
/// seen counter, unless the object's commit made no handle.
pub struct Stash;

impl Module for Stash {
    const NAME: &'static str = "stash";
    type Config = StashConfig;
    type Prepared<'p> = ();

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let state = &cx.config().state;
        let slots = state.get(cx).slots.as_slice(cx);
        let prepared = state.prepared(cx);
        let seed = prepared.seed;
        let seen = &cx.config().seen;
        batch.dispatch(|packet| {
            let slot = packet
                .data()
                .first()
                .and_then(|&byte| usize::from(byte).checked_rem(slots.len()))
                .and_then(|idx| slots.get(idx));
            if let Some(slot) = slot {
                slot.fetch_add(seed, Ordering::Relaxed);
            }
            if let Some(worker) = &prepared.worker {
                seen.add(cx, worker.update(cx, |count| count + 1));
            }
            Verdict::Pass
        });
    }

    fn prepare(_: &CommitCx<'_, Self>) {}
}
