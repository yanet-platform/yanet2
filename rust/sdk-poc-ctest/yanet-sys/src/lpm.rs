//! Read-only view of a C `struct lpm` and its control-plane validator.
//!
//! The lookup is a Rust port of the C `lpm_lookup` hot path; the cold path
//! (building the tree) stays in C. The embedded memory context, whose sibling
//! links C may rewrite after publication, is opaque in the mirror, so a
//! reference to the whole tree is sound; the view itself borrows the page
//! table slot, the page table and the pages.

use core::{
    fmt::{self, Display, Formatter},
    mem::{ManuallyDrop, align_of, offset_of, size_of},
    ops::Range,
};

use crate::{
    Root,
    ffi::{LPM_CHUNK_SIZE, LPM_VALUE_FLAG, LPM_VALUE_INVALID, RelPtr, lpm, lpm_page, lpm_value},
};

/// Read-only view of one published LPM tree.
#[derive(Clone, Copy)]
pub struct LpmView<'g> {
    root: Root<'g>,
    first_page: &'g lpm_page,
}

impl<'g> LpmView<'g> {
    /// Attaches to the tree at `lpm`.
    ///
    /// # Safety
    ///
    /// `lpm` must lie in the mapping covered by `root` and its page graph
    /// must have passed [`validate_lpm`] before publication; `root` must
    /// satisfy the [`Root::from_raw`] contract for that graph.
    #[inline]
    pub unsafe fn attach(root: Root<'g>, lpm: &'g lpm) -> Self {
        let pages: &'g RelPtr<RelPtr<lpm_page>> = &lpm.pages;
        debug_assert!(pages.offset() != 0, "a validated tree has a page table");
        // SAFETY: validation proved the page table and its first chunk lie
        // inside the mapping and are aligned; both are frozen for 'g.
        let first_chunk: &'g RelPtr<lpm_page> = unsafe { &*root.resolve_nonnull(pages) };
        // SAFETY: as above; a validated tree has at least one page.
        let first_page = unsafe { &*root.resolve_nonnull(first_chunk) };
        Self { root, first_page }
    }

    /// Value stored for `key`, or `LPM_VALUE_INVALID` when no range covers it.
    ///
    /// Keys are big-endian, as in C. Bit-for-bit equal to `lpm_lookup`,
    /// including the value returned when a walk ends on an intermediate slot.
    #[inline]
    pub fn lookup<const N: usize>(&self, key: &[u8; N]) -> u32 {
        const { assert!(N > 0, "an LPM key has at least one byte") };
        let mut page = self.first_page;
        let mut hop = 0;
        loop {
            let value = &page.values[usize::from(key[hop])];
            let raw = slot_value(value);
            hop += 1;
            if raw & LPM_VALUE_FLAG != 0 || hop == N {
                // The shift runs on the full 64-bit slot and only the result
                // is narrowed, as in C.
                return (raw >> 1) as u32;
            }
            page = self.child(value);
        }
    }

    /// Whether any range covers `key`.
    #[inline]
    pub fn contains<const N: usize>(&self, key: &[u8; N]) -> bool {
        self.lookup(key) != LPM_VALUE_INVALID
    }

