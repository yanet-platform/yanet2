//! Closed, statically composed native classifier graphs.

pub mod init;
mod query;

use core::{
    marker::PhantomData,
    mem::{align_of, size_of},
};

pub use query::{Classes, DeviceClass, Ipv4Key, Matches, Query, Rules};
use yanet_shm::shm::ShmLayout;

use crate::{
    bindings,
    cp::{MemoryContext, Owner, Stored},
};

pub mod records;
#[derive(Clone, Copy)]
pub enum Fragment {
    Any,
    Whole,
    Later,
}
pub struct Net6;
pub struct ByteLine;
pub struct Port;
pub struct Device;
pub struct IpFrag;
pub struct RuleTable<const LEFT: usize, const RIGHT: usize>;

pub struct Net4;
pub struct Joint<const LEFT: usize, const RIGHT: usize>;
pub struct Decode<const SOURCE: usize>;

#[doc(hidden)]
#[derive(Clone, Copy)]
pub enum Kind {
    Net4,
    Net6,
    Byte,
    Port,
    Device,
    Fragment,
    Joint(usize, usize),
    RuleTable(usize, usize),
    Decode(usize),
}

mod sealed {
    pub trait Spec {}
    pub trait Shape {}
    pub trait Init<S, E> {}
}

#[doc(hidden)]
pub trait Spec: sealed::Spec {
    const KIND: Kind;
}
macro_rules! leaf { ($($ty:ident:$kind:ident),+) => { $(impl sealed::Spec for $ty {} impl Spec for $ty { const KIND: Kind = Kind::$kind; })+ }; }
leaf!(Net6:Net6,ByteLine:Byte,Port:Port,Device:Device,IpFrag:Fragment);
impl<const L: usize, const R: usize> sealed::Spec for RuleTable<L, R> {}
impl<const L: usize, const R: usize> Spec for RuleTable<L, R> {
    const KIND: Kind = Kind::RuleTable(L, R);
}
impl sealed::Spec for Net4 {}
impl Spec for Net4 {
    const KIND: Kind = Kind::Net4;
}
impl<const L: usize, const R: usize> sealed::Spec for Joint<L, R> {}
impl<const L: usize, const R: usize> Spec for Joint<L, R> {
    const KIND: Kind = Kind::Joint(L, R);
}
impl<const S: usize> sealed::Spec for Decode<S> {}
impl<const S: usize> Spec for Decode<S> {
    const KIND: Kind = Kind::Decode(S);
}

#[doc(hidden)]
pub trait Shape: sealed::Shape + 'static {
    type Storage: ShmLayout + Sync;
    const KINDS: &'static [Kind];
    const VALID: () = {
        let mut index = 0;
        while index < Self::KINDS.len() {
            match Self::KINDS[index] {
                Kind::Net4 | Kind::Net6 | Kind::Byte | Kind::Port | Kind::Device | Kind::Fragment => {}
                Kind::Joint(left, right) | Kind::RuleTable(left, right) => {
                    assert!(left < index && right < index, "joints require preceding nodes");
                    assert!(
                        !matches!(Self::KINDS[left], Kind::Decode(_) | Kind::RuleTable(..)),
                        "a decoder has no class space"
                    );
                    assert!(
                        !matches!(Self::KINDS[right], Kind::Decode(_) | Kind::RuleTable(..)),
                        "a decoder has no class space"
                    );
                }
                Kind::Decode(source) => {
                    assert!(source < index, "decoders require a preceding node");
                    assert!(
                        !matches!(Self::KINDS[source], Kind::Decode(_) | Kind::RuleTable(..)),
                        "a decoder has no class space"
                    );
                }
            }
            index += 1;
        }
    };
}

#[doc(hidden)]
pub trait At<const INDEX: usize>: Shape {
    type Node: Spec;
}

const SLOT_BYTES: usize = {
    let sizes = [
        size_of::<bindings::classify_attr_net4>(),
        size_of::<bindings::classify_attr_net6>(),
        size_of::<bindings::value_table>(),
        size_of::<bindings::rule_table>(),
        size_of::<bindings::vline>(),
    ];
    let mut largest = 0;
    let mut index = 0;
    while index < sizes.len() {
        if sizes[index] > largest {
            largest = sizes[index];
        }
        index += 1;
    }
    largest
};

