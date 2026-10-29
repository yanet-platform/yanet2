//! Query-local class spaces and packet-associated ordered results.

use core::marker::PhantomData;

use super::{At, ByteLine, Decode, Device, Graph, IpFrag, Joint, Net4, Net6, Port, RuleTable, Shape, Spec};
use crate::{
    bindings,
    dp::{Batch, Cx, Module, Packet, PacketView, Verdict},
};

/// One aligned network-order IPv4 word, as the native lookup consumes it.
#[derive(Clone, Copy)]
#[repr(transparent)]
pub struct Ipv4Key(u32);
impl Ipv4Key {
    pub const ZERO: Self = Self(0);
    pub const fn new(octets: [u8; 4]) -> Self {
        Self(u32::from_ne_bytes(octets))
    }
}

type Invariant<'fresh> = PhantomData<fn(&'fresh ()) -> &'fresh ()>;

/// Immutable classes belonging to one query invocation and exact graph node.
#[derive(Clone, Copy)]
pub struct Classes<'fresh, const NODE: usize, const CAP: usize> {
    values: [u32; CAP],
    fresh: Invariant<'fresh>,
}

/// A query-local device class, constant for this module invocation.
#[derive(Clone, Copy)]
pub struct DeviceClass<'fresh, const NODE: usize> {
    value: u32,
    fresh: Invariant<'fresh>,
}

/// Resolved first-rule indices, valid while their graph remains borrowed.
pub struct Rules<'graph, 'fresh, const CAP: usize> {
    values: [u32; CAP],
    graph: PhantomData<&'graph ()>,
    fresh: Invariant<'fresh>,
}

/// A fresh query over the original input packet prefix, bounded by capacity.
pub struct Query<'graph, 'fresh, 'id, S: Shape, const CAP: usize> {
    graph: &'graph Graph<S>,
    batch: &'fresh Batch<'id>,
    count: u32,
    device: u32,
    fresh: Invariant<'fresh>,
}

#[doc(hidden)]
pub struct Node<'query, 'graph, 'fresh, 'id, S: Shape, const INDEX: usize, const CAP: usize, N: Spec> {
    query: &'query Query<'graph, 'fresh, 'id, S, CAP>,
    node: PhantomData<N>,
}

impl<S: Shape> Graph<S> {
    pub fn with_query<'graph, 'batch, 'id, const CAP: usize, M: Module>(
        &'graph self,
        cx: &Cx<'id, M>,
        batch: &'batch mut Batch<'id>,
        classify: impl for<'fresh> FnOnce(Query<'graph, 'fresh, 'id, S, CAP>) -> Rules<'graph, 'fresh, CAP>,
    ) -> Option<Matches<'graph, 'batch, 'id, CAP>> {
        const {
            let () = S::VALID;
            assert!(CAP > 0 && CAP <= u32::MAX as usize);
        }
        let count = batch.peek().take(CAP).count();
        if count == 0 {
            return None;
        }
        let values = classify(Query {
            graph: self,
            batch,
            count: count as u32,
            device: cx.classify_device(),
            fresh: PhantomData,
        })
        .values;
        Some(Matches {
            batch,
            values,
            count,
            graph: PhantomData,
        })
    }
}

impl<'graph, 'fresh, 'id, S: Shape, const CAP: usize> Query<'graph, 'fresh, 'id, S, CAP> {
    pub fn node<const INDEX: usize>(&self) -> Node<'_, 'graph, 'fresh, 'id, S, INDEX, CAP, <S as At<INDEX>>::Node>
    where
        S: At<INDEX>,
    {
        Node { query: self, node: PhantomData }
    }
}