    /// Child page of an intermediate slot.
    #[inline(always)]
    fn child(&self, value: &'g lpm_value) -> &'g lpm_page {
        // SAFETY: a clear flag bit selects the page member of the union; its
        // bytes are the same frozen slot, read as a relative pointer.
        let page: &'g ManuallyDrop<RelPtr<lpm_page>> = unsafe { &value.page };
        let slot: &'g RelPtr<lpm_page> = page;
        debug_assert!(slot.offset() != 0, "an intermediate slot has a child");
        // SAFETY: validation proved every intermediate slot points at the
        // start of a page of this tree, inside the mapping and frozen for 'g.
        unsafe { &*self.root.resolve_nonnull(slot) }
    }
}

/// Raw 64-bit content of an LPM slot.
#[inline(always)]
fn slot_value(value: &lpm_value) -> u64 {
    // SAFETY: every bit pattern is a valid `u64`, and the slot is initialised
    // by the C writer before publication.
    unsafe { value.value }
}

/// Reason a tree failed validation.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum LpmError {
    /// An object lies partly or wholly outside the validated bounds.
    OutOfBounds { what: &'static str, addr: usize },
    /// An object is not aligned for its type.
    Misaligned { what: &'static str, addr: usize },
    /// The tree has no page table or no pages.
    NoPages,
    /// A chunk slot of the page table is NULL.
    NullChunk { chunk: usize },
    /// Two chunks overlap.
    OverlappingChunks,
    /// The page table or a chunk overlaps bytes C may write after publication.
    OverlapsMutable { what: &'static str, addr: usize },
    /// An intermediate slot does not point at the start of one of the pages.
    DanglingChild { page: usize, slot: usize },
}

impl Display for LpmError {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        match self {
            Self::OutOfBounds { what, addr } => write!(f, "{what} at {addr:#x} is out of bounds"),
            Self::Misaligned { what, addr } => write!(f, "{what} at {addr:#x} is misaligned"),
            Self::NoPages => write!(f, "tree has no pages"),
            Self::NullChunk { chunk } => write!(f, "chunk {chunk} is NULL"),
            Self::OverlappingChunks => write!(f, "page chunks overlap"),
            Self::OverlapsMutable { what, addr } => write!(f, "{what} at {addr:#x} overlaps C-mutable bytes"),
            Self::DanglingChild { page, slot } => write!(f, "slot {slot} of page {page} has no child page"),
        }
    }
}

impl core::error::Error for LpmError {}

/// Bytes of one chunk of pages.
const CHUNK_BYTES: usize = LPM_CHUNK_SIZE * size_of::<lpm_page>();

/// Checks that the tree at `lpm` is safe to attach.
///
/// Every page table entry, page and child edge must lie inside `bounds`,
/// be aligned, and every intermediate slot must point at the start of one of
/// the tree's own pages. The page table and the pages, which the view
/// borrows, must not overlap any of the `mutable` ranges: bytes C may write
/// after publication. This is the control-plane step before publication;
/// the dataplane attaches without repeating it.
///
/// # Safety
///
/// `root` must carry provenance over `bounds`, every byte in `bounds` must be
/// initialised and not written for the duration of the call, and `lpm` must
/// be derived from `root`.
pub unsafe fn validate_lpm(
    root: Root<'_>,
    lpm: *const lpm,
    bounds: Range<usize>,
    mutable: &[Range<usize>],
) -> Result<(), LpmError> {
    let mem = Bounded { root, bounds, mutable };
    let lpm_addr = lpm.addr();
    mem.check(lpm_addr, size_of::<lpm>(), align_of::<lpm>(), "lpm")?;

    let pages_slot = lpm_addr + offset_of!(lpm, pages);
    // SAFETY: the whole `struct lpm` was bounds-checked above.
    let (pages_offset, page_count) = unsafe {
        (
            mem.read::<isize>(pages_slot),
            mem.read::<usize>(lpm_addr + offset_of!(lpm, page_count)),
        )
    };
    if pages_offset == 0 || page_count == 0 {
        return Err(LpmError::NoPages);
    }

    let chunk_count = page_count.div_ceil(LPM_CHUNK_SIZE);
    let table = pages_slot.wrapping_add_signed(pages_offset);
    let table_len = chunk_count
        .checked_mul(size_of::<RelPtr<lpm_page>>())
        .ok_or(LpmError::OutOfBounds { what: "page table", addr: table })?;
    mem.check_borrowed(table, table_len, align_of::<RelPtr<lpm_page>>(), "page table")?;

    let mut chunks = Vec::with_capacity(chunk_count);
    for chunk in 0..chunk_count {
        let slot = table + chunk * size_of::<RelPtr<lpm_page>>();
        // SAFETY: the page table was bounds-checked above.
        let offset = unsafe { mem.read::<isize>(slot) };
        if offset == 0 {
            return Err(LpmError::NullChunk { chunk });
        }
        let start = slot.wrapping_add_signed(offset);
        mem.check_borrowed(start, CHUNK_BYTES, align_of::<lpm_page>(), "page chunk")?;
        chunks.push((start, chunk));
    }

    // Starts of page chunks sorted by address, for the child-edge search.
    let mut sorted = chunks.clone();
    sorted.sort_unstable();
    if sorted.windows(2).any(|pair| pair[0].0 + CHUNK_BYTES > pair[1].0) {
        return Err(LpmError::OverlappingChunks);
    }
    let is_page_start = |target: usize| {
        let at = sorted.partition_point(|&(start, _)| start <= target);
        let Some(&(start, chunk)) = at.checked_sub(1).and_then(|idx| sorted.get(idx)) else {
            return false;
        };
        let rel = target - start;
        rel < CHUNK_BYTES
            && rel.is_multiple_of(size_of::<lpm_page>())
            && chunk * LPM_CHUNK_SIZE + rel / size_of::<lpm_page>() < page_count
    };

    for page in 0..page_count {
        let page_addr = chunks[page / LPM_CHUNK_SIZE].0 + (page % LPM_CHUNK_SIZE) * size_of::<lpm_page>();
        for slot in 0..256 {
            let slot_addr = page_addr + slot * size_of::<lpm_value>();
            // SAFETY: every chunk was bounds-checked above.
            let raw = unsafe { mem.read::<u64>(slot_addr) };
            if raw & LPM_VALUE_FLAG == 0 && !is_page_start(slot_addr.wrapping_add(raw as usize)) {
                return Err(LpmError::DanglingChild { page, slot });
            }
        }
    }
    Ok(())
}

/// Raw reads confined to a checked address range of one root.
struct Bounded<'a> {
    root: Root<'a>,
    bounds: Range<usize>,
    mutable: &'a [Range<usize>],
}

impl Bounded<'_> {
    fn check(&self, addr: usize, len: usize, align: usize, what: &'static str) -> Result<(), LpmError> {
        let end = addr.checked_add(len).ok_or(LpmError::OutOfBounds { what, addr })?;
        if addr < self.bounds.start || end > self.bounds.end {
            return Err(LpmError::OutOfBounds { what, addr });
        }
        if !addr.is_multiple_of(align) {
            return Err(LpmError::Misaligned { what, addr });
        }
        Ok(())
    }

