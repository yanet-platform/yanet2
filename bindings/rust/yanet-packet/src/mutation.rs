//! The modes of a header mutation and the checksum arithmetic they share.
//!
//! Changes are accounted as the stored bytes read in native order, so no
//! byte is swapped: the one's-complement sum does not depend on the byte
//! order it is taken in (RFC 1071, section 2).

use zerocopy::byteorder::network_endian::{U16, U32};

use crate::{fold, no_zero};

/// The mode of a header mutation: whether its changes adjust the checksum.
///
/// Sealed: [`Adjusted`] and [`Unadjusted`] are the only modes.
pub trait Mode: Sealed {}

/// The operations a mode performs on each change, out of reach of other
/// crates.
pub trait Sealed {
    /// Accounts a change of a 16-bit word, both values as its stored bytes
    /// read in native order.
    fn add16(&mut self, old: u16, new: u16);

    /// Accounts a change of a 32-bit word, as for a 16-bit one.
    fn add32(&mut self, old: u32, new: u32);

    /// Accounts a change of an address as its 32-bit words.
    #[inline]
    fn add_octets<const N: usize>(&mut self, old: [u8; N], new: [u8; N]) {
        let (olds, _) = old.as_chunks::<4>();
        let (news, _) = new.as_chunks::<4>();
        for (&old, &new) in olds.iter().zip(news) {
            self.add32(u32::from_ne_bytes(old), u32::from_ne_bytes(new));
        }
    }
}

/// The mode of a mutation that keeps the checksum valid.
///
/// Each change adds the complement of the old word and the new word to a sum
/// held apart from the header; finishing the mutation adjusts the checksum by
/// that sum once (RFC 1624, eqn. 3).
#[derive(Debug)]
pub struct Adjusted {
    sum: u64,
}

/// The mode of a mutation that stores the fields alone.
///
/// The checksum is left as it was, to be filled anew once the header and the
/// bytes the checksum covers are in place: a header built from scratch, or
/// one whose covered bytes change beyond its fields.
#[derive(Debug)]
pub struct Unadjusted;

impl Mode for Adjusted {}

impl Mode for Unadjusted {}

impl Sealed for Adjusted {
    #[inline]
    fn add16(&mut self, old: u16, new: u16) {
        self.sum += u64::from(!old) + u64::from(new);
    }

    #[inline]
    fn add32(&mut self, old: u32, new: u32) {
        self.sum += u64::from(!old) + u64::from(new);
    }
}

impl Sealed for Unadjusted {
    #[inline]
    fn add16(&mut self, _old: u16, _new: u16) {}

    #[inline]
    fn add32(&mut self, _old: u32, _new: u32) {}
}

/// An adjusting mode with no change accounted yet.
#[inline]
pub fn adjusted() -> Adjusted {
    Adjusted { sum: 0 }
}

/// The checksum after the changes the mode accounted, adjusted once.
///
/// An all-ones sum is stored as zero, as the IPv4 and ICMP checksums take it.
#[inline]
pub fn adjust(checksum: U16, mode: Adjusted) -> U16 {
    U16::from_bytes((!folded(checksum, mode)).to_ne_bytes())
}

/// Same as [`adjust`], but an all-ones sum is kept as all-ones.
///
/// The checksum is never stored as zero, which in a UDP header means no
/// checksum (RFC 768); TCP keeps the same rule.
#[inline]
pub fn adjust_no_zero(checksum: U16, mode: Adjusted) -> U16 {
    U16::from_bytes(no_zero(folded(checksum, mode)).to_ne_bytes())
}

/// The complement of the stored checksum plus the accounted changes, folded
/// to 16 bits.
#[inline]
fn folded(checksum: U16, Adjusted { sum }: Adjusted) -> u16 {
    fold(sum + u64::from(!word16(checksum)))
}

/// The stored bytes of a 16-bit field read in native order.
#[inline]
pub fn word16(field: U16) -> u16 {
    u16::from_ne_bytes(field.to_bytes())
}

/// The stored bytes of a 32-bit field read in native order.
#[inline]
pub fn word32(field: U32) -> u32 {
    u32::from_ne_bytes(field.to_bytes())
}

