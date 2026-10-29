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
//!
//! A ruleset is a slice with one slot per rule, `None` for a rule outside the
//! projection a compile or a join covers, as C spells a projection with NULL
//! rules; an attribute reads its own view of each rule.

mod compile;

use core::{
    convert::Infallible,
    marker::PhantomData,
    mem::{align_of, size_of},
    ptr, slice,
};

pub use compile::{Stage, Staged};
use yanet_shm::{
    opaque::Opaque,
    resolve::Resolve,
    shm::{MAX_ALIGN, ShmLayout},
};

pub use crate::bindings::{
    classify_line_range as LineRange, filter_port_range as PortRange, net4 as Net4, net6 as Net6,
};
use crate::{
    bindings,
    cp::{Build, FfiRecord, MemoryContext, Owner, Refusal, Registered},
    dp::{Cx, Device, Module},
};

// SAFETY: each is a `#[repr(C)]` bindgen struct of integers and byte arrays,
// without drop glue, so every bit pattern is a value.
unsafe impl FfiRecord for Net4 {}
// SAFETY: as above.
unsafe impl FfiRecord for Net6 {}
// SAFETY: as above.
unsafe impl FfiRecord for PortRange {}
// SAFETY: as above.
unsafe impl FfiRecord for LineRange {}

/// The rule a lookup reports for a packet no rule matches.
pub const NO_RULE: u32 = bindings::YANET_SYS_CLASSIFY_NO_RULE;

/// A device of a rule, by the index its build linked it under.
#[repr(transparent)]
pub struct RuleDevice(bindings::filter_device);

impl RuleDevice {
    /// The device a build linked.
    #[must_use]
    pub fn new(device: Registered<'_, Device>) -> Self {
        Self(bindings::filter_device {
            id: device.index(),
            ..bindings::filter_device::default()
        })
    }
}