    /// Like [`Self::check`], and the range must also stay clear of the
    /// mutable ranges because the view will borrow it.
    fn check_borrowed(&self, addr: usize, len: usize, align: usize, what: &'static str) -> Result<(), LpmError> {
        self.check(addr, len, align, what)?;
        if self
            .mutable
            .iter()
            .any(|range| addr < range.end && range.start < addr + len)
        {
            return Err(LpmError::OverlapsMutable { what, addr });
        }
        Ok(())
    }

    /// Reads a `T` at `addr` through the root.
    ///
    /// # Safety
    ///
    /// `addr` must have passed [`Self::check`] for `T`.
    unsafe fn read<T: Copy>(&self, addr: usize) -> T {
        // SAFETY: the caller checked bounds and alignment; the root carries
        // provenance over the bounds and the bytes are initialised.
        unsafe { self.root.as_ptr().with_addr(addr).cast::<T>().read() }
    }
}

#[cfg(test)]
mod tests {
    use core::mem::offset_of;

    use yanet_testkit::{CImage, Family, RawBuf, XorShift, fixture, key_len, random_prefix};

    use super::{LpmError, LpmView, validate_lpm};
    use crate::{Root, body_offset, config_range, ffi::lpm, test_body::TwoTrees};

    /// Raw pointer to one prefix tree of the two-tree configuration at
    /// `root`: IPv4 first, IPv6 second, as the C fixture lays them out.
    fn tree(root: Root<'_>, family: Family) -> *const lpm {
        let field = match family {
            Family::V4 => offset_of!(TwoTrees, first),
            Family::V6 => offset_of!(TwoTrees, second),
        };
        root.as_ptr()
            .wrapping_add(body_offset::<TwoTrees>() + field)
            .cast::<lpm>()
    }

    /// Views of both trees of the configuration at `root`.
    ///
    /// # Safety
    ///
    /// `root` must point at a validated, unwritten two-tree configuration.
    unsafe fn views(root: Root<'_>) -> (LpmView<'_>, LpmView<'_>) {
        // SAFETY: forwarded caller contract; both trees lie in the mapping.
        unsafe {
            (
                LpmView::attach(root, &*tree(root, Family::V4)),
                LpmView::attach(root, &*tree(root, Family::V6)),
            )
        }
    }

    /// Asserts the fixture's recorded C results for the image at `buf`.
    fn assert_fixture_results(buf: &RawBuf) {
        // SAFETY: the copy is a validated C image and is not written.
        let root = unsafe { Root::from_raw(buf.ptr_at(fixture::CONFIG_OFFSET)) };
        // SAFETY: as above.
        let (v4, v6) = unsafe { views(root) };
        for (key, expected) in fixture::keys4() {
            assert_eq!(expected, v4.lookup(&key), "IPv4 key {key:?}");
        }
        for (key, expected) in fixture::keys6() {
            assert_eq!(expected, v6.lookup(&key), "IPv6 key {key:?}");
        }
    }

