//! The C LPM as a shared-memory library type.
//!
//! [`Lpm`] mirrors `struct lpm` with its embedded memory context opaque, so
//! a reference to it stays sound while C rewrites the context's sibling
//! link. Validation states every invariant the lookup relies on; the lookup
//! itself lives in the safe SDK crate.

use std::collections::BTreeSet;

use crate::{
    bindings,
    rel::{self, RelRef, Resolver},
    shm::{CObject, Shm, ShmLayout, ValidationError, Validator, fingerprint_mix},
    views::{LpmChunk, LpmPage, LpmRaw, LpmValue},
};

/// Longest-prefix-match table over big-endian keys of `K` bytes.
#[repr(transparent)]
pub struct Lpm<const K: usize> {
    raw: LpmRaw,
}

/// IPv4 LPM.
pub type Lpm4 = Lpm<4>;
/// IPv6 LPM.
pub type Lpm6 = Lpm<16>;

const PAGE_SLOTS: usize = 256;
const CHUNK_PAGES: usize = bindings::LPM_CHUNK_SIZE as usize;
const PAGE_SIZE: usize = core::mem::size_of::<LpmPage>();
/// Upper bound on pages: one per possible slot path of a 16-byte key is far
/// beyond any arena, so this only rejects absurd counts before arithmetic.
const MAX_PAGES: usize = 1 << 24;

impl<const K: usize> Lpm<K> {
    /// Page count.
    pub fn page_count(&self) -> usize {
        *self.raw.page_count()
    }
}

// SAFETY: `Lpm` is a transparent wrapper of a layout-checked mirror of the C
// struct whose bytes are all valid for any pattern (opaque context, offset,
// count); validation walks every edge the lookup follows.
unsafe impl<const K: usize> ShmLayout for Lpm<K> {
    const FINGERPRINT: u64 = fingerprint_mix(0x4c50_4d00, K as u64);

    fn validate(v: &Validator<'_>, lpm: usize) -> Result<(), ValidationError> {
        let invalid = |addr, msg: &str| Err(ValidationError::new(addr, msg));
        v.check(lpm, core::mem::size_of::<Self>(), 8)?;
        let page_count = v.read_u64(lpm + core::mem::offset_of!(bindings::lpm, page_count))? as usize;
        if page_count == 0 || page_count > MAX_PAGES {
            return invalid(lpm, "LPM page count is out of range");
        }
        let chunk_count = page_count.div_ceil(CHUNK_PAGES);
        let Some(directory) = v.edge(lpm + core::mem::offset_of!(bindings::lpm, pages))? else {
            return invalid(lpm, "LPM page directory is NULL");
        };
        v.check(directory, chunk_count * 8, 8)?;

        // The count is not trusted yet: reserve for what one chunk holds.
        let mut pages = Vec::with_capacity(page_count.min(CHUNK_PAGES));
        for chunk_idx in 0..chunk_count {
            let slot = directory + chunk_idx * 8;
            let Some(chunk) = v.edge(slot)? else {
                return invalid(slot, "LPM chunk pointer is NULL");
            };
            v.check(chunk, CHUNK_PAGES * PAGE_SIZE, 8)?;
            let in_chunk = (page_count - chunk_idx * CHUNK_PAGES).min(CHUNK_PAGES);
            pages.extend((0..in_chunk).map(|idx| chunk + idx * PAGE_SIZE));
        }
        let page_set: BTreeSet<usize> = pages.iter().copied().collect();
        if page_set.len() != pages.len() {
            return invalid(directory, "LPM chunk pointers alias the same pages");
        }
        for &page in &pages {
            for slot_idx in 0..PAGE_SLOTS {
                let slot = page + slot_idx * 8;
                let raw = v.read_u64(slot)?;
                if raw & u64::from(bindings::LPM_VALUE_FLAG) != 0 {
                    if raw != u64::MAX && raw >> 1 > u64::from(u32::MAX) {
                        return invalid(slot, "LPM leaf value is out of range");
                    }
                } else if raw == 0 || !page_set.contains(&slot.wrapping_add_signed(raw as i64 as isize)) {
                    return invalid(slot, "LPM child pointer is not a page of this LPM");
                }
            }
        }
        Ok(())
    }

    fn visit_c_objects(addr: usize, out: &mut dyn FnMut(CObject)) {
        out(CObject::Lpm { addr, key_size: K });
    }
}

/// Decoded LPM slot.
pub enum LpmEntry<'a> {
    /// Leaf: the stored value, already shifted past the flag bit.
    Leaf(u32),
    /// Intermediate node: the child page.
    Child(&'a RelRef<LpmPage>),
}

impl LpmValue {
    /// Decodes the slot.
    #[inline(always)]
    pub fn entry(&self) -> LpmEntry<'_> {
        let raw = *self.value();
        if raw & u64::from(bindings::LPM_VALUE_FLAG) != 0 {
            // Narrowing after the shift matches the C lookup bit for bit.
            LpmEntry::Leaf((raw >> 1) as u32)
        } else {
            // SAFETY: a slot with the flag clear is an intermediate node, and
            // a validated LPM gives every intermediate node a child page.
            LpmEntry::Child(unsafe { self.page().assume_non_null() })
        }
    }

    /// Raw 64-bit slot contents.
    #[inline(always)]
    pub fn raw(&self) -> u64 {
        *self.value()
    }
}

impl<'g, const K: usize, R: Resolver<'g>> Shm<'g, Lpm<K>, R> {
    /// Chunk directory: one non-null chunk pointer per started chunk.
    #[inline(always)]
    pub fn chunks(&self) -> &'g [RelRef<LpmChunk>] {
        let raw = &self.get().raw;
        let count = raw.page_count().div_ceil(CHUNK_PAGES);
        // SAFETY: a validated LPM holds one chunk pointer per started chunk.
        unsafe { rel::resolve_slice(self.resolver(), raw.pages(), count) }.unwrap_or(&[])
    }

    /// Root page, or `None` for an LPM that was never initialised.
    #[inline(always)]
    pub fn root_page(&self) -> Option<&'g LpmPage> {
        let res = self.resolver();
        let first = res.resolve(self.get().raw.pages())?;
        Some(&res.resolve_ref(first)[0])
    }
}
