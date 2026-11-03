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
    ops::Range,
    ptr, slice,
};

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
    cp::{Build, FfiRecord, MemoryContext, Owner, Refusal},
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
            /// # Errors
            ///
            /// The set holds more items than C counts, `u32::MAX`.
            pub fn new(items: &'a [$item]) -> Result<Self, Refusal> {
                let count = u32::try_from(items.len()).map_err(|_| Refusal::TooManyItems)?;
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
    /// The IPv4 networks of one side of a rule; none matches every address.
    Nets4: filter_net4s of Net4;
    /// The IPv6 networks of one side of a rule; none matches every address.
    Nets6: filter_net6s of Net6;
    /// The port ranges of one side of a rule, closed; none matches every
    /// port.
    Ports: filter_port_ranges of PortRange;
    /// The closed byte ranges of a line attribute of a rule (the protocol
    /// numbers, the TCP flags, the ICMP types), as the control plane that
    /// authors the rules derives them; bounds beyond the byte domain are
    /// clamped to it, and none matches every byte.
    Lines: classify_line_ranges of LineRange;
}

/// A view of one attribute of a rule, which the classifier of the attribute
/// is compiled from by [`Scope::compile`](crate::cp::Scope::compile).
///
/// Sealed: the attributes are the views of this module, each that of a C
/// attribute.
///
/// ```compile_fail,E0277
/// # use yanet_sys::{classify::{Attribute, Net4Attr}, cp::Refusal};
/// struct Mine;
/// impl Attribute for Mine {
///     type Classifier = Net4Attr;
///     fn check(&self) -> Result<(), Refusal> {
///         Ok(())
///     }
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

    /// Refuses a view the C compile would read as other than it says: a
    /// network mask that is not a prefix, a range that ends before it
    /// starts.
    #[doc(hidden)]
    fn check(&self) -> Result<(), Refusal>;

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

/// Reports whether the mask is a prefix: ones, then zeros only.
///
/// The compiles read a network as the range from its address under the mask
/// to its address over it, of the prefix length the mask's ones count, which
/// is the network only for a prefix.
fn is_prefix(mask: &[u8]) -> bool {
    let mut ended = false;
    mask.iter().all(|&byte| {
        let fits = if ended {
            byte == 0
        } else {
            byte.leading_ones() + byte.trailing_zeros() >= 8
        };
        ended |= byte != 0xff;
        fits
    })
}

/// Refuses an item of the view that `valid` rejects.
fn check_all<T>(items: &[T], refusal: Refusal, valid: impl Fn(&T) -> bool) -> Result<(), Refusal> {
    items.iter().all(valid).then_some(()).ok_or(refusal)
}

impl Attribute for Nets4<'_> {
    type Classifier = Net4Attr;

    fn check(&self) -> Result<(), Refusal> {
        check_all(self.as_slice(), Refusal::InvalidMask, |net| is_prefix(&net.mask))
    }

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

    /// The IPv6 compile ranges each half of the address apart, so the mask
    /// of each half is a prefix of its own.
    fn check(&self) -> Result<(), Refusal> {
        check_all(self.as_slice(), Refusal::InvalidMask, |net| {
            let (hi, lo) = net.mask.split_at(8);
            is_prefix(hi) && is_prefix(lo)
        })
    }

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

    fn check(&self) -> Result<(), Refusal> {
        check_all(self.as_slice(), Refusal::InvalidRange, |range| range.from <= range.to)
    }

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

    fn check(&self) -> Result<(), Refusal> {
        check_all(self.as_slice(), Refusal::InvalidRange, |range| range.from <= range.to)
    }

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

/// Calls `lookup` over the inputs in runs C counts, of at most `u32::MAX`
/// each, with the range of each run and its length.
#[inline(always)]
fn runs(len: usize, mut lookup: impl FnMut(Range<usize>, u32)) {
    let mut start = 0;
    while start < len {
        let run = (len - start).min(u32::MAX as usize);
        // The run fits, as it is at most `u32::MAX`.
        lookup(start..start + run, run as u32);
        start += run;
    }
}

// SAFETY (every lookup below): the classifier belongs to the configuration
// published and frozen for 'g (`Resolve` contract) and was built whole by its
// C build; C only reads it and the inputs, and writes one output per input of
// the run, into an output run of the same length.

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
        runs(addrs.len(), |run, count| {
            // SAFETY: see above.
            unsafe {
                bindings::yanet_sys_classify_net4_lookup(
                    raw(cx, self),
                    addrs[run.clone()].as_ptr(),
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
        runs(addrs.len(), |run, count| {
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
        runs(ports.len(), |run, count| {
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
        runs(bytes.len(), |run, count| {
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
        runs(left.len(), |run, count| {
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
    /// The first rule each pair of classes matches, [`NO_RULE`] for none or a
    /// class outside its stage.
    ///
    /// # Panics
    ///
    /// The right classes or the output are fewer than the left classes.
    #[inline]
    pub fn lookup<'g>(&'g self, cx: &impl Resolve<'g>, left: &[u32], right: &[u32], rules: &mut [u32]) {
        let (right, rules) = (&right[..left.len()], &mut rules[..left.len()]);
        runs(left.len(), |run, count| {
            // SAFETY: see above; the shim reads a pair outside the table as
            // no rule, and an empty table holds none.
            unsafe {
                bindings::yanet_sys_classify_rule_table_lookup(
                    raw(cx, self),
                    left[run.clone()].as_ptr(),
                    right[run.clone()].as_ptr(),
                    rules[run].as_mut_ptr(),
                    count,
                );
            }
        });
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
        runs(classes.len(), |run, count| {
            // SAFETY: see above; the shim reads a class outside the map as no
            // rule, and an empty map holds none.
            unsafe {
                bindings::yanet_sys_classify_resolve(
                    raw(cx, self),
                    classes[run.clone()].as_ptr(),
                    rules[run].as_mut_ptr(),
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
pub fn results_min(rules: &mut [u32], extra: &[u32]) {
    let extra = &extra[..rules.len()];
    runs(rules.len(), |run, count| {
        // SAFETY: C reads and writes the run of both slices.
        unsafe {
            bindings::yanet_sys_classify_results_min(rules[run.clone()].as_mut_ptr(), extra[run].as_ptr(), count);
        }
    });
}

#[cfg(test)]
mod tests {
    use super::is_prefix;

    /// verifies that a mask is a prefix exactly when its ones all come
    /// before its zeros.
    #[test]
    fn test_is_prefix_accepts_ones_then_zeros_only() {
        for (mask, prefix) in [
            ([0, 0, 0, 0], true),
            ([0xff, 0xff, 0xff, 0xff], true),
            ([0xff, 0xff, 0xf0, 0], true),
            ([0x80, 0, 0, 0], true),
            ([0xff, 0, 0xff, 0], false),
            ([0xff, 0xf7, 0, 0], false),
            ([0x7f, 0, 0, 0], false),
            ([0, 0, 0, 1], false),
        ] {
            assert_eq!(prefix, is_prefix(&mask), "{mask:x?}");
        }
    }
}