impl<'graph, 'fresh, 'id, S: Shape, const INDEX: usize, const CAP: usize>
    Node<'_, 'graph, 'fresh, 'id, S, INDEX, CAP, Net4>
{
    pub fn net4(self, mut key: impl FnMut(PacketView<'_>) -> Ipv4Key) -> Classes<'fresh, INDEX, CAP> {
        let mut keys = [Ipv4Key::ZERO; CAP];
        for (index, packet) in self.query.batch.peek().take(self.query.count as usize).enumerate() {
            keys[index] = key(packet);
        }
        drop(key);
        let mut values = [0; CAP];
        // SAFETY: node selection fixes the native attribute type; every key is
        // aligned and the fresh query bounds both arrays by the same count.
        unsafe {
            bindings::yanet_sys_classify_net4_lookup(
                self.query.graph.slots().add(INDEX).cast(),
                keys.as_ptr().cast(),
                values.as_mut_ptr(),
                self.query.count,
            )
        };
        Classes { values, fresh: PhantomData }
    }
}

impl<'graph, 'fresh, 'id, S: Shape, const INDEX: usize, const CAP: usize, const LEFT: usize, const RIGHT: usize>
    Node<'_, 'graph, 'fresh, 'id, S, INDEX, CAP, Joint<LEFT, RIGHT>>
{
    pub fn join(
        self,
        left: Classes<'fresh, LEFT, CAP>,
        right: Classes<'fresh, RIGHT, CAP>,
    ) -> Classes<'fresh, INDEX, CAP> {
        let mut values = [0; CAP];
        // SAFETY: both class domains belong to this joint and packet batch.
        //
        // Invariant query brands, exact endpoint indices and capacity enforce
        // that relation before the native lookup.
        unsafe {
            bindings::yanet_sys_classify_joint_lookup(
                self.query.graph.slots().add(INDEX).cast(),
                left.values.as_ptr(),
                right.values.as_ptr(),
                values.as_mut_ptr(),
                self.query.count,
            )
        };
        Classes { values, fresh: PhantomData }
    }
}

impl<'graph, 'fresh, 'id, S: Shape, const INDEX: usize, const CAP: usize, const SOURCE: usize>
    Node<'_, 'graph, 'fresh, 'id, S, INDEX, CAP, Decode<SOURCE>>
{
    pub fn decode(self, classes: Classes<'fresh, SOURCE, CAP>) -> Rules<'graph, 'fresh, CAP> {
        let mut values = [u32::MAX; CAP];
        // SAFETY: only classes from this decoder's exact source in the same
        // query can reach the native line; output words are final rule indices.
        unsafe {
            bindings::yanet_sys_classify_decode_lookup(
                self.query.graph.slots().add(INDEX).cast(),
                classes.values.as_ptr(),
                values.as_mut_ptr(),
                self.query.count,
            )
        };
        Rules {
            values,
            graph: PhantomData,
            fresh: PhantomData,
        }
    }
}

/// Classified packets remain attached to the batch prefix that supplied them.
#[must_use]
pub struct Matches<'graph, 'batch, 'id, const CAP: usize> {
    batch: &'batch mut Batch<'id>,
    values: [u32; CAP],
    graph: PhantomData<&'graph ()>,
    count: usize,
}

