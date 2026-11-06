//! Bindings of `lib/classify`, the classification library of the C modules.
//!
//! A module composes its classifier as a C module does, as one tree ([`Join`],
//! [`Rules`], [`Decode`]). The control plane compiles a classifier of each
//! attribute of the rules, joins the classes of two classifiers into the
//! classes of the next, and ends in a rule table or a rule map that maps the
//! last classes to the first rule they match. Every classifier, joint, rule
//! table and rule map is a C object a build writes, its type here a mirror of
//! its C struct, and each node of the tree holds the classifiers whose classes
//! it reads. The dataplane looks a packet's fields up, for one packet or a
//! burst of them, through Rust ports of the C lookups over those layouts, as
//! the LPM's is: no call into C and, the classes never leaving the tree, no
//! check of a class against the table it indexes.
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
    mem::{align_of, offset_of, size_of},
    net::Ipv4Addr,
    ptr, slice,
};

use netip::{Ipv4Network, Ipv6Network};
use yanet_shm::{
    opaque::Opaque,
    rel::RelPtr,
    resolve::Resolve,
    shm::{MAX_ALIGN, ShmLayout},
};

pub use self::tree::{BURST, Decode, Join, Node, Paths, Rules, link};
pub use crate::bindings::{
    classify_line_range as LineRange, filter_port_range as PortRange, net4 as Net4, net6 as Net6,
};
use crate::{
    bindings,
    common::lpm::Lpm,
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
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
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

/// The memory context a C object of the classification allocates its blocks
/// in, held by offset: C frees it with the object, Rust never follows it.
type ContextSlot = RelPtr<Opaque<bindings::memory_context>>;

/// C's `struct vline`: a value for each index of a dense domain, in one
/// block.
#[repr(C)]
struct Line {
    _memory_context: ContextSlot,
    size: u32,
    /// The padding of the C layout before the slot of the values.
    _pad: u32,
    values: RelPtr<u32>,
}

/// The rows of C's `struct value_table` and `struct rule_table`: a directory
/// of offset slots, one per row, each to the block of the row's values.
#[repr(transparent)]
struct Rows(RelPtr<RelPtr<u32>>);

/// C's `struct value_table`: a value for each pair of one of its rows and one
/// of its columns.
#[repr(C)]
struct Table {
    _memory_context: ContextSlot,
    v_dim: u32,
    h_dim: u32,
    values: Rows,
}

const _: () = {
    type C = bindings::vline;
    assert!(size_of::<Line>() == size_of::<C>());
    assert!(align_of::<Line>() == align_of::<C>());
    assert!(offset_of!(Line, _memory_context) == offset_of!(C, memory_context));
    assert!(offset_of!(Line, size) == offset_of!(C, size));
    assert!(offset_of!(Line, values) == offset_of!(C, values));
    // The fields leave no padding.
    assert!(size_of::<Line>() == size_of::<ContextSlot>() + 2 * size_of::<u32>() + size_of::<RelPtr<u32>>());
};

const _: () = {
    type C = bindings::value_table;
    assert!(size_of::<Table>() == size_of::<C>());
    assert!(align_of::<Table>() == align_of::<C>());
    assert!(offset_of!(Table, _memory_context) == offset_of!(C, memory_context));
    assert!(offset_of!(Table, v_dim) == offset_of!(C, v_dim));
    assert!(offset_of!(Table, h_dim) == offset_of!(C, h_dim));
    assert!(offset_of!(Table, values) == offset_of!(C, values));
    // The fields leave no padding.
    assert!(size_of::<Table>() == size_of::<ContextSlot>() + 2 * size_of::<u32>() + size_of::<Rows>());
};

// The mirrored fields keep the C types, not only their places.
const _: fn(&bindings::vline) -> (*mut bindings::memory_context, u32, *mut u32) =
    |line| (line.memory_context, line.size, line.values);
const _: fn(&bindings::value_table) -> (*mut bindings::memory_context, u32, u32, *mut *mut u32) =
    |table| (table.memory_context, table.v_dim, table.h_dim, table.values);

impl Line {
    /// The value at an index of the line.
    ///
    /// # Safety
    ///
    /// The index is below the line's size.
    #[inline(always)]
    unsafe fn get<'g>(&'g self, cx: &impl Resolve<'g>, idx: u32) -> u32 {
        debug_assert!(idx < self.size, "index {idx} past a line of {}", self.size);
        let slot = ptr::from_ref(&self.values).addr();
        let values: *const u32 = cx.shm_root().rel(slot, self.values.offset());
        // SAFETY: a line of a classifier is built whole by C, its slot
        // pointing at its values, and frozen for 'g (`Resolve` contract); the
        // caller keeps the index inside.
        unsafe { values.add(idx as usize).read() }
    }
}

