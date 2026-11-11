//! Header bookkeeping of an insert into or a remove from a frame: the
//! lengths, header lengths and checksums the parsed headers carry over the
//! bytes that move.
//!
//! A [`Layout`] read from the frame and what the packet parser found plans
//! an [`Edit`]; whoever moves the bytes writes the edit into the frame after
//! the move. Every field an edit changes lies in a header that starts before
//! the edit, which keeps its offset whichever side of the frame moves, so a
//! plan read before the move is written after it. The checksum changes
//! accumulate as a mutation's do and are folded in once (RFC 1624).
//!
//! The SDK's packet insert and remove use it after checking the edit against
//! the packet; it is not meant to be called on its own.

use zerocopy::byteorder::network_endian::U16;

use super::{Adjusted, Sealed, adjust, adjust_no_zero, adjusted};
use crate::{ETHERTYPE_IPV4, ETHERTYPE_IPV6, IPPROTO_TCP, IPPROTO_UDP, checksum};

/// The longest TCP header: fifteen 32-bit words.
const TCP_HEADER_MAX: usize = 60;

/// An edit the bookkeeping cannot describe, refused before anything moves.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Refused;

/// What an insert's bytes are to the transport header around them.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Insertion {
    /// Bytes of the datagram behind the headers: the payload, or a header
    /// the caller links in.
    Payload,
    /// TCP options, which the data offset counts.
    TcpOptions,
}

/// What the packet parser found in a frame.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct Parsed {
    /// Ether type of the network header, host order; zero when unparsed.
    pub network_type: u16,
    /// Offset of the network header from the start of the frame.
    pub network_offset: u16,
    /// Protocol and frame offset of an available transport header.
    pub transport: Option<(u8, u16)>,
    /// Whether the packet is a fragment of a datagram.
    pub fragment: bool,
}

/// A parsed network header whose length the bookkeeping maintains.
#[derive(Clone, Copy, Debug)]
enum Network {
    /// An IPv4 header with its options, `header` bytes long, of a datagram
    /// `total` bytes long; `word0` holds the version and header length.
    V4 {
        start: usize,
        header: usize,
        total: u16,
        word0: u16,
    },
    /// An IPv6 header followed by `payload` bytes.
    V6 { start: usize, payload: u16 },
}

impl Network {
    /// End of the datagram the length covers, from the start of the frame.
    #[inline]
    fn end(self) -> usize {
        match self {
            Self::V4 { start, total, .. } => start + usize::from(total),
            Self::V6 { start, payload } => start + 40 + usize::from(payload),
        }
    }

    /// End of the header itself, options included: the length covers the
    /// bytes from it to the end.
    #[inline]
    fn header_end(self) -> usize {
        match self {
            Self::V4 { start, header, .. } => start + header,
            Self::V6 { start, .. } => start + 40,
        }
    }
}

/// A parsed TCP or UDP header whose lengths the bookkeeping maintains.
#[derive(Clone, Copy, Debug)]
enum Transport {
    /// A TCP header with its options, `header` bytes long; `word6` holds the
    /// data offset.
    Tcp { start: usize, header: usize, word6: u16 },
    /// A UDP header of a datagram `length` bytes long.
    Udp { start: usize, length: u16 },
}

impl Transport {
    #[inline]
    fn start(self) -> usize {
        match self {
            Self::Tcp { start, .. } | Self::Udp { start, .. } => start,
        }
    }

    /// Length of the fixed header, which the parser vouches for.
    #[inline]
    fn fixed(self) -> usize {
        match self {
            Self::Tcp { .. } => 20,
            Self::Udp { .. } => 8,
        }
    }

    /// Offset of the checksum field from the start of the header.
    #[inline]
    fn checksum(self) -> usize {
        match self {
            Self::Tcp { .. } => 16,
            Self::Udp { .. } => 6,
        }
    }
}

/// The stored bytes of a numeric word, read in native order, as a
/// mutation accounts them.
#[inline]
fn stored(value: u16) -> u16 {
    value.to_be()
}

