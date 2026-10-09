//! Attribute-classifier mirrors (`lib/classify` and `lib/filter` halves).

use crate::lpm::{Lpm, lpm4_lookup, lpm8_lookup};
use crate::value::{ValueTable, value_table_get};

/// Marks a high-half trie value whose result row lives in the join table.
///
/// The trie stores values shifted left by one, so the mark sits in a bit
/// the trie preserves; classes and dense rows stay below it.
pub const FILTER_NET6_ROW_MARK: u32 = 0x4000_0000;

/// IPv6 address length in bytes.
pub const NET6_LEN: usize = 16;

/// Mirror of `struct classify_attr_net4` (`lib/classify/classifiers/net4.h`)
/// and of the filter-side net4 leaf payload: a lone LPM over the address.
#[repr(C)]
#[derive(Default)]
pub struct ClassifyAttrNet4 {
    pub lpm: Lpm,
}

/// Resolve the class of every big-endian address of a batch, mirroring
/// `classify_net4_lookup`.
pub fn classify_net4_lookup(attr: &ClassifyAttrNet4, addrs: &[u32], results: &mut [u32]) {
    debug_assert_eq!(addrs.len(), results.len());
    for (idx, addr) in addrs.iter().enumerate() {
        results[idx] = lpm4_lookup(&attr.lpm, &addr.to_be_bytes());
    }
}

/// Mirror of `struct classify_attr_net6`
/// (`lib/classify/classifiers/net6.h`) and of `struct net6_classifier`
/// (`lib/filter/classifiers/net6.h`) — identical shapes: two half-trie
/// LPMs plus their join table.
#[repr(C)]
#[derive(Default)]
pub struct ClassifyAttrNet6 {
    pub hi: Lpm,
    pub lo: Lpm,
    pub comb: ValueTable,
}

/// The filter-side spelling of the net6 classifier.
pub type Net6Classifier = ClassifyAttrNet6;

/// Resolve the class of one whole IPv6 address, mirroring
/// `classify_net6_lookup`.
///
/// The high half alone decides unless its value carries
/// [`FILTER_NET6_ROW_MARK`], in which case the low half indexes the
/// marked row of the join table.
pub fn classify_net6_lookup(attr: &ClassifyAttrNet6, addr: &[u8; NET6_LEN]) -> u32 {
    let hi = lpm8_lookup(&attr.hi, &addr[..8].try_into().expect("half"));
    if hi & FILTER_NET6_ROW_MARK == 0 {
        return hi;
    }
    let lo = lpm8_lookup(&attr.lo, &addr[8..].try_into().expect("half"));
    value_table_get(&attr.comb, hi & !FILTER_NET6_ROW_MARK, lo)
}

/// Mirror of `struct proto_range_classifier`
/// (`lib/filter/classifiers/proto_range.h`).
///
/// A dense line over `protocol * 256 + subtype`, plus the classes for
/// packets whose transport header is unavailable.
#[repr(C)]
pub struct ProtoRangeClassifier {
    pub line: crate::value::Vline,
    pub unavailable_classes: [u32; PROTO_UNAVAILABLE_CLASS_COUNT],
}

/// Count of protocols whose classification reads a subtype byte.
pub const PROTO_UNAVAILABLE_CLASS_COUNT: usize = 3;

/// Indices into [`ProtoRangeClassifier::unavailable_classes`].
pub const PROTO_UNAVAILABLE_TCP: usize = 0;
pub const PROTO_UNAVAILABLE_ICMP: usize = 1;
pub const PROTO_UNAVAILABLE_ICMPV6: usize = 2;

impl Default for ProtoRangeClassifier {
    fn default() -> Self {
        // SAFETY: all-zero is the valid unbuilt state (null line values,
        // no unavailable classes).
        unsafe { core::mem::zeroed() }
    }
}

const _: () = assert!(core::mem::size_of::<ClassifyAttrNet4>() == 144);
const _: () = assert!(core::mem::size_of::<ClassifyAttrNet6>() == 312);