impl Rows {
    /// The value at a column of a row.
    ///
    /// # Safety
    ///
    /// The directory holds the row, and the row the column.
    #[inline(always)]
    unsafe fn get<'g>(&'g self, cx: &impl Resolve<'g>, row: u32, column: u32) -> u32 {
        let Self(directory) = self;
        let root = cx.shm_root();
        let slots: *const isize = root.rel(ptr::from_ref(directory).addr(), directory.offset());
        // SAFETY: the rows of a classifier are built whole by C, an aligned
        // offset slot per row, and frozen for 'g (`Resolve` contract); the
        // caller keeps the row inside.
        let slot = unsafe { slots.add(row as usize) };
        // SAFETY: as above; the offset is read as integer bits and never as a
        // pointer.
        let block: *const u32 = root.rel(slot.addr(), unsafe { slot.read() });
        // SAFETY: as above, the row's block holds every column; the caller
        // keeps the column inside.
        unsafe { block.add(column as usize).read() }
    }
}

impl Table {
    /// The value of a pair inside the table.
    ///
    /// # Safety
    ///
    /// The row and the column are below the table's counts of them.
    #[inline(always)]
    unsafe fn get<'g>(&'g self, cx: &impl Resolve<'g>, row: u32, column: u32) -> u32 {
        debug_assert!(
            row < self.v_dim && column < self.h_dim,
            "pair ({row}, {column}) outside a table of {} by {}",
            self.v_dim,
            self.h_dim
        );
        // SAFETY: the table's directory holds every row below its count of
        // rows, each every column below its count of columns.
        unsafe { self.values.get(cx, row, column) }
    }
}

/// The classifier of an IPv4 address: the longest-prefix match of the
/// address, whose value is its class.
///
/// Compiled only with its stage, by
/// [`Scope::compile`](crate::cp::Scope::compile).
#[repr(C)]
pub struct Net4Attr {
    lpm: Lpm<4>,
}

/// The classifier of an IPv6 address: a longest-prefix match of each half,
/// and a table of the regions the low half splits.
///
/// The high half's value is the class of the address or, marked, a row of the
/// table, read at the low half's class. Compiled only with its stage, as
/// above.
#[repr(C)]
pub struct Net6Attr {
    hi: Lpm<8>,
    lo: Lpm<8>,
    comb: Table,
}

/// The classifier of a port.
///
/// Compiled only with its stage, as above.
#[repr(C)]
pub struct PortAttr {
    line: Line,
}

/// The classifier of a byte of a packet: its protocol number, its TCP flags
/// or its ICMP type.
///
/// Compiled only with its stage, as above.
#[repr(C)]
pub struct LineAttr {
    line: Line,
}

/// The joint of the classes of two stages, C's `struct value_table`: the
/// table of a [`Join`], which the join writes.
#[repr(C)]
pub struct Joint {
    table: Table,
}

/// The first rule each pair of classes of two stages matches, C's
/// `struct rule_table`: the table of a [`Rules`], which the join writes.
#[repr(C)]
pub struct RuleTable {
    _memory_context: ContextSlot,
    v_dim: u32,
    h_dim: u32,
    /// The row of the rules of each left class, which the left classes of one
    /// first-match row share.
    class_map: Line,
    rows: Rows,
    _class_count: u32,
    /// The padding of the C layout after the count of the rows.
    _pad: u32,
}

/// The first rule each class of a stage matches, C's `struct vline`: the map
/// of a [`Decode`], which the decode writes.
#[repr(C)]
pub struct RuleMap {
    line: Line,
}

