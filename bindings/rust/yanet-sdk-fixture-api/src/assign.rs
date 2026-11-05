//! The assign module's records and build: each family's classifier composed
//! over the classify bindings of the SDK, over the rules of the family, and
//! that of the rules of any family over theirs; a path without rules is
//! written empty.

use core::ffi::CStr;

use yanet_sdk::{
    Attribute, Builder, Built, FfiRecord, Joint, LineAttr, LineRange, Lines, Net4, Net6, Nets4, Nets6, Owned, PortAttr,
    PortRange, Ports, RuleMap, RuleTable, Scope,
};
use yanet_sdk_api::{Agent, ApiError, ErrorKind};
use yanet_sdk_fixture::assign::{ActionParts, Assign4Parts, Assign6Parts, AssignAnyParts, AssignConfigParts};

use crate::text;

/// `AssignRule::family`: the rule matches packets of either family by their
/// protocol and port only, and names no networks.
pub const ASSIGN_FAMILY_ANY: u64 = 0;
/// `AssignRule::family`: the rule matches IPv4 packets.
pub const ASSIGN_FAMILY_IPV4: u64 = 4;
/// `AssignRule::family`: the rule matches IPv6 packets.
pub const ASSIGN_FAMILY_IPV6: u64 = 6;

/// Bytes of a rule's counter name, the terminating NUL included.
pub const ASSIGN_COUNTER_NAME_LEN: usize = 64;

/// A run of `count` items of an array of an assign build, from `offset`.
#[derive(FfiRecord)]
#[repr(C)]
pub struct Span {
    pub offset: u64,
    pub count: u64,
}

/// A rule of an assign module: an `ASSIGN_FAMILY_*` value, its networks as
/// runs of the build's array of the family (none for a rule of any family,
/// whose network runs are not read), its protocols and destination
/// ports as runs of the line and port arrays (an empty run matches every
/// value), what it does with its packets and the name of its counter.
#[derive(FfiRecord)]
#[repr(C)]
pub struct AssignRule {
    pub family: u64,
    pub src: Span,
    pub dst: Span,
    pub protos: Span,
    pub ports: Span,
    pub pass: u64,
    pub counter: [u8; ASSIGN_COUNTER_NAME_LEN],
}

pub struct Assign;