#[cfg(test)]
mod tests {
    use std::prelude::v1::*;

    use super::*;
    use crate::lpm::{LpmPage, LpmValue, lpm_value_set};
    use crate::offset::OffsetPtr;

    fn net6_fixture() -> (Box<ClassifyAttrNet6>, Box<[LpmPage; 3]>) {
        // Pages 0-1 form the hi trie: the 0x20 region descends and its
        // 0x01 child byte carries the row mark; every other slot holds
        // class 7 directly. Page 2 is the lo trie's single page, all 7 —
        // the low half of any address resolves column 7.
        let mut pages: Box<[LpmPage; 3]> = Box::new(core::array::from_fn(|_| LpmPage {
            values: [LpmValue { value: lpm_value_set(7) }; 256],
        }));
        pages[1].values[0x01] = LpmValue {
            value: lpm_value_set(FILTER_NET6_ROW_MARK | 3),
        };
        let hi_child = unsafe { (pages.as_mut_ptr()).add(1) };
        let mut link = OffsetPtr::<LpmPage>::null();
        link.store(hi_child);
        pages[0].values[0x20] = LpmValue { page: link };

        let mut hi_dir: Box<[OffsetPtr<LpmPage>; 1]> = Box::new([OffsetPtr::null()]);
        hi_dir[0].store(pages.as_mut_ptr());
        let mut lo_dir: Box<[OffsetPtr<LpmPage>; 1]> = Box::new([OffsetPtr::null()]);
        lo_dir[0].store(unsafe { (pages.as_mut_ptr()).add(2) });

        let mut attr = Box::new(ClassifyAttrNet6::default());
        attr.hi.pages.store(hi_dir.as_mut_ptr());
        attr.hi.page_count = 2;
        attr.lo.pages.store(lo_dir.as_mut_ptr());
        attr.lo.page_count = 1;

        // Join table row 3: column 7 maps to class 11.
        let mut rows: Box<[[u32; 1]]> = Box::new([[0], [0], [0], [11]]);
        let mut chunks: Box<[OffsetPtr<u32>; 4]> = Box::new(core::array::from_fn(|_| OffsetPtr::null()));
        for (idx, row) in rows.iter_mut().enumerate() {
            chunks[idx].store(row.as_mut_ptr());
        }
        attr.comb.v_dim = 4;
        attr.comb.h_dim = 1;
        attr.comb.values.store(chunks.as_mut_ptr());

        (attr, pages)
    }

    #[test]
    fn net6_marked_row_joins_low_half() {
        let (attr, _pages) = net6_fixture();
        // 2001:... hits the marked hi value; the low half (all 0x07 here)
        // resolves column 7 of row 3.
        let mut marked = [0u8; 16];
        marked[..2].copy_from_slice(&[0x20, 0x01]);
        marked[8..].copy_from_slice(&[0x07; 8]);
        assert_eq!(classify_net6_lookup(&attr, &marked), 11);

        // Any other first byte holds class 7 directly from the hi trie.
        let mut plain = [0u8; 16];
        plain[0] = 0x40;
        plain[8..].copy_from_slice(&[0x07; 8]);
        assert_eq!(classify_net6_lookup(&attr, &plain), 7);
    }

    #[test]
    fn net4_batch_resolves_words() {
        let mut pages: Box<[LpmPage; 1]> = Box::new(core::array::from_fn(|_| LpmPage {
            values: [LpmValue { value: lpm_value_set(9) }; 256],
        }));
        let mut dir: Box<[OffsetPtr<LpmPage>; 1]> = Box::new([OffsetPtr::null()]);
        dir[0].store(pages.as_mut_ptr());
        let mut attr = Box::new(ClassifyAttrNet4::default());
        attr.lpm.pages.store(dir.as_mut_ptr());
        attr.lpm.page_count = 1;

        let addrs = [u32::from_be_bytes([1, 2, 3, 4]), 0];
        let mut results = [0u32; 2];
        classify_net4_lookup(&attr, &addrs, &mut results);
        assert_eq!(results, [9, 9]);
    }
}
