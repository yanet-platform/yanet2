//! Bindings of `lib/classify`, the classification library of the C modules.
//!
//! A module composes its classifier as a C module does. The control plane
//! compiles a classifier of each attribute of the rules, joins the classes of
//! two stages into the classes of the next, and ends in a decoder or a rule
//! table that maps the last classes to the first rule they match; the
//! dataplane looks the classes of a batch up attribute by attribute and
//! through the same joints. Every classifier, joint and decoder is a C object
//! a build writes in a block of its own, as the field types of this module,
//! and every lookup is the C one, through the shim compiled into the plugin.
//! The lookups of the joints, the rule tables and the rule maps take their
//! classes from the caller, so they check each against the table it indexes.
//!
//! A module may compose the same stages as one tree instead ([`Join`],
//! [`Rules`], [`Decode`]): each node holds the classifiers whose classes it
//! reads, so a lookup takes a packet's fields, one key at a time, and reads
//! every table unchecked.
//!
//! The compiles are C the shim compiles in, but the joins and the decode call
//! the classification library's compiler (`lib/classify/compiler/helper.c`):
//! a program that builds with them links that library, as the fixture api
//! archive does in meson; a missing link shows only when the program links.
//!
//! A ruleset is a slice with one slot per rule, `None` for a rule outside the
//! projection a compile or a join covers, as C spells a projection with NULL
//! rules; an attribute reads its own view of each rule. The first match is
//! the rule of the lowest index, so the order of the slice is the precedence.

use core::{
    convert::Infallible,
    marker::PhantomData,
    mem::{align_of, size_of},
    net::Ipv4Addr,
    ops::Range,
    ptr, slice,
};

use netip::{Ipv4Network, Ipv6Network};
use yanet_shm::{
    opaque::Opaque,
    resolve::Resolve,
    shm::{MAX_ALIGN, ShmLayout},
};

pub use self::tree::{BURST, Decode, Join, Node, Rules, link};
pub use crate::bindings::{
    classify_line_range as LineRange, filter_port_range as PortRange, net4 as Net4, net6 as Net6,
};
use crate::{
    bindings,
    cp::{Build, FfiRecord, MemoryContext, Owner, Refusal},
};

mod tree;

// SAFETY: each is a `#[repr(C)]` bindgen struct of integers and byte arrays,
// without drop glue, so every bit pattern is a value.
unsafe impl FfiRecord for Net4 {}
// SAFETY: as above.
unsafe impl FfiRecord for Net6 {}
// SAFETY: as above.
unsafe impl FfiRecord for PortRange {}
// SAFETY: as above.
unsafe impl FfiRecord for LineRange {}

/// The first rule a packet matches, by its index in the ruleset, or none.
///
/// The word C writes, so a lookup fills a slice of them in place; no match is
/// C's invalid rule, which [`Rule::index`] reads as `None`.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
#[repr(transparent)]
pub struct Rule(u32);

impl Rule {
    /// No rule: what a lookup reports for a packet no rule matches.
    pub const NONE: Self = Self(bindings::YANET_SYS_CLASSIFY_NO_RULE);

    /// Index of the rule in the ruleset, `None` for no rule.
    #[inline(always)]
    pub fn index(self) -> Option<usize> {
        (self != Self::NONE).then_some(self.0 as usize)
    }
}

impl Default for Rule {
    fn default() -> Self {
        Self::NONE
    }
}

/// An IPv4 address as the IPv4 lookup takes it: its four bytes in network
/// order, held as one aligned word.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
#[repr(transparent)]
pub struct Addr4(u32);

impl From<[u8; 4]> for Addr4 {
    #[inline(always)]
    fn from(octets: [u8; 4]) -> Self {
        // The word's bytes in memory are the octets, in network order.
        Self(u32::from_ne_bytes(octets))
    }
}

impl From<Ipv4Addr> for Addr4 {
    #[inline(always)]
    fn from(addr: Ipv4Addr) -> Self {
        Self::from(addr.octets())
    }
}

