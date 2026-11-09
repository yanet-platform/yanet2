//! The control-plane api of the fixture modules, which the Go tests of the
//! dataplane harness call to build the configurations they run packets
//! through.
//!
//! Each module has one impl with a build export and the free export the macro
//! adds, and the state object one with a build, queries and its free. What a
//! test varies is in the records and arguments of the build; the header
//! carries the records, the mode values and the fault values.

#![forbid(unsafe_code)]
#![allow(
    missing_docs,
    reason = "test fixture: its fields and impls would only restate their names"
)]

pub mod assign;

use core::{
    ffi::CStr,
    sync::atomic::{AtomicU64, Ordering},
};

use yanet_sdk::{Builder, Built, BuiltObject, FfiRecord, LpmRange, ObjectBuilder, Refusal};
use yanet_sdk_api::{Agent, Error, ErrorKind};
use yanet_sdk_fixture::{
    ConfigParts, RouteFibParts, RouteListParts, RouteParts, StashConfigParts, StateConfigParts, TallyConfigParts,
};

/// Bytes of a name in a record, the terminating NUL included.
pub const FIB_NAME_LEN: usize = 64;

/// `FixtureSpec::fault`: the build succeeds.
pub const FIXTURE_FAULT_NONE: u64 = 0;
/// `FixtureSpec::fault`: the build fails with its own error after it has
/// allocated a block.
pub const FIXTURE_FAULT_ERROR: u64 = 1;
/// `FixtureSpec::fault`: the build panics after it has allocated a block.
pub const FIXTURE_FAULT_PANIC: u64 = 2;
/// The build panics with a payload whose destructor also panics.
pub const FIXTURE_FAULT_PANIC_PAYLOAD: u64 = 3;

/// Values in each worker's region of a state object.
const STATE_REGION_LEN: usize = 2;

/// What a `Fixture` configuration is made of besides its names and ranges.
#[derive(FfiRecord)]
#[repr(C)]
pub struct FixtureSpec {
    /// A `FIXTURE_MODE_*` constant.
    pub mode: u64,
    pub arg: u64,
    /// A `FIXTURE_FAULT_*` constant.
    pub fault: u64,
}

/// An inclusive IPv4 range of an LPM table and its value.
pub type Range4 = LpmRange<4>;

/// An inclusive IPv6 range of an LPM table and its value.
pub type Range6 = LpmRange<16>;

/// A device of a FIB, by name.
#[derive(FfiRecord)]
#[repr(C)]
pub struct FibDevice {
    pub name: [u8; FIB_NAME_LEN],
}

/// A route of a FIB: its neighbour, the index of its device in the device
/// list and the name of its counter.
#[derive(FfiRecord)]
#[repr(C)]
pub struct FibRoute {
    pub dst_addr: [u8; 6],
    pub device: u16,
    pub counter: [u8; FIB_NAME_LEN],
}

/// A run of `count` entries of the route index, from `start`.
#[derive(FfiRecord)]
#[repr(C)]
pub struct FibList {
    pub start: u64,
    pub count: u64,
}

/// Bytes of an object's type or name in a record, the terminating NUL
/// included, as in C's fields of them.
pub const OBJECT_NAME_LEN: usize = 80;

/// An object a configuration links: its type and its name, NUL terminated.
#[derive(FfiRecord)]
#[repr(C)]
pub struct Link {
    pub ty: [u8; OBJECT_NAME_LEN],
    pub name: [u8; OBJECT_NAME_LEN],
}

/// The shape of a state object, as one query reads it: its seed, its number
/// of slots, and the workers and values per worker of its regions.
#[derive(FfiRecord)]
#[repr(C)]
pub struct StateShape {
    pub seed: u64,
    pub slots: u64,
    pub workers: u64,
    pub region_len: u64,
}

/// The text of a NUL terminated name.
fn text(name: &[u8; FIB_NAME_LEN]) -> Result<&str, Error> {
    CStr::from_bytes_until_nul(name)
        .ok()
        .and_then(|name| name.to_str().ok())
        .ok_or_else(|| Error::new(ErrorKind::InvalidArgument, "a name is not NUL terminated UTF-8"))
}

pub struct Fixture;