/// The views of a rule a compile reads: each borrows the items of the rule's
/// set for the build, as the C view of a set does.
macro_rules! views {
    ($($(#[$doc:meta])* $name:ident: $raw:ident of $item:ty;)*) => {$(
        $(#[$doc])*
        #[repr(transparent)]
        pub struct $name<'a> {
            raw: bindings::$raw,
            _items: PhantomData<&'a [$item]>,
        }

        impl<'a> $name<'a> {
            /// The view of the items.
            ///
            /// # Panics
            ///
            /// The set holds more than `u32::MAX` items, more than C counts.
            #[must_use]
            pub fn new(items: &'a [$item]) -> Self {
                Self {
                    raw: bindings::$raw {
                        // The compile only reads through the pointer.
                        items: items.as_ptr() as _,
                        count: u32::try_from(items.len()).expect("more items than C counts"),
                    },
                    _items: PhantomData,
                }
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
    /// The devices of a rule; none matches every device.
    Devices: filter_devices of RuleDevice;
    /// The IPv4 networks of one side of a rule.
    Nets4: filter_net4s of Net4;
    /// The IPv6 networks of one side of a rule.
    Nets6: filter_net6s of Net6;
    /// The port ranges of one side of a rule; none matches every port.
    Ports: filter_port_ranges of PortRange;
    /// The byte ranges of a line attribute of a rule (the protocol numbers,
    /// the TCP flags, the ICMP types), as the control plane that authors the
    /// rules derives them; none matches every byte.
    Lines: classify_line_ranges of LineRange;
}

/// The fragmentation a rule matches, C's `enum filter_ip_fragment`.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
#[repr(u32)]
pub enum Fragment {
    /// Fragments and whole packets.
    #[default]
    Any = bindings::filter_ip_fragment_FILTER_IP_FRAG_ANY,
    /// Whole packets only.
    Whole = bindings::filter_ip_fragment_FILTER_IP_FRAG_NONE,
    /// Fragments only.
    Only = bindings::filter_ip_fragment_FILTER_IP_FRAG_FRAG,
}

const _: () = assert!(size_of::<Fragment>() == size_of::<bindings::filter_ip_fragment>());

/// A view of one attribute of a rule, which the classifier of the attribute
/// is compiled from by [`Scope::compile`](crate::cp::Scope::compile).
///
/// # Safety
///
/// [`Attribute::compile`] is the compile of the C attribute that
/// `Classifier` holds, reading each rule that is not null as a `Self`; it
/// allocates only in the memory context, which its classifier and its stage
/// free.
pub unsafe trait Attribute {
    /// The classifier of the attribute.
    type Classifier: Build;

    /// Compiles the classifier in place over the rules and fills the stage;
    /// zero on success.
    ///
    /// # Safety
    ///
    /// As the shim compiles (`classify_shim.h`) take their arguments.
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

macro_rules! attribute {
    ($($view:ty => $classifier:ident, $compile:ident;)*) => {$(
        // SAFETY: the shim compile of the classifier's C attribute, which
        // reads a rule as the C view or enum the view type wraps.
        unsafe impl Attribute for $view {
            type Classifier = $classifier;

            unsafe fn compile(
                memory: *mut bindings::memory_context,
                name: *const core::ffi::c_char,
                rules: *const *const Self,
                count: u32,
                classifier: *mut Self::Classifier,
                stage: *mut bindings::classifier,
            ) -> core::ffi::c_int {
                // SAFETY: the caller's contract, the views being their C
                // views.
                unsafe { bindings::$compile(memory, name, rules.cast(), count, classifier.cast(), stage) }
            }
        }
    )*};
}

attribute! {
    Devices<'_> => DeviceAttr, yanet_sys_classify_device_compile;
    Nets4<'_> => Net4Attr, yanet_sys_classify_net4_compile;
    Nets6<'_> => Net6Attr, yanet_sys_classify_net6_compile;
    Ports<'_> => PortAttr, yanet_sys_classify_port_compile;
    Lines<'_> => LineAttr, yanet_sys_classify_line_compile;
    Fragment => FragAttr, yanet_sys_classify_ipfrag_compile;
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
    /// The classifier of the device a packet runs under.
    ///
    /// Compiled only with its stage, by
    /// [`Scope::compile`](crate::cp::Scope::compile).
    DeviceAttr: classify_attr_device, yanet_sys_classify_device_free, compiled;
    /// The classifier of an IPv4 address.
    ///
    /// Compiled only with its stage, as above.
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
    /// The classifier of whether a packet is a fragment.
    ///
    /// Compiled only with its stage, as above.
    FragAttr: classify_attr_ipfrag, yanet_sys_classify_ipfrag_free, compiled;
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

/// The count of a batch as C takes it; a batch beyond `u32::MAX` is looked up
/// in part.
fn count(len: usize) -> u32 {
    u32::try_from(len).unwrap_or(u32::MAX)
}

// SAFETY (every lookup below): the classifier belongs to the configuration
// published and frozen for 'g (`Resolve` contract) and was built whole by its
// C build; C only reads it and the inputs, and writes one output per input,
// into an output slice at least as long.

impl DeviceAttr {
    /// The class of the module device the call runs under.
    #[inline]
    pub fn lookup<'id, M: Module>(&'id self, cx: &Cx<'id, M>) -> u32 {
        // SAFETY: see above.
        unsafe { bindings::yanet_sys_classify_device_lookup(raw(cx, self), cx.module_device_id()) }
    }
}

impl Net4Attr {
    /// The class of each address, given as its four bytes in network order
    /// read as a word in memory order (`u32::from_ne_bytes`).
    ///
    /// # Panics
    ///
    /// The classes are fewer than the addresses.
    #[inline]
    pub fn lookup<'g>(&'g self, cx: &impl Resolve<'g>, addrs: &[u32], classes: &mut [u32]) {
        let classes = &mut classes[..addrs.len()];
        // SAFETY: see above.
        unsafe {
            bindings::yanet_sys_classify_net4_lookup(
                raw(cx, self),
                addrs.as_ptr(),
                classes.as_mut_ptr(),
                count(addrs.len()),
            );
        }
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
        // SAFETY: see above; the addresses are consecutive bytes.
        unsafe {
            bindings::yanet_sys_classify_net6_lookup(
                raw(cx, self),
                addrs.as_ptr().cast(),
                classes.as_mut_ptr(),
                count(addrs.len()),
            );
        }
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
        // SAFETY: see above; the line spans the whole port domain.
        unsafe {
            bindings::yanet_sys_classify_port_lookup(
                raw(cx, self),
                ports.as_ptr(),
                classes.as_mut_ptr(),
                count(ports.len()),
            );
        }
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
        // SAFETY: see above; the line spans the whole byte domain.
        unsafe {
            bindings::yanet_sys_classify_line_lookup(
                raw(cx, self),
                bytes.as_ptr(),
                classes.as_mut_ptr(),
                count(bytes.len()),
            );
        }
    }
}

impl FragAttr {
    /// The class of each packet by whether it is a fragment.
    ///
    /// # Panics
    ///
    /// The classes are fewer than the packets.
    #[inline]
    pub fn lookup<'g>(&'g self, cx: &impl Resolve<'g>, fragments: &[bool], classes: &mut [u32]) {
        let classes = &mut classes[..fragments.len()];
        // SAFETY: see above; the table spans both regions.
        unsafe {
            bindings::yanet_sys_classify_ipfrag_lookup(
                raw(cx, self),
                fragments.as_ptr(),
                classes.as_mut_ptr(),
                count(fragments.len()),
            );
        }
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
        // SAFETY: see above; the shim reads a pair outside the joint as
        // class zero.
        unsafe {
            bindings::yanet_sys_classify_joint_lookup(
                raw(cx, self),
                left.as_ptr(),
                right.as_ptr(),
                classes.as_mut_ptr(),
                count(left.len()),
            );
        }
    }

    /// As [`Joint::lookup`], with one class of the left stage for every
    /// class of the right one.
    ///
    /// # Panics
    ///
    /// The output is shorter than the right classes.
    #[inline]
    pub fn lookup_const<'g>(&'g self, cx: &impl Resolve<'g>, left: u32, right: &[u32], classes: &mut [u32]) {
        let classes = &mut classes[..right.len()];
        // SAFETY: as above.
        unsafe {
            bindings::yanet_sys_classify_joint_lookup_const(
                raw(cx, self),
                left,
                right.as_ptr(),
                classes.as_mut_ptr(),
                count(right.len()),
            );
        }
    }
}

impl RuleTable {
    /// The first rule each pair of classes matches, [`NO_RULE`] for none or a
    /// class outside its stage.
    ///
    /// # Panics
    ///
    /// The right classes or the output are fewer than the left classes.
    #[inline]
    pub fn lookup<'g>(&'g self, cx: &impl Resolve<'g>, left: &[u32], right: &[u32], rules: &mut [u32]) {
        let (right, rules) = (&right[..left.len()], &mut rules[..left.len()]);
        // SAFETY: see above; the shim reads a pair outside the table as no
        // rule, and an empty table holds none.
        unsafe {
            bindings::yanet_sys_classify_rule_table_lookup(
                raw(cx, self),
                left.as_ptr(),
                right.as_ptr(),
                rules.as_mut_ptr(),
                count(left.len()),
            );
        }
    }
}

impl RuleMap {
    /// The first rule each class matches, [`NO_RULE`] for none or a class
    /// outside its stage.
    ///
    /// # Panics
    ///
    /// The output is shorter than the classes.
    #[inline]
    pub fn lookup<'g>(&'g self, cx: &impl Resolve<'g>, classes: &[u32], rules: &mut [u32]) {
        let rules = &mut rules[..classes.len()];
        // SAFETY: see above; the shim reads a class outside the map as no
        // rule, and an empty map holds none.
        unsafe {
            bindings::yanet_sys_classify_resolve(
                raw(cx, self),
                classes.as_ptr(),
                rules.as_mut_ptr(),
                count(classes.len()),
            );
        }
    }
}

/// Keeps in each rule the first match of it and the rule of another decoding
/// of the same packet, as `classify_results_min` does.
///
/// # Panics
///
/// The other rules are fewer than the rules.
#[inline]
pub fn results_min(rules: &mut [u32], extra: &[u32]) {
    let extra = &extra[..rules.len()];
    // SAFETY: C reads and writes as many rules as the slices hold.
    unsafe { bindings::yanet_sys_classify_results_min(rules.as_mut_ptr(), extra.as_ptr(), count(rules.len())) };
}
