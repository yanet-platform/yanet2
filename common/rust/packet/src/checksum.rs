//! Internet checksum (RFC 1071) with incremental update (RFC 1624).

/// Fold a 32-bit ones-complement accumulation and complement it into the
/// final checksum value.
fn fold(mut sum: u32) -> u16 {
    while (sum >> 16) != 0 {
        sum = (sum & 0xffff) + (sum >> 16);
    }
    !(sum as u16)
}

fn sum_bytes(data: &[u8]) -> u32 {
    let mut sum: u32 = 0;
    let mut idx = 0;
    while idx + 2 <= data.len() {
        sum += u16::from_be_bytes([data[idx], data[idx + 1]]) as u32;
        idx += 2;
    }
    // An odd trailing byte pads with a zero low byte, matching the RFC's
    // convention of summing 16-bit big-endian words.
    if idx < data.len() {
        sum += (data[idx] as u32) << 8;
    }
    sum
}

/// Compute the internet checksum over one byte range.
pub fn checksum(data: &[u8]) -> u16 {
    fold(sum_bytes(data))
}

/// Compute the internet checksum over several byte ranges as if they
/// were contiguous: pseudo-header, header and payload of a transport
/// segment, for example.
pub fn checksum_compose(parts: &[&[u8]]) -> u16 {
    let mut acc = Checksum::new();
    for part in parts {
        acc.push(part);
    }
    acc.value()
}

/// Incrementally update a checksum after in-place header rewrites
/// (RFC 1624): `old_words`/`new_words` are the changed 16-bit words in
/// wire order, before and after the edit.
///
/// Equivalent to recomputing [`checksum`] over the whole datagram, but
/// without touching unchanged bytes.
pub fn checksum_update(old: u16, old_words: &[u16], new_words: &[u16]) -> u16 {
    debug_assert_eq!(
        old_words.len(),
        new_words.len(),
        "an update rewrites the same word count"
    );
    let mut sum = (!old) as u32;
    for (idx, word) in old_words.iter().enumerate() {
        sum += (!*word) as u32;
        sum += new_words[idx] as u32;
    }
    fold(sum)
}

/// Incremental checksum accumulator for multi-part datagrams.
///
/// Carries an odd trailing byte across [`Checksum::push`] calls, so
/// pushing several slices equals pushing their concatenation.
#[derive(Debug, Clone, Copy, Default)]
pub struct Checksum {
    sum: u32,
    pending_high: Option<u8>,
}

impl Checksum {
    /// A fresh accumulator.
    pub fn new() -> Self {
        Self::default()
    }

    /// Fold the accumulated sum into the final checksum value.
    pub fn value(&self) -> u16 {
        let mut sum = self.sum;
        if let Some(high) = self.pending_high {
            sum += (high as u32) << 8;
        }
        fold(sum)
    }

    /// Accumulate a byte range.
    pub fn push(&mut self, data: &[u8]) {
        let mut idx = 0;
        if let Some(high) = self.pending_high.take() {
            match data.first() {
                Some(&low) => {
                    self.sum += u32::from((high as u16) << 8 | low as u16);
                    idx = 1;
                }
                None => {
                    self.pending_high = Some(high);
                    return;
                }
            }
        }
        while idx + 1 < data.len() {
            self.sum += u16::from_be_bytes([data[idx], data[idx + 1]]) as u32;
            idx += 2;
        }
        if idx < data.len() {
            self.pending_high = Some(data[idx]);
        }
    }

    /// Accumulate a 16-bit value, host byte order semantics.
    pub fn push_u16(&mut self, word: u16) {
        self.push(&word.to_be_bytes());
    }

    /// Accumulate a 32-bit value, host byte order semantics.
    pub fn push_u32(&mut self, word: u32) {
        self.push(&word.to_be_bytes());
    }

    /// Accumulate an IPv4 pseudo-header (RFC 793): addresses, zeros,
    /// protocol and the segment length.
    pub fn push_ipv4_pseudo_header(&mut self, src: [u8; 4], dst: [u8; 4], protocol: u8, length: u16) {
        self.push(&src);
        self.push(&dst);
        self.push_u16(protocol as u16);
        self.push_u16(length);
    }

    /// Accumulate an IPv6 pseudo-header (RFC 8200): addresses, the
    /// upper-layer packet length, zeros and the next header value.
    pub fn push_ipv6_pseudo_header(&mut self, src: [u8; 16], dst: [u8; 16], next_header: u8, payload_length: u32) {
        self.push(&src);
        self.push(&dst);
        self.push_u32(payload_length);
        self.push(&[0, 0, 0, next_header]);
    }
}

/// Verify a checksummed byte range: a valid datagram recomputes to zero
/// with its embedded checksum in place.
pub fn verify(data_with_checksum: &[u8]) -> bool {
    checksum(data_with_checksum) == 0
}