/// The views of a rule a compile reads: each borrows the items of the rule's
/// set for the build, as the C view of a set does, and holds only items the C
/// compile reads as they say.
macro_rules! views {
    ($($(#[$doc:meta])* $name:ident: $raw:ident of $item:ty, $valid:ident or $refusal:ident;)*) => {$(
        $(#[$doc])*
        #[repr(transparent)]
        pub struct $name<'a> {
            raw: bindings::$raw,
            _items: PhantomData<&'a [$item]>,
        }

        impl<'a> $name<'a> {
            /// The view of the items.
            ///
            /// # Errors
            ///
            /// The set holds more items than C counts, `u32::MAX`, or an item
            /// the C compile would read as other than it says.
            pub fn new(items: &'a [$item]) -> Result<Self, Refusal> {
                let count = u32::try_from(items.len()).map_err(|_| Refusal::TooManyItems)?;
                if !items.iter().all($valid) {
                    return Err(Refusal::$refusal);
                }
                Ok(Self {
                    raw: bindings::$raw {
                        // The compile only reads through the pointer.
                        items: items.as_ptr() as _,
                        count,
                    },
                    _items: PhantomData,
                })
            }

            /// The items the view was made of.
            #[must_use]
            pub fn as_slice(&self) -> &'a [$item] {
                // SAFETY: the pointer and the count are those of a slice
                // borrowed for 'a.
                unsafe { slice::from_raw_parts(self.raw.items.cast::<$item>(), self.raw.count as usize) }
            }
        }
    )*};
}

views! {
    /// The IPv4 networks of one side of a rule, each of a contiguous mask;
    /// none matches every address.
    Nets4: filter_net4s of Net4, net4_valid or InvalidMask;
    /// The IPv6 networks of one side of a rule, each of a mask contiguous in
    /// each 64-bit half, as the IPv6 compile ranges the halves apart; none
    /// matches every address.
    Nets6: filter_net6s of Net6, net6_valid or InvalidMask;
    /// The port ranges of one side of a rule, closed; none matches every
    /// port.
    Ports: filter_port_ranges of PortRange, port_range_valid or InvalidRange;
    /// The closed byte ranges of a line attribute of a rule (the protocol
    /// numbers, the TCP flags, the ICMP types), as the control plane that
    /// authors the rules derives them; bounds beyond the byte domain are
    /// clamped to it, and none matches every byte.
    Lines: classify_line_ranges of LineRange, line_range_valid or InvalidRange;
}

fn net4_valid(net: &Net4) -> bool {
    Ipv4Network::from_bits(u32::from_be_bytes(net.addr), u32::from_be_bytes(net.mask)).is_contiguous()
}

fn net6_valid(net: &Net6) -> bool {
    Ipv6Network::from_bits(u128::from_be_bytes(net.addr), u128::from_be_bytes(net.mask)).is_bicontiguous()
}

fn port_range_valid(range: &PortRange) -> bool {
    range.from <= range.to
}

fn line_range_valid(range: &LineRange) -> bool {
    range.from <= range.to
}

/// A view of one attribute of a rule, which the classifier of the attribute
/// is compiled from by [`Scope::compile`](crate::cp::Scope::compile).
///
/// Sealed: the attributes are the views of this module, each that of a C
/// attribute.
///
/// ```compile_fail,E0277
/// # use yanet_sys::classify::{Attribute, Net4Attr};
/// struct Mine;
/// impl Attribute for Mine {
///     type Classifier = Net4Attr;
///     unsafe fn compile(
///         _: *mut yanet_sys::bindings::memory_context,
///         _: *const core::ffi::c_char,
///         _: *const *const Self,
///         _: u32,
///         _: *mut Net4Attr,
///         _: *mut yanet_sys::bindings::classifier,
///     ) -> core::ffi::c_int {
///         0
///     }
/// }
/// ```
pub trait Attribute: sealed::Sealed {
    /// The classifier of the attribute.
    type Classifier: Build;

    /// Compiles the classifier in place over the rules and fills the stage;
    /// zero on success.
    ///
    /// # Safety
    ///
    /// As the shim compiles (`classify_shim.h`) take their arguments: each
    /// rule null or a view of the attribute, borrowed for the call.
    #[doc(hidden)]
    unsafe fn compile(
        memory: *mut bindings::memory_context,
        name: *const core::ffi::c_char,
        rules: *const *const Self,
        count: u32,
        classifier: *mut Self::Classifier,
        stage: *mut bindings::classifier,
    ) -> core::ffi::c_int;
}

impl Attribute for Nets4<'_> {
    type Classifier = Net4Attr;

    unsafe fn compile(
        memory: *mut bindings::memory_context,
        name: *const core::ffi::c_char,
        rules: *const *const Self,
        count: u32,
        classifier: *mut Net4Attr,
        stage: *mut bindings::classifier,
    ) -> core::ffi::c_int {
        // SAFETY: the caller's contract; the view is its C view.
        unsafe {
            bindings::yanet_sys_classify_net4_compile(memory, name, rules.cast(), count, classifier.cast(), stage)
        }
    }
}

