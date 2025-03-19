use core::fmt::{self, Display, Formatter};

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