/// Accounts a field changing from one numeric value to another.
#[inline]
fn replace(delta: &mut Adjusted, old: u16, new: u16) {
    delta.add16(stored(old), stored(new));
}

/// Sum of a run of covered bytes as its segment pairs them, stored bytes in
/// native order: a run at an odd offset of the segment holds each byte in
/// the other half of its word (RFC 1071, section 2).
#[inline]
fn run_sum(bytes: &[u8], segment_offset: usize) -> u16 {
    let sum = stored(checksum::sum(bytes));
    if segment_offset.is_multiple_of(2) {
        sum
    } else {
        sum.swap_bytes()
    }
}

/// Accounts a run of covered bytes joining the segment.
#[inline]
fn add_run(delta: &mut Adjusted, bytes: &[u8], segment_offset: usize) {
    delta.sum += u64::from(run_sum(bytes, segment_offset));
}

/// Accounts a run of covered bytes leaving the segment.
#[inline]
fn sub_run(delta: &mut Adjusted, bytes: &[u8], segment_offset: usize) {
    delta.sum += u64::from(!run_sum(bytes, segment_offset));
}

/// Accounts a run of covered bytes moving by an odd count: from words it
/// shared in one half to words it shares in the other.
#[inline]
fn shift_parity(delta: &mut Adjusted, bytes: &[u8], segment_offset: usize) {
    let sum = run_sum(bytes, segment_offset);
    delta.sum += u64::from(!sum) + u64::from(sum.swap_bytes());
}

#[inline]
fn read16(frame: &[u8], at: usize) -> Option<u16> {
    Some(u16::from_be_bytes(frame.get(at..at + 2)?.try_into().ok()?))
}

#[inline]
fn write16(frame: &mut [u8], at: usize, value: u16) {
    if let Some(field) = frame.get_mut(at..at + 2) {
        field.copy_from_slice(&value.to_be_bytes());
    }
}

/// End of the bytes a transport checksum covers, from the start of the
/// frame: the end of the datagram for TCP, the UDP length for UDP (RFC 768).
#[inline]
fn covered_end(network: Network, transport: Transport) -> usize {
    match transport {
        Transport::Tcp { .. } => network.end(),
        Transport::Udp { start, length } => start + usize::from(length),
    }
}

/// The parsed headers of a frame, as the bookkeeping reads them.
#[derive(Clone, Copy, Debug)]
pub struct Layout {
    network: Option<Network>,
    transport: Option<Transport>,
    /// Whether the transport checksum is kept: the packet is no fragment,
    /// the segment lies whole in the frame behind the network header, and a
    /// UDP one carries a checksum and a length within the datagram.
    checksummed: bool,
}

impl Layout {
    /// Reads the headers the parser found from the frame.
    ///
    /// The parser vouches that the fixed IPv4, IPv6, TCP or UDP header it
    /// found lies whole in the frame; a length field the frame does not
    /// hold leaves its header out, as do a non-IP network header and an
    /// unavailable or other transport header.
    #[inline]
    pub fn read(frame: &[u8], parsed: Parsed) -> Self {
        let network_start = usize::from(parsed.network_offset);
        let network = match parsed.network_type {
            ETHERTYPE_IPV4 => (|| {
                let word0 = read16(frame, network_start)?;
                Some(Network::V4 {
                    start: network_start,
                    header: (usize::from(word0 >> 8) & 0xf).max(5) * 4,
                    total: read16(frame, network_start + 2)?,
                    word0,
                })
            })(),
            ETHERTYPE_IPV6 => {
                read16(frame, network_start + 4).map(|payload| Network::V6 { start: network_start, payload })
            }
            _ => None,
        };
        let transport = match parsed.transport {
            Some((IPPROTO_TCP, start)) => {
                let start = usize::from(start);
                read16(frame, start + 12).map(|word6| Transport::Tcp {
                    start,
                    header: (usize::from(word6 >> 12)).max(5) * 4,
                    word6,
                })
            }
            Some((IPPROTO_UDP, start)) => {
                let start = usize::from(start);
                read16(frame, start + 4).map(|length| Transport::Udp { start, length })
            }
            _ => None,
        };
        let checksummed = match (network, transport) {
            (Some(network), Some(transport)) => {
                let checksum = read16(frame, transport.start() + transport.checksum());
                !parsed.fragment
                    && network.header_end() <= transport.start()
                    && transport.start() + transport.fixed() <= network.end()
                    && network.end() <= frame.len()
                    && match (transport, checksum) {
                        // The UDP checksum ends with the UDP length (RFC
                        // 768), which may stop short of the datagram's end:
                        // the rest is a surplus area it does not cover (RFC
                        // 9868).
                        (Transport::Udp { start, length }, Some(checksum)) => {
                            checksum != 0 && usize::from(length) >= 8 && start + usize::from(length) <= network.end()
                        }
                        (Transport::Tcp { .. }, Some(_)) => true,
                        (_, None) => false,
                    }
            }
            _ => false,
        };
        Self { network, transport, checksummed }
    }