impl Attribute for Nets6<'_> {
    type Classifier = Net6Attr;

    unsafe fn compile(
        memory: *mut bindings::memory_context,
        name: *const core::ffi::c_char,
        rules: *const *const Self,
        count: u32,
        classifier: *mut Net6Attr,
        stage: *mut bindings::classifier,
    ) -> core::ffi::c_int {
        // SAFETY: as above.
        unsafe {
            bindings::yanet_sys_classify_net6_compile(memory, name, rules.cast(), count, classifier.cast(), stage)
        }
    }
}

impl Attribute for Ports<'_> {
    type Classifier = PortAttr;

    unsafe fn compile(
        memory: *mut bindings::memory_context,
        name: *const core::ffi::c_char,
        rules: *const *const Self,
        count: u32,
        classifier: *mut PortAttr,
        stage: *mut bindings::classifier,
    ) -> core::ffi::c_int {
        // SAFETY: as above.
        unsafe {
            bindings::yanet_sys_classify_port_compile(memory, name, rules.cast(), count, classifier.cast(), stage)
        }
    }
}

impl Attribute for Lines<'_> {
    type Classifier = LineAttr;

    unsafe fn compile(
        memory: *mut bindings::memory_context,
        name: *const core::ffi::c_char,
        rules: *const *const Self,
        count: u32,
        classifier: *mut LineAttr,
        stage: *mut bindings::classifier,
    ) -> core::ffi::c_int {
        // SAFETY: as above.
        unsafe {
            bindings::yanet_sys_classify_line_compile(memory, name, rules.cast(), count, classifier.cast(), stage)
        }
    }
}

mod sealed {
    pub trait Sealed {}

    impl Sealed for super::Nets4<'_> {}

    impl Sealed for super::Nets6<'_> {}

    impl Sealed for super::Ports<'_> {}

    impl Sealed for super::Lines<'_> {}
}