const _: () = {
    assert!(offset_of!(Net4Attr, lpm) == offset_of!(bindings::classify_attr_net4, lpm));
    assert!(offset_of!(Net6Attr, hi) == offset_of!(bindings::classify_attr_net6, hi));
    assert!(offset_of!(Net6Attr, lo) == offset_of!(bindings::classify_attr_net6, lo));
    assert!(offset_of!(Net6Attr, comb) == offset_of!(bindings::classify_attr_net6, comb));
    assert!(offset_of!(PortAttr, line) == offset_of!(bindings::classify_attr_port, line));
    assert!(offset_of!(LineAttr, line) == offset_of!(bindings::classify_attr_line, line));
    type C = bindings::rule_table;
    assert!(offset_of!(RuleTable, _memory_context) == offset_of!(C, memory_context));
    assert!(offset_of!(RuleTable, v_dim) == offset_of!(C, v_dim));
    assert!(offset_of!(RuleTable, h_dim) == offset_of!(C, h_dim));
    assert!(offset_of!(RuleTable, class_map) == offset_of!(C, class_map));
    assert!(offset_of!(RuleTable, rows) == offset_of!(C, rows));
    assert!(offset_of!(RuleTable, _class_count) == offset_of!(C, class_count));
    // The fields leave no padding.
    assert!(size_of::<Net6Attr>() == 2 * size_of::<Lpm<8>>() + size_of::<Table>());
    assert!(
        size_of::<RuleTable>()
            == size_of::<ContextSlot>() + 4 * size_of::<u32>() + size_of::<Line>() + size_of::<Rows>()
    );
};

// The mirrored fields keep the C types, not only their places.
const _: fn(&bindings::classify_attr_net4) -> &bindings::lpm = |attr| &attr.lpm;
const _: fn(&bindings::classify_attr_net6) -> (&bindings::lpm, &bindings::lpm, &bindings::value_table) =
    |attr| (&attr.hi, &attr.lo, &attr.comb);
const _: fn(&bindings::classify_attr_port) -> &bindings::vline = |attr| &attr.line;
const _: fn(&bindings::classify_attr_line) -> &bindings::vline = |attr| &attr.line;
#[allow(clippy::type_complexity)]
const _: fn(
    &bindings::rule_table,
) -> (
    *mut bindings::memory_context,
    u32,
    u32,
    &bindings::vline,
    *mut *mut u32,
    u32,
) = |table| {
    (
        table.memory_context,
        table.v_dim,
        table.h_dim,
        &table.class_map,
        table.rows,
        table.class_count,
    )
};

/// The shared-memory impls of the C objects of a classification.
///
/// Each mirrors its C struct, is written only by its compile, join or decode
/// and is freed by its C free.
macro_rules! classifiers {
    ($($name:ident: $raw:ident, $free:ident;)*) => {$(
        const _: () = {
            assert!(size_of::<$name>() == size_of::<bindings::$raw>());
            assert!(align_of::<$name>() == align_of::<bindings::$raw>());
            assert!(align_of::<$name>() <= MAX_ALIGN);
        };

        // SAFETY: a `#[repr(C)]` mirror of the C struct, of its size and
        // alignment, the alignment within the shared-memory bound, whose
        // fields lie at the C offsets and leave no padding (asserted with the
        // types). Every field is an integer, an offset slot, an LPM or a
        // mirror of those, so every pattern is valid; once the configuration
        // is published C rewrites only the memory contexts the LPMs embed,
        // which are opaque. Dropping is a no-op.
        unsafe impl ShmLayout for $name {}

        // SAFETY: the C object is the whole value; the compile, the join or
        // the decode that wrote it allocated only in the memory context, in
        // blocks and child contexts its C free returns and unlinks.
        unsafe impl Build for $name {
            type Parts<'b, W: Owner> = Infallible;

            unsafe fn write<W: Owner>(parts: Infallible, _: &MemoryContext<'_, W>, _: usize) -> Result<(), Refusal> {
                match parts {}
            }

            unsafe fn free<W: Owner>(addr: usize, memory: &MemoryContext<'_, W>) {
                // SAFETY: the object was written whole (caller contract).
                unsafe { bindings::$free(memory.root().ptr(addr)) };
            }
        }
    )*};
}