/// Fixed-address private storage; tuples never persist in shared memory.
#[doc(hidden)]
#[repr(C)]
pub struct Slot {
    words: [u64; SLOT_BYTES.div_ceil(8)],
}
// SAFETY: all bytes belong to integer words; active native fields are accessed
// only after the trusted encoder completes them in place.
unsafe impl ShmLayout for Slot {}

macro_rules! count { ($($item:ident),*) => { <[()]>::len(&[$(count!(@one $item)),*]) }; (@one $item:ident) => { () }; }
macro_rules! at {
    (($($index:tt:$ty:ident),+); $selected:tt:$node:ident) => {
        impl<$($ty: Spec + 'static),+> At<$selected> for ($($ty,)+) { type Node = $node; }
    };
}
macro_rules! shape {
    ($($index:tt:$ty:ident),+) => {
        impl<$($ty: Spec + 'static),+> sealed::Shape for ($($ty,)+) {}
        impl<$($ty: Spec + 'static),+> Shape for ($($ty,)+) {
            type Storage = [Slot; count!($($ty),+)];
            const KINDS: &'static [Kind] = &[$($ty::KIND),+];
        }
        shape!(@each ($($index:$ty),+); $($index:$ty),+);
    };
    (@each $all:tt; $head:tt:$node:ident $(,$tail:tt:$rest:ident)*) => {
        at!($all; $head:$node);
        $(shape!(@each $all; $tail:$rest);)*
    };
}
macro_rules! shapes {
    (($($done:tt:$old:ident),*); $index:tt:$ty:ident $(,$tail:tt:$rest:ident)*) => {
        shape!($($done:$old,)*$index:$ty);
        shapes!(($($done:$old,)*$index:$ty); $($tail:$rest),*);
    };
    (($($done:tt:$old:ident),*);) => {};
}
shapes!(() ; 0:A,1:B,2:C,3:D,4:E,5:F,6:G,7:H,8:I,9:J,10:K,11:L,12:M,13:N,14:O,15:P,16:Q,17:R,18:S,19:T,20:U,21:V,22:W,23:X,24:Y,25:Z,26:AA,27:AB,28:AC,29:AD,30:AE,31:AF,32:AG,33:AH,34:AI,35:AJ,36:AK,37:AL,38:AM,39:AN,40:AO,41:AP,42:AQ,43:AR,44:AS,45:AT,46:AU,47:AV,48:AW,49:AX,50:AY,51:AZ,52:BA,53:BB,54:BC,55:BD,56:BE,57:BF,58:BG,59:BH,60:BI,61:BJ,62:BK,63:BL);

/// An immutable native DAG with statically checked class-space edges.
#[repr(C)]
pub struct Graph<S: Shape> {
    storage: S::Storage,
    shape: PhantomData<fn() -> S>,
}

impl<S: Shape> Graph<S> {
    fn slots(&self) -> *const Slot {
        core::ptr::addr_of!(self.storage).cast()
    }
}

// SAFETY: sealed shapes store only a fixed C-layout array of padded word slots.
unsafe impl<S: Shape> ShmLayout for Graph<S> {}
// SAFETY: construction completes every node at its final address; release walks
// the same static shape backwards without copying relative-pointer fields.
unsafe impl<S: Shape> Stored for Graph<S> {
    unsafe fn free<W: Owner>(address: usize, memory: &MemoryContext<'_, W>) {
        // SAFETY: the complete graph and original context are supplied by its
        // owner.
        unsafe { release_nodes::<S>(memory.root().ptr(address), S::KINDS.len(), memory.native_context()) }
    }
}

unsafe fn release_nodes<S: Shape>(slots: *mut Slot, count: usize, context: *mut bindings::memory_context) {
    for index in (0..count).rev() {
        // SAFETY: each visited slot is initialized or zeroed after native
        // failure.
        unsafe {
            let slot = slots.add(index);
            match S::KINDS[index] {
                Kind::Net4 => bindings::yanet_sys_classify_net4_free(context, slot.cast()),
                Kind::Net6 => bindings::yanet_sys_classify_net6_free(context, slot.cast()),
                Kind::Byte => bindings::yanet_sys_classify_byte_free(context, slot.cast()),
                Kind::Port => bindings::yanet_sys_classify_port_free(context, slot.cast()),
                Kind::Device => bindings::yanet_sys_classify_device_free(context, slot.cast()),
                Kind::Fragment => bindings::yanet_sys_classify_fragment_free(context, slot.cast()),
                Kind::RuleTable(..) => bindings::yanet_sys_classify_rule_table_free(slot.cast()),
                Kind::Joint(..) => bindings::yanet_sys_classify_joint_free(slot.cast()),
                Kind::Decode(_) => bindings::yanet_sys_classify_decode_free(slot.cast()),
            }
        }
    }
}

const _: () = {
    assert!(align_of::<bindings::classify_attr_net6>() <= align_of::<Slot>());
    assert!(align_of::<bindings::rule_table>() <= align_of::<Slot>());
    assert!(align_of::<bindings::classify_attr_net4>() <= align_of::<Slot>());
    assert!(align_of::<bindings::value_table>() <= align_of::<Slot>());
    assert!(align_of::<bindings::vline>() <= align_of::<Slot>());
};

// Native partition collection interprets masks as prefixes; validate that
// assumption before constructing the typed lookup graph.
fn prefix(mask: &[u8]) -> bool {
    let mut zero = false;
    for byte in mask {
        for bit in (0..8).rev() {
            if byte & (1 << bit) == 0 {
                zero = true;
            } else if zero {
                return false;
            }
        }
    }
    true
}
fn valid_net4(network: &records::Net4) -> bool {
    prefix(&network.mask)
}
fn valid_net6(network: &records::Net6) -> bool {
    prefix(&network.mask[..8]) && prefix(&network.mask[8..])
}
#[cfg(test)]
mod tests {
    use super::{
        records::{Net4, Net6},
        valid_net4, valid_net6,
    };
    #[test]
    fn test_network4_masks_reject_noncontiguous_input() {
        assert!(valid_net4(&Net4 {
            addr: [10, 0, 0, 1],
            mask: [255, 255, 255, 0]
        }));
        assert!(valid_net4(&Net4 { addr: [0; 4], mask: [0; 4] }));
        assert!(!valid_net4(&Net4 { addr: [0; 4], mask: [0x55; 4] }));
    }
    #[test]
    fn test_network6_half_prefixes_accept_bicontiguous_masks() {
        let mut mask = [0; 16];
        mask[8] = 255;
        mask[9] = 255;
        assert!(valid_net6(&Net6 { addr: [0; 16], mask }));
        assert!(!valid_net6(&Net6 { addr: [0; 16], mask: [0x55; 16] }));
    }
}
#[cfg(doctest)]
mod topology {
    /// A static edge cannot refer forward into an uninitialized stage.
    ///
    /// ```compile_fail,E0080
    /// use yanet_sys::classify::{Joint,Net4,Shape};
    /// const _:()=<(Joint<1,1>,Net4) as Shape>::VALID;
    /// ```
    ///
    /// Preceding class stages form a valid static edge.
    ///
    /// ```
    /// use yanet_sys::classify::{Joint, Net4, Shape};
    /// const _: () = <(Net4, Joint<0, 0>) as Shape>::VALID;
    /// ```
    struct PrecedingEdges;
    /// A resolved rule domain cannot be reused as an unchecked class domain.
    ///
    /// ```compile_fail,E0080
    /// use yanet_sys::classify::{Decode,Joint,Net4,Shape};
    /// const _:()=<(Net4,Decode<0>,Joint<1,0>) as Shape>::VALID;
    /// ```
    ///
    /// A decoder of a preceding class stage is accepted.
    ///
    /// ```
    /// use yanet_sys::classify::{Decode, Net4, Shape};
    /// const _: () = <(Net4, Decode<0>) as Shape>::VALID;
    /// ```
    struct ClassDomains;
}
