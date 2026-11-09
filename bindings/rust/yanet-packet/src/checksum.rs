//! One's-complement checksum arithmetic.
//!
//! The incremental helpers port the C checksum helpers bit for bit. All values
//! are numeric 16-bit words read in network byte order; the C code keeps the
//! same values in host order, which is a byte swap of the same arithmetic.

use core::net::{Ipv4Addr, Ipv6Addr};

/// Unfolded one's-complement sum of the bytes as big-endian 16-bit words.
///
/// An odd trailing byte is the high half of a final word. The sum runs over
/// 32-bit words, half the additions of 16-bit ones; as 2^16 is 1 modulo
/// 0xffff, it folds to the same value as the 16-bit word sum (RFC 1071,
/// section 2). The 64-bit accumulator cannot overflow below 16 GiB of data,
/// unlike the 32-bit C one.
fn partial_sum(data: &[u8]) -> u64 {
    let (chunks, remainder) = data.as_chunks::<4>();
    let mut acc: u64 = chunks.iter().map(|&chunk| u64::from(u32::from_be_bytes(chunk))).sum();
    match *remainder {
        [] => {}
        [hi] => acc += u64::from(u16::from_be_bytes([hi, 0])),
        [hi, lo] => acc += u64::from(u16::from_be_bytes([hi, lo])),
        [b0, b1, hi, ..] => acc += u64::from(u16::from_be_bytes([b0, b1])) + u64::from(u16::from_be_bytes([hi, 0])),
    }
    acc
}

/// Folds a wide accumulator into 16 bits by end-around carry.
fn fold(mut acc: u64) -> u16 {
    while acc >> 16 != 0 {
        acc = (acc >> 16) + (acc & 0xffff);
    }
    acc as u16
}

/// Folded one's-complement sum of the bytes, not complemented.
///
/// Equals the C sum of the same bytes up to a byte swap of the result.
pub fn sum(data: &[u8]) -> u16 {
    fold(partial_sum(data))
}

/// Unfolded one's-complement sum of the slices' bytes taken in order as one
/// run, and the length of the run.
///
/// The run's words start at its even bytes, so a slice that starts at an odd
/// byte holds each byte in the other half of a word than its own sum reads it
/// in: that sum is the byte swap of the slice's share (RFC 1071, section 2).
/// The slices that start at odd bytes are summed together, and their sum is
/// folded before the swap, which keeps its carries.
fn run_sum<'a>(slices: impl IntoIterator<Item = &'a [u8]>) -> (u64, usize) {
    let (mut even, mut odd, mut len) = (0, 0, 0);
    for slice in slices {
        if len % 2 == 0 {
            even += partial_sum(slice);
        } else {
            odd += partial_sum(slice);
        }
        len += slice.len();
    }
    (even + u64::from(fold(odd).swap_bytes()), len)
}

/// Folded one's-complement sum of the slices' bytes taken in order as one
/// run, not complemented: [`sum`] of their concatenation, without copying it.
///
/// Sums a run that lies in several buffers, such as the segments of a chained
/// packet.
pub fn sum_slices<'a>(slices: impl IntoIterator<Item = &'a [u8]>) -> u16 {
    fold(run_sum(slices).0)
}

/// One's-complement addition with end-around carry.
fn plus(val0: u16, val1: u16) -> u16 {
    let sum = val0.wrapping_add(val1);
    if sum < val0 { sum.wrapping_add(1) } else { sum }
}

/// One's-complement subtraction with end-around borrow.
fn minus(val0: u16, val1: u16) -> u16 {
    let sum = val0.wrapping_sub(val1);
    if sum > val0 { sum.wrapping_sub(1) } else { sum }
}

fn replace_words(cksum: u16, old: &[u8], new: &[u8]) -> u16 {
    let (olds, _) = old.as_chunks::<2>();
    let (news, _) = new.as_chunks::<2>();
    let mut acc = !cksum;
    for (&old, &new) in olds.iter().zip(news) {
        acc = minus(acc, u16::from_be_bytes(old));
        acc = plus(acc, u16::from_be_bytes(new));
    }
    acc
}

/// Replaces one word covered by a checksum and returns the new checksum.
///
/// Port of the DSCP rewrite of the C code, which is the form used for IPv4
/// header checksums: the complement of an all-ones sum is stored as zero.
pub fn update(cksum: u16, old: u16, new: u16) -> u16 {
    !plus(minus(!cksum, old), new)
}

