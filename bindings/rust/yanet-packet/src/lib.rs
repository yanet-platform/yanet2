//! Byte-level packet headers for the YANET2 module SDK.
//!
//! The crate works on byte slices only. A header view is built at the offset
//! where the header starts and refuses short or malformed input with a
//! [`ParseError`] that says why; nothing here panics on attacker-controlled
//! bytes.
//!
//! The fields of a header a checksum covers change through a mutation, whose
//! mode is chosen when it starts: one that keeps the checksum valid adjusts it
//! once when it finishes, and one that stores the fields alone, as when a
//! header is built, leaves the checksum to be filled after it.

#![cfg_attr(not(test), no_std)]
#![forbid(unsafe_code)]

pub mod checksum;
mod error;
mod ethernet;
mod icmp;
mod ipv4;
mod ipv6;
mod mutation;
mod tcp;
#[cfg(test)]
mod testing;
mod udp;

pub use error::ParseError;
pub use ethernet::{ETHERTYPE_IPV4, ETHERTYPE_IPV6, ETHERTYPE_QINQ, ETHERTYPE_VLAN, EthernetHeader, VlanTag};
pub use icmp::{Icmpv4Header, Icmpv4Mutation, Icmpv6Header, Icmpv6Mutation};
pub use ipv4::{Ipv4Header, Ipv4Mutation, Ipv4Packet, Ipv4PacketMut};
pub use ipv6::Ipv6Header;
/// The header bookkeeping of the SDK's packet insert and remove.
#[doc(hidden)]
pub use mutation::resize;
pub use mutation::{Adjusted, Mode, Unadjusted};
pub use tcp::{TcpHeader, TcpMutation, TcpPacket, TcpPacketMut, options as tcp_options};
pub use udp::{UdpHeader, UdpMutation};
use zerocopy::{FromBytes, Immutable, IntoBytes, KnownLayout, Unaligned};

/// Splits a header of type `T` off the front of the input.
///
/// Fails with [`ParseError::Truncated`] when the input is shorter than `T`.
fn split_header<T>(buf: &[u8]) -> Result<(&T, &[u8]), ParseError>
where
    T: FromBytes + KnownLayout + Immutable + Unaligned,
{
    T::ref_from_prefix(buf).map_err(|_| ParseError::Truncated {
        needed: size_of::<T>(),
        available: buf.len(),
    })
}

/// Splits a header of type `T` off the front of the input for in-place
/// editing.
///
/// Fails like [`split_header`].
fn split_header_mut<T>(buf: &mut [u8]) -> Result<(&mut T, &mut [u8]), ParseError>
where
    T: FromBytes + IntoBytes + KnownLayout + Unaligned,
{
    let available = buf.len();
    T::mut_from_prefix(buf).map_err(|_| ParseError::Truncated { needed: size_of::<T>(), available })
}

/// Folds an unfolded sum into 16 bits by end-around carry.
///
/// The result is zero only for a zero sum, as for the one's-complement
/// addition the sum stands for; any other sum folds to the value from 1 to
/// all-ones it is congruent to.
#[inline]
fn fold(sum: u64) -> u16 {
    let (sum, carry) = (sum as u32).overflowing_add((sum >> 32) as u32);
    let sum = sum + u32::from(carry);
    let (sum, carry) = (sum as u16).overflowing_add((sum >> 16) as u16);
    sum + u16::from(carry)
}

/// The complement of a folded sum, kept as all-ones where it would be zero.
///
/// A zero UDP checksum means no checksum (RFC 768); TCP keeps the same rule.
#[inline]
fn no_zero(sum: u16) -> u16 {
    if sum == 0xffff { sum } else { !sum }
}

pub const IPPROTO_ICMP: u8 = 1;
pub const IPPROTO_TCP: u8 = 6;
pub const IPPROTO_UDP: u8 = 17;
pub const IPPROTO_ICMPV6: u8 = 58;

#[cfg(test)]
mod tests {
    use proptest::prelude::*;

    use super::*;
    use crate::testing;

    /// Folds by repeated end-around carries of the high bits into the low
    /// sixteen, the definition the fold shortens.
    fn fold_by_definition(mut sum: u64) -> u16 {
        while sum >> 16 != 0 {
            sum = (sum >> 16) + (sum & 0xffff);
        }
        sum as u16
    }

    #[test]
    fn test_fold_keeps_zero_and_all_ones() {
        assert_eq!(0, fold(0));
        assert_eq!(0xffff, fold(0xffff));
        assert_eq!(0xffff, fold(u64::MAX));
    }

    #[test]
    fn test_no_zero_keeps_all_ones_for_zero_complement() {
        assert_eq!(0xffff, no_zero(0xffff));
        assert_eq!(0xffff, no_zero(0));
        assert_eq!(0xedcb, no_zero(0x1234));
    }

    proptest! {
        #![proptest_config(testing::config())]

        #[test]
        fn test_fold_equals_repeated_end_around_carries(sum in prop_oneof![any::<u64>(), 0u64..0x4_0000]) {
            prop_assert_eq!(fold_by_definition(sum), fold(sum));
        }
    }
}