#[yanet_sdk::api(module = yanet_sdk_fixture::assign::Assign)]
impl Assign {
    /// An assign module named `name` of the rules, in order of precedence,
    /// whose sets are runs of the network, line and port arrays.
    ///
    /// The items of the arrays are the C structs of the classification
    /// headers (`common/network.h`, `common/filter_views.h`,
    /// `lib/classify/rule.h`), which the C side includes for them.
    pub fn build(
        agent: &Agent,
        name: &str,
        rules: &[AssignRule],
        nets4: &[Net4],
        nets6: &[Net6],
        lines: &[LineRange],
        ports: &[PortRange],
    ) -> Result<Built<yanet_sdk_fixture::assign::Assign, Agent>, ApiError> {
        let views = rules
            .iter()
            .map(|rule| RuleViews::new(rule, nets4, nets6, lines, ports))
            .collect::<Result<Vec<_>, _>>()?;
        Builder::<yanet_sdk_fixture::assign::Assign>::build::<ApiError, _>(agent, name, |scope| {
            let v4 = views.iter().map(|view| view.v4.as_ref()).collect::<Vec<_>>();
            let v6 = views.iter().map(|view| view.v6.as_ref()).collect::<Vec<_>>();
            let (src, dst, proto, port, nets, transport, rules4) = family(scope, &views, &v4, NAMES4)?;
            let v4 = Assign4Parts {
                src,
                dst,
                proto,
                port,
                nets,
                transport,
                rules: rules4,
            };
            let (src, dst, proto, port, nets, transport, rules6) = family(scope, &views, &v6, NAMES6)?;
            let v6 = Assign6Parts {
                src,
                dst,
                proto,
                port,
                nets,
                transport,
                rules: rules6,
            };
            let any = views.iter().map(|view| view.any.as_ref()).collect::<Vec<_>>();
            let any = any_family(scope, &views, &any)?;
            let actions = scope.slice(rules.len(), |idx| {
                Ok(ActionParts {
                    pass: rules[idx].pass,
                    counter: scope.counter(text(&rules[idx].counter)?)?,
                })
            })?;
            Ok(AssignConfigParts { v4, v6, any, actions })
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

/// The views of a rule: its networks in its family's slot, the others empty,
/// or only the slot of any family for a rule without networks.
struct RuleViews<'a> {
    v4: Option<(Nets4<'a>, Nets4<'a>)>,
    v6: Option<(Nets6<'a>, Nets6<'a>)>,
    any: Option<()>,
    lines: Lines<'a>,
    ports: Ports<'a>,
}

impl<'a> RuleViews<'a> {
    fn new(
        rule: &AssignRule,
        nets4: &'a [Net4],
        nets6: &'a [Net6],
        lines: &'a [LineRange],
        ports: &'a [PortRange],
    ) -> Result<Self, ApiError> {
        let (v4, v6, any) = match rule.family {
            ASSIGN_FAMILY_IPV4 => {
                let pair = (Nets4::new(run(nets4, &rule.src)?)?, Nets4::new(run(nets4, &rule.dst)?)?);
                (Some(pair), None, None)
            }
            ASSIGN_FAMILY_IPV6 => {
                let pair = (Nets6::new(run(nets6, &rule.src)?)?, Nets6::new(run(nets6, &rule.dst)?)?);
                (None, Some(pair), None)
            }
            ASSIGN_FAMILY_ANY => (None, None, Some(())),
            _ => return Err(ApiError::new(ErrorKind::InvalidArgument, "a rule of an unknown family")),
        };
        Ok(Self {
            v4,
            v6,
            any,
            lines: Lines::new(run(lines, &rule.protos)?)?,
            ports: Ports::new(run(ports, &rule.ports)?)?,
        })
    }
}

/// Names of the memory contexts of a family's compiles and joins, in order:
/// the source, the destination, the protocol, the port, both joints and the
/// rule table.
type Names = [&'static CStr; 7];

const NAMES4: Names = [
    c"assign v4 src",
    c"assign v4 dst",
    c"assign v4 proto",
    c"assign v4 port",
    c"assign v4 nets",
    c"assign v4 transport",
    c"assign v4 rules",
];

const NAMES6: Names = [
    c"assign v6 src",
    c"assign v6 dst",
    c"assign v6 proto",
    c"assign v6 port",
    c"assign v6 nets",
    c"assign v6 transport",
    c"assign v6 rules",
];

type Scoped<'b, 'env> = Scope<'b, 'env, Agent, ApiError>;

/// The parts of a family's classifier: its four attributes, both joints and
/// the rule table.
type FamilyParts<'b, C> = (
    Owned<'b, C, Agent>,
    Owned<'b, C, Agent>,
    Owned<'b, LineAttr, Agent>,
    Owned<'b, PortAttr, Agent>,
    Owned<'b, Joint, Agent>,
    Owned<'b, Joint, Agent>,
    Owned<'b, RuleTable, Agent>,
);

/// Composes a family's classifier over the rules whose networks are of the
/// family, `nets` holding each rule's pair or `None` for a rule of the other.
fn family<'b, N: Attribute>(
    scope: Scoped<'b, '_>,
    views: &[RuleViews<'_>],
    nets: &[Option<&(N, N)>],
    [
        src_name,
        dst_name,
        proto_name,
        port_name,
        nets_name,
        transport_name,
        rules_name,
    ]: Names,
) -> Result<FamilyParts<'b, N::Classifier>, ApiError> {
    let src = nets.iter().map(|pair| pair.map(|(src, _)| src)).collect::<Vec<_>>();
    let dst = nets.iter().map(|pair| pair.map(|(_, dst)| dst)).collect::<Vec<_>>();
    let lines = views
        .iter()
        .zip(nets)
        .map(|(view, pair)| pair.map(|_| &view.lines))
        .collect::<Vec<_>>();
    let ports = views
        .iter()
        .zip(nets)
        .map(|(view, pair)| pair.map(|_| &view.ports))
        .collect::<Vec<_>>();
    let (src, src_stage) = scope.compile(src_name, &src)?;
    let (dst, dst_stage) = scope.compile(dst_name, &dst)?;
    let (proto, proto_stage) = scope.compile(proto_name, &lines)?;
    let (port, port_stage) = scope.compile(port_name, &ports)?;
    let (nets_joint, nets_stage) = scope.join(nets_name, &src_stage, &dst_stage)?;
    let (transport, transport_stage) = scope.join(transport_name, &proto_stage, &port_stage)?;
    let rules = if nets.iter().all(Option::is_none) {
        scope.boxed::<RuleTable>(())?
    } else {
        scope.join_rules(rules_name, &nets_stage, &transport_stage, nets)?
    };
    Ok((src, dst, proto, port, nets_joint, transport, rules))
}

/// Composes the classifier of the rules of any family, `any` holding a slot
/// for each such rule and `None` for a rule of a family: the protocol and the
/// port join into one stage, decoded into the first rule of each class.
fn any_family<'b>(
    scope: Scoped<'b, '_>,
    views: &[RuleViews<'_>],
    any: &[Option<&()>],
) -> Result<AssignAnyParts<'b, Agent>, ApiError> {
    let lines = views
        .iter()
        .zip(any)
        .map(|(view, slot)| slot.map(|_| &view.lines))
        .collect::<Vec<_>>();
    let ports = views
        .iter()
        .zip(any)
        .map(|(view, slot)| slot.map(|_| &view.ports))
        .collect::<Vec<_>>();
    let (proto, proto_stage) = scope.compile(c"assign any proto", &lines)?;
    let (port, port_stage) = scope.compile(c"assign any port", &ports)?;
    let (transport, transport_stage) = scope.join(c"assign any transport", &proto_stage, &port_stage)?;
    let rules = if any.iter().all(Option::is_none) {
        scope.boxed::<RuleMap>(())?
    } else {
        scope.decode(c"assign any rules", &transport_stage, any)?
    };
    Ok(AssignAnyParts { proto, port, transport, rules })
}