classifiers! {
    Net4Attr: classify_attr_net4, yanet_sys_classify_net4_free;
    Net6Attr: classify_attr_net6, yanet_sys_classify_net6_free;
    PortAttr: classify_attr_port, yanet_sys_classify_port_free;
    LineAttr: classify_attr_line, yanet_sys_classify_line_free;
    Joint: value_table, yanet_sys_classify_joint_free;
    RuleTable: rule_table, yanet_sys_classify_rule_table_free;
    RuleMap: vline, yanet_sys_classify_rule_map_free;
}

/// The mark of a value of the high half of an IPv6 classifier that is a row
/// of its table.
const ROW_MARK: u32 = bindings::FILTER_NET6_ROW_MARK;

/// The value an LPM of a classifier holds for a key, as C reads it: the
/// invalid value where no range covers the key, which a compile never leaves.
#[inline(always)]
fn lpm_value<'g, const K: usize>(lpm: &'g Lpm<K>, cx: &impl Resolve<'g>, key: &[u8; K]) -> u32 {
    lpm.lookup(cx, key).unwrap_or(bindings::LPM_VALUE_INVALID)
}

/// The high and the low half of an IPv6 address.
#[inline(always)]
fn halves(addr: &[u8; 16]) -> (&[u8; 8], &[u8; 8]) {
    let (hi, lo) = addr.split_at(8);
    // Each half is eight bytes long.
    (hi.try_into().unwrap(), lo.try_into().unwrap())
}

impl Net6Attr {
    /// The class of an address: the value of its high half, or for a marked
    /// one the table's value at that row and at the class of the low half.
    #[inline(always)]
    fn class<'g>(&'g self, cx: &impl Resolve<'g>, addr: &[u8; 16]) -> u32 {
        let (hi, lo) = halves(addr);
        let value = lpm_value(&self.hi, cx, hi);
        if value & ROW_MARK == 0 {
            return value;
        }
        let column = lpm_value(&self.lo, cx, lo);
        // SAFETY: the compile marks only rows of the table, whose columns are
        // the classes of the low half.
        unsafe { self.comb.get(cx, value & !ROW_MARK, column) }
    }
}

// The merge of two decodings takes the smaller word as the first match.
const _: () = assert!(Rule::NONE.0 == u32::MAX);

/// Keeps in each rule the first match of it and the rule of another decoding
/// of the same packet, as `classify_results_min` does.
///
/// # Panics
///
/// The other rules are fewer than the rules.
#[inline(always)]
pub fn results_min(rules: &mut [Rule], extra: &[Rule]) {
    let extra = &extra[..rules.len()];
    for (Rule(rule), &Rule(extra)) in rules.iter_mut().zip(extra) {
        // No rule is the largest word, so the first match is the smaller.
        *rule = (*rule).min(extra);
    }
}

#[cfg(test)]
mod tests {
    use core::{
        mem::{offset_of, size_of, size_of_val},
        net::Ipv4Addr,
    };

    use yanet_sdk_testing::{BlockArena, View};

    use super::{Addr4, LineRange, Lines, Net4, Net6, Net6Attr, Nets4, Nets6, PortRange, Ports, ROW_MARK, Rule};
    use crate::{bindings, cp::Refusal};

    /// Writes a value at an address of a block of the arena.
    pub fn put<T>(arena: &'static BlockArena, addr: usize, value: T) {
        // SAFETY: the tests write aligned values inside blocks of the arena
        // they allocated, which nothing references yet.
        unsafe { arena.root().ptr::<T>(addr).write(value) }
    }

    /// The address of a zeroed block of the arena.
    pub fn block(arena: &'static BlockArena, size: usize) -> usize {
        arena.alloc(size).as_ptr().addr()
    }

    /// Points the offset slot at an address to the target, as C sets an
    /// offset pointer.
    pub fn point(arena: &'static BlockArena, slot: usize, target: usize) {
        put(arena, slot, target.wrapping_sub(slot) as isize);
    }