    #[test]
    fn test_lookup_matches_c_results_on_fixture() {
        assert_fixture_results(&RawBuf::from_bytes(fixture::IMAGE));
    }

    #[test]
    fn test_lookup_matches_c_results_after_remap() {
        let first = RawBuf::from_bytes(fixture::IMAGE);
        assert_fixture_results(&first);
        let second = first.duplicate();
        drop(first);
        assert_fixture_results(&second);
    }

    #[test]
    fn test_validate_accepts_c_built_fixture() {
        let buf = RawBuf::from_bytes(fixture::IMAGE);
        // SAFETY: raw allocation pointer; the copy is not written.
        let root = unsafe { Root::from_raw(buf.ptr_at(fixture::CONFIG_OFFSET)) };
        for family in [Family::V4, Family::V6] {
            // SAFETY: the bounds are the copy's own allocation.
            assert_eq!(Ok(()), unsafe {
                validate_lpm(root, tree(root, family), buf.bounds(), &[])
            });
        }
    }

    /// Address of the first intermediate slot of the first IPv4 page.
    fn first_intermediate_slot(buf: &RawBuf) -> *mut u64 {
        // SAFETY: raw allocation pointer; the copy is only read here.
        let root = unsafe { Root::from_raw(buf.ptr_at(fixture::CONFIG_OFFSET)) };
        let lpm = tree(root, Family::V4);
        // SAFETY: the fixture is a valid tree inside the copy; reads go
        // through the raw root and do not form references.
        unsafe {
            let pages_slot = (&raw const (*lpm).pages).cast::<u8>();
            let table = pages_slot.wrapping_offset((&raw const (*lpm).pages).cast::<isize>().read());
            let chunk = table.wrapping_offset(table.cast::<isize>().read());
            let page = buf.as_ptr().with_addr(chunk.addr()).cast::<u64>();
            (0..256)
                .map(|slot| page.add(slot))
                .find(|slot| slot.read() & 1 == 0)
                .expect("the fixture's first page has an intermediate slot")
        }
    }

    #[test]
    fn test_validate_rejects_child_not_at_page_start() {
        let buf = RawBuf::from_bytes(fixture::IMAGE);
        let slot = first_intermediate_slot(&buf);
        // SAFETY: the slot lies inside the copy; nothing borrows it.
        unsafe { slot.write(slot.read() + 8) };
        // SAFETY: raw allocation pointer; the copy is not written below.
        let root = unsafe { Root::from_raw(buf.ptr_at(fixture::CONFIG_OFFSET)) };
        // SAFETY: the bounds are the copy's own allocation.
        let result = unsafe { validate_lpm(root, tree(root, Family::V4), buf.bounds(), &[]) };
        assert!(
            matches!(result, Err(LpmError::DanglingChild { page: 0, .. })),
            "{result:?}"
        );
    }

    #[test]
    fn test_validate_rejects_page_table_out_of_bounds() {
        let buf = RawBuf::from_bytes(fixture::IMAGE);
        // SAFETY: raw allocation pointer; the copy is written only below.
        let root = unsafe { Root::from_raw(buf.ptr_at(fixture::CONFIG_OFFSET)) };
        let lpm = tree(root, Family::V4);
        // SAFETY: the page table slot lies inside the copy; nothing borrows it.
        unsafe { (&raw const (*lpm).pages).cast::<isize>().cast_mut().write(1 << 40) };
        // SAFETY: the bounds are the copy's own allocation.
        let result = unsafe { validate_lpm(root, lpm, buf.bounds(), &[]) };
        assert!(
            matches!(result, Err(LpmError::OutOfBounds { what: "page table", .. })),
            "{result:?}"
        );
    }