    /// Plans an insert at an offset, refusing one the bookkeeping cannot
    /// describe.
    ///
    /// Refused are an offset inside a parsed IPv4, IPv6, TCP or UDP header;
    /// payload inside the IPv4 or TCP options, which their header length
    /// would not count; TCP options outside the options of a TCP header, of
    /// a length no data offset holds, in a header that runs past its
    /// datagram, or in one whose data offset is below five words; and a
    /// length past what a length field holds.
    ///
    /// The caller checks the edit against the packet first: the frame is the
    /// one the layout was read from, the offset or range lies in it, and a
    /// range holds neither a parsed header's start nor a byte of a fixed
    /// header the parser vouches for. The SDK's packet insert and remove do;
    /// the plan is not meant to be called on its own.
    ///
    /// # Panics
    ///
    /// May panic, or plan a wrong edit, when those checks were skipped.
    #[inline]
    pub fn plan_insert(&self, frame: &[u8], at: usize, len: usize, insertion: Insertion) -> Result<Edit, Refused> {
        let grow = |value: u16| u16::try_from(usize::from(value) + len).map_err(|_| Refused);
        let mut edit = Edit::new();
        match self.network {
            Some(Network::V4 { start, header, total, .. }) => {
                if start < at && at < start + header {
                    return Err(Refused);
                }
                if start + header <= at && at <= start + usize::from(total) {
                    let new = grow(total)?;
                    edit.change(start + 2, total, new);
                    edit.ipv4_replace(start, total, new);
                }
            }
            Some(Network::V6 { start, payload }) => {
                if start < at && at < start + 40 {
                    return Err(Refused);
                }
                if start + 40 <= at && at <= start + 40 + usize::from(payload) {
                    edit.change(start + 4, payload, grow(payload)?);
                }
            }
            None => {}
        }
        match (self.transport, insertion) {
            (Some(Transport::Tcp { start, header, .. }), Insertion::Payload) => {
                if start < at && at < start + header {
                    return Err(Refused);
                }
            }
            (Some(Transport::Tcp { start, header, word6: old }), Insertion::TcpOptions) => {
                // A data offset below five words is no header to grow: the
                // field would still not count the fixed header.
                if old >> 12 < 5
                    || at < start + 20
                    || at > start + header
                    || !len.is_multiple_of(4)
                    || header + len > TCP_HEADER_MAX
                    || self.network.is_some_and(|network| start + header > network.end())
                {
                    return Err(Refused);
                }
                let new = old + ((len as u16 / 4) << 12);
                edit.change(start + 12, old, new);
            }
            (Some(Transport::Udp { start, length }), Insertion::Payload) => {
                if start < at && at < start + 8 {
                    return Err(Refused);
                }
                if start + 8 <= at && at <= start + usize::from(length) {
                    edit.change(start + 4, length, grow(length)?);
                }
            }
            (_, Insertion::TcpOptions) => return Err(Refused),
            (None, Insertion::Payload) => {}
        }
        if let (true, Some(network), Some(transport)) = (self.checksummed, self.network, self.transport) {
            let (start, covered) = (transport.start(), covered_end(network, transport));
            if start + transport.fixed() <= at && at <= covered {
                let segment = (covered - start) as u16;
                let mut delta = edit.covered_changes(start, covered);
                replace(&mut delta, segment, segment + len as u16);
                if len % 2 == 1 {
                    shift_parity(&mut delta, &frame[at..covered], at - start);
                }
                edit.l4 = Some(L4 {
                    start: start as u16,
                    checksum: (start + transport.checksum()) as u16,
                    delta,
                    gap_at: at as u16,
                    gap_len: len as u16,
                });
            }
        }
        Ok(edit)
    }

