//! Test-only fixtures backed by the real C code.
//!
//! [`CImage`] builds decap configurations with the C `lpm_insert`, [`fixture`]
//! embeds one such image for Miri, which cannot call C, and [`RawBuf`] holds
//! byte copies of images at fresh addresses. With the `dataplane` feature,
//! [`dataplane`] runs the C or a Rust module handler over parsed packets.

use core::{
    alloc::Layout,
    ptr::{self, NonNull},
};
use std::alloc::{alloc_zeroed, dealloc};

mod sys {
    #[repr(C)]
    pub struct tk_image {
        pub base: *mut u8,
        pub size: usize,
        pub config_offset: usize,
    }

    unsafe extern "C" {
        pub fn tk_image_new(size: usize) -> *mut tk_image;
        pub fn tk_image_free(image: *mut tk_image);
        pub fn tk_image_insert(image: *mut tk_image, v6: i32, from: *const u8, to: *const u8, value: u32) -> i32;
        pub fn tk_image_lookup(image: *const tk_image, v6: i32, key: *const u8) -> u32;
        pub fn tk_image_lookup_many(image: *const tk_image, v6: i32, keys: *const u8, count: usize, results: *mut u32);
    }
}

/// Address family of one of the two decap prefix trees.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Family {
    V4,
    V6,
}

impl Family {
    fn flag(self) -> i32 {
        match self {
            Self::V4 => 0,
            Self::V6 => 1,
        }
    }
}

/// A decap configuration image built and queried by the C LPM code.
pub struct CImage {
    raw: NonNull<sys::tk_image>,
}

impl CImage {
    /// Empty image with `size` bytes in total.
    pub fn new(size: usize) -> Self {
        // SAFETY: plain constructor call; NULL is handled.
        let raw = NonNull::new(unsafe { sys::tk_image_new(size) }).expect("C image allocation failed");
        Self { raw }
    }

    fn image(&self) -> &sys::tk_image {
        // SAFETY: the image is live until drop.
        unsafe { self.raw.as_ref() }
    }

    /// Inserts the inclusive big-endian range with the C `lpm_insert`.
    pub fn insert(&mut self, family: Family, from: &[u8], to: &[u8], value: u32) {
        assert_eq!(from.len(), to.len());
        assert_eq!(from.len(), key_len(family));
        // SAFETY: both keys have the family's key length.
        let rc = unsafe { sys::tk_image_insert(self.raw.as_ptr(), family.flag(), from.as_ptr(), to.as_ptr(), value) };
        assert_eq!(0, rc, "C lpm_insert failed");
    }

    /// Looks `key` up with the C `lpm_lookup`.
    pub fn lookup(&self, family: Family, key: &[u8]) -> u32 {
        assert_eq!(key.len(), key_len(family));
        // SAFETY: the key has the family's key length.
        unsafe { sys::tk_image_lookup(self.raw.as_ptr(), family.flag(), key.as_ptr()) }
    }

    /// Looks up consecutive keys in one C loop with the inlined C lookup.
    pub fn lookup_many(&self, family: Family, keys: &[u8], results: &mut [u32]) {
        assert_eq!(keys.len(), results.len() * key_len(family));
        // SAFETY: `keys` holds one key per result slot.
        unsafe {
            sys::tk_image_lookup_many(
                self.raw.as_ptr(),
                family.flag(),
                keys.as_ptr(),
                results.len(),
                results.as_mut_ptr(),
            )
        }
    }

    /// Size of the image in bytes.
    pub fn len(&self) -> usize {
        self.image().size
    }

    /// Whether the image is empty; never true for a constructed image.
    pub fn is_empty(&self) -> bool {
        self.len() == 0
    }

    /// Offset of the decap configuration inside the image.
    pub fn config_offset(&self) -> usize {
        self.image().config_offset
    }

    /// Raw base pointer of the image allocation, with its full provenance.
    pub fn base(&self) -> NonNull<u8> {
        NonNull::new(self.image().base).expect("a live image has a base")
    }

    /// Raw pointer to the decap configuration, derived from the C base.
    pub fn config_ptr(&self) -> NonNull<u8> {
        // SAFETY: the configuration offset lies inside the C allocation.
        unsafe { self.base().add(self.config_offset()) }
    }

    /// Byte copy of the image at a fresh address.
    pub fn copy_to_raw(&self) -> RawBuf {
        let buf = RawBuf::zeroed(self.len());
        // SAFETY: both regions are live, disjoint and `len` bytes long.
        unsafe { ptr::copy_nonoverlapping(self.base().as_ptr(), buf.as_ptr(), self.len()) };
        buf
    }
}

impl Drop for CImage {
    fn drop(&mut self) {
        // SAFETY: the image is owned and freed exactly once.
        unsafe { sys::tk_image_free(self.raw.as_ptr()) }
    }
}

/// Key length in bytes of a family.
pub fn key_len(family: Family) -> usize {
    match family {
        Family::V4 => 4,
        Family::V6 => 16,
    }
}

/// Zeroed, 64-byte aligned heap bytes handled only through a raw pointer.
///
/// No Rust reference to the buffer is ever formed, so the base pointer keeps
/// the provenance of the whole allocation, as a mapping pointer from C does.
pub struct RawBuf {
    ptr: NonNull<u8>,
    layout: Layout,
}