    #[test]
    fn test_validate_rejects_page_table_over_mutable_header() {
        let buf = RawBuf::from_bytes(fixture::IMAGE);
        // SAFETY: raw allocation pointer; the copy is written only below.
        let root = unsafe { Root::from_raw(buf.ptr_at(fixture::CONFIG_OFFSET)) };
        let lpm = tree(root, Family::V4);
        let header = config_range::<TwoTrees>(root);
        // SAFETY: the page table slot lies inside the copy; nothing borrows
        // it. The new offset aims the table at the configuration header.
        unsafe {
            let slot = (&raw const (*lpm).pages).cast::<isize>().cast_mut();
            slot.write(header.start as isize - slot.addr() as isize);
        }
        // SAFETY: the bounds are the copy's own allocation.
        let result = unsafe { validate_lpm(root, lpm, buf.bounds(), core::slice::from_ref(&header)) };
        assert!(
            matches!(result, Err(LpmError::OverlapsMutable { what: "page table", .. })),
            "{result:?}"
        );
    }

    /// Random keys: half next to inserted prefixes, half uniform.
    fn random_keys(rng: &mut XorShift, family: Family, seeds: &[Vec<u8>], count: usize) -> Vec<Vec<u8>> {
        let size = key_len(family);
        (0..count)
            .map(|idx| {
                let mut key = vec![0; size];
                if idx % 2 == 0 {
                    key.copy_from_slice(&seeds[rng.below(seeds.len() as u64) as usize]);
                    key[size - 1 - rng.below(size as u64) as usize] ^= rng.next_u64() as u8;
                } else {
                    rng.fill(&mut key);
                }
                key
            })
            .collect()
    }

    fn lookup(view: &LpmView<'_>, key: &[u8]) -> u32 {
        match key.len() {
            4 => view.lookup::<4>(key.try_into().expect("IPv4 key")),
            16 => view.lookup::<16>(key.try_into().expect("IPv6 key")),
            len => panic!("unexpected key length {len}"),
        }
    }

    /// Verifies that the Rust port returns the C result for 200 000 random
    /// keys per family, on the C image and on a byte copy after the C image
    /// is freed.
    #[test]
    #[cfg_attr(miri, ignore = "calls C")]
    fn test_lookup_differential_against_c_with_remap() {
        const KEYS: usize = 200_000;
        let mut rng = XorShift::new(0x5eed_1234);
        let mut image = CImage::new(64 << 20);
        let mut seeds: [Vec<Vec<u8>>; 2] = [Vec::new(), Vec::new()];
        for (family, count, lens, seeds) in [(Family::V4, 4000, 8..=32), (Family::V6, 1500, 16..=64)]
            .into_iter()
            .zip(seeds.iter_mut())
            .map(|((family, count, lens), seeds)| (family, count, lens, seeds))
        {
            for _ in 0..count {
                let (from, to) = random_prefix(&mut rng, key_len(family), lens.clone());
                image.insert(family, &from, &to, rng.below(1 << 30) as u32);
                seeds.push(from);
            }
        }
        let keys = [
            random_keys(&mut rng, Family::V4, &seeds[0], KEYS),
            random_keys(&mut rng, Family::V6, &seeds[1], KEYS),
        ];
        let expected: Vec<Vec<u32>> = [Family::V4, Family::V6]
            .iter()
            .zip(&keys)
            .map(|(&family, keys)| keys.iter().map(|key| image.lookup(family, key)).collect())
            .collect();
        let hits = expected
            .iter()
            .flatten()
            .filter(|&&v| v != super::LPM_VALUE_INVALID)
            .count();
        assert!(hits > KEYS / 4, "keys must exercise matching prefixes, got {hits} hits");

        let check = |root: Root<'_>, bounds: core::ops::Range<usize>| {
            for family in [Family::V4, Family::V6] {
                // SAFETY: the image is live, C-built and not written.
                assert_eq!(Ok(()), unsafe {
                    validate_lpm(root, tree(root, family), bounds.clone(), &[])
                });
            }
            // SAFETY: validated above; not written while used.
            let views = unsafe { views(root) };
            for ((view, keys), expected) in [views.0, views.1].iter().zip(&keys).zip(&expected) {
                for (key, &want) in keys.iter().zip(expected) {
                    assert_eq!(want, lookup(view, key), "key {key:?}");
                }
            }
        };

        let base = image.base().addr().get();
        // SAFETY: the C image is not modified while the root is used.
        check(unsafe { Root::from_raw(image.config_ptr()) }, base..base + image.len());
        let config_offset = image.config_offset();
        let copy = image.copy_to_raw();
        drop(image);
        // SAFETY: the copy is not written while the root is used.
        check(unsafe { Root::from_raw(copy.ptr_at(config_offset)) }, copy.bounds());
    }
}