/// The shared-memory C objects of a classification, each opaque and freed by
/// its C free: a compiled one is written only by its compile, an empty one
/// also from the parts `()`, as the zeroed object C frees and reads as empty.
macro_rules! classifiers {
    ($($(#[$doc:meta])* $name:ident: $raw:ident, $free:ident, $kind:ident;)*) => {$(
        $(#[$doc])*
        #[repr(C)]
        pub struct $name {
            /// Written by the C build only; the lookups read it from C.
            _c: Opaque<bindings::$raw>,
        }

        const _: () = {
            assert!(size_of::<$name>() == size_of::<bindings::$raw>());
            assert!(align_of::<$name>() == align_of::<bindings::$raw>());
            assert!(align_of::<$name>() <= MAX_ALIGN);
        };

        // SAFETY: a `#[repr(C)]` opaque wrapper of the C struct, of its size
        // and alignment, the alignment within the shared-memory bound. Rust
        // never reads the bytes, so every pattern is valid, and C rewrites
        // none of them once the configuration is published. Dropping is a
        // no-op.
        unsafe impl ShmLayout for $name {}

        // SAFETY: the C object is the whole value; the compile, the join or
        // the decode that wrote it allocated only in the memory context, in
        // blocks and child contexts its C free returns and unlinks, and the
        // zeroed object owns nothing.
        unsafe impl Build for $name {
            type Parts<'b, W: Owner> = classifiers!(@parts $kind);

            unsafe fn write<W: Owner>(
                parts: Self::Parts<'_, W>,
                memory: &MemoryContext<'_, W>,
                addr: usize,
            ) -> Result<(), Refusal> {
                classifiers!(@write $kind, parts, memory, addr, $raw)
            }

            unsafe fn free<W: Owner>(addr: usize, memory: &MemoryContext<'_, W>) {
                // SAFETY: the object was written whole (caller contract).
                unsafe { bindings::$free(memory.root().ptr(addr)) };
            }
        }
    )*};
    (@parts compiled) => { Infallible };
    (@parts empty) => { () };
    (@write compiled, $parts:ident, $memory:ident, $addr:ident, $raw:ident) => {{
        let _ = ($memory, $addr);
        match $parts {}
    }};
    (@write empty, $parts:ident, $memory:ident, $addr:ident, $raw:ident) => {{
        let () = $parts;
        // SAFETY: the address is aligned and exclusive (caller contract).
        unsafe { $memory.root().ptr::<bindings::$raw>($addr).write(bindings::$raw::default()) };
        Ok(())
    }};
}

classifiers! {
    /// The classifier of an IPv4 address.
    ///
    /// Compiled only with its stage, by
    /// [`Scope::compile`](crate::cp::Scope::compile).
    Net4Attr: classify_attr_net4, yanet_sys_classify_net4_free, compiled;
    /// The classifier of an IPv6 address.
    ///
    /// Compiled only with its stage, as above.
    Net6Attr: classify_attr_net6, yanet_sys_classify_net6_free, compiled;
    /// The classifier of a port.
    ///
    /// Compiled only with its stage, as above.
    PortAttr: classify_attr_port, yanet_sys_classify_port_free, compiled;
    /// The classifier of a byte of a packet: its protocol number, its TCP
    /// flags or its ICMP type.
    ///
    /// Compiled only with its stage, as above.
    LineAttr: classify_attr_line, yanet_sys_classify_line_free, compiled;
    /// The joint of the classes of two stages, C's `struct value_table`.
    ///
    /// Joined only with its stage, by [`Scope::join`](crate::cp::Scope::join).
    Joint: value_table, yanet_sys_classify_joint_free, compiled;
    /// The first rule each pair of classes of two stages matches.
    ///
    /// Joined by [`Scope::join_rules`](crate::cp::Scope::join_rules); the
    /// parts `()` are a table of no rule.
    RuleTable: rule_table, yanet_sys_classify_rule_table_free, empty;
    /// The first rule each class of a stage matches.
    ///
    /// Decoded by [`Scope::decode`](crate::cp::Scope::decode); the parts `()`
    /// are a map of no rule.
    RuleMap: vline, yanet_sys_classify_rule_map_free, empty;
}

/// The C object behind a published classifier, with the root's provenance.
fn raw<'g, T, C>(cx: &impl Resolve<'g>, value: &'g T) -> *const C {
    cx.shm_root().ptr::<C>(ptr::from_ref(value).addr())
}

/// The longest run a lookup passes C: what its `uint32_t` count holds.
const RUN: usize = u32::MAX as usize;

/// The longest run of IPv6 addresses: C offsets an address by its index times
/// its sixteen bytes in `uint32_t`, which a longer run would wrap.
const RUN6: usize = RUN / 16;

// The last address of a run starts below 2^32 bytes.
const _: () = assert!((RUN6 - 1) * 16 <= u32::MAX as usize);

/// Calls `lookup` over the inputs in runs of at most `max`, with the range of
/// each run and its length; a batch a dataplane hands a module is one run.
#[inline(always)]
fn runs(len: usize, max: usize, mut lookup: impl FnMut(Range<usize>, u32)) {
    let mut start = 0;
    while start < len {
        let run = (len - start).min(max);
        // The run fits, as `max` is at most `u32::MAX`.
        lookup(start..start + run, run as u32);
        start += run;
    }
}

// SAFETY (every lookup below): the classifier belongs to the configuration
// published and frozen for 'g (`Resolve` contract) and was built whole by its
// C build; C only reads it and the inputs, and writes one output per input of
// the run, into an output run of the same length.

impl Net4Attr {
    /// The class of each address.
    ///
    /// # Panics
    ///
    /// The classes are fewer than the addresses.
    #[inline]
    pub fn lookup<'g>(&'g self, cx: &impl Resolve<'g>, addrs: &[Addr4], classes: &mut [u32]) {
        let classes = &mut classes[..addrs.len()];
        runs(addrs.len(), RUN, |run, count| {
            // SAFETY: see above.
            unsafe {
                bindings::yanet_sys_classify_net4_lookup(
                    raw(cx, self),
                    addrs[run.clone()].as_ptr().cast(),
                    classes[run].as_mut_ptr(),
                    count,
                );
            }
        });
    }
}

impl Net6Attr {
    /// The class of each address.
    ///
    /// # Panics
    ///
    /// The classes are fewer than the addresses.
    #[inline]
    pub fn lookup<'g>(&'g self, cx: &impl Resolve<'g>, addrs: &[[u8; 16]], classes: &mut [u32]) {
        let classes = &mut classes[..addrs.len()];
        runs(addrs.len(), RUN6, |run, count| {
            // SAFETY: see above; the addresses are consecutive bytes.
            unsafe {
                bindings::yanet_sys_classify_net6_lookup(
                    raw(cx, self),
                    addrs[run.clone()].as_ptr().cast(),
                    classes[run].as_mut_ptr(),
                    count,
                );
            }
        });
    }
}