/// Same as [`update`], but never produces zero.
///
/// Port of the MSS clamp of the C code: an all-ones sum is kept as all-ones.
/// Required for UDP, where a zero checksum means "no checksum".
pub fn update_no_zero(cksum: u16, old: u16, new: u16) -> u16 {
    no_zero(plus(minus(!cksum, old), new))
}

/// Replaces a run of consecutive covered words by new ones.
///
/// Both runs are read as whole big-endian words, a shorter run limits the
/// count and an odd tail byte is ignored. Used for 4- and 16-byte addresses.
pub fn update_bytes(cksum: u16, old: &[u8], new: &[u8]) -> u16 {
    !replace_words(cksum, old, new)
}

/// Same as [`update_bytes`], but never produces zero.
pub fn update_bytes_no_zero(cksum: u16, old: &[u8], new: &[u8]) -> u16 {
    no_zero(replace_words(cksum, old, new))
}

fn no_zero(inverted: u16) -> u16 {
    if inverted == 0xffff { inverted } else { !inverted }
}

/// Unfolded sum of the IPv4 pseudo header; only its [`fold`] is meaningful.
fn pseudo_sum_ipv4(src: [u8; 4], dst: [u8; 4], protocol: u8, l4_len: u16) -> u64 {
    partial_sum(&src) + partial_sum(&dst) + u64::from(protocol) + u64::from(l4_len)
}

/// Unfolded sum of the IPv6 pseudo header; only its [`fold`] is meaningful.
fn pseudo_sum_ipv6(src: [u8; 16], dst: [u8; 16], next_header: u8, l4_len: u32) -> u64 {
    partial_sum(&src)
        + partial_sum(&dst)
        + u64::from(l4_len >> 16)
        + u64::from(l4_len & 0xffff)
        + u64::from(next_header)
}

fn l4_checksum(pseudo: u64, segment: &[u8]) -> u16 {
    !fold(pseudo + partial_sum(segment))
}

/// Whether the pseudo header's and the segment's unfolded sums add up to an
/// all-ones sum.
fn l4_is_valid(pseudo: u64, segment: u64) -> bool {
    fold(pseudo + segment) == 0xffff
}

/// Full L4 checksum over IPv4 of a segment whose checksum field is zero.
///
/// The pseudo-header length is the low 16 bits of the segment length: a
/// segment longer than 65535 bytes cannot be an IPv4 one. A UDP checksum that
/// computes to zero is sent as `0xffff`: zero in the UDP field means no
/// checksum (RFC 768); the caller maps it.
pub fn l4_checksum_ipv4(src: Ipv4Addr, dst: Ipv4Addr, protocol: u8, segment: &[u8]) -> u16 {
    l4_checksum(
        pseudo_sum_ipv4(src.octets(), dst.octets(), protocol, segment.len() as u16),
        segment,
    )
}

/// Full L4 checksum over IPv6 of a segment, as [`l4_checksum_ipv4`].
///
/// The protocol number is the upper-layer one, which follows any extension
/// headers, not the next header field of the fixed header.
pub fn l4_checksum_ipv6(src: Ipv6Addr, dst: Ipv6Addr, next_header: u8, segment: &[u8]) -> u16 {
    l4_checksum(
        pseudo_sum_ipv6(src.octets(), dst.octets(), next_header, segment.len() as u32),
        segment,
    )
}

/// Verifies over IPv4 an L4 segment that includes its checksum field.
///
/// The pseudo-header length is taken as in [`l4_checksum_ipv4`].
pub fn l4_is_valid_ipv4(src: Ipv4Addr, dst: Ipv4Addr, protocol: u8, segment: &[u8]) -> bool {
    l4_is_valid(
        pseudo_sum_ipv4(src.octets(), dst.octets(), protocol, segment.len() as u16),
        partial_sum(segment),
    )
}

/// Verifies over IPv6 an L4 segment that includes its checksum field.
///
/// The protocol is the upper-layer one, as in [`l4_checksum_ipv6`].
pub fn l4_is_valid_ipv6(src: Ipv6Addr, dst: Ipv6Addr, next_header: u8, segment: &[u8]) -> bool {
    l4_is_valid(
        pseudo_sum_ipv6(src.octets(), dst.octets(), next_header, segment.len() as u32),
        partial_sum(segment),
    )
}

