//! The classifier trees of the tests, built from rules by the C compiler
//! in a C block arena, and the oracle that matches the rules one by one.

use yanet_shm::rel::ShmBox;
use yanet_sys::{
    classify::{
        Decode, Join, LineAttr, LineRange, Lines, Net4, Net4Attr, Net6, Net6Attr, Nets4, Nets6, PortAttr, PortRange,
        Ports, Rules,
    },
    cp::{BuildError, Builder, Built},
    dp::{Batch, CommitCx, Cx, Module},
};

use crate::arena::{ArenaOwner, Never};

pub type Error = BuildError<Never>;

/// The tree of the IPv4 rules: the networks joined, the protocol and the
/// port joined, and the rule of each pair.
pub type Tree4 = Rules<Join<Net4Attr, Net4Attr>, Join<LineAttr, PortAttr>>;

/// The tree of the IPv6 rules, shaped as the IPv4 one.
pub type Tree6 = Rules<Join<Net6Attr, Net6Attr>, Join<LineAttr, PortAttr>>;

/// The tree of the rules of any family: the protocol and the port joined
/// and decoded.
pub type TreeAny = Decode<Join<LineAttr, PortAttr>>;

/// A module whose configuration is one tree; never run.
pub struct Tree<T>(T);

macro_rules! tree_module {
    ($($tree:ty: $name:literal;)*) => {$(
        impl Module for Tree<$tree> {
            const NAME: &'static str = $name;
            type Config = ShmBox<$tree>;
            type Prepared<'p> = ();

            fn handle<'id>(_: &mut Cx<'id, Self>, _: &mut Batch<'id>) {}

            fn prepare<'p>(_: &CommitCx<'p, Self>) {}
        }
    )*};
}

tree_module! {
    Tree4: "classify-tree-v4";
    Tree6: "classify-tree-v6";
    TreeAny: "classify-tree-any";
}

/// The IPv4 network of an address and a prefix length.
pub fn net4(addr: [u8; 4], len: u32) -> Net4 {
    let mask = u32::MAX.checked_shl(32 - len).unwrap_or(0);
    Net4 { addr, mask: mask.to_be_bytes() }
}

/// The IPv6 network of an address and a prefix length.
pub fn net6(addr: u128, len: u32) -> Net6 {
    let mask = u128::MAX.checked_shl(128 - len).unwrap_or(0);
    Net6 {
        addr: addr.to_be_bytes(),
        mask: mask.to_be_bytes(),
    }
}

/// A rule of the tests: its networks, of one family, its protocols and its
/// destination ports, an empty set matching every value.
#[derive(Clone)]
pub struct TestRule<N> {
    pub src: Vec<N>,
    pub dst: Vec<N>,
    pub protos: Vec<LineRange>,
    pub ports: Vec<PortRange>,
}

/// Whether a set matches a value: an empty one matches every value.
pub fn any_or<T>(set: &[T], matches: impl Fn(&T) -> bool) -> bool {
    set.is_empty() || set.iter().any(matches)
}

pub fn in4(net: &Net4, addr: u32) -> bool {
    let mask = u32::from_be_bytes(net.mask);
    addr & mask == u32::from_be_bytes(net.addr) & mask
}

pub fn in6(net: &Net6, addr: u128) -> bool {
    let mask = u128::from_be_bytes(net.mask);
    addr & mask == u128::from_be_bytes(net.addr) & mask
}

/// The index of the first rule that matches the protocol and the port, of
/// those the network test admits.
pub fn first<N>(rules: &[TestRule<N>], nets: impl Fn(&TestRule<N>) -> bool, proto: u8, port: u16) -> Option<usize> {
    rules.iter().position(|rule| {
        nets(rule)
            && any_or(&rule.protos, |range| {
                (range.from..=range.to).contains(&u32::from(proto))
            })
            && any_or(&rule.ports, |range| (range.from..=range.to).contains(&port))
    })
}

