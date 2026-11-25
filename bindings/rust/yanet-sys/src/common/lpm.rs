//! The C LPM as a shared-memory field type.
//!
//! [`Lpm`] mirrors `struct lpm` with its embedded memory context opaque, so
//! a reference to it stays sound while C rewrites the context's sibling
//! link. The lookup is a Rust port of the C one, compiling to the same loads,
//! over a table the C routines of `common/` build: the configuration builder
//! embeds one by calling them.

use core::{
    borrow::Borrow,
    mem::{align_of, offset_of, size_of},
    net::{Ipv4Addr, Ipv6Addr},
};

use yanet_shm::{
    opaque::Opaque,
    rel::RelPtr,
    resolve::Resolve,
    shm::{MAX_ALIGN, ShmLayout},
};

use crate::{bindings, cp::FfiRecord};

/// One LPM trie page, as bytes: 256 slots indexed by a key byte.
///
/// Only names the target of the page pointers; the lookup reads a page
/// through the bindgen type. That type is not the phantom parameter: its
/// union holds a raw pointer, which would make `Lpm` neither `Send` nor
/// `Sync`, while a module configuration must be `Sync`.
type Page = [u64; 256];

/// An inclusive range of keys of `K` big-endian bytes and the value it maps
/// to, as the C insert takes them.
///
/// Values keep the C semantics documented on [`Lpm::lookup`].
///
/// The first key must not exceed the last: the C insert does not check it and
/// a reversed range would wrap round, so a build refuses one. Ranges are
/// inserted in slice order, and on an overlap the later range wins, so a broad
/// range goes before the narrow ones in it.
///
/// The ranges are borrowed for the whole build, so they are made before it
/// starts, for a table in a box or a slice too. A range is also a record of an
/// api crate: the ranges C hands in are borrowed by the build as they are.
#[derive(Clone, Copy, Debug)]
#[repr(C)]
pub struct LpmRange<const K: usize> {
    /// First key of the range.
    pub from: [u8; K],
    /// Last key of the range.
    pub to: [u8; K],
    /// Value every key of the range maps to.
    pub value: u32,
}

impl<const K: usize> LpmRange<K> {
    /// The range of the leading bits of an address, mapped to a value.
    ///
    /// The bits of the address past the prefix length are ignored.
    ///
    /// ```
    /// use yanet_sys::common::lpm::LpmRange;
    ///
    /// let range = LpmRange::prefix([10, 1, 2, 3], 12, 7);
    /// assert_eq!(([10, 0, 0, 0], [10, 15, 255, 255]), (range.from, range.to));
    /// ```
    ///
    /// # Panics
    ///
    /// The prefix length is more than the bits of a key.
    #[must_use]
    pub fn prefix(addr: [u8; K], len: usize, value: u32) -> Self {
        assert!(len <= 8 * K, "a prefix of {len} bits in a key of {K} bytes");
        let mut from = addr;
        let mut to = addr;
        for (idx, (from, to)) in from.iter_mut().zip(&mut to).enumerate() {
            let kept = len.saturating_sub(idx * 8).min(8);
            let host = (0xffu16 >> kept) as u8;
            *from &= !host;
            *to |= host;
        }
        Self { from, to, value }
    }
}

// SAFETY: `#[repr(C)]` over byte arrays and an integer, without drop glue;
// every bit pattern is a range, a reversed one refused by the build.
unsafe impl<const K: usize> FfiRecord for LpmRange<K> {}

/// Longest-prefix-match table over big-endian keys of `K` bytes.
///
/// A table built from no ranges is empty: it holds no page, opens no C memory
/// context and finds nothing, so it costs only its own bytes.
#[repr(C)]
pub struct Lpm<const K: usize> {
    /// The allocator context of the table's blocks. C rewrites its sibling
    /// link after publish when another context under the same parent is
    /// finalised.
    _memory_context: Opaque<bindings::memory_context>,
    /// Directory of page chunks.
    pages: RelPtr<RelPtr<Page>>,
    _page_count: usize,
}

