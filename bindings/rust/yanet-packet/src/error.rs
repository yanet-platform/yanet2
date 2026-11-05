use core::{
    error::Error,
    fmt::{self, Display, Formatter},
};

/// Why a packet header does not parse.
///
/// Lengths count bytes from the first byte of the input handed to `parse`;
/// `available` is the length of that input.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[non_exhaustive]
pub enum ParseError {
    /// The input ends inside the header: before the fixed part, or before the
    /// end of the options that the header length field counts.
    Truncated { needed: usize, available: usize },
    /// The IP version field does not match the header type.
    Version { found: u8 },
    /// The header length field, in 32-bit words, is below the five words of
    /// the fixed part.
    HeaderLen { words: u8 },
    /// The length field is below the length of the header it covers.
    LengthBelowHeader { length: usize, header_len: usize },
    /// The length field reaches past the end of the input.
    LengthBeyondInput { length: usize, available: usize },
}

impl Display for ParseError {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        match *self {
            Self::Truncated { needed, available } => {
                write!(f, "input truncated: {needed} bytes needed, {available} available")
            }
            Self::Version { found } => write!(f, "unexpected IP version {found}"),
            Self::HeaderLen { words } => write!(f, "header length of {words} words is below the minimum of 5"),
            Self::LengthBelowHeader { length, header_len } => {
                write!(f, "length {length} is below the header length {header_len}")
            }
            Self::LengthBeyondInput { length, available } => {
                write!(f, "length {length} is beyond the {available} bytes available")
            }
        }
    }
}

impl Error for ParseError {}
