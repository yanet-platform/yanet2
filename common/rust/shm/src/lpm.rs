//! Longest-prefix-match trie mirror over shared memory.

use crate::{memory::MemoryContext, offset::OffsetPtr};

/// Sentinel produced by a lookup that matched no inserted range.
pub const LPM_VALUE_INVALID: u32 = 0xffff_ffff;

/// Flag marking a trie slot that carries a value rather than a child page.
const LPM_VALUE_FLAG: u64 = 0x1;

/// Encodes `value` the way `lpm_insert` stores it in a slot.
pub const fn lpm_value_set(value: u32) -> u64 {
    ((value as u64) << 1) | LPM_VALUE_FLAG
}

/// Decodes a raw slot the way `lpm_lookup` returns it.
pub const fn lpm_value_get(slot: u64) -> u32 {
    // The shift runs on the full 64-bit slot and only the result is
    // narrowed, matching the C walk bit-for-bit: the empty sentinel must
    // narrow to LPM_VALUE_INVALID.
    (slot >> 1) as u32
}

/// Pages per chunk of the two-level page directory.
pub const LPM_CHUNK_SIZE: usize = 16;

/// Max LPM key size in bytes (an IPv6 address).
pub const LPM_KEY_SIZE_MAX: usize = 16;

/// Lanes processed per chunk by [`lpm_lookup_batch`].
pub const LPM_LOOKUP_BATCH_LANES: usize = 32;

/// One trie slot: a value with the flag set, or an offset pointer to the
/// child page when clear.
#[repr(C)]
#[derive(Clone, Copy)]
pub union LpmValue {
    pub page: OffsetPtr<LpmPage>,
    pub value: u64,
}

/// One trie page: 256 slots indexed by the next key byte.
#[repr(C)]
pub struct LpmPage {
    pub values: [LpmValue; 256],
}

impl Default for LpmPage {
    fn default() -> Self {
        // SAFETY: any bit pattern is a valid page body; callers that
        // mirror lpm_init fill it with the all-invalid sentinel instead.
        unsafe { core::mem::zeroed() }
    }
}

/// Mirror of `struct lpm` (`common/lpm.h`).
///
/// The trie is built by the control-plane side (`lpm_insert`) and frozen
/// once published into a generation; the dataplane only walks it.
#[repr(C)]
#[derive(Default)]
pub struct Lpm {
    pub memory_context: MemoryContext,
    /// Offset pointer to an array of offset pointers, one chunk of
    /// [`LPM_CHUNK_SIZE`] contiguous pages per entry.
    pub pages: OffsetPtr<OffsetPtr<LpmPage>>,
    pub page_count: usize,
}

const _: () = assert!(core::mem::size_of::<LpmPage>() == 2048);
const _: () = assert!(core::mem::size_of::<Lpm>() == 144);

/// Address of page `page_idx`, walking the chunked page directory.
///
/// Mirrors `lpm_page()`: the directory is an array of offset pointers to
/// chunks, and the index inside the chunk advances in page units.
///
/// # Safety
///
/// `page_idx` must be below `lpm.page_count` of a trie built by the C
/// control-plane side.
pub unsafe fn lpm_page_ptr(lpm: &Lpm, page_idx: usize) -> *mut LpmPage {
    // SAFETY: caller guarantees the index is inside the directory.
    unsafe {
        let chunks = lpm.pages.resolve();
        let chunk_field = &*chunks.add(page_idx / LPM_CHUNK_SIZE);
        chunk_field.resolve_non_null().add(page_idx % LPM_CHUNK_SIZE)
    }
}

