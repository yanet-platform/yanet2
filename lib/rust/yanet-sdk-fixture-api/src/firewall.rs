//! The firewall's records and build, and the composition of its classifier, as
//! the C ACL module composes its own (`modules/acl/api/controlplane.c`), over
//! the classify bindings of the SDK.
//!
//! Each attribute compiles over the projection of the ruleset that needs it:
//! the rules without networks compile the device alone; each family compiles
//! its core over the rules with both of its networks, the fragment suffix over
//! the plain rules, and each protocol path over the rules that take it. The
//! paths decide nothing of the protocols: the control plane that authors the
//! rules marks the paths a rule takes and derives its protocol lines.

use std::ffi::CString;

use yanet_sdk::{
    Attribute, Build, Builder, Built, Devices, FfiRecord, Fragment, LineRange, Lines, Net4, Net6, Nets4, Nets6, Owned,
    PortRange, Ports, RuleDevice, Scope,
};
use yanet_sdk_api::{Agent, ApiError, ErrorKind};
use yanet_sdk_fixture::firewall::{
    FIREWALL_MAX_ACTIONS, FamilyParts, FirewallConfigParts, Ip4Parts, Ip6Parts, TargetParts,
};

use crate::text;

/// `Rule::paths`: the rule takes the plain path of its family.
pub const FIREWALL_PATH_PLAIN: u64 = 1;
/// `Rule::paths`: the rule takes the TCP path.
pub const FIREWALL_PATH_TCP: u64 = 2;
/// `Rule::paths`: the rule takes the UDP path.
pub const FIREWALL_PATH_UDP: u64 = 4;
/// `Rule::paths`: the rule takes the ICMP path.
pub const FIREWALL_PATH_ICMP: u64 = 8;

/// `Rule::fragment`: fragments and whole packets.
pub const FIREWALL_FRAGMENT_ANY: u64 = 0;
/// `Rule::fragment`: whole packets only.
pub const FIREWALL_FRAGMENT_WHOLE: u64 = 1;
/// `Rule::fragment`: fragments only.
pub const FIREWALL_FRAGMENT_ONLY: u64 = 2;

/// Bytes of a device name, the terminating NUL included, as C keeps it.
pub const FIREWALL_DEVICE_NAME_LEN: usize = 80;
/// Bytes of a rule's counter name, the terminating NUL included.
pub const FIREWALL_COUNTER_NAME_LEN: usize = 64;

/// A run of `count` items of an array of a firewall build, from `offset`.
#[derive(FfiRecord)]
#[repr(C)]
pub struct Span {
    pub offset: u64,
    pub count: u64,
}

/// A device of a firewall, by name.
#[derive(FfiRecord)]
#[repr(C)]
pub struct DeviceName {
    pub name: [u8; FIREWALL_DEVICE_NAME_LEN],
}

/// A rule of a firewall: its sets as runs of the build's arrays, the
/// `FIREWALL_PATH_*` bits of its paths, a `FIREWALL_FRAGMENT_*` value, its
/// first `action_count` actions, of the C ACL's kinds, and the name of its
/// counter, `rule <index>` when empty, as C names it.
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
    pub actions: [u8; FIREWALL_MAX_ACTIONS],
    pub action_count: u64,
    pub counter: [u8; FIREWALL_COUNTER_NAME_LEN],
}

pub struct Firewall;

#[yanet_sdk::api(module = yanet_sdk_fixture::firewall::Firewall)]
impl Firewall {
    /// A firewall named `name` of the rules, in order, whose sets are runs of
    /// the device, network, port and protocol line arrays.
    ///
    /// The items of the network, port and line arrays are the C structs of the
    /// classification headers (`common/network.h`, `common/filter_views.h`,
    /// `lib/classify/rule.h`), which the C side includes for them.
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
    ) -> Result<Built<yanet_sdk_fixture::firewall::Firewall, Agent>, ApiError> {
        let arrays = Arrays {
            devices: &devices
                .iter()
                .map(|device| text(&device.name))
                .collect::<Result<Vec<_>, _>>()?,
            nets4,
            nets6,
            ports,
            lines,
        };
        Builder::<yanet_sdk_fixture::firewall::Firewall>::build::<ApiError, _>(agent, name, |scope| {
            compose(scope, rules, &arrays)
        })
    }
}