#[yanet_sdk::api(module = yanet_sdk_fixture::Fixture)]
impl Fixture {
    /// A configuration named `name` with weights 1, 2 and 3 allocated first,
    /// then the fault the spec selects, then the counter and the device.
    ///
    /// # Panics
    ///
    /// When the fault selects a panic, for the panic guard of the export.
    pub fn build(
        agent: &Agent,
        name: &str,
        counter: &str,
        device: &str,
        spec: &FixtureSpec,
        ranges: &[Range4],
    ) -> Result<Built<yanet_sdk_fixture::Fixture, Agent>, Error> {
        Builder::<yanet_sdk_fixture::Fixture>::build::<Error, _>(agent, name, |scope| {
            let weights = scope.slice::<u64>(3, |idx| Ok(idx as u64 + 1))?;
            match spec.fault {
                FIXTURE_FAULT_NONE => {}
                FIXTURE_FAULT_ERROR => return Err(Error::from(Refusal::BlockTooLarge)),
                FIXTURE_FAULT_PANIC => panic!("fixture fault"),
                FIXTURE_FAULT_PANIC_PAYLOAD => std::panic::panic_any(PanicOnDrop),
                _ => return Err(Error::new(ErrorKind::InvalidArgument, "unknown fault")),
            }
            Ok(ConfigParts {
                mode: spec.mode,
                arg: spec.arg,
                counter: scope.counter(counter)?,
                device: scope.device(device)?,
                routes: ranges,
                weights,
            })
        })
    }
}

pub struct Tally;

#[yanet_sdk::api(module = yanet_sdk_fixture::Tally)]
impl Tally {
    /// A configuration named `name` over `word`, whose counters are registered
    /// in the order `held`, `v2`, `v0`, `v1`, an order other than the fields',
    /// and then `traffic` and `running`, with no packet seen on any worker.
    ///
    /// Its tagged counters are `even` of the registry `routes`, the held one,
    /// registered first, and then `odd` and `even` of the registry `rules`,
    /// the counters by parity: a registry order and a counter order other than
    /// the fields', and one name in two registries. The counters picked by id
    /// are those a registrar registers last in the registry `lanes` as
    /// `wide`, `narrow` and `wide` again: two counters, ids 0 and 1.
    pub fn build(agent: &Agent, name: &str, word: u64) -> Result<Built<yanet_sdk_fixture::Tally, Agent>, Error> {
        Builder::<yanet_sdk_fixture::Tally>::build::<Error, _>(agent, name, |scope| {
            let tagged_held = scope.counter_in("routes", "even")?;
            let held = scope.counter("held")?;
            let v2 = scope.counter("v2")?;
            let odd = scope.counter_in("rules", "odd")?;
            let v0 = scope.counter("v0")?;
            let v1 = scope.counter("v1")?;
            let even = scope.counter_in("rules", "even")?;
            let traffic = scope.counter("traffic")?;
            let running = scope.counter("running")?;
            let mut lanes = scope.counters_in("lanes")?;
            for name in ["wide", "narrow", "wide"] {
                lanes.counter(name)?;
            }
            Ok(TallyConfigParts {
                word,
                by_value: [v0, v1, v2],
                held,
                traffic,
                seen: scope.per_worker(0)?,
                running,
                by_parity: [even, odd],
                tagged_held,
                by_id: lanes,
            })
        })
    }

    /// Writes the packets each worker has seen under the configuration named
    /// `name`, as the generation the dataplane runs holds it, by worker, into
    /// as many places as `seen` has, and returns the number of workers.
    pub fn published_seen(agent: &Agent, name: &str, seen: &mut [u64]) -> Result<u64, Error> {
        agent.read_published::<yanet_sdk_fixture::Tally, _>(name, |cx, config| fill(seen, cx.slots(&config.seen)))
    }
}

pub struct Fib;

#[yanet_sdk::api(module = yanet_sdk_fixture::Fib)]
impl Fib {
    /// A FIB named `name` over `devices`, each registered once and copied into
    /// the routes that use it, with a counter per route.
    #[allow(clippy::too_many_arguments)]
    pub fn build(
        agent: &Agent,
        name: &str,
        devices: &[FibDevice],
        routes: &[FibRoute],
        lists: &[FibList],
        indexes: &[u64],
        v6: &[Range6],
        v4: &[Range4],
    ) -> Result<Built<yanet_sdk_fixture::Fib, Agent>, Error> {
        Builder::<yanet_sdk_fixture::Fib>::build::<Error, _>(agent, name, |scope| {
            let devices = devices
                .iter()
                .map(|device| scope.device(text(&device.name)?))
                .collect::<Result<Vec<_>, Error>>()?;
            Ok(RouteFibParts {
                lpm_v6: v6,
                lpm_v4: v4,
                routes: scope.slice(routes.len(), |idx| {
                    let route = &routes[idx];
                    let device = devices.get(usize::from(route.device)).copied().ok_or_else(|| {
                        Error::new(ErrorKind::InvalidArgument, "a route names a device out of the list")
                    })?;
                    Ok(RouteParts {
                        dst_addr: route.dst_addr,
                        src_addr: [0xee; 6],
                        pad: 0,
                        device,
                        counter: scope.counter(text(&route.counter)?)?,
                    })
                })?,
                route_lists: scope.slice(lists.len(), |idx| {
                    Ok(RouteListParts {
                        start: lists[idx].start,
                        count: lists[idx].count,
                    })
                })?,
                route_indexes: scope.slice(indexes.len(), |idx| Ok(indexes[idx]))?,
            })
        })
    }
}