/// Longest-prefix-match of one key.
///
/// Keys are big-endian encoded, `key_size` bytes of `key`. A trie built
/// by `lpm_init` always has a root page, so every lookup terminates with
/// the sentinel [`LPM_VALUE_INVALID`] at worst.
pub fn lpm_lookup(lpm: &Lpm, key_size: u8, key: &[u8]) -> u32 {
    debug_assert!(key_size as usize <= LPM_KEY_SIZE_MAX);
    debug_assert!(key.len() >= key_size as usize);

    // SAFETY: page index 0 exists in any built trie (lpm_init creates it).
    let mut page = unsafe { lpm_page_ptr(lpm, 0) };
    let mut slot = 0xffff_ffff_ffff_ffffu64;
    for key_byte in key[..key_size as usize].iter() {
        // SAFETY: page is a valid page for every hop below key_size; the
        // slot index is a byte value and values is a fixed [LpmValue; 256].
        let entry = unsafe { &(*page).values[*key_byte as usize] };
        // SAFETY: reading the value arm is valid for both slot kinds: a
        // flagged slot stores the encoded value, a clear slot stores the
        // child-page offset, whose flag bit is clear by construction.
        slot = unsafe { entry.value };
        if slot & LPM_VALUE_FLAG != 0 {
            break;
        }
        // SAFETY: an intermediate node (flag clear) always has a child
        // page — the same contract ADDR_OF_NONNULL relies on.
        page = unsafe { entry.page.resolve_non_null() };
    }
    lpm_value_get(slot)
}

/// IPv4 lookup, mirroring `lpm4_lookup`.
pub fn lpm4_lookup(lpm: &Lpm, key: &[u8; 4]) -> u32 {
    lpm_lookup(lpm, 4, key)
}

/// 8-byte lookup, mirroring `lpm8_lookup` (one half of an IPv6 address).
pub fn lpm8_lookup(lpm: &Lpm, key: &[u8; 8]) -> u32 {
    lpm_lookup(lpm, 8, key)
}

/// IPv6 lookup, mirroring `lpm_lookup(lpm, 16, key)`.
pub fn lpm16_lookup(lpm: &Lpm, key: &[u8; 16]) -> u32 {
    lpm_lookup(lpm, 16, key)
}

/// Batched lookup, hop-major across lanes, mirroring `lpm_lookup_batch`.
///
/// `keys` holds `count` consecutive keys of `key_size` bytes each and
/// `results` receives `count` decoded values.
pub fn lpm_lookup_batch(lpm: &Lpm, key_size: u8, keys: &[u8], results: &mut [u32]) {
    let count = results.len();
    debug_assert!(keys.len() >= count * key_size as usize);

    let mut keys = keys;
    let mut results = results;
    let mut remaining_count = count;
    while remaining_count > 0 {
        let lane_count = remaining_count.min(LPM_LOOKUP_BATCH_LANES);

        let mut pages: [*mut LpmPage; LPM_LOOKUP_BATCH_LANES] = [core::ptr::null_mut(); LPM_LOOKUP_BATCH_LANES];
        let mut values = [0u64; LPM_LOOKUP_BATCH_LANES];
        let mut done = [false; LPM_LOOKUP_BATCH_LANES];
        let mut remaining = lane_count;

        for lane in 0..lane_count {
            // SAFETY: page index 0 exists in any built trie.
            pages[lane] = unsafe { lpm_page_ptr(lpm, 0) };
            done[lane] = false;
        }

        for hop in 0..key_size as usize {
            if remaining == 0 {
                break;
            }
            for lane in 0..lane_count {
                if done[lane] {
                    continue;
                }
                // SAFETY: pages[lane] is valid until the lane is done.
                let entry = unsafe { &(*pages[lane]).values[keys[lane * key_size as usize + hop] as usize] };
                // SAFETY: both slot kinds read validly through the value
                // arm, as in the single-key walk.
                let slot = unsafe { entry.value };
                if slot & LPM_VALUE_FLAG != 0 {
                    values[lane] = slot;
                    done[lane] = true;
                    remaining -= 1;
                } else if hop + 1 < key_size as usize {
                    // SAFETY: flag clear means a child page exists.
                    pages[lane] = unsafe { entry.page.resolve_non_null() };
                } else {
                    // No deeper hop is possible, so a clear flag at the
                    // last key byte still terminates the walk.
                    values[lane] = slot;
                    done[lane] = true;
                    remaining -= 1;
                }
            }
        }

        for lane in 0..lane_count {
            results[lane] = lpm_value_get(values[lane]);
        }

        keys = &keys[lane_count * key_size as usize..];
        results = &mut results[lane_count..];
        remaining_count -= lane_count;
    }
}

