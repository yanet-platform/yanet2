//! Borrowed ACL rule views compiled by the authored Rust API.
#![forbid(unsafe_code)]
use core::ops::Range;
use std::borrow::Cow;

use yanet_sdk::{
    Built, FfiRecord, Init,
    classify::{
        Fragment, init,
        records::{LineRange, Net4, Net6, PortRange},
    },
    counter_pair, slice,
};
use yanet_sdk_api::{Agent, ApiError, ErrorKind};
use yanet_sdk_fixture::firewall::{Config, ConfigInit, Firewall as FirewallModule, TargetInit};

pub const FIREWALL_PATH_PLAIN: u64 = 1;
pub const FIREWALL_PATH_TCP: u64 = 2;
pub const FIREWALL_PATH_UDP: u64 = 4;
pub const FIREWALL_PATH_ICMP: u64 = 8;
pub const FIREWALL_FRAGMENT_ANY: u64 = 0;
pub const FIREWALL_FRAGMENT_WHOLE: u64 = 1;
pub const FIREWALL_FRAGMENT_ONLY: u64 = 2;

#[derive(FfiRecord)]
#[repr(C)]
pub struct Span {
    pub offset: u64,
    pub count: u64,
}
impl Span {
    fn range(&self, len: usize) -> Result<Range<usize>, ApiError> {
        let offset = usize::try_from(self.offset).map_err(|_| invalid("a view offset overflows"))?;
        let count = usize::try_from(self.count).map_err(|_| invalid("a view count overflows"))?;
        let end = offset.checked_add(count).ok_or_else(|| invalid("a view overflows"))?;
        if end > len {
            return Err(invalid("a view is outside its input"));
        }
        Ok(offset..end)
    }
    fn get<'a, T>(&self, values: &'a [T]) -> Result<&'a [T], ApiError> {
        Ok(&values[self.range(values.len())?])
    }
}
#[derive(FfiRecord)]
#[repr(C)]
pub struct DeviceName {
    pub name: [u8; 80],
}
#[derive(FfiRecord)]
#[repr(C)]
pub struct Rule {
    pub devices: Span,
    pub src4: Span,
    pub dst4: Span,
    pub src6: Span,
    pub dst6: Span,
    pub src_ports: Span,
    pub dst_ports: Span,
    pub protocols: Span,
    pub tcp_flags: Span,
    pub icmp_types: Span,
    pub paths: u64,
    pub fragment: u64,
    pub actions: [u8; 8],
    pub action_count: u64,
    pub counter: [u8; 64],
}
impl Rule {
    fn l2(&self) -> bool {
        self.src4.count == 0 && self.dst4.count == 0 && self.src6.count == 0 && self.dst6.count == 0
    }
    fn family4(&self) -> bool {
        self.src4.count != 0 && self.dst4.count != 0
    }
    fn family6(&self) -> bool {
        self.src6.count != 0 && self.dst6.count != 0
    }
    fn plain(&self) -> bool {
        self.paths & FIREWALL_PATH_PLAIN != 0
    }
    fn tcp(&self) -> bool {
        self.paths & FIREWALL_PATH_TCP != 0
    }
    fn udp(&self) -> bool {
        self.paths & FIREWALL_PATH_UDP != 0
    }
    fn icmp(&self) -> bool {
        self.paths & FIREWALL_PATH_ICMP != 0
    }
    fn vacuous(&self, ports: &[PortRange]) -> Result<bool, ApiError> {
        let full = |span: &Span| -> Result<bool, ApiError> {
            let ranges = span.get(ports)?;
            Ok(ranges.is_empty() || (ranges[0].from == 0 && ranges[0].to == u16::MAX))
        };
        Ok(full(&self.src_ports)? && full(&self.dst_ports)?)
    }
    fn restricted(&self, ports: &[PortRange]) -> Result<bool, ApiError> {
        Ok((self.tcp() || self.udp()) && !self.vacuous(ports)?)
    }
    fn fragment(&self) -> Result<Fragment, ApiError> {
        match self.fragment {
            FIREWALL_FRAGMENT_ANY => Ok(Fragment::Any),
            FIREWALL_FRAGMENT_WHOLE => Ok(Fragment::Whole),
            FIREWALL_FRAGMENT_ONLY => Ok(Fragment::Later),
            _ => Err(invalid("a fragment condition is invalid")),
        }
    }
}
fn invalid(message: &str) -> ApiError {
    ApiError::new(ErrorKind::InvalidArgument, message)
}
fn text(value: &[u8]) -> Result<&str, ApiError> {
    core::ffi::CStr::from_bytes_until_nul(value)
        .ok()
        .and_then(|s| s.to_str().ok())
        .ok_or_else(|| invalid("a name is invalid"))
}

pub struct Firewall;
#[yanet_sdk::api(module=FirewallModule)]
impl Firewall {
    // The native input boundary supplies independent typed borrowed views.
    #[allow(clippy::too_many_arguments)]
    pub fn build(
        agent: &Agent,
        name: &str,
        rules: &[Rule],
        devices: &[DeviceName],
        nets4: &[Net4],
        nets6: &[Net6],
        ports: &[PortRange],
        lines: &[LineRange],
    ) -> Result<Built<FirewallModule, Agent>, ApiError> {
        agent.build_config(
            name,
            input(
                rules,
                devices,
                nets4,
                nets6,
                ports,
                lines,
                |index| text(&devices[index].name),
                false,
            ),
        )
    }
}