const _: () = {
    type C = bindings::lpm;
    type Rust = Lpm<4>;
    assert!(size_of::<Rust>() == size_of::<C>());
    assert!(align_of::<Rust>() == align_of::<C>());
    assert!(offset_of!(Rust, _memory_context) == offset_of!(C, memory_context));
    assert!(offset_of!(Rust, pages) == offset_of!(C, pages));
    assert!(offset_of!(Rust, _page_count) == offset_of!(C, page_count));
    // The fields leave no padding.
    assert!(size_of::<Rust>() == size_of::<bindings::memory_context>() + 2 * size_of::<usize>());
    assert!(align_of::<Rust>() <= MAX_ALIGN);
    assert!(size_of::<Page>() == size_of::<bindings::lpm_page>());
    assert!(align_of::<Page>() == align_of::<bindings::lpm_page>());
};

// The mirrored fields keep the C types, not only their places.
const _: fn(&bindings::lpm) -> (&bindings::memory_context, *mut *mut bindings::lpm_page, usize) =
    |lpm| (&lpm.memory_context, lpm.pages, lpm.page_count);

impl<const K: usize> Lpm<K> {
    /// Reports whether the table was built from no ranges, so it holds no page.
    #[inline(always)]
    pub fn is_empty(&self) -> bool {
        self.pages.offset() == 0
    }

    /// Looks a big-endian key up through `cx`, bit for bit like the C
    /// `lpm_lookup`; `None` when no inserted range covers it.
    ///
    /// Walks one page per key byte from the first page until a leaf slot; the
    /// value of the last visited slot is returned shifted past the flag, as C
    /// does when the key ends on an intermediate node.
    ///
    /// Values keep the C semantics: the C insert stores a value shifted past
    /// the flag in a 64-bit slot, so every 32-bit value reads back whole,
    /// except the invalid mark, which reads as `None`.
    #[doc(alias = "lpm_lookup")]
    #[inline(always)]
    pub fn lookup<'g>(&'g self, cx: &impl Resolve<'g>, key: impl LpmKey<K>) -> Option<u32> {
        const { assert!(K >= 1 && K <= 16, "LPM keys are 1 to 16 bytes") };
        if self.is_empty() {
            return None;
        }
        let root = cx.graph().root();
        let directory_slot = core::ptr::from_ref(&self.pages).addr();
        let directory: *const isize = root.rel(directory_slot, self.pages.offset());
        // SAFETY: the LPM belongs to a published graph frozen for 'g
        // (graph token contract), and a built table that is not empty has a
        // non-null directory whose first entry is an offset word, aligned.
        let first_chunk = unsafe { directory.read() };
        // The offset is read as integer bits and never as a pointer.
        let mut page: *const bindings::lpm_page = root.rel(directory.addr(), first_chunk);
        let mut word = u64::from(bindings::LPM_VALUE_INVALID);
        let octets = key.into_octets();
        for (hop, &byte) in octets.borrow().iter().enumerate() {
            // SAFETY: `page` is a page of this LPM (C built the child
            // offsets), so the slot is in bounds of its 256 slots.
            let slot = unsafe {
                (&raw const (*page).values)
                    .cast::<bindings::lpm_value>()
                    .add(usize::from(byte))
            };
            // SAFETY: as above; a slot accepts every bit pattern, and its
            // eight bytes are read as an integer in either of its meanings.
            word = unsafe { (*slot).value };
            if word & FLAG != 0 || hop + 1 == K {
                break;
            }
            // A slot with the flag clear is an intermediate node whose child
            // page is the slot's offset.
            page = root.rel(slot.addr(), word as isize);
        }
        let value = (word >> 1) as u32;
        (value != bindings::LPM_VALUE_INVALID).then_some(value)
    }
}

/// IPv4 LPM.
pub type Lpm4 = Lpm<4>;
/// IPv6 LPM.
pub type Lpm6 = Lpm<16>;