pub struct Stash;

#[yanet_sdk::api(module = yanet_sdk_fixture::Stash)]
impl Stash {
    /// A configuration named `name` that writes the state object named
    /// `state`, which a generation must hold when it is installed, and counts
    /// in `seen` and `filled`.
    pub fn build(agent: &Agent, name: &str, state: &str) -> Result<Built<yanet_sdk_fixture::Stash, Agent>, Error> {
        Builder::<yanet_sdk_fixture::Stash>::build::<Error, _>(agent, name, |scope| {
            Ok(StashConfigParts {
                state: scope.object(state)?,
                seen: scope.counter("seen")?,
                filled: scope.counter("filled")?,
            })
        })
    }

    /// The seed of the state object that the configuration named `name`
    /// writes, as the generation the dataplane runs holds them; the fault, a
    /// `FIXTURE_FAULT_*` constant, panics inside the read.
    ///
    /// # Panics
    ///
    /// When the fault selects a panic, for the release of the pin as the read
    /// unwinds.
    pub fn published_seed(agent: &Agent, name: &str, fault: u64) -> Result<u64, Error> {
        agent.read_published::<yanet_sdk_fixture::Stash, _>(name, |cx, config| match fault {
            FIXTURE_FAULT_NONE => Ok(cx.object(&config.state).seed),
            FIXTURE_FAULT_PANIC => panic!("fixture fault"),
            _ => Err(Error::new(ErrorKind::InvalidArgument, "unknown fault")),
        })?
    }

    /// Writes the values of the slots of that state object, as `State::values`
    /// does, and returns the number of slots.
    pub fn published_values(agent: &Agent, name: &str, values: &mut [u64]) -> Result<u64, Error> {
        agent.read_published::<yanet_sdk_fixture::Stash, _>(name, |cx, config| {
            let slots = cx.object(&config.state).slots.as_slice(&cx);
            for (value, slot) in values.iter_mut().zip(slots) {
                *value = slot.load(Ordering::Relaxed);
            }
            slots.len() as u64
        })
    }

    /// Writes the values of the per-worker slots of that state object, as
    /// `State::workers` does, and returns the number of workers.
    pub fn published_workers(agent: &Agent, name: &str, values: &mut [u64]) -> Result<u64, Error> {
        agent.read_published::<yanet_sdk_fixture::Stash, _>(name, |cx, config| {
            fill(values, cx.slots(&cx.object(&config.state).workers))
        })
    }

    /// Writes the objects the configuration named `name` links, in link
    /// order, into as many places as `links` has, and returns the number of
    /// links.
    pub fn published_links(agent: &Agent, name: &str, links: &mut [Link]) -> Result<u64, Error> {
        agent.read_published::<yanet_sdk_fixture::Stash, _>(name, |cx, _| {
            for (link, key) in links.iter_mut().zip(cx.links()) {
                put(&mut link.ty, key.ty());
                put(&mut link.name, key.name());
            }
            cx.links().len() as u64
        })
    }
}

/// Copies values into as many places as there are, and returns the number of
/// values.
fn fill<I>(places: &mut [u64], values: I) -> u64
where
    I: ExactSizeIterator<Item = u64>,
{
    let count = values.len() as u64;
    for (place, value) in places.iter_mut().zip(values) {
        *place = value;
    }
    count
}

/// Copies a name into a field of a record, which the export zeroed: as much
/// of it as fits before the field's last byte, which stays the terminator.
fn put(field: &mut [u8; OBJECT_NAME_LEN], name: &CStr) {
    for (to, from) in field[..OBJECT_NAME_LEN - 1].iter_mut().zip(name.to_bytes()) {
        *to = *from;
    }
}

pub struct State;

#[yanet_sdk::api(object = yanet_sdk_fixture::State)]
impl State {
    /// An object named `name` of `count` slots, slot n holding `seed` + n,
    /// wrapping, a slot per worker holding `seed` and a region per worker of
    /// two values holding `seed`; the fault, a `FIXTURE_FAULT_*`
    /// constant, fails the build after it has allocated the slots and the
    /// regions.
    ///
    /// # Panics
    ///
    /// When the fault selects a panic, for the panic guard of the export.
    pub fn build(
        agent: &Agent,
        name: &str,
        seed: u64,
        count: u64,
        fault: u64,
    ) -> Result<BuiltObject<yanet_sdk_fixture::State, Agent>, Error> {
        let count = usize::try_from(count).map_err(|_| Error::from(Refusal::TooManyItems))?;
        ObjectBuilder::<yanet_sdk_fixture::State>::build::<Error, _>(agent, name, |scope| {
            let slots = scope.slice(count, |idx| Ok(AtomicU64::new(seed.wrapping_add(idx as u64))))?;
            let workers = scope.per_worker(seed)?;
            let regions = scope.per_worker_slice(STATE_REGION_LEN, seed)?;
            match fault {
                FIXTURE_FAULT_NONE => {}
                FIXTURE_FAULT_ERROR => return Err(Error::from(Refusal::BlockTooLarge)),
                FIXTURE_FAULT_PANIC => panic!("fixture fault"),
                _ => return Err(Error::new(ErrorKind::InvalidArgument, "unknown fault")),
            }
            Ok(StateConfigParts { seed, slots, workers, regions })
        })
    }