impl PortAttr {
    /// The class of each port.
    ///
    /// # Panics
    ///
    /// The classes are fewer than the ports.
    #[inline]
    pub fn lookup<'g>(&'g self, cx: &impl Resolve<'g>, ports: &[u16], classes: &mut [u32]) {
        let classes = &mut classes[..ports.len()];
        runs(ports.len(), RUN, |run, count| {
            // SAFETY: see above; the line spans the whole port domain.
            unsafe {
                bindings::yanet_sys_classify_port_lookup(
                    raw(cx, self),
                    ports[run.clone()].as_ptr(),
                    classes[run].as_mut_ptr(),
                    count,
                );
            }
        });
    }
}

impl LineAttr {
    /// The class of each byte.
    ///
    /// # Panics
    ///
    /// The classes are fewer than the bytes.
    #[inline]
    pub fn lookup<'g>(&'g self, cx: &impl Resolve<'g>, bytes: &[u8], classes: &mut [u32]) {
        let classes = &mut classes[..bytes.len()];
        runs(bytes.len(), RUN, |run, count| {
            // SAFETY: see above; the line spans the whole byte domain.
            unsafe {
                bindings::yanet_sys_classify_line_lookup(
                    raw(cx, self),
                    bytes[run.clone()].as_ptr(),
                    classes[run].as_mut_ptr(),
                    count,
                );
            }
        });
    }
}

impl Joint {
    /// The class of each pair of classes of the two stages joined; a class
    /// outside its stage reads class zero.
    ///
    /// # Panics
    ///
    /// The right classes or the output are fewer than the left classes.
    #[inline]
    pub fn lookup<'g>(&'g self, cx: &impl Resolve<'g>, left: &[u32], right: &[u32], classes: &mut [u32]) {
        let (right, classes) = (&right[..left.len()], &mut classes[..left.len()]);
        runs(left.len(), RUN, |run, count| {
            // SAFETY: see above; the shim reads a pair outside the joint as
            // class zero.
            unsafe {
                bindings::yanet_sys_classify_joint_lookup(
                    raw(cx, self),
                    left[run.clone()].as_ptr(),
                    right[run.clone()].as_ptr(),
                    classes[run].as_mut_ptr(),
                    count,
                );
            }
        });
    }
}

impl RuleTable {
    /// The first rule each pair of classes matches, [`Rule::NONE`] for none or
    /// a class outside its stage.
    ///
    /// # Panics
    ///
    /// The right classes or the output are fewer than the left classes.
    #[inline]
    pub fn lookup<'g>(&'g self, cx: &impl Resolve<'g>, left: &[u32], right: &[u32], rules: &mut [Rule]) {
        let (right, rules) = (&right[..left.len()], &mut rules[..left.len()]);
        runs(left.len(), RUN, |run, count| {
            // SAFETY: see above; the shim reads a pair outside the table as
            // no rule, and an empty table holds none.
            unsafe {
                bindings::yanet_sys_classify_rule_table_lookup(
                    raw(cx, self),
                    left[run.clone()].as_ptr(),
                    right[run.clone()].as_ptr(),
                    rules[run].as_mut_ptr().cast(),
                    count,
                );
            }
        });
    }
}

impl RuleMap {
    /// The first rule each class matches, [`Rule::NONE`] for none or a class
    /// outside its stage.
    ///
    /// # Panics
    ///
    /// The output is shorter than the classes.
    #[inline]
    pub fn lookup<'g>(&'g self, cx: &impl Resolve<'g>, classes: &[u32], rules: &mut [Rule]) {
        let rules = &mut rules[..classes.len()];
        runs(classes.len(), RUN, |run, count| {
            // SAFETY: see above; the shim reads a class outside the map as no
            // rule, and an empty map holds none.
            unsafe {
                bindings::yanet_sys_classify_resolve(
                    raw(cx, self),
                    classes[run.clone()].as_ptr(),
                    rules[run].as_mut_ptr().cast(),
                    count,
                );
            }
        });
    }
}

/// Keeps in each rule the first match of it and the rule of another decoding
/// of the same packet, as `classify_results_min` does.
///
/// # Panics
///
/// The other rules are fewer than the rules.
#[inline]
pub fn results_min(rules: &mut [Rule], extra: &[Rule]) {
    let extra = &extra[..rules.len()];
    runs(rules.len(), RUN, |run, count| {
        // SAFETY: C reads and writes the run of both slices.
        unsafe {
            bindings::yanet_sys_classify_results_min(
                rules[run.clone()].as_mut_ptr().cast(),
                extra[run].as_ptr().cast(),
                count,
            );
        }
    });
}