/// Builds the IPv4 tree of the rules.
pub fn build4(owner: &ArenaOwner, rules: &[TestRule<Net4>]) -> Built<Tree<Tree4>, ArenaOwner> {
    let views = rules
        .iter()
        .map(|rule| {
            (
                Nets4::new(&rule.src).unwrap(),
                Nets4::new(&rule.dst).unwrap(),
                Lines::new(&rule.protos).unwrap(),
                Ports::new(&rule.ports).unwrap(),
            )
        })
        .collect::<Vec<_>>();
    let src = views.iter().map(|view| Some(&view.0)).collect::<Vec<_>>();
    let dst = views.iter().map(|view| Some(&view.1)).collect::<Vec<_>>();
    let protos = views.iter().map(|view| Some(&view.2)).collect::<Vec<_>>();
    let ports = views.iter().map(|view| Some(&view.3)).collect::<Vec<_>>();
    let projection = views.iter().map(|_| Some(&())).collect::<Vec<_>>();
    Builder::build(owner, "v4", |scope| -> Result<_, Error> {
        let src = scope.compile(c"src", &src)?;
        let dst = scope.compile(c"dst", &dst)?;
        let proto = scope.compile(c"proto", &protos)?;
        let port = scope.compile(c"port", &ports)?;
        let nets = scope.join(c"nets", src, dst)?;
        let transport = scope.join(c"transport", proto, port)?;
        scope.join_rules(c"rules", nets, transport, &projection)
    })
    .unwrap()
}

/// Builds the IPv6 tree of the rules.
pub fn build6(owner: &ArenaOwner, rules: &[TestRule<Net6>]) -> Built<Tree<Tree6>, ArenaOwner> {
    let views = rules
        .iter()
        .map(|rule| {
            (
                Nets6::new(&rule.src).unwrap(),
                Nets6::new(&rule.dst).unwrap(),
                Lines::new(&rule.protos).unwrap(),
                Ports::new(&rule.ports).unwrap(),
            )
        })
        .collect::<Vec<_>>();
    let src = views.iter().map(|view| Some(&view.0)).collect::<Vec<_>>();
    let dst = views.iter().map(|view| Some(&view.1)).collect::<Vec<_>>();
    let protos = views.iter().map(|view| Some(&view.2)).collect::<Vec<_>>();
    let ports = views.iter().map(|view| Some(&view.3)).collect::<Vec<_>>();
    let projection = views.iter().map(|_| Some(&())).collect::<Vec<_>>();
    Builder::build(owner, "v6", |scope| -> Result<_, Error> {
        let src = scope.compile(c"src", &src)?;
        let dst = scope.compile(c"dst", &dst)?;
        let proto = scope.compile(c"proto", &protos)?;
        let port = scope.compile(c"port", &ports)?;
        let nets = scope.join(c"nets", src, dst)?;
        let transport = scope.join(c"transport", proto, port)?;
        scope.join_rules(c"rules", nets, transport, &projection)
    })
    .unwrap()
}

/// The protocols and the ports every tree is looked up at: the edges of
/// every range of the rules, and the ends of the domains.
pub fn edges<N>(rules: &[TestRule<N>]) -> (Vec<u8>, Vec<u16>) {
    let mut protos = vec![0, u8::MAX];
    let mut ports = vec![0, u16::MAX];
    for rule in rules {
        for range in &rule.protos {
            let around = [
                range.from.checked_sub(1),
                Some(range.from),
                Some(range.to),
                range.to.checked_add(1),
            ];
            protos.extend(
                around
                    .into_iter()
                    .flatten()
                    .filter_map(|proto| u8::try_from(proto).ok()),
            );
        }
        for range in &rule.ports {
            ports.extend([range.from, range.to]);
            ports.extend(range.from.checked_sub(1));
            ports.extend(range.to.checked_add(1));
        }
    }
    protos.sort_unstable();
    protos.dedup();
    ports.sort_unstable();
    ports.dedup();
    (protos, ports)
}
