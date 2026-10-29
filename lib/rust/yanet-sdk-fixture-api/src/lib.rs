//! Control-plane APIs used by the SDK fixture integration tests.
//!
//! Each module exposes construction and automatic release through the export
//! macro. Input records, mode constants and fault constants form the generated
//! C header consumed by the Go tests.

#![forbid(unsafe_code)]

pub mod firewall;
pub mod firewall4;
pub mod firewall_faults;

use core::ffi::CStr;

use yanet_sdk::{Built, FfiRecord, LpmRange, Refusal, counter, device, lpm, slice, with_devices};
use yanet_sdk_api::{Agent, ApiError, ErrorKind};
use yanet_sdk_fixture::{ConfigInit, RouteFibInit, RouteInit, RouteListInit, TallyConfigInit};

/// Bytes of a name in a record, the terminating NUL included.
pub const FIB_NAME_LEN: usize = 64;

/// Builds successfully without injecting a fault.
pub const FIXTURE_FAULT_NONE: u64 = 0;
/// Returns a domain error after allocating a descendant block.
pub const FIXTURE_FAULT_ERROR: u64 = 1;
/// Panics after allocating a descendant block.
pub const FIXTURE_FAULT_PANIC: u64 = 2;
/// Panics before construction with a payload whose destructor also panics.
pub const FIXTURE_FAULT_PANIC_PAYLOAD: u64 = 3;
/// Refuses the request before construction to exercise independent archives.
pub const FIXTURE_FAULT_PROBE_REFUSAL: u64 = 4;
/// Panics when the completed LPM initializer callback is destroyed.
pub const FIXTURE_FAULT_LPM_DROP: u64 = 5;
/// Panics before construction to exercise independent archive unwinding.
pub const FIXTURE_FAULT_PROBE_PANIC: u64 = 6;

/// Fixture behavior and fault injection independent of names and address
/// ranges.
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
#[derive(FfiRecord)]
#[repr(C)]
pub struct Range4 {
    pub from: [u8; 4],
    pub to: [u8; 4],
    pub value: u32,
}

/// An inclusive IPv6 range of an LPM table and its value.
#[derive(FfiRecord)]
#[repr(C)]
pub struct Range6 {
    pub from: [u8; 16],
    pub to: [u8; 16],
    pub value: u32,
}

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

/// A contiguous run in the route-index array.
#[derive(FfiRecord)]
#[repr(C)]
pub struct FibList {
    pub start: u64,
    pub count: u64,
}

fn lpm4(range: &Range4) -> LpmRange<4> {
    LpmRange {
        from: range.from,
        to: range.to,
        value: range.value,
    }
}

fn lpm6(range: &Range6) -> LpmRange<16> {
    LpmRange {
        from: range.from,
        to: range.to,
        value: range.value,
    }
}

/// The text of a NUL terminated name.
fn text(name: &[u8; FIB_NAME_LEN]) -> Result<&str, ApiError> {
    CStr::from_bytes_until_nul(name)
        .ok()
        .and_then(|name| name.to_str().ok())
        .ok_or_else(|| ApiError::new(ErrorKind::InvalidArgument, "a name is not NUL terminated UTF-8"))
}

pub struct Tally;

#[yanet_sdk::api(module = yanet_sdk_fixture::Tally)]
impl Tally {
    pub fn build(
        agent: &Agent,
        name: &str,
        word: u64,
        fault: u64,
    ) -> Result<Built<yanet_sdk_fixture::Tally, Agent>, ApiError> {
        match fault {
            1 => return Err(ApiError::new(ErrorKind::FailedPrecondition, "tally probe refusal")),
            2 => std::panic::panic_any(PanicOnDrop),
            3 => panic!("tally probe panic"),
            _ => {}
        }
        agent.build_config(
            name,
            TallyConfigInit {
                word,
                by_value: [counter("v0"), counter("v1"), counter("v2")],
                held: counter("held"),
            },
        )
    }
}

pub struct Fixture;

