use core::{
    error::Error,
    fmt::{self, Display, Formatter},
};

/// Why a packet header does not parse, or is not written.
///
/// Lengths count bytes from the first byte of the buffer a header is parsed
/// from or written to, and the bytes available are that buffer's length.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[non_exhaustive]
pub enum ParseError {
    /// The buffer ends inside the header: before the fixed part, or before
    /// the end of the options that the header length field counts.
    Truncated {
        /// Bytes the header needs.
        needed: usize,
        /// Bytes of the buffer.
        available: usize,
    },
    /// The IP version field does not match the header type.
    Version {
        /// Version the field holds.
        found: u8,
    },
    /// The header length field, in 32-bit words, is below the five words of
    /// the fixed part.
    HeaderLen {
        /// Value of the header length field.
        words: u8,
    },
    /// The length field is below the length of the header it covers.
    LengthBelowHeader {
        /// Bytes the length field reaches, from the start of the input.
        length: usize,
        /// Length of the header, options included.
        header_len: usize,
    },
    /// The length field reaches past the end of the input.
    LengthBeyondInput {
        /// Bytes the length field reaches, from the start of the input.
        length: usize,
        /// Bytes of the input.
        available: usize,
    },
    /// A header to write counts more bytes than its length field holds.
    LengthBeyondField {
        /// Bytes the length field would count.
        length: usize,
        /// The most the field holds.
        max: usize,
    },
    /// A TCP option's length cannot be trusted.
    ///
    /// It is below its two header bytes, past the options area, other than
    /// its kind's, or holds more SACK blocks than are kept.
    OptionLength {
        /// Offset of the option in the options area.
        offset: usize,
    },
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
            Self::LengthBeyondField { length, max } => {
                write!(f, "length {length} is beyond the {max} its field holds")
            }
            Self::OptionLength { offset } => write!(f, "TCP option at offset {offset} has an unusable length"),
        }
    }
}

impl Error for ParseError {}