// A fixture-only caller injects the last projection error after native tables
// have been built; the canonical export always disables that injection.
#[doc(hidden)]
#[allow(clippy::too_many_arguments)]
pub fn build_fault(
    agent: &Agent,
    name: &str,
    rules: &[Rule],
    devices: &[DeviceName],
    nets4: &[Net4],
    nets6: &[Net6],
    ports: &[PortRange],
    lines: &[LineRange],
) -> Result<Built<FirewallModule, Agent>, ApiError> {
    agent.build_config(
        name,
        input(
            rules,
            devices,
            nets4,
            nets6,
            ports,
            lines,
            |index| text(&devices[index].name),
            true,
        ),
    )
}
#[doc(hidden)]
#[allow(clippy::too_many_arguments)]
pub fn input<'a, F>(
    rules: &'a [Rule],
    devices: &'a [DeviceName],
    nets4: &'a [Net4],
    nets6: &'a [Net6],
    ports: &'a [PortRange],
    lines: &'a [LineRange],
    read_name: F,
    late_fault: bool,
) -> impl Init<ApiError, Output = Config> + 'a
where
    F: FnMut(usize) -> Result<&'a str, ApiError> + 'a,
{
    ConfigInit {
        graph: init::classify(
            rules.len(),
            (
                init::device(|i| rules[i].l2().then(|| rules[i].devices.range(devices.len())).transpose()),
                init::decode(|i| Ok(rules[i].l2())),
                init::device(|i| {
                    rules[i]
                        .family4()
                        .then(|| rules[i].devices.range(devices.len()))
                        .transpose()
                }),
                init::net4(|i| rules[i].family4().then(|| rules[i].src4.get(nets4)).transpose()),
                init::net4(|i| rules[i].family4().then(|| rules[i].dst4.get(nets4)).transpose()),
                init::byte(|i| rules[i].family4().then(|| rules[i].protocols.get(lines)).transpose()),
                init::joint(),
                init::joint(),
                init::joint(),
                init::fragment(|i| rules[i].plain().then(|| rules[i].fragment()).transpose()),
                init::joint(),
                init::decode(|i| Ok(rules[i].plain())),
                init::port(|i| {
                    rules[i]
                        .restricted(ports)?
                        .then(|| rules[i].src_ports.get(ports))
                        .transpose()
                }),
                init::port(|i| {
                    rules[i]
                        .restricted(ports)?
                        .then(|| rules[i].dst_ports.get(ports))
                        .transpose()
                }),
                init::joint(),
                init::byte(|i| rules[i].tcp().then(|| rules[i].tcp_flags.get(lines)).transpose()),
                init::joint(),
                init::rule_table(|i| Ok(rules[i].tcp())),
                init::rule_table(|i| Ok(rules[i].udp())),
                init::byte(|i| rules[i].icmp().then(|| rules[i].icmp_types.get(lines)).transpose()),
                init::rule_table(|i| Ok(rules[i].icmp())),
                init::optional_rule_table(|i| Ok(rules[i].tcp() && rules[i].vacuous(ports)?)),
                init::optional_decode(|i| Ok(rules[i].udp() && rules[i].vacuous(ports)?)),
                init::device(|i| {
                    rules[i]
                        .family6()
                        .then(|| rules[i].devices.range(devices.len()))
                        .transpose()
                }),
                init::net6(|i| rules[i].family6().then(|| rules[i].src6.get(nets6)).transpose()),
                init::net6(|i| rules[i].family6().then(|| rules[i].dst6.get(nets6)).transpose()),
                init::byte(|i| rules[i].family6().then(|| rules[i].protocols.get(lines)).transpose()),
                init::joint(),
                init::joint(),
                init::joint(),
                init::fragment(|i| rules[i].plain().then(|| rules[i].fragment()).transpose()),
                init::joint(),
                init::decode(|i| Ok(rules[i].plain())),
                init::port(|i| {
                    rules[i]
                        .restricted(ports)?
                        .then(|| rules[i].src_ports.get(ports))
                        .transpose()
                }),
                init::port(|i| {
                    rules[i]
                        .restricted(ports)?
                        .then(|| rules[i].dst_ports.get(ports))
                        .transpose()
                }),
                init::joint(),
                init::byte(|i| rules[i].tcp().then(|| rules[i].tcp_flags.get(lines)).transpose()),
                init::joint(),
                init::rule_table(|i| Ok(rules[i].tcp())),
                init::rule_table(|i| Ok(rules[i].udp())),
                init::byte(|i| rules[i].icmp().then(|| rules[i].icmp_types.get(lines)).transpose()),
                init::rule_table(|i| Ok(rules[i].icmp())),
                init::optional_rule_table(|i| Ok(rules[i].tcp() && rules[i].vacuous(ports)?)),
                init::optional_decode(move |i| {
                    if late_fault && i + 1 == rules.len() {
                        return Err(invalid("late firewall projection fault"));
                    }
                    Ok(rules[i].udp() && rules[i].vacuous(ports)?)
                }),
            ),
        )
        .with_devices(devices.len(), read_name),
        targets: slice(rules.len(), |index| {
            let rule = &rules[index];
            if rule.paths > 15 || rule.fragment > 2 || rule.action_count > 8 {
                return Err(invalid("a rule scalar is invalid"));
            }
            let name = text(&rule.counter)?;
            let name = if name.is_empty() {
                Cow::Owned(format!("rule {index}"))
            } else {
                Cow::Borrowed(name)
            };
            Ok(TargetInit {
                counter: counter_pair("rules", name),
                actions: rule.actions,
                action_count: rule.action_count,
            })
        }),
    }
}