#[yanet_sdk::api(module = yanet_sdk_fixture::Fixture)]
impl Fixture {
    /// Builds a fixture and injects selected failures during construction.
    pub fn build(
        agent: &Agent,
        name: &str,
        counter_name: &str,
        device_name: &str,
        spec: &FixtureSpec,
        ranges: &[Range4],
    ) -> Result<Built<yanet_sdk_fixture::Fixture, Agent>, ApiError> {
        if spec.fault == FIXTURE_FAULT_PROBE_PANIC {
            panic!("fixture probe panic");
        }
        if spec.fault == FIXTURE_FAULT_PANIC_PAYLOAD {
            std::panic::panic_any(PanicOnDrop);
        }
        if spec.fault == FIXTURE_FAULT_PROBE_REFUSAL {
            return Err(ApiError::new(ErrorKind::FailedPrecondition, "fixture probe refusal"));
        }
        let lpm_drop = LpmDrop(spec.fault == FIXTURE_FAULT_LPM_DROP);
        agent.build_config(
            name,
            ConfigInit {
                mode: spec.mode,
                arg: spec.arg,
                counter: counter(counter_name),
                device: device(device_name),
                routes: lpm(ranges.len(), move |index| Ok(lpm_drop.range(&ranges[index]))),
                weights: slice(3, |index| {
                    if index == 2 {
                        match spec.fault {
                            FIXTURE_FAULT_NONE => {}
                            FIXTURE_FAULT_ERROR => return Err(ApiError::from(Refusal::BlockTooLarge)),
                            FIXTURE_FAULT_PANIC => panic!("fixture fault"),
                            _ => return Err(ApiError::new(ErrorKind::InvalidArgument, "unknown fault")),
                        }
                    }
                    Ok(index as u64 + 1)
                }),
            },
        )
    }
}

pub struct Fib;

#[yanet_sdk::api(module = yanet_sdk_fixture::Fib)]
impl Fib {
    /// Builds a FIB directly from borrowed records, with a counter per route.
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
    ) -> Result<Built<yanet_sdk_fixture::Fib, Agent>, ApiError> {
        agent.build_config(
            name,
            with_devices(
                devices.len(),
                |index| text(&devices[index].name),
                |devices| {
                    Ok(RouteFibInit {
                        lpm_v6: lpm(v6.len(), |index| checked_range(lpm6(&v6[index]), lists.len())),
                        lpm_v4: lpm(v4.len(), |index| checked_range(lpm4(&v4[index]), lists.len())),
                        routes: slice(routes.len(), move |index| {
                            let route = &routes[index];
                            let linked = devices
                                .get(usize::from(route.device))
                                .ok_or_else(|| invalid("a route names a device out of the list"))?;
                            Ok(RouteInit {
                                dst_addr: route.dst_addr,
                                src_addr: [0xeeu8; 6],
                                pad: 0u32,
                                device: linked,
                                counter: counter(text(&route.counter)?),
                            })
                        }),
                        route_lists: slice(lists.len(), |index| {
                            let list = &lists[index];
                            let start = usize::try_from(list.start)
                                .map_err(|_| invalid("a list start does not fit the index"))?;
                            let count = usize::try_from(list.count)
                                .map_err(|_| invalid("a list count does not fit the index"))?;
                            if start.checked_add(count).is_none_or(|end| end > indexes.len()) {
                                return Err(invalid("a route list exceeds the index"));
                            }
                            Ok(RouteListInit { start: list.start, count: list.count })
                        }),
                        route_indexes: slice(indexes.len(), |index| {
                            let route = indexes[index];
                            if usize::try_from(route).map_or(true, |route| route >= routes.len()) {
                                return Err(invalid("a route index exceeds the route table"));
                            }
                            Ok(route)
                        }),
                    })
                },
            ),
        )
    }
}

fn invalid(message: &str) -> ApiError {
    ApiError::new(ErrorKind::InvalidArgument, message)
}

fn checked_range<const K: usize>(range: LpmRange<K>, lists: usize) -> Result<LpmRange<K>, ApiError> {
    if usize::try_from(range.value).map_or(true, |value| value >= lists) {
        return Err(invalid("an LPM range names a missing route list"));
    }
    Ok(range)
}

struct PanicOnDrop;
impl Drop for PanicOnDrop {
    fn drop(&mut self) {
        std::panic::panic_any(String::from("fixture panic payload destructor"));
    }
}

struct LpmDrop(bool);
impl LpmDrop {
    fn range(&self, range: &Range4) -> LpmRange<4> {
        lpm4(range)
    }
}
impl Drop for LpmDrop {
    fn drop(&mut self) {
        if self.0 {
            panic!("LPM callback destructor");
        }
    }
}