#[cfg(test)]
mod tests {
    use std::prelude::v1::*;

    use super::*;

    /// The hand-built trie plus the two allocations it points into,
    /// kept alive together.
    struct Fixture {
        lpm: Box<Lpm>,
        _pages: Box<[LpmPage; 2]>,
        _chunk: Box<[OffsetPtr<LpmPage>; 1]>,
    }

    /// A hand-built two-level trie fixture:
    /// - key byte 10 resolves to value 1 at the root,
    /// - key byte 20 descends into a child page where byte 5 is value 2.
    fn fixture() -> Fixture {
        let mut pages: Box<[LpmPage; 2]> = Box::new(core::array::from_fn(|_| LpmPage {
            // The all-invalid sentinel: every slot flagged, value >> 1
            // narrows to LPM_VALUE_INVALID.
            values: [LpmValue { value: 0xffff_ffff_ffff_ffff }; 256],
        }));
        pages[0].values[10] = LpmValue { value: lpm_value_set(1) };
        pages[1].values[5] = LpmValue { value: lpm_value_set(2) };
        let child = unsafe { (pages.as_mut_ptr()).add(1) };
        let mut link = OffsetPtr::<LpmPage>::null();
        link.store(child);

        let mut chunk: Box<[OffsetPtr<LpmPage>; 1]> = Box::new([OffsetPtr::null()]);
        chunk[0].store(pages.as_mut_ptr());

        let mut lpm = Box::new(Lpm {
            memory_context: MemoryContext::default(),
            pages: OffsetPtr::null(),
            page_count: 2,
        });
        // Root page is the chunk's first page; the child link aims
        // straight at the second page of the same chunk.
        lpm.pages.store(chunk.as_mut_ptr());
        pages[0].values[20] = LpmValue { page: link };

        Fixture { lpm, _pages: pages, _chunk: chunk }
    }

    #[test]
    fn lookup_hits_and_misses() {
        let fixture = fixture();

        // Root-level hit.
        assert_eq!(lpm4_lookup(&fixture.lpm, &[10, 0, 0, 0]), 1);
        // Two-level hit through the child page.
        assert_eq!(lpm4_lookup(&fixture.lpm, &[20, 5, 0, 0]), 2);
        // The child page's untouched slots keep the sentinel.
        assert_eq!(lpm4_lookup(&fixture.lpm, &[20, 6, 0, 0]), LPM_VALUE_INVALID);
        // Untouched root slots keep the sentinel too.
        assert_eq!(lpm4_lookup(&fixture.lpm, &[30, 5, 0, 0]), LPM_VALUE_INVALID);

        // Longer keys walk the same trie: an 8-byte key with the same
        // first byte hits the same value.
        assert_eq!(lpm8_lookup(&fixture.lpm, &[10, 1, 2, 3, 4, 5, 6, 7]), 1);
    }

    #[test]
    fn batch_matches_single() {
        let fixture = fixture();
        let keys: Vec<u8> = (0..8).flat_map(|i| [10 + (i % 3) * 5, i, 0, 0]).collect();
        let mut results = [0u32; 8];
        lpm_lookup_batch(&fixture.lpm, 4, &keys, &mut results);
        for (lane, result) in results.iter().enumerate() {
            let single = lpm4_lookup(&fixture.lpm, &[keys[lane * 4], keys[lane * 4 + 1], 0, 0]);
            assert_eq!(*result, single);
        }
        // Lanes beyond one batch iteration work too.
        let mut many = [0u32; 40];
        let mut many_keys = Vec::new();
        for lane in 0..40 {
            many_keys.extend_from_slice(&[10, lane as u8, 0, 0]);
        }
        lpm_lookup_batch(&fixture.lpm, 4, &many_keys, &mut many);
        assert!(many.iter().all(|value| *value == 1));
    }

    #[test]
    fn value_codec_matches_c() {
        assert_eq!(lpm_value_get(lpm_value_set(1)), 1);
        // The empty sentinel narrows to the invalid marker.
        assert_eq!(lpm_value_get(0xffff_ffff_ffff_ffff), LPM_VALUE_INVALID);
    }
}