    /// Lays a line of the values out at an address as C does: its size, and
    /// the slot of a block of the values.
    pub fn lay_line(arena: &'static BlockArena, line: usize, values: &[u32]) {
        let values_at = block(arena, size_of_val(values));
        for (idx, &value) in values.iter().enumerate() {
            put(arena, values_at + idx * size_of::<u32>(), value);
        }
        put(arena, line + offset_of!(bindings::vline, size), values.len() as u32);
        point(arena, line + offset_of!(bindings::vline, values), values_at);
    }

    /// Lays rows of values out behind the slot at an address as C lays out a
    /// table's: a directory with a slot per row, each to a block of the row.
    pub fn lay_rows(arena: &'static BlockArena, slot: usize, rows: &[&[u32]]) {
        let directory = block(arena, rows.len() * size_of::<isize>());
        for (idx, row) in rows.iter().enumerate() {
            let row_at = block(arena, size_of_val(*row));
            for (column, &value) in row.iter().enumerate() {
                put(arena, row_at + column * size_of::<u32>(), value);
            }
            point(arena, directory + idx * size_of::<isize>(), row_at);
        }
        point(arena, slot, directory);
    }

    /// Lays a table of the rows, all of one length, out at an address as C
    /// does.
    pub fn lay_table(arena: &'static BlockArena, table: usize, rows: &[&[u32]]) {
        put(
            arena,
            table + offset_of!(bindings::value_table, v_dim),
            rows.len() as u32,
        );
        put(
            arena,
            table + offset_of!(bindings::value_table, h_dim),
            rows[0].len() as u32,
        );
        lay_rows(arena, table + offset_of!(bindings::value_table, values), rows);
    }

    /// Lays an LPM out at an address as C does, of one page of leaves: a key
    /// maps to the value of its first byte.
    pub fn lay_lpm(arena: &'static BlockArena, lpm: usize, value: impl Fn(u8) -> u32) {
        let page = block(arena, 256 * size_of::<u64>());
        for byte in 0..=u8::MAX {
            // A leaf stores its value as the C insert does.
            let leaf = (u64::from(value(byte)) << 1) | u64::from(bindings::LPM_VALUE_FLAG);
            put(arena, page + usize::from(byte) * size_of::<u64>(), leaf);
        }
        let directory = block(arena, size_of::<isize>());
        point(arena, directory, page);
        point(arena, lpm + offset_of!(bindings::lpm, pages), directory);
        put(arena, lpm + offset_of!(bindings::lpm, page_count), 1usize);
    }

    /// verifies that an IPv6 classifier takes the class of an address from
    /// its high half, and for a marked high half from its table, at the row
    /// the mark names and the column of the low half's class.
    #[test]
    fn test_net6_attr_class_takes_a_marked_row_from_the_table() {
        type C = bindings::classify_attr_net6;
        let arena = BlockArena::new();
        let attr = block(arena, size_of::<Net6Attr>());
        // High halves from 0x20 are of the second row, the others of class 7.
        lay_lpm(arena, attr + offset_of!(C, hi), |byte| {
            if byte == 0x20 { ROW_MARK | 1 } else { 7 }
        });
        // Low halves from 0 are of column 0, from 1 of column 2, the others
        // of column 1.
        lay_lpm(arena, attr + offset_of!(C, lo), |byte| match byte {
            0 => 0,
            1 => 2,
            _ => 1,
        });
        lay_table(arena, attr + offset_of!(C, comb), &[&[30, 31, 32], &[40, 41, 42]]);

        // SAFETY: laid out as C lays it out, and frozen for the test.
        let (view, attr) = unsafe { (View::new(arena.root()), &*arena.root().ptr::<Net6Attr>(attr)) };
        let addr = |hi: u8, lo: u8| {
            let mut addr = [0; 16];
            (addr[0], addr[8]) = (hi, lo);
            addr
        };
        let classes = [addr(0x21, 1), addr(0x20, 0), addr(0x20, 5), addr(0x20, 1)].map(|addr| attr.class(&view, &addr));
        assert_eq!([7, 40, 41, 42], classes);
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