impl RawBuf {
    /// Allocates `len` zeroed bytes.
    pub fn zeroed(len: usize) -> Self {
        let layout = Layout::from_size_align(len, 64).expect("valid layout");
        // SAFETY: the layout has a non-zero size.
        let ptr = NonNull::new(unsafe { alloc_zeroed(layout) }).expect("allocation failed");
        Self { ptr, layout }
    }

    /// Copy of `bytes` at a fresh address.
    pub fn from_bytes(bytes: &[u8]) -> Self {
        let buf = Self::zeroed(bytes.len());
        // SAFETY: both regions are live, disjoint and of equal length.
        unsafe { ptr::copy_nonoverlapping(bytes.as_ptr(), buf.as_ptr(), bytes.len()) };
        buf
    }

    /// Copy of this buffer at a fresh address.
    pub fn duplicate(&self) -> Self {
        let buf = Self::zeroed(self.len());
        // SAFETY: both regions are live, disjoint and of equal length.
        unsafe { ptr::copy_nonoverlapping(self.as_ptr(), buf.as_ptr(), self.len()) };
        buf
    }

    /// Base pointer with the provenance of the whole allocation.
    pub fn as_ptr(&self) -> *mut u8 {
        self.ptr.as_ptr()
    }

    /// Length in bytes.
    pub fn len(&self) -> usize {
        self.layout.size()
    }

    /// Whether the buffer is empty; never true for an allocated buffer.
    pub fn is_empty(&self) -> bool {
        self.len() == 0
    }

    /// Address range of the buffer.
    pub fn bounds(&self) -> core::ops::Range<usize> {
        self.ptr.addr().get()..self.ptr.addr().get() + self.len()
    }

    /// Interior raw pointer `offset` bytes into the buffer, derived from the
    /// allocation pointer without any reference in between.
    pub fn ptr_at(&self, offset: usize) -> NonNull<u8> {
        assert!(offset < self.len());
        // SAFETY: the offset is inside the allocation.
        unsafe { self.ptr.add(offset) }
    }
}

impl Drop for RawBuf {
    fn drop(&mut self) {
        // SAFETY: allocated in `zeroed` with this layout.
        unsafe { dealloc(self.ptr.as_ptr(), self.layout) }
    }
}

/// Deterministic xorshift64 generator for reproducible random tests.
pub struct XorShift(u64);

impl XorShift {
    /// Generator with a non-zero seed.
    pub fn new(seed: u64) -> Self {
        Self(seed.max(1))
    }

    /// Next 64 random bits.
    pub fn next_u64(&mut self) -> u64 {
        let Self(state) = self;
        *state ^= *state << 13;
        *state ^= *state >> 7;
        *state ^= *state << 17;
        *state
    }

    /// Uniform value in `0..bound`.
    pub fn below(&mut self, bound: u64) -> u64 {
        self.next_u64() % bound
    }

    /// Fills `bytes` with random data.
    pub fn fill(&mut self, bytes: &mut [u8]) {
        for byte in bytes {
            *byte = self.next_u64() as u8;
        }
    }
}

/// Inclusive range `[from, to]` of a random prefix with length in `lens`.
pub fn random_prefix(rng: &mut XorShift, size: usize, lens: core::ops::RangeInclusive<u32>) -> (Vec<u8>, Vec<u8>) {
    let len = lens.start() + rng.below(u64::from(lens.end() - lens.start() + 1)) as u32;
    let mut from = vec![0; size];
    rng.fill(&mut from);
    let mut to = from.clone();
    for bit in len as usize..size * 8 {
        from[bit / 8] &= !(0x80 >> (bit % 8));
        to[bit / 8] |= 0x80 >> (bit % 8);
    }
    (from, to)
}

/// The C-built image embedded for Miri, with keys and C lookup results.
pub mod fixture {
    include!(concat!(env!("OUT_DIR"), "/fixture.rs"));

    /// Image bytes as written by the C generator.
    pub static IMAGE: &[u8] = include_bytes!(concat!(env!("OUT_DIR"), "/image.bin"));
    static KEYS4: &[u8] = include_bytes!(concat!(env!("OUT_DIR"), "/keys4.bin"));
    static KEYS6: &[u8] = include_bytes!(concat!(env!("OUT_DIR"), "/keys6.bin"));

    /// IPv4 keys with the result the C lookup returned for each.
    pub fn keys4() -> impl Iterator<Item = ([u8; 4], u32)> {
        records(KEYS4)
    }

    /// IPv6 keys with the result the C lookup returned for each.
    pub fn keys6() -> impl Iterator<Item = ([u8; 16], u32)> {
        records(KEYS6)
    }

    // A record is a key plus a result; its size is not a const expression
    // the chunking helpers accept.
    #[allow(clippy::chunks_exact_to_as_chunks)]
    fn records<const N: usize>(bytes: &'static [u8]) -> impl Iterator<Item = ([u8; N], u32)> {
        bytes.chunks_exact(N + 4).map(|record| {
            let (key, result) = record.split_at(N);
            (
                key.try_into().expect("record holds a key"),
                u32::from_ne_bytes(result.try_into().expect("record holds a result")),
            )
        })
    }
}

#[cfg(feature = "dataplane")]
pub mod dataplane;
