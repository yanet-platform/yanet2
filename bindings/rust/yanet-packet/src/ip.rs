//! An IPv4 or IPv6 header as a value written whole, such as the header a
//! packet translated between the two takes.
//!
//! A value whose protocol is a transport one writes headers the packet
//! parser reads as the value holds them, so their type, length, protocol and
//! fragment are known without reading the bytes back; an extension header it
//! names is the caller's to write behind them.

use crate::{ETHERTYPE_IPV4, ETHERTYPE_IPV6, Ipv4Header, Ipv4Repr, Ipv6Repr, ParseError};

/// An IPv4 header without options, or an IPv6 header with a Fragment header
/// behind it when the packet is one.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum IpRepr {
    V4(Ipv4Repr),
    V6(Ipv6Repr),
}

impl IpRepr {
    /// Ether type of the header, host order.
    #[inline]
    pub fn network_type(&self) -> u16 {
        match self {
            Self::V4(_) => ETHERTYPE_IPV4,
            Self::V6(_) => ETHERTYPE_IPV6,
        }
    }

    /// Next header behind the IP headers: the transport protocol, or an
    /// extension header the caller writes after them.
    #[inline]
    pub fn protocol(&self) -> u8 {
        match self {
            Self::V4(Ipv4Repr { protocol, .. }) | Self::V6(Ipv6Repr { protocol, .. }) => *protocol,
        }
    }

    /// Bytes the headers take written.
    #[inline]
    pub fn header_len(&self) -> usize {
        match self {
            Self::V4(_) => Ipv4Header::MIN_LEN,
            Self::V6(repr) => repr.header_len(),
        }
    }

    /// Writes the headers at the start of an area, for a datagram of the given
    /// number of bytes behind them.
    ///
    /// The IP length counts those bytes and the headers, and an IPv4 header
    /// checksum is filled; the transport checksum, whose pseudo header the
    /// addresses and lengths enter, is the caller's.
    ///
    /// # Errors
    ///
    /// Those of [`Ipv4Repr::write`] and [`Ipv6Repr::write`].
    #[inline]
    pub fn write(&self, area: &mut [u8], upper_len: usize) -> Result<(), ParseError> {
        match self {
            Self::V4(repr) => repr.write(area, upper_len),
            Self::V6(repr) => repr.write(area, upper_len),
        }
    }
}

#[cfg(test)]
mod tests {
    use core::{
        assert_matches,
        net::{Ipv4Addr, Ipv6Addr},
    };

    use super::*;

    /// verifies that a datagram no length field holds is refused, nothing
    /// written, however long the bytes behind the headers are
    #[test]
    fn test_ip_repr_write_refuses_a_datagram_no_length_field_holds() {
        let v4 = IpRepr::V4(Ipv4Repr {
            dscp: 0,
            ecn: 0,
            id: 0,
            frag: 0,
            ttl: 64,
            protocol: 6,
            src: Ipv4Addr::LOCALHOST,
            dst: Ipv4Addr::LOCALHOST,
        });
        let v6 = IpRepr::V6(Ipv6Repr {
            traffic_class: 0,
            flow_label: 0,
            hop_limit: 64,
            protocol: 6,
            src: Ipv6Addr::LOCALHOST,
            dst: Ipv6Addr::LOCALHOST,
            fragment: None,
        });
        for (header, upper_len) in [
            (v4, usize::from(u16::MAX) - 19),
            (v4, usize::MAX),
            (v6, usize::from(u16::MAX) + 1),
            (v6, usize::MAX),
        ] {
            let mut area = [0x55; 48];

            let refused = header.write(&mut area, upper_len);

            assert_matches!(
                refused,
                Err(ParseError::LengthBeyondField { .. }),
                "{header:?} {upper_len}"
            );
            assert_eq!([0x55; 48], area, "{header:?} {upper_len}");
        }
    }
}