/// Verifies over IPv4 an L4 segment that includes its checksum field and lies
/// in several slices, taken in order as one run: [`l4_is_valid_ipv4`] of their
/// concatenation, without copying it.
pub fn l4_is_valid_ipv4_slices<'a>(
    src: Ipv4Addr,
    dst: Ipv4Addr,
    protocol: u8,
    segment: impl IntoIterator<Item = &'a [u8]>,
) -> bool {
    let (sum, len) = run_sum(segment);
    l4_is_valid(pseudo_sum_ipv4(src.octets(), dst.octets(), protocol, len as u16), sum)
}

/// Verifies over IPv6 an L4 segment that lies in several slices, as
/// [`l4_is_valid_ipv4_slices`] does over IPv4: [`l4_is_valid_ipv6`] of their
/// concatenation.
pub fn l4_is_valid_ipv6_slices<'a>(
    src: Ipv6Addr,
    dst: Ipv6Addr,
    next_header: u8,
    segment: impl IntoIterator<Item = &'a [u8]>,
) -> bool {
    let (sum, len) = run_sum(segment);
    l4_is_valid(
        pseudo_sum_ipv6(src.octets(), dst.octets(), next_header, len as u32),
        sum,
    )
}

#[cfg(test)]
mod tests {
    use proptest::{collection::vec, prelude::*, sample::Index};

    use super::*;
    use crate::testing;