impl<'id, const CAP: usize> Matches<'_, '_, 'id, CAP> {
    pub fn dispatch(self, mut action: impl FnMut(&mut Packet<'id>, Option<usize>) -> Verdict<'id>) {
        for index in 0..self.count {
            let Some(mut packet) = self.batch.next() else {
                unreachable!("the borrowed batch prefix changed")
            };
            let rule = self.values[index];
            let verdict = action(&mut packet, (rule != u32::MAX).then_some(rule as usize));
            self.batch.dispose(packet, verdict);
        }
    }
}

macro_rules! lookup {
    ($spec:ty,$method:ident,$key:ty,$zero:expr,$native:ident,$convert:expr) => {
        impl<'graph, 'fresh, 'id, S: Shape, const INDEX: usize, const CAP: usize>
            Node<'_, 'graph, 'fresh, 'id, S, INDEX, CAP, $spec>
        {
            pub fn $method(self, mut key: impl FnMut(PacketView<'_>) -> $key) -> Classes<'fresh, INDEX, CAP> {
                let mut keys = [$zero; CAP];
                for (index, packet) in self
                    .query
                    .batch
                    .peek()
                    .take(self.query.count as usize)
                    .enumerate()
                {
                    keys[index] = key(packet);
                }
                drop(key);
                let mut values = [0; CAP];
                // SAFETY: static node type and constrained key domain match the
                // native lookup, with every array bounded by this fresh query.
                unsafe {
                    bindings::$native(
                        self.query.graph.slots().add(INDEX).cast(),
                        keys.as_ptr().cast(),
                        values.as_mut_ptr(),
                        self.query.count,
                    )
                };
                Classes { values, fresh: PhantomData }
            }
        }
    };
}
lookup!(
    Net6,
    net6,
    [u8; 16],
    [0; 16],
    yanet_sys_classify_net6_lookup,
    |k: &[[u8; 16]; CAP]| k.as_ptr()
);
lookup!(
    ByteLine,
    byte,
    u8,
    0,
    yanet_sys_classify_byte_lookup,
    |k: &[u8; CAP]| k.as_ptr()
);
lookup!(Port, port, u16, 0, yanet_sys_classify_port_lookup, |k: &[u16; CAP]| k
    .as_ptr());
// bool has exactly the native two-region fragment domain.
impl<'graph, 'fresh, 'id, S: Shape, const INDEX: usize, const CAP: usize>
    Node<'_, 'graph, 'fresh, 'id, S, INDEX, CAP, IpFrag>
{
    pub fn fragment(self, mut key: impl FnMut(PacketView<'_>) -> bool) -> Classes<'fresh, INDEX, CAP> {
        let mut keys = [0u8; CAP];
        for (i, p) in self.query.batch.peek().take(self.query.count as usize).enumerate() {
            keys[i] = u8::from(key(p));
        }
        drop(key);
        let mut values = [0; CAP];
        // SAFETY: booleans constrain every key to the native two-cell domain.
        unsafe {
            bindings::yanet_sys_classify_fragment_lookup(
                self.query.graph.slots().add(INDEX).cast(),
                keys.as_ptr(),
                values.as_mut_ptr(),
                self.query.count,
            )
        };
        Classes { values, fresh: PhantomData }
    }
}
impl<'graph, 'fresh, 'id, S: Shape, const INDEX: usize, const CAP: usize>
    Node<'_, 'graph, 'fresh, 'id, S, INDEX, CAP, Device>
{
    pub fn device(self) -> DeviceClass<'fresh, INDEX> {
        // SAFETY: the key comes from this invocation's module namespace and the
        // native seam clamps a key outside this attribute's line to default0.
        let value = unsafe {
            bindings::yanet_sys_classify_device_lookup(self.query.graph.slots().add(INDEX).cast(), self.query.device)
        };
        DeviceClass { value, fresh: PhantomData }
    }
}
impl<'graph, 'fresh, 'id, S: Shape, const INDEX: usize, const CAP: usize, const L: usize, const R: usize>
    Node<'_, 'graph, 'fresh, 'id, S, INDEX, CAP, Joint<L, R>>
{
    pub fn join_device(
        self,
        left: DeviceClass<'fresh, L>,
        right: Classes<'fresh, R, CAP>,
    ) -> Classes<'fresh, INDEX, CAP> {
        let mut values = [0; CAP];
        // SAFETY: invariant query and exact endpoint brands match this joint.
        unsafe {
            bindings::yanet_sys_classify_joint_const_lookup(
                self.query.graph.slots().add(INDEX).cast(),
                left.value,
                right.values.as_ptr(),
                values.as_mut_ptr(),
                self.query.count,
            )
        };
        Classes { values, fresh: PhantomData }
    }
}
impl<'graph, 'fresh, 'id, S: Shape, const INDEX: usize, const CAP: usize, const L: usize, const R: usize>
    Node<'_, 'graph, 'fresh, 'id, S, INDEX, CAP, RuleTable<L, R>>
{
    pub fn rules(self, left: Classes<'fresh, L, CAP>, right: Classes<'fresh, R, CAP>) -> Rules<'graph, 'fresh, CAP> {
        let mut values = [u32::MAX; CAP];
        // SAFETY: endpoint classes have the exact node domains retained by this
        // first-rule table, from the same immutable packet query.
        unsafe {
            bindings::yanet_sys_classify_rule_table_lookup(
                self.query.graph.slots().add(INDEX).cast(),
                left.values.as_ptr(),
                right.values.as_ptr(),
                values.as_mut_ptr(),
                self.query.count,
            )
        };
        Rules {
            values,
            graph: PhantomData,
            fresh: PhantomData,
        }
    }
}
impl<'graph, 'fresh, const CAP: usize> Rules<'graph, 'fresh, CAP> {
    /// Chooses the earliest original rule in either projection.
    pub fn first(mut self, other: Self) -> Self {
        for i in 0..CAP {
            self.values[i] = self.values[i].min(other.values[i]);
        }
        self
    }
}
impl<'graph, 'fresh, 'id, S: Shape, const CAP: usize> Query<'graph, 'fresh, 'id, S, CAP> {
    /// Masks a projection using the same original packet prefix.
    pub fn select(
        &self,
        mut rules: Rules<'graph, 'fresh, CAP>,
        mut include: impl FnMut(PacketView<'_>) -> bool,
    ) -> Rules<'graph, 'fresh, CAP> {
        for (i, packet) in self.batch.peek().take(self.count as usize).enumerate() {
            if !include(packet) {
                rules.values[i] = u32::MAX;
            }
        }
        rules
    }
}
impl<'graph, 'fresh, 'id, S: Shape, const INDEX: usize, const CAP: usize, const SOURCE: usize>
    Node<'_, 'graph, 'fresh, 'id, S, INDEX, CAP, Decode<SOURCE>>
{
    pub fn decode_device(self, classes: DeviceClass<'fresh, SOURCE>) -> Rules<'graph, 'fresh, CAP> {
        let mut value = u32::MAX;
        // SAFETY: a device class has the exact source brand of this decoder.
        unsafe {
            bindings::yanet_sys_classify_decode_lookup(
                self.query.graph.slots().add(INDEX).cast(),
                &classes.value,
                &mut value,
                1,
            )
        };
        Rules {
            values: [value; CAP],
            graph: PhantomData,
            fresh: PhantomData,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::{PhantomData, Rules};
    #[test]
    fn test_first_original_rule_preserves_invalid_and_priority() {
        let left = Rules {
            values: [u32::MAX, 8, 3, u32::MAX],
            graph: PhantomData,
            fresh: PhantomData,
        };
        let right = Rules {
            values: [5, 2, 7, u32::MAX],
            graph: PhantomData,
            fresh: PhantomData,
        };
        assert_eq!([5, 2, 3, u32::MAX], left.first(right).values);
    }
}

#[cfg(doctest)]
mod contracts {
    /// Query-local node identities reject reversed class domains.
    ///
    /// ```compile_fail,E0308
    /// use yanet_sys::{classify::{Graph,Net4,Joint,Decode,Ipv4Key},dp::{Batch,Cx,Module}};
    /// fn swapped<'id,M:Module>(graph:&Graph<(Net4,Net4,Joint<0,1>,Decode<2>)>,cx:&Cx<'id,M>,batch:&mut Batch<'id>){
    ///     graph.with_query::<4,_>(cx,batch,|query|{
    ///         let left=query.node::<0>().net4(|_|Ipv4Key::ZERO);
    ///         let right=query.node::<1>().net4(|_|Ipv4Key::ZERO);
    ///         query.node::<3>().decode(query.node::<2>().join(right,left))
    ///     });
    /// }
    /// ```
    ///
    /// Matching endpoints are accepted.
    ///
    /// ```
    /// use yanet_sys::{
    ///     classify::{Decode, Graph, Ipv4Key, Joint, Net4},
    ///     dp::{Batch, Cx, Module},
    /// };
    /// fn ordered<'id, M: Module>(
    ///     graph: &Graph<(Net4, Net4, Joint<0, 1>, Decode<2>)>,
    ///     cx: &Cx<'id, M>,
    ///     batch: &mut Batch<'id>,
    /// ) {
    ///     graph.with_query::<4, _>(cx, batch, |query| {
    ///         let left = query.node::<0>().net4(|_| Ipv4Key::ZERO);
    ///         let right = query.node::<1>().net4(|_| Ipv4Key::ZERO);
    ///         query
    ///             .node::<3>()
    ///             .decode(query.node::<2>().join(left, right))
    ///     });
    /// }
    /// ```
    struct EndpointIdentity;

    /// Fresh query brands reject a class from another graph invocation.
    ///
    /// ```compile_fail
    /// use yanet_sys::{classify::{Graph,Net4,Joint,Decode,Ipv4Key},dp::{Batch,Cx,Module}};
    /// type Shape=(Net4,Net4,Joint<0,1>,Decode<2>);
    /// fn crossed<'id,M:Module>(a:&Graph<Shape>,b:&Graph<Shape>,cx:&Cx<'id,M>,packets:&mut Batch<'id>,other:&mut Batch<'id>){
    ///     a.with_query::<4,_>(cx,packets,|outer|{
    ///         let left=outer.node::<0>().net4(|_|Ipv4Key::ZERO);
    ///         b.with_query::<4,_>(cx,other,|inner|{
    ///             let right=inner.node::<1>().net4(|_|Ipv4Key::ZERO);
    ///             inner.node::<3>().decode(inner.node::<2>().join(left,right))
    ///         });
    ///         outer.node::<3>().decode(outer.node::<2>().join(left,outer.node::<1>().net4(|_|Ipv4Key::ZERO)))
    ///     });
    /// }
    /// ```
    ///
    /// The inner query accepts its own class of the same static node.
    ///
    /// ```
    /// use yanet_sys::{
    ///     classify::{Decode, Graph, Ipv4Key, Joint, Net4},
    ///     dp::{Batch, Cx, Module},
    /// };
    /// type Shape = (Net4, Net4, Joint<0, 1>, Decode<2>);
    /// fn separate<'id, M: Module>(
    ///     a: &Graph<Shape>,
    ///     b: &Graph<Shape>,
    ///     cx: &Cx<'id, M>,
    ///     packets: &mut Batch<'id>,
    ///     other: &mut Batch<'id>,
    /// ) {
    ///     a.with_query::<4, _>(cx, packets, |outer| {
    ///         b.with_query::<4, _>(cx, other, |inner| {
    ///             let left = inner.node::<0>().net4(|_| Ipv4Key::ZERO);
    ///             let right = inner.node::<1>().net4(|_| Ipv4Key::ZERO);
    ///             inner
    ///                 .node::<3>()
    ///                 .decode(inner.node::<2>().join(left, right))
    ///         });
    ///         outer.node::<3>().decode(outer.node::<2>().join(
    ///             outer.node::<0>().net4(|_| Ipv4Key::ZERO),
    ///             outer.node::<1>().net4(|_| Ipv4Key::ZERO),
    ///         ))
    ///     });
    /// }
    /// ```
    struct InvocationIdentity;

    /// Native class words cannot be forged through the safe query API.
    ///
    /// ```compile_fail,E0451
    /// use core::marker::PhantomData;
    /// use yanet_sys::classify::Classes;
    /// let forged=Classes::<'static,0,4>{values:[u32::MAX;4],fresh:PhantomData};
    /// ```
    struct PrivateClassWords;
}