    /// The value of slot `index` of the object, read by the control plane
    /// while the object may be published.
    pub fn slot(agent: &Agent, state: &BuiltObject<yanet_sdk_fixture::State, Agent>, index: u64) -> Result<u64, Error> {
        with_slot(agent, state, index, |slot| slot.load(Ordering::Relaxed))
    }

    /// Adds `delta` to slot `index` of the object, which may be published,
    /// and returns the sum, wrapping.
    pub fn add(
        agent: &Agent,
        state: &BuiltObject<yanet_sdk_fixture::State, Agent>,
        index: u64,
        delta: u64,
    ) -> Result<u64, Error> {
        with_slot(agent, state, index, |slot| {
            slot.fetch_add(delta, Ordering::Relaxed).wrapping_add(delta)
        })
    }

    /// Writes the values of the object's slots, from the first, into as many
    /// places as `values` has, and returns the number of slots; the places
    /// past the slots stay zero.
    pub fn values(
        agent: &Agent,
        state: &BuiltObject<yanet_sdk_fixture::State, Agent>,
        values: &mut [u64],
    ) -> Result<u64, Error> {
        agent.read(state, |cx, config| {
            let slots = config.slots.as_slice(&cx);
            for (value, slot) in values.iter_mut().zip(slots) {
                *value = slot.load(Ordering::Relaxed);
            }
            Ok(slots.len() as u64)
        })
    }

    /// Writes the values of the object's slot per worker, by worker, into as
    /// many places as `values` has, and returns the number of workers; the
    /// places past the workers stay zero.
    pub fn workers(
        agent: &Agent,
        state: &BuiltObject<yanet_sdk_fixture::State, Agent>,
        values: &mut [u64],
    ) -> Result<u64, Error> {
        Ok(agent.read(state, |cx, config| fill(values, cx.slots(&config.workers))))
    }

    /// Writes the shape of the object in `shape` and returns its number of
    /// slots, the length of the places `values` fills whole.
    ///
    /// The output comes before the object, so that a refused object shows
    /// the output untouched: the export zeroes it only once it accepts every
    /// argument.
    pub fn shape(
        agent: &Agent,
        shape: &mut StateShape,
        state: &BuiltObject<yanet_sdk_fixture::State, Agent>,
    ) -> Result<u64, Error> {
        agent.read(state, |cx, config| {
            *shape = StateShape {
                seed: config.seed,
                slots: config.slots.as_slice(&cx).len() as u64,
                workers: config.regions.worker_count(),
                region_len: config.regions.len() as u64,
            };
            Ok(shape.slots)
        })
    }

    /// The seed of the object, unless the fault, a `FIXTURE_FAULT_*`
    /// constant, fails the query.
    ///
    /// # Panics
    ///
    /// When the fault selects a panic, for the panic guard of the export.
    pub fn seed(agent: &Agent, state: &BuiltObject<yanet_sdk_fixture::State, Agent>, fault: u64) -> Result<u64, Error> {
        match fault {
            FIXTURE_FAULT_NONE => Ok(agent.read(state, |_, config| config.seed)),
            FIXTURE_FAULT_ERROR => Err(Error::from(Refusal::BlockTooLarge)),
            FIXTURE_FAULT_PANIC => panic!("fixture fault"),
            _ => Err(Error::new(ErrorKind::InvalidArgument, "unknown fault")),
        }
    }
}

/// Runs `f` over slot `index` of the object.
fn with_slot(
    agent: &Agent,
    state: &BuiltObject<yanet_sdk_fixture::State, Agent>,
    index: u64,
    f: impl FnOnce(&AtomicU64) -> u64,
) -> Result<u64, Error> {
    agent.read(state, |cx, config| {
        usize::try_from(index)
            .ok()
            .and_then(|index| config.slots.as_slice(&cx).get(index))
            .map(f)
            .ok_or_else(|| Error::new(ErrorKind::InvalidArgument, "the slot is out of the object"))
    })
}

struct PanicOnDrop;

impl Drop for PanicOnDrop {
    fn drop(&mut self) {
        panic!("fixture panic payload destructor");
    }
}
