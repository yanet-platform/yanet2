//! LPM structure invariants the views rely on.
//!
//! The lookup algorithm itself lives in the safe SDK crate; this module only
//! states which edges of the C LPM are non-null and how slots are tagged.

use crate::{
    bindings,
    rel::{self, RelRef, Resolver},
    views::{Lpm, LpmChunk, LpmPage, LpmValue},
};

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

impl<'g, R: Resolver<'g>> Lpm<'g, R> {
    /// Chunk directory: one non-null chunk pointer per started chunk.
    #[inline(always)]
    pub fn chunks(&self) -> &'g [RelRef<LpmChunk>] {
        let count = self.page_count().div_ceil(bindings::LPM_CHUNK_SIZE as usize);
        // SAFETY: a validated LPM holds one chunk pointer per started chunk.
        unsafe { rel::resolve_slice(self.resolver(), self.pages(), count) }.unwrap_or(&[])
    }

    /// Root page, or `None` for an LPM that was never initialised.
    #[inline(always)]
    pub fn root_page(&self) -> Option<&'g LpmPage> {
        let res = self.resolver();
        let first = res.resolve(self.pages())?;
        Some(&res.resolve_ref(first)[0])
    }
}