/// The run of an array a span names.
fn run<'a, T>(items: &'a [T], span: &Span) -> Result<&'a [T], ApiError> {
    usize::try_from(span.offset)
        .ok()
        .zip(usize::try_from(span.count).ok())
        .and_then(|(offset, count)| items.get(offset..offset.checked_add(count)?))
        .ok_or_else(|| ApiError::new(ErrorKind::InvalidArgument, "a rule names a run out of its array"))
}

type Scoped<'b, 'env> = Scope<'b, 'env, Agent, ApiError>;

/// The arrays the sets of the rule records are runs of, the devices by name.
struct Arrays<'a> {
    devices: &'a [&'a str],
    nets4: &'a [Net4],
    nets6: &'a [Net6],
    ports: &'a [PortRange],
    lines: &'a [LineRange],
}

/// A rule as the compiles read it: a view of each of its sets.
struct RuleView<'a> {
    devices: Devices<'a>,
    src4: Nets4<'a>,
    dst4: Nets4<'a>,
    src6: Nets6<'a>,
    dst6: Nets6<'a>,
    src_ports: Ports<'a>,
    dst_ports: Ports<'a>,
    protocols: Lines<'a>,
    tcp_flags: Lines<'a>,
    icmp_types: Lines<'a>,
    fragment: Fragment,
    paths: u64,
}

impl<'a> RuleView<'a> {
    fn new(record: &Rule, devices: &'a [RuleDevice], arrays: &Arrays<'a>) -> Result<Self, ApiError> {
        let fragment = match record.fragment {
            FIREWALL_FRAGMENT_ANY => Fragment::Any,
            FIREWALL_FRAGMENT_WHOLE => Fragment::Whole,
            FIREWALL_FRAGMENT_ONLY => Fragment::Only,
            _ => return Err(ApiError::new(ErrorKind::InvalidArgument, "unknown fragment")),
        };
        Ok(Self {
            devices: Devices::new(run(devices, &record.devices)?),
            src4: Nets4::new(run(arrays.nets4, &record.src4)?),
            dst4: Nets4::new(run(arrays.nets4, &record.dst4)?),
            src6: Nets6::new(run(arrays.nets6, &record.src6)?),
            dst6: Nets6::new(run(arrays.nets6, &record.dst6)?),
            src_ports: Ports::new(run(arrays.ports, &record.src_ports)?),
            dst_ports: Ports::new(run(arrays.ports, &record.dst_ports)?),
            protocols: Lines::new(run(arrays.lines, &record.protocols)?),
            tcp_flags: Lines::new(run(arrays.lines, &record.tcp_flags)?),
            icmp_types: Lines::new(run(arrays.lines, &record.icmp_types)?),
            fragment,
            paths: record.paths,
        })
    }

    /// A rule without networks matches every packet of its devices.
    fn l2(&self) -> bool {
        self.src4.as_slice().is_empty()
            && self.dst4.as_slice().is_empty()
            && self.src6.as_slice().is_empty()
            && self.dst6.as_slice().is_empty()
    }

    fn ip4(&self) -> bool {
        !self.src4.as_slice().is_empty() && !self.dst4.as_slice().is_empty()
    }

    fn ip6(&self) -> bool {
        !self.src6.as_slice().is_empty() && !self.dst6.as_slice().is_empty()
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

    /// The port pair compiles over the rules that restrict a port side only:
    /// the rules of any port on both sides resolve without it, so that their
    /// whole core footprint is not crossed with every port class.
    fn ports_restricted(&self) -> bool {
        let any = |ports: &Ports<'_>| {
            ports
                .as_slice()
                .first()
                .is_none_or(|range| range.from == 0 && range.to == u16::MAX)
        };
        !any(&self.src_ports) || !any(&self.dst_ports)
    }

    fn tcp_any_port(&self) -> bool {
        self.tcp() && !self.ports_restricted()
    }