#[cfg(test)]
mod tests {
    extern crate std;

    use core::net::Ipv4Addr;
    use std::{vec, vec::Vec};

    use super::{Addr4, LineRange, Lines, Net4, Net6, Nets4, Nets6, PortRange, Ports, Rule, runs};
    use crate::cp::Refusal;

    /// Collects the runs of an input of the length under a cap.
    fn collect(len: usize, max: usize) -> Vec<(usize, usize, u32)> {
        let mut seen = Vec::new();
        runs(len, max, |run, count| seen.push((run.start, run.end, count)));
        seen
    }

    /// verifies that the runs cover the input in order, each at most the cap
    /// long and its length as its count, and that an empty input makes none.
    #[test]
    fn test_runs_cover_the_input_under_the_cap() {
        assert_eq!(Vec::<(usize, usize, u32)>::new(), collect(0, 4));
        assert_eq!(vec![(0, 3, 3)], collect(3, 4));
        assert_eq!(vec![(0, 4, 4)], collect(4, 4));
        assert_eq!(vec![(0, 4, 4), (4, 8, 4), (8, 10, 2)], collect(10, 4));
    }

    /// verifies that an IPv4 view takes contiguous masks only, the empty one
    /// and the full one included.
    #[test]
    fn test_nets4_new_refuses_a_mask_that_is_not_contiguous() {
        for (mask, result) in [
            ([0, 0, 0, 0], Ok(())),
            ([0xff, 0xff, 0xf0, 0], Ok(())),
            ([0xff, 0xff, 0xff, 0xff], Ok(())),
            ([0xff, 0, 0xff, 0], Err(Refusal::InvalidMask)),
            ([0x7f, 0, 0, 0], Err(Refusal::InvalidMask)),
        ] {
            let nets = [Net4 { addr: [10, 0, 0, 0], mask }];
            assert_eq!(result, Nets4::new(&nets).map(drop), "{mask:x?}");
        }
    }

    /// verifies that an IPv6 view takes a mask contiguous in each 64-bit
    /// half, a hole at the half boundary included, and refuses a hole inside
    /// a half.
    #[test]
    fn test_nets6_new_refuses_a_hole_inside_a_half() {
        for (mask, result) in [
            ([0xff; 16], Ok(())),
            ([0xff, 0xff, 0, 0, 0, 0, 0, 0, 0xff, 0, 0, 0, 0, 0, 0, 0], Ok(())),
            (
                [0xff, 0, 0xff, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0],
                Err(Refusal::InvalidMask),
            ),
            (
                [
                    0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x0f, 0, 0, 0, 0, 0, 0, 0,
                ],
                Err(Refusal::InvalidMask),
            ),
        ] {
            let nets = [Net6 { addr: [0; 16], mask }];
            assert_eq!(result, Nets6::new(&nets).map(drop), "{mask:x?}");
        }
    }

    /// verifies that port and byte views refuse a range that ends before it
    /// starts and take a range of one value.
    #[test]
    fn test_range_views_refuse_a_reversed_range() {
        assert_eq!(Ok(()), Ports::new(&[PortRange { from: 53, to: 53 }]).map(drop));
        assert_eq!(
            Err(Refusal::InvalidRange),
            Ports::new(&[PortRange { from: 80, to: 53 }]).map(drop)
        );
        assert_eq!(Ok(()), Lines::new(&[LineRange { from: 6, to: 6 }]).map(drop));
        assert_eq!(
            Err(Refusal::InvalidRange),
            Lines::new(&[LineRange { from: 17, to: 6 }]).map(drop)
        );
    }

    /// verifies that no rule has no index and any other rule its own.
    #[test]
    fn test_rule_index_is_none_for_no_rule_only() {
        assert_eq!(None, Rule::NONE.index());
        assert_eq!(None, Rule::default().index());
        assert_eq!(Some(3), Rule(3).index());
    }

    /// verifies that an IPv4 address holds its octets in network order in
    /// memory, whether made from octets or from an address.
    #[test]
    fn test_addr4_holds_the_octets_in_network_order() {
        let addr = Addr4::from([192, 0, 2, 1]);

        assert_eq!(addr, Addr4::from(Ipv4Addr::new(192, 0, 2, 1)));
        assert_eq!([192, 0, 2, 1], addr.0.to_ne_bytes());
    }
}