/// The native-order word of two bytes a checksum covers, the byte at the
/// first offset of the word.
#[inline]
pub fn first_byte(byte: u8) -> u16 {
    u16::from_ne_bytes([byte, 0])
}

/// The native-order word of two bytes a checksum covers, the byte at the
/// second offset of the word.
#[inline]
pub fn second_byte(byte: u8) -> u16 {
    u16::from_ne_bytes([0, byte])
}

#[cfg(test)]
mod tests {
    use proptest::{collection::vec, prelude::*};

    use super::*;
    use crate::{checksum, testing};

    /// Adjusts a checksum stored as its numeric value by the changes of
    /// numeric 16-bit words, through the mode.
    fn adjust_words(adjust: fn(U16, Adjusted) -> U16, cksum: u16, changes: &[(u16, u16)]) -> u16 {
        let mut mode = adjusted();
        for &(old, new) in changes {
            mode.add16(old.to_be(), new.to_be());
        }
        adjust(U16::new(cksum), mode).get()
    }

    #[test]
    fn test_adjust_keeps_checksum_without_changes() {
        for cksum in [0x0000, 0x0001, 0x1234, 0xfffe, 0xffff] {
            assert_eq!(cksum, adjust_words(adjust, cksum, &[]), "checksum {cksum:#06x}");
        }
    }

    #[test]
    fn test_adjust_no_zero_stores_all_ones_for_zero_result() {
        // The sum's complement 0xfffe plus a change of one adds up to
        // all-ones, whose complement zero is stored as all-ones.
        assert_eq!(0xffff, adjust_words(adjust_no_zero, 0x0001, &[(0, 1)]));
        assert_eq!(0x0000, adjust_words(adjust, 0x0001, &[(0, 1)]));
    }

    /// verifies that a sum whose low 32 bits overflow when its high ones
    /// fold in keeps that carry.
    #[test]
    fn test_adjust_keeps_carry_out_of_low_word() {
        // Native-order changes summing to 0x1_ffff_ffff with the stored
        // all-ones checksum, whose complement adds nothing.
        let mut mode = adjusted();
        mode.add32(0, u32::MAX);
        mode.add16(u16::MAX, 1);
        let new = u16::from_be_bytes(1u16.to_ne_bytes());
        let chained = checksum::update(checksum::update_bytes(0xffff, &[0; 4], &[0xff; 4]), 0xffff, new);
        let once = adjust(U16::new(0xffff), mode).get();
        assert!(
            testing::same_checksum(chained, once),
            "{chained:#06x} against {once:#06x}"
        );
    }

    proptest! {
        #![proptest_config(testing::config())]

        /// verifies that one adjustment by the accounted changes leaves the
        /// checksum the per-change updates of the C port leave.
        #[test]
        fn test_adjust_equals_chained_updates(
            cksum in any::<u16>(),
            changes in vec(any::<(u16, u16)>(), 0..=24),
        ) {
            let chained = changes.iter().fold(cksum, |cksum, &(old, new)| checksum::update(cksum, old, new));
            let once = adjust_words(adjust, cksum, &changes);
            prop_assert!(testing::same_checksum(chained, once));
        }

        /// verifies that the never-zero adjustment equals the chained
        /// never-zero updates bit for bit, as each residue has one non-zero
        /// form.
        #[test]
        fn test_adjust_no_zero_equals_chained_updates(
            cksum in any::<u16>(),
            changes in vec(any::<(u16, u16)>(), 1..=24),
        ) {
            let chained = changes
                .iter()
                .fold(cksum, |cksum, &(old, new)| checksum::update_no_zero(cksum, old, new));
            prop_assert_eq!(chained, adjust_words(adjust_no_zero, cksum, &changes));
        }

        /// verifies that a 32-bit change adjusts the checksum as its two
        /// 16-bit halves do.
        #[test]
        fn test_add32_equals_two_halves(cksum in any::<u16>(), old in any::<u32>(), new in any::<u32>()) {
            let mut whole = adjusted();
            whole.add32(old.to_be(), new.to_be());
            let halves = [((old >> 16) as u16, (new >> 16) as u16), (old as u16, new as u16)];
            let expected = adjust_words(adjust_no_zero, cksum, &halves);
            prop_assert_eq!(expected, adjust_no_zero(U16::new(cksum), whole).get());
        }
    }
}