    fn udp_any_port(&self) -> bool {
        self.udp() && !self.ports_restricted()
    }
}

/// One slot per rule: the view of each rule the predicate keeps, `None` for
/// the others.
fn project<'r, V>(
    rules: &'r [RuleView<'r>],
    keep: fn(&RuleView<'r>) -> bool,
    view: fn(&'r RuleView<'r>) -> &'r V,
) -> Vec<Option<&'r V>> {
    rules.iter().map(|rule| keep(rule).then(|| view(rule))).collect()
}

/// The rules the predicate keeps, for a join or a decode.
fn kept<'r>(rules: &'r [RuleView<'r>], keep: fn(&RuleView<'r>) -> bool) -> Vec<Option<&'r RuleView<'r>>> {
    project(rules, keep, |rule| rule)
}

/// The name of a memory context of the build: a family and a role.
fn name(family: &str, role: &str) -> CString {
    CString::new(format!("{family}:{role}")).expect("names hold no NUL")
}

/// The parts of a firewall of the rule records over the arrays of their sets.
fn compose<'b>(
    scope: Scoped<'b, '_>,
    records: &[Rule],
    arrays: &Arrays<'_>,
) -> Result<FirewallConfigParts<'b, Agent>, ApiError> {
    let devices = arrays
        .devices
        .iter()
        .map(|&device| scope.device(device).map(RuleDevice::new))
        .collect::<Result<Vec<_>, _>>()?;
    let rules = records
        .iter()
        .map(|record| RuleView::new(record, &devices, arrays))
        .collect::<Result<Vec<_>, _>>()?;
    let targets = scope.slice(records.len(), |idx| {
        let record = &records[idx];
        if record.action_count > FIREWALL_MAX_ACTIONS as u64 {
            return Err(ApiError::new(
                ErrorKind::InvalidArgument,
                "a rule has more actions than a target holds",
            ));
        }
        let counter = match text(&record.counter)? {
            "" => format!("rule {idx}"),
            counter => counter.to_owned(),
        };
        Ok(TargetParts {
            counter: scope.counter_pair(&counter)?,
            actions: record.actions,
            action_count: record.action_count,
        })
    })?;

    let l2 = kept(&rules, RuleView::l2);
    let (l2_device, l2_stage) = scope.compile(c"l2:device", &project(&rules, RuleView::l2, |rule| &rule.devices))?;
    let l2_rules = scope.decode(c"l2:rules", &l2_stage, &l2)?;

    let Composed { src, dst, family: ip4 } =
        family(scope, "ip4", &rules, RuleView::ip4, |rule| (&rule.src4, &rule.dst4))?;
    let ip4 = Ip4Parts { src, dst, family: ip4 };
    let Composed { src, dst, family: ip6 } =
        family(scope, "ip6", &rules, RuleView::ip6, |rule| (&rule.src6, &rule.dst6))?;
    let ip6 = Ip6Parts { src, dst, family: ip6 };
    Ok(FirewallConfigParts {
        l2_device,
        l2_rules,
        ip4,
        ip6,
        targets,
    })
}

/// The network classifiers of a family and its composition.
struct Composed<'b, C: Build> {
    src: Owned<'b, C, Agent>,
    dst: Owned<'b, C, Agent>,
    family: FamilyParts<'b, Agent>,
}