    /// The slices the cuts split the bytes into, in order; repeated cuts make
    /// empty slices.
    fn split<'a>(data: &'a [u8], cuts: &[Index]) -> Vec<&'a [u8]> {
        let mut ends: Vec<usize> = cuts.iter().map(|cut| cut.index(data.len() + 1)).collect();
        ends.sort_unstable();
        let mut start = 0;
        let mut parts = Vec::with_capacity(ends.len() + 1);
        for end in ends {
            parts.push(&data[start..end]);
            start = end;
        }
        parts.push(&data[start..]);
        parts
    }

    /// The C reducer: folds a 32-bit accumulator with two end-around
    /// carries; the second fold cannot overflow 16 bits.
    fn reduce(cksum: u32) -> u16 {
        let cksum = (cksum >> 16) + (cksum & 0xffff);
        let cksum = (cksum >> 16) + (cksum & 0xffff);
        cksum as u16
    }

    /// The C checksum of the bytes: little-endian words, reduced, in host
    /// order.
    fn c_csum(data: &[u8]) -> u16 {
        let mut result: u32 = 0;
        let mut rest = data;
        while let [a, b, tail @ ..] = rest {
            result = result.wrapping_add(u32::from(u16::from_le_bytes([*a, *b])));
            rest = tail;
        }
        if let [a] = rest {
            result = result.wrapping_add(u32::from(*a));
        }
        reduce(result)
    }

    /// The C one's-complement addition, the oracle for `plus`.
    fn c_plus(val0: u16, val1: u16) -> u16 {
        let mut sum = val0.wrapping_add(val1);
        if sum < val0 {
            sum += 1;
        }
        sum
    }

    /// The C one's-complement subtraction, the oracle for `minus`.
    fn c_minus(val0: u16, val1: u16) -> u16 {
        let mut sum = val0.wrapping_sub(val1);
        if sum > val0 {
            sum -= 1;
        }
        sum
    }

    #[test]
    fn test_sum_odd_tail_is_high_byte() {
        assert_eq!(0x0102 + 0x0300, sum(&[1, 2, 3]));
    }

    #[test]
    fn test_sum_slices_odd_start_is_low_byte() {
        assert_eq!(0x0102 + 0x0300, sum_slices([&[1u8][..], &[2, 3]]));
        assert_eq!(0x0102 + 0x0300, sum_slices([&[1u8][..], &[], &[2], &[3]]));
        assert_eq!(0, sum_slices([]));
    }

    #[test]
    fn test_sum_slices_odd_start_keeps_carries() {
        // The second slice sums to 0x1_0000_feff unfolded: a swap of its low
        // 16 bits would give 0xfffe.
        let data = [0, 0xff, 0xff, 0xff, 0xff, 0xff];
        assert_eq!(0x00ff, sum(&data));
        assert_eq!(0x00ff, sum_slices([&data[..1], &data[1..]]));
    }

    #[test]
    fn test_reduce_two_folds() {
        assert_eq!(0xffff, reduce(0xffff));
        assert_eq!(0x0001, reduce(0x1_0000));
        assert_eq!(0xffff, reduce(0x1_fffe));
        assert_eq!(0xffff, reduce(0xffff_ffff));
    }

    #[test]
    fn test_plus_minus_match_c_at_edges() {
        let edges = [0u16, 1, 2, 0x7fff, 0x8000, 0xfffe, 0xffff];
        for a in edges {
            for b in edges {
                assert_eq!(c_plus(a, b), plus(a, b), "plus {a:#x} {b:#x}");
                assert_eq!(c_minus(a, b), minus(a, b), "minus {a:#x} {b:#x}");
            }
        }
    }

    #[test]
    fn test_plus_minus_all_ones_is_identity() {
        assert_eq!(0xffff, plus(0xffff, 0));
        assert_eq!(0xffff, minus(0xffff, 0));
        assert_eq!(0x0001, plus(0xffff, 1));
        assert_eq!(0xfffe, minus(0xffff, 1));
        assert_eq!(0, minus(0xffff, 0xffff));
    }

    #[test]
    fn test_update_unchanged_word_keeps_checksum_value() {
        for c in [0x0001u16, 0x1234, 0xfffe] {
            assert_eq!(c, update(c, 0xabcd, 0xabcd));
        }
    }

    #[test]
    fn test_update_zero_and_all_ones_corners() {
        assert_eq!(0x0000, update(0xffff, 0x1234, 0x1234));
        assert_eq!(0xffff, update_no_zero(0xffff, 0x1234, 0x1234));
        // Inner sum all-ones: plain stores zero, no-zero stores all-ones.
        assert_eq!(0x0000, update(0x0000, 0x0000, 0x0000));
        assert_eq!(0xffff, update_no_zero(0x0000, 0x0000, 0x0000));
    }

    #[test]
    fn test_l4_checksum_matches_explicit_pseudo_header() {
        let segment = [0x12, 0x34, 0x00, 0x35, 0x00, 0x0b, 0x00, 0x00, 1, 2, 3];
        let (src, dst) = (Ipv4Addr::new(10, 0, 0, 1), Ipv4Addr::new(192, 168, 1, 2));
        let mut bytes = [0u8; 12 + 11];
        bytes[0..4].copy_from_slice(&src.octets());
        bytes[4..8].copy_from_slice(&dst.octets());
        bytes[9] = 17;
        bytes[10..12].copy_from_slice(&(segment.len() as u16).to_be_bytes());
        bytes[12..].copy_from_slice(&segment);
        assert_eq!(!sum(&bytes), l4_checksum_ipv4(src, dst, 17, &segment));

        let (src, dst) = (Ipv6Addr::new(0x2001, 0xdb8, 0, 0, 0, 0, 0, 1), Ipv6Addr::LOCALHOST);
        let mut bytes = [0u8; 40 + 11];
        bytes[0..16].copy_from_slice(&src.octets());
        bytes[16..32].copy_from_slice(&dst.octets());
        bytes[32..36].copy_from_slice(&(segment.len() as u32).to_be_bytes());
        bytes[39] = 17;
        bytes[40..].copy_from_slice(&segment);
        assert_eq!(!sum(&bytes), l4_checksum_ipv6(src, dst, 17, &segment));
    }

    #[test]
    fn test_pseudo_sums_ipv4_ipv6() {
        assert_eq!(
            fold(0x0a00 + 0x0001 + 0xc0a8 + 0x0102 + 6 + 40),
            fold(pseudo_sum_ipv4([10, 0, 0, 1], [192, 168, 1, 2], 6, 40))
        );
        let src = [1u8; 16];
        let dst = [2u8; 16];
        let expected = 8 * 0x0101 + 8 * 0x0202 + 1 + 0x0002 + 17;
        assert_eq!(fold(expected), fold(pseudo_sum_ipv6(src, dst, 17, 0x1_0002)));
    }

    proptest! {
        #![proptest_config(testing::config())]

        #[test]
        fn test_sum_matches_c_up_to_byte_swap(data in vec(any::<u8>(), 0..=300)) {
            prop_assert_eq!(c_csum(&data).swap_bytes(), sum(&data));
        }

        #[test]
        fn test_sum_slices_equals_sum_of_concatenation(
            data in vec(any::<u8>(), 0..=300),
            cuts in vec(any::<Index>(), 0..=8),
        ) {
            prop_assert_eq!(sum(&data), sum_slices(split(&data, &cuts)));
        }

        #[test]
        fn test_l4_is_valid_slices_equals_contiguous(
            family in testing::family(),
            addrs in any::<([u8; 16], [u8; 16])>(),
            protocol in any::<u8>(),
            data in vec(any::<u8>(), 8..=300),
            checksummed in any::<bool>(),
            cuts in vec(any::<Index>(), 0..=8),
        ) {
            let ((src, dst), mut data) = (addrs, data);
            if checksummed {
                // UDP's checksum field; any word of the segment would do.
                data[6..8].fill(0);
                let cksum = family.l4_checksum(src, dst, protocol, &data);
                data[6..8].copy_from_slice(&cksum.to_be_bytes());
            }
            let valid = family.l4_is_valid(src, dst, protocol, &data);
            prop_assert!(valid || !checksummed);
            prop_assert_eq!(valid, family.l4_is_valid_slices(src, dst, protocol, split(&data, &cuts)));
        }

        #[test]
        fn test_partial_sum_folds_like_16_bit_words(data in vec(any::<u8>(), 0..=300)) {
            let words: u64 = data
                .chunks(2)
                .map(|pair| u64::from(u16::from_be_bytes([pair[0], *pair.get(1).unwrap_or(&0)])))
                .sum();
            prop_assert_eq!(fold(words), fold(partial_sum(&data)));
        }

        #[test]
        fn test_plus_minus_match_c(a in any::<u16>(), b in any::<u16>()) {
            prop_assert_eq!(c_plus(a, b), plus(a, b));
            prop_assert_eq!(c_minus(a, b), minus(a, b));
        }

        #[test]
        fn test_update_matches_c_dscp_form(cksum in any::<u16>(), old in any::<u16>(), new in any::<u16>()) {
            prop_assert_eq!(!c_plus(c_minus(!cksum, old), new), update(cksum, old, new));
        }

        #[test]
        fn test_update_no_zero_matches_c_mss_form(cksum in any::<u16>(), old in any::<u16>(), new in any::<u16>()) {
            let inner = c_plus(c_minus(!cksum, old), new);
            let expected = if inner == 0xffff { inner } else { !inner };
            prop_assert_eq!(expected, update_no_zero(cksum, old, new));
            prop_assert_ne!(0, update_no_zero(cksum, old, new));
        }

        #[test]
        fn test_update_equals_full_recompute_modulo_zero_sign(
            data in vec(any::<u8>(), 12..=64),
            new in any::<u16>(),
        ) {
            let mut data = data;
            data[10..12].fill(0);
            let full = !sum(&data);
            let old = u16::from_be_bytes([data[8], data[9]]);
            let updated = update(full, old, new);
            data[8..10].copy_from_slice(&new.to_be_bytes());
            prop_assert!(testing::same_checksum(updated, !sum(&data)));
        }

        #[test]
        fn test_update_bytes_equals_sequential_word_updates(
            cksum in any::<u16>(),
            old in vec(any::<u8>(), 16),
            new in vec(any::<u8>(), 16),
            long in any::<bool>(),
        ) {
            let len = if long { 16 } else { 4 };
            let (old, new) = (&old[..len], &new[..len]);
            let mut expected = cksum;
            let mut expected_no_zero = cksum;
            for (o, n) in old.chunks(2).zip(new.chunks(2)) {
                let (o, n) = (u16::from_be_bytes([o[0], o[1]]), u16::from_be_bytes([n[0], n[1]]));
                expected = update(expected, o, n);
                expected_no_zero = update_no_zero(expected_no_zero, o, n);
            }
            prop_assert_eq!(expected, update_bytes(cksum, old, new));
            // Each step of the word-by-word form may pick the other sign of zero.
            prop_assert!(testing::same_checksum(expected_no_zero, update_bytes_no_zero(cksum, old, new)));
            prop_assert_ne!(0, update_bytes_no_zero(cksum, old, new));
        }
    }
}