    /// Plans a remove of a range of the frame, refusing one the bookkeeping
    /// cannot describe.
    ///
    /// Option bytes removed shrink the IPv4 header length or the TCP data
    /// offset; refused are a range that holds both options and bytes after
    /// them, options of a length no header length counts, and a length a
    /// field holds less of than the range removes.
    ///
    /// The caller checks the edit against the packet first: the frame is the
    /// one the layout was read from, the offset or range lies in it, and a
    /// range holds neither a parsed header's start nor a byte of a fixed
    /// header the parser vouches for. The SDK's packet insert and remove do;
    /// the plan is not meant to be called on its own.
    ///
    /// # Panics
    ///
    /// May panic, or plan a wrong edit, when those checks were skipped.
    #[inline]
    pub fn plan_remove(&self, frame: &[u8], at: usize, len: usize) -> Result<Edit, Refused> {
        let end = at + len;
        let overlap = |from: usize, to: usize| end.min(to).saturating_sub(at.max(from));
        let mut edit = Edit::new();
        match self.network {
            Some(Network::V4 { start, header, total, word0 }) if end > start => {
                let removed = if at < start + header {
                    if end > start + header || !len.is_multiple_of(4) {
                        return Err(Refused);
                    }
                    // The header checksum covers the removed options too;
                    // whole words removed keep the pairing of the rest.
                    let new = word0 - ((len as u16 / 4) << 8);
                    edit.change(start, word0, new);
                    edit.ipv4_replace(start, word0, new);
                    if let Some((_, delta)) = &mut edit.ipv4 {
                        sub_run(delta, &frame[at..end], at - start);
                    }
                    len
                } else {
                    overlap(start + header, start + usize::from(total))
                };
                if removed > 0 {
                    // A total a module rewrote below the options removed
                    // cannot shrink by them.
                    let new = total.checked_sub(removed as u16).ok_or(Refused)?;
                    edit.change(start + 2, total, new);
                    edit.ipv4_replace(start, total, new);
                }
            }
            Some(network @ Network::V6 { start, payload }) => {
                let removed = overlap(start + 40, network.end());
                if removed > 0 {
                    edit.change(start + 4, payload, payload - removed as u16);
                }
            }
            _ => {}
        }
        match self.transport {
            Some(Transport::Tcp { start, header, word6: old }) if end > start && at < start + header => {
                if end > start + header
                    || !len.is_multiple_of(4)
                    || self.network.is_some_and(|network| start + header > network.end())
                {
                    return Err(Refused);
                }
                let new = old - ((len as u16 / 4) << 12);
                edit.change(start + 12, old, new);
            }
            Some(Transport::Udp { start, length }) => {
                let removed = overlap(start + 8, start + usize::from(length));
                if removed > 0 {
                    edit.change(start + 4, length, length - removed as u16);
                }
            }
            _ => {}
        }
        if let (true, Some(network), Some(transport)) = (self.checksummed, self.network, self.transport) {
            let (start, covered) = (transport.start(), covered_end(network, transport));
            let (from, to) = (at.max(start + transport.fixed()), end.min(covered));
            if from < to {
                let removed = to - from;
                let segment = (covered - start) as u16;
                let mut delta = edit.covered_changes(start, covered);
                replace(&mut delta, segment, segment - removed as u16);
                sub_run(&mut delta, &frame[from..to], from - start);
                if removed % 2 == 1 {
                    shift_parity(&mut delta, &frame[to..covered], to - start);
                }
                edit.l4 = Some(L4 {
                    start: start as u16,
                    checksum: (start + transport.checksum()) as u16,
                    delta,
                    gap_at: 0,
                    gap_len: 0,
                });
            }
        }
        Ok(edit)
    }
}