/// A key of an LPM table: its octets in network order, as many as the table's
/// key width.
///
/// An array of that many bytes, by value or by reference, is a key, an IPv4
/// address is a key of an IPv4 table and an IPv6 address of an IPv6 one, so an
/// address read from a header is looked up as it is. A key of another width
/// does not compile. A key type of a module may implement the trait; the walk
/// takes the octets as the key gives them, so a key in host order looks up
/// another range.
///
/// # Examples
///
/// ```
/// // An address is a key of the table of its width.
/// use core::net::Ipv4Addr;
///
/// use yanet_shm::resolve::Resolve;
/// use yanet_sys::common::lpm::Lpm4;
///
/// fn route<'g>(routes: &'g Lpm4, cx: &impl Resolve<'g>, dst: Ipv4Addr) -> Option<u32> {
///     routes.lookup(cx, dst)
/// }
/// ```
///
/// ```compile_fail,E0277
/// // An address of another width is not.
/// use core::net::Ipv6Addr;
///
/// use yanet_shm::resolve::Resolve;
/// use yanet_sys::common::lpm::Lpm4;
///
/// fn route<'g>(routes: &'g Lpm4, cx: &impl Resolve<'g>, dst: Ipv6Addr) -> Option<u32> {
///     routes.lookup(cx, dst)
/// }
/// ```
///
/// ```compile_fail,E0277
/// // Nor is an array of another width.
/// use yanet_shm::resolve::Resolve;
/// use yanet_sys::common::lpm::Lpm6;
///
/// fn route<'g>(routes: &'g Lpm6, cx: &impl Resolve<'g>, dst: &[u8; 4]) -> Option<u32> {
///     routes.lookup(cx, dst)
/// }
/// ```
///
/// ```
/// // A key type of a module hands over its octets.
/// use yanet_shm::resolve::Resolve;
/// use yanet_sys::common::lpm::{Lpm4, LpmKey};
///
/// struct Destination([u8; 4]);
///
/// impl LpmKey<4> for Destination {
///     type Octets = [u8; 4];
///
///     fn into_octets(self) -> [u8; 4] {
///         self.0
///     }
/// }
///
/// fn route<'g>(routes: &'g Lpm4, cx: &impl Resolve<'g>, dst: Destination) -> Option<u32> {
///     routes.lookup(cx, dst)
/// }
/// ```
pub trait LpmKey<const K: usize> {
    /// The octets as the key holds them: an array reference stays a
    /// reference, so the walk reads the bytes it reaches where they are.
    type Octets: Borrow<[u8; K]>;

    /// The octets of the key, the first one selecting the slot of the first
    /// page.
    fn into_octets(self) -> Self::Octets;
}

impl<const K: usize> LpmKey<K> for [u8; K] {
    type Octets = Self;

    #[inline(always)]
    fn into_octets(self) -> Self {
        self
    }
}

impl<'a, const K: usize> LpmKey<K> for &'a [u8; K] {
    type Octets = &'a [u8; K];

    #[inline(always)]
    fn into_octets(self) -> &'a [u8; K] {
        self
    }
}

impl<'a, const K: usize> LpmKey<K> for &'a mut [u8; K] {
    type Octets = &'a [u8; K];

    #[inline(always)]
    fn into_octets(self) -> &'a [u8; K] {
        self
    }
}

impl LpmKey<4> for Ipv4Addr {
    type Octets = [u8; 4];

    #[inline(always)]
    fn into_octets(self) -> [u8; 4] {
        self.octets()
    }
}

impl LpmKey<16> for Ipv6Addr {
    type Octets = [u8; 16];

    #[inline(always)]
    fn into_octets(self) -> [u8; 16] {
        self.octets()
    }
}

const FLAG: u64 = bindings::LPM_VALUE_FLAG as u64;

// SAFETY: `Lpm` is a `#[repr(C)]` mirror of the C struct whose size and
// field offsets are asserted equal to the bindings' and leave no padding.
// Every pattern is valid: the opaque context is never read by Rust, the
// directory slot is an offset word and the count an integer. Dropping is a
// no-op and the alignment is 8.
unsafe impl<const K: usize> ShmLayout for Lpm<K> {}
