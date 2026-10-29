//! The control-plane api of the fixture modules, which the Go tests of the
//! dataplane harness call to build the configurations they run packets
//! through.
//!
//! Each module has one impl with a build export and the free export the macro
//! adds. What a test varies is in the records and arguments of the build; the
//! header carries the records, the mode values and the fault values.

#![forbid(unsafe_code)]

use core::ffi::CStr;

use yanet_sdk::{Builder, Built, FfiRecord, LpmRange, Refusal};
use yanet_sdk_api::{Agent, ApiError, ErrorKind};
use yanet_sdk_fixture::{ConfigParts, RouteFibParts, RouteListParts, RouteParts, TallyConfigParts};

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

/// The text of a NUL terminated name.
fn text(name: &[u8; FIB_NAME_LEN]) -> Result<&str, ApiError> {
    CStr::from_bytes_until_nul(name)
        .ok()
        .and_then(|name| name.to_str().ok())
        .ok_or_else(|| ApiError::new(ErrorKind::InvalidArgument, "a name is not NUL terminated UTF-8"))
}

pub struct Fixture;

#[yanet_sdk::api(module = yanet_sdk_fixture::Fixture)]
impl Fixture {
    /// A configuration named `name` with weights 1, 2 and 3 allocated first,
    /// then the fault the spec selects, then the counter and the device.
    pub fn build(
        agent: &Agent,
        name: &str,
        counter: &str,
        device: &str,
        spec: &FixtureSpec,
        ranges: &[Range4],
    ) -> Result<Built<yanet_sdk_fixture::Fixture, Agent>, ApiError> {
        Builder::<yanet_sdk_fixture::Fixture>::build::<ApiError, _>(agent, name, |scope| {
            let weights = scope.slice::<u64>(3, |idx| Ok(idx as u64 + 1))?;
            match spec.fault {
                FIXTURE_FAULT_NONE => {}
                FIXTURE_FAULT_ERROR => return Err(ApiError::from(Refusal::BlockTooLarge)),
                FIXTURE_FAULT_PANIC => panic!("fixture fault"),
                FIXTURE_FAULT_PANIC_PAYLOAD => std::panic::panic_any(PanicOnDrop),
                _ => return Err(ApiError::new(ErrorKind::InvalidArgument, "unknown fault")),
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
    /// in the order `held`, `v2`, `v0`, `v1`, an order other than the fields'.
    pub fn build(agent: &Agent, name: &str, word: u64) -> Result<Built<yanet_sdk_fixture::Tally, Agent>, ApiError> {
        Builder::<yanet_sdk_fixture::Tally>::build::<ApiError, _>(agent, name, |scope| {
            let held = scope.counter("held")?;
            let v2 = scope.counter("v2")?;
            let v0 = scope.counter("v0")?;
            let v1 = scope.counter("v1")?;
            Ok(TallyConfigParts { word, by_value: [v0, v1, v2], held })
        })
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
    ) -> Result<Built<yanet_sdk_fixture::Fib, Agent>, ApiError> {
        Builder::<yanet_sdk_fixture::Fib>::build::<ApiError, _>(agent, name, |scope| {
            let devices = devices
                .iter()
                .map(|device| scope.device(text(&device.name)?))
                .collect::<Result<Vec<_>, ApiError>>()?;
            Ok(RouteFibParts {
                lpm_v6: v6,
                lpm_v4: v4,
                routes: scope.slice(routes.len(), |idx| {
                    let route = &routes[idx];
                    let device = devices.get(usize::from(route.device)).copied().ok_or_else(|| {
                        ApiError::new(ErrorKind::InvalidArgument, "a route names a device out of the list")
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

struct PanicOnDrop;

impl Drop for PanicOnDrop {
    fn drop(&mut self) {
        panic!("fixture panic payload destructor");
    }
}