/// A field an edit changes: its frame offset and its old and new values.
#[derive(Clone, Copy, Debug, Default)]
struct Field {
    at: u16,
    old: u16,
    new: u16,
}

/// The transport checksum an edit changes: the transport header's frame
/// offset, the checksum field's, the change, and the inserted bytes, whose
/// sum joins the change once they are written (none for a remove).
///
/// Frame offsets fit 16 bits, as the frame's length does.
#[derive(Debug)]
struct L4 {
    start: u16,
    checksum: u16,
    delta: Adjusted,
    gap_at: u16,
    gap_len: u16,
}

/// What an edit writes once its bytes moved: the fields that change and
/// the checksums over them.
///
/// An insert changes at most an IP length and a transport field, a remove
/// also the IPv4 header length.
#[derive(Debug)]
pub struct Edit {
    fields: [Field; 3],
    count: u8,
    /// The IPv4 header checksum's frame offset and its change.
    ipv4: Option<(u16, Adjusted)>,
    l4: Option<L4>,
}

impl Edit {
    #[inline]
    fn new() -> Self {
        Self {
            fields: [Field::default(); 3],
            count: 0,
            ipv4: None,
            l4: None,
        }
    }

    #[inline]
    fn change(&mut self, at: usize, old: u16, new: u16) {
        if let Some(field) = self.fields.get_mut(usize::from(self.count)) {
            *field = Field { at: at as u16, old, new };
            self.count += 1;
        }
    }

    #[inline]
    fn changed(&self) -> &[Field] {
        &self.fields[..usize::from(self.count)]
    }

    /// The change of the fields the edit writes inside a segment, which its
    /// checksum covers.
    #[inline]
    fn covered_changes(&self, from: usize, to: usize) -> Adjusted {
        let mut delta = adjusted();
        for field in self.changed() {
            if from <= usize::from(field.at) && usize::from(field.at) < to {
                replace(&mut delta, field.old, field.new);
            }
        }
        delta
    }

    /// Accounts a change of a word of the IPv4 header at `start`, in the
    /// header checksum.
    #[inline]
    fn ipv4_replace(&mut self, start: usize, old: u16, new: u16) {
        let (_, delta) = self.ipv4.get_or_insert_with(|| ((start + 10) as u16, adjusted()));
        replace(delta, old, new);
    }

    /// Writes the changed fields and the IPv4 header checksum over them.
    #[inline]
    pub fn write_headers(&mut self, frame: &mut [u8]) {
        for field in self.changed() {
            write16(frame, usize::from(field.at), field.new);
        }
        if let Some((at, delta)) = self.ipv4.take() {
            write_checksum(frame, usize::from(at), |checksum| adjust(checksum, delta));
        }
    }

    /// Writes the transport checksum over the edit, the inserted bytes
    /// included, which the frame holds by now.
    ///
    /// The old checksum stays the base of the new one, so a segment that
    /// arrived damaged stays damaged (RFC 1624). An all-ones sum is stored
    /// as all-ones, as the header mutations store it.
    #[inline]
    pub fn write_transport_checksum(self, frame: &mut [u8]) {
        let Some(L4 {
            start,
            checksum,
            mut delta,
            gap_at,
            gap_len,
        }) = self.l4
        else {
            return;
        };
        let (start, at) = (usize::from(start), usize::from(gap_at));
        if gap_len > 0
            && let Some(bytes) = frame.get(at..at + usize::from(gap_len))
        {
            add_run(&mut delta, bytes, at - start);
        }
        write_checksum(frame, usize::from(checksum), |checksum| adjust_no_zero(checksum, delta));
    }
}

/// Replaces the checksum field at a frame offset by what `adjust` makes of
/// it.
#[inline]
fn write_checksum(frame: &mut [u8], at: usize, adjust: impl FnOnce(U16) -> U16) {
    if let Some(field) = frame.get_mut(at..at + 2) {
        let checksum = U16::from_bytes([field[0], field[1]]);
        field.copy_from_slice(&adjust(checksum).to_bytes());
    }
}
