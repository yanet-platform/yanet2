use core::{
    fmt::{self, Display, Formatter},
    str::FromStr,
};

/// A MAC address.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Ord, PartialOrd, Default, Hash)]
pub struct MacAddr(u64);

impl MacAddr {
    /// Creates a new MAC address from its 6 octets in transmission order.
    ///
    /// Each parameter represents one byte of the MAC address in the standard
    /// format `xx:xx:xx:xx:xx:xx`.
    #[inline]
    pub const fn new(a: u8, b: u8, c: u8, d: u8, e: u8, f: u8) -> MacAddr {
        Self(u64::from_be_bytes([a, b, c, d, e, f, 0, 0]))
    }

    /// Returns the MAC address as a u64 value.    
    #[inline]
    pub const fn as_u64(&self) -> u64 {
        match self {
            Self(addr) => *addr,
        }
    }
}

impl From<u64> for MacAddr {
    #[inline]
    fn from(addr: u64) -> MacAddr {
        MacAddr(addr)
    }
}

impl Display for MacAddr {
    fn fmt(&self, fmt: &mut Formatter) -> Result<(), fmt::Error> {
        let [a, b, c, d, e, f, ..] = self.as_u64().to_be_bytes();

        write!(fmt, "{a:02x}:{b:02x}:{c:02x}:{d:02x}:{e:02x}:{f:02x}")
    }
}

impl FromStr for MacAddr {
    type Err = MacAddrParseError;

    fn from_str(s: &str) -> Result<Self, Self::Err> {
        let mut octets = [0u8; 6];
        let mut idx = 0;

        for part in s.split(':') {
            if idx >= 6 {
                return Err(MacAddrParseError);
            }
            octets[idx] = u8::from_str_radix(part, 16).map_err(|_| MacAddrParseError)?;
            idx += 1;
        }

        if idx != 6 {
            return Err(MacAddrParseError);
        }

        Ok(MacAddr::new(
            octets[0], octets[1], octets[2], octets[3], octets[4], octets[5],
        ))
    }
}

/// Error returned when parsing a MAC address string fails.
#[derive(Debug, Clone, Copy)]
pub struct MacAddrParseError;

impl Display for MacAddrParseError {
    fn fmt(&self, f: &mut Formatter) -> Result<(), fmt::Error> {
        write!(f, "invalid MAC address format, expected EUI-48: xx:xx:xx:xx:xx:xx")
    }
}

impl core::error::Error for MacAddrParseError {}