/// The network classifiers of a family and its composition, compiled over
/// the rules the predicate keeps.
fn family<'b, 'r, A: Attribute + 'r>(
    scope: Scoped<'b, '_>,
    family: &str,
    rules: &'r [RuleView<'r>],
    has: fn(&RuleView<'r>) -> bool,
    nets: fn(&'r RuleView<'r>) -> (&'r A, &'r A),
) -> Result<Composed<'b, A::Classifier>, ApiError> {
    // The core over the rules of the family: the network pair first, then the
    // device, then the protocol.
    let (device, device_stage) = scope.compile(&name(family, "device"), &project(rules, has, |rule| &rule.devices))?;
    let src = rules
        .iter()
        .map(|rule| has(rule).then(|| nets(rule).0))
        .collect::<Vec<_>>();
    let dst = rules
        .iter()
        .map(|rule| has(rule).then(|| nets(rule).1))
        .collect::<Vec<_>>();
    let (src, src_stage) = scope.compile(&name(family, "net_src"), &src)?;
    let (dst, dst_stage) = scope.compile(&name(family, "net_dst"), &dst)?;
    let (nets, nets_stage) = scope.join(&name(family, "nets_joint"), &src_stage, &dst_stage)?;
    let (mid, mid_stage) = scope.join(&name(family, "mid_joint"), &device_stage, &nets_stage)?;
    let (proto, proto_stage) = scope.compile(&name(family, "ipproto"), &project(rules, has, |rule| &rule.protocols))?;
    let (core, core_stage) = scope.join(&name(family, "proto_joint"), &mid_stage, &proto_stage)?;

    // The plain filter: the fragment classes onto the core.
    let plain_rules = kept(rules, RuleView::plain);
    let (frag, frag_stage) = scope.compile(
        &name(family, "ipfrag"),
        &project(rules, RuleView::plain, |rule| &rule.fragment),
    )?;
    let (plain, plain_stage) = scope.join(&name(family, "root_joint"), &core_stage, &frag_stage)?;
    let plain_rules = scope.decode(&name(family, "rules"), &plain_stage, &plain_rules)?;

    // The port pair the TCP and the UDP paths share.
    let restricted = |rule: &RuleView<'_>| (rule.tcp() || rule.udp()) && rule.ports_restricted();
    let (src_port, src_port_stage) = scope.compile(
        &name(family, "src_port"),
        &project(rules, restricted, |rule| &rule.src_ports),
    )?;
    let (dst_port, dst_port_stage) = scope.compile(
        &name(family, "dst_port"),
        &project(rules, restricted, |rule| &rule.dst_ports),
    )?;
    let (ports, ports_stage) = scope.join(&name(family, "ports_joint"), &src_port_stage, &dst_port_stage)?;

    let udp = scope.join_rules(
        &name(family, "udp_root_joint"),
        &core_stage,
        &ports_stage,
        &kept(rules, RuleView::udp),
    )?;

    let (tcp_flags, flags_stage) = scope.compile(
        &name(family, "tcp_flags"),
        &project(rules, RuleView::tcp, |rule| &rule.tcp_flags),
    )?;
    let (flags, tcp_stage) = scope.join(&name(family, "flags_joint"), &ports_stage, &flags_stage)?;
    let tcp = scope.join_rules(
        &name(family, "tcp_root_joint"),
        &core_stage,
        &tcp_stage,
        &kept(rules, RuleView::tcp),
    )?;

    let (icmp_type, icmp_stage) = scope.compile(
        &name(family, "icmp_type"),
        &project(rules, RuleView::icmp, |rule| &rule.icmp_types),
    )?;
    let icmp = scope.join_rules(
        &name(family, "icmp_root_joint"),
        &core_stage,
        &icmp_stage,
        &kept(rules, RuleView::icmp),
    )?;

    // The rules of any port: the TCP ones keep their flags, the UDP ones
    // need the core alone; a ruleset without them builds neither.
    let tcp_any_port = kept(rules, RuleView::tcp_any_port);
    let tcp_any_port = if tcp_any_port.iter().any(Option::is_some) {
        scope.join_rules(&name(family, "tcp_vac_joint"), &core_stage, &flags_stage, &tcp_any_port)?
    } else {
        scope.boxed(())?
    };
    let udp_any_port = kept(rules, RuleView::udp_any_port);
    let udp_any_port = if udp_any_port.iter().any(Option::is_some) {
        scope.decode(&name(family, "udp_vac_rules"), &core_stage, &udp_any_port)?
    } else {
        scope.boxed(())?
    };

    let family = FamilyParts {
        device,
        proto,
        nets,
        mid,
        core,
        frag,
        plain,
        plain_rules,
        src_port,
        dst_port,
        ports,
        tcp_flags,
        flags,
        icmp_type,
        tcp,
        tcp_any_port,
        udp,
        udp_any_port,
        icmp,
    };
    Ok(Composed { src, dst, family })
}
