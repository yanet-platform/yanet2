//! TCP options: read one by one, edited in place, or held as the options an
//! endpoint negotiates and written as a whole.
//!
//! The options area is the bytes the data offset counts past the fixed
//! header. Reading refuses an option whose length cannot be trusted to
//! advance past it, so a reader never stalls or reads past the area. An
//! option whose values change keeps its place through
//! [`TcpMutation::edit_options`], which keeps the checksum valid and the
//! options it does not know; options of another size replace the area,
//! written from [`TcpOptions`].

use super::{TcpHeader, TcpMutation};
use crate::{
    ParseError, fold,
    mutation::{self, Mode},
};

/// End of the option list: nothing after it is an option.
const KIND_EOL: u8 = 0;
/// No-operation, padding between options.
const KIND_NOP: u8 = 1;
/// Maximum segment size (RFC 9293, section 3.7.1).
const KIND_MSS: u8 = 2;
/// Window scale (RFC 7323, section 2).
const KIND_WINDOW_SCALE: u8 = 3;
/// SACK permitted (RFC 2018, section 2).
const KIND_SACK_PERMITTED: u8 = 4;
/// Selective acknowledgment (RFC 2018, section 3).
const KIND_SACK: u8 = 5;
/// Timestamps (RFC 7323, section 3).
const KIND_TIMESTAMPS: u8 = 8;

const LEN_MSS: usize = 4;
const LEN_WINDOW_SCALE: usize = 3;
const LEN_SACK_PERMITTED: usize = 2;
const LEN_TIMESTAMPS: usize = 10;

/// The largest shift a window scale takes: a larger one counts as this
/// (RFC 7323, section 2.3).
pub const WINDOW_SCALE_MAX: u8 = 14;

/// The most SACK blocks [`TcpOptions`] keeps, as many as fit the options
/// area.
pub const SACK_BLOCKS_MAX: usize = 4;

/// The segment size an IPv4 peer that sent no MSS option takes (RFC 9293,
/// section 3.7.1).
pub const MSS_DEFAULT_IPV4: u16 = 536;

/// The segment size an IPv6 peer that sent no MSS option takes (RFC 9293,
/// section 3.7.1).
pub const MSS_DEFAULT_IPV6: u16 = 1220;

/// The most bytes [`TcpOptions::write`] writes: every option present, before
/// [`TcpOptions::fit_to_header`] trims them to a header, padded to a word.
#[cfg(test)]
const WRITTEN_MAX: usize =
    (LEN_MSS + LEN_WINDOW_SCALE + LEN_SACK_PERMITTED + 2 + 8 * SACK_BLOCKS_MAX + LEN_TIMESTAMPS).next_multiple_of(4);

/// One option of a TCP header, as the options area holds it.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[non_exhaustive]
pub enum TcpOption<'a> {
    /// The largest segment the sender receives, in bytes.
    Mss(u16),
    /// The sender's window scale shift, as sent: it may exceed
    /// [`WINDOW_SCALE_MAX`].
    WindowScale(u8),
    /// The sender takes selective acknowledgments.
    SackPermitted,
    /// Blocks of sequence numbers the sender holds out of order, eight bytes
    /// each.
    Sack(SackBlocks<'a>),
    /// The sender's clock and the clock value it echoes.
    Timestamps(Timestamps),
    /// An option of another kind and the bytes after its length.
    Other {
        /// Kind of the option.
        kind: u8,
        /// Bytes after the kind and length.
        data: &'a [u8],
    },
}

/// The SACK blocks of a SACK option, eight bytes each.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct SackBlocks<'a>(&'a [u8]);

impl<'a> SackBlocks<'a> {
    /// The blocks, in the order the option holds them.
    #[inline]
    pub fn iter(&self) -> impl Iterator<Item = SackBlock> + 'a {
        let Self(bytes) = *self;
        bytes
            .as_chunks::<8>()
            .0
            .iter()
            .map(|&[a, b, c, d, e, f, g, h]| SackBlock {
                start: u32::from_be_bytes([a, b, c, d]),
                end: u32::from_be_bytes([e, f, g, h]),
            })
    }

    #[inline]
    pub fn len(&self) -> usize {
        let Self(bytes) = self;
        bytes.len() / 8
    }

    #[inline]
    pub fn is_empty(&self) -> bool {
        self.len() == 0
    }
}

/// A block of sequence numbers a SACK option reports: from `start` up to,
/// not including, `end`.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct SackBlock {
    /// First sequence number of the block.
    pub start: u32,
    /// Sequence number after the last of the block.
    pub end: u32,
}

impl SackBlock {
    /// The block moved up the sequence space by `delta`, wrapping as
    /// sequence numbers do.
    #[inline]
    pub fn wrapping_add(self, delta: u32) -> Self {
        Self {
            start: self.start.wrapping_add(delta),
            end: self.end.wrapping_add(delta),
        }
    }

    /// The block moved down the sequence space by `delta`, as
    /// [`SackBlock::wrapping_add`] moves it up.
    #[inline]
    pub fn wrapping_sub(self, delta: u32) -> Self {
        self.wrapping_add(delta.wrapping_neg())
    }
}

/// The Timestamps option: the sender's clock, and the value it echoes back.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct Timestamps {
    /// The sender's clock (`TSval`).
    pub value: u32,
    /// The clock value the sender echoes (`TSecr`).
    pub echo: u32,
}

/// The options of an options area, in order; padding and the end of the
/// list are skipped.
///
/// An option whose length cannot be trusted ends the iteration with an
/// error: a known kind of a length other than its own, a SACK option not of
/// one or more whole blocks, or any option whose length is below its two
/// header bytes
/// or runs past the area.
#[derive(Debug, Clone)]
pub struct TcpOptionsIter<'a> {
    area: &'a [u8],
    at: usize,
}

/// Reads the options of an options area one by one.
#[inline]
pub fn options(area: &[u8]) -> TcpOptionsIter<'_> {
    TcpOptionsIter { area, at: 0 }
}

impl<'a> Iterator for TcpOptionsIter<'a> {
    type Item = Result<TcpOption<'a>, ParseError>;

    #[inline]
    fn next(&mut self) -> Option<Self::Item> {
        loop {
            let at = self.at;
            let kind = *self.area.get(at)?;
            match kind {
                KIND_EOL => {
                    self.at = self.area.len();
                    return None;
                }
                KIND_NOP => self.at += 1,
                _ => return Some(self.option(at, kind)),
            }
        }
    }
}

impl<'a> TcpOptionsIter<'a> {
    /// Reads the option of the kind at the offset; an unusable length ends
    /// the iteration.
    #[inline]
    fn option(&mut self, at: usize, kind: u8) -> Result<TcpOption<'a>, ParseError> {
        // Every refusal ends the reading, so the cursor never stays put. The
        // option is sliced once, so its shape, not each byte, is checked.
        let option = self
            .area
            .get(at + 1)
            .map(|&len| usize::from(len))
            .filter(|&len| len >= 2)
            .and_then(|len| self.area.get(at..at + len));
        let read = option.and_then(|option| decode(kind, option));
        match (read, option) {
            (Some(read), Some(option)) => {
                self.at = at + option.len();
                Ok(read)
            }
            _ => {
                self.at = self.area.len();
                Err(ParseError::OptionLength { offset: at })
            }
        }
    }
}

/// The option a whole option's bytes hold, its kind first.
///
/// `None` for a known kind of a length other than its own or a SACK option
/// not of one or more whole blocks.
#[inline]
fn decode(kind: u8, option: &[u8]) -> Option<TcpOption<'_>> {
    match (kind, option) {
        (KIND_MSS, &[_, _, high, low]) => Some(TcpOption::Mss(u16::from_be_bytes([high, low]))),
        (KIND_WINDOW_SCALE, &[_, _, shift]) => Some(TcpOption::WindowScale(shift)),
        (KIND_SACK_PERMITTED, &[_, _]) => Some(TcpOption::SackPermitted),
        (KIND_TIMESTAMPS, &[_, _, v0, v1, v2, v3, e0, e1, e2, e3]) => Some(TcpOption::Timestamps(Timestamps {
            value: u32::from_be_bytes([v0, v1, v2, v3]),
            echo: u32::from_be_bytes([e0, e1, e2, e3]),
        })),
        (KIND_SACK, [_, _, blocks @ ..]) if !blocks.is_empty() && blocks.len().is_multiple_of(8) => {
            Some(TcpOption::Sack(SackBlocks(blocks)))
        }
        (KIND_MSS | KIND_WINDOW_SCALE | KIND_SACK_PERMITTED | KIND_TIMESTAMPS | KIND_SACK, _) => None,
        (_, [_, _, data @ ..]) => Some(TcpOption::Other { kind, data }),
        (_, _) => None,
    }
}

/// The options a TCP endpoint negotiates and acknowledges with: what a
/// header carries of them, read from its options area or written into one.
///
/// Absent options are `None` or empty, so a zero shift or a zero clock
/// still counts as an option the sender sent. A window scale is at most
/// [`WINDOW_SCALE_MAX`] and SACK blocks at most [`SACK_BLOCKS_MAX`]: the
/// builders hold the options to both.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct TcpOptions {
    mss: Option<u16>,
    window_scale: Option<u8>,
    sack_permitted: bool,
    sack: Sack,
    timestamps: Option<Timestamps>,
}

/// Up to [`SACK_BLOCKS_MAX`] SACK blocks, in order.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct Sack {
    blocks: [SackBlock; SACK_BLOCKS_MAX],
    len: u8,
}

impl Sack {
    #[inline]
    pub fn as_slice(&self) -> &[SackBlock] {
        &self.blocks[..usize::from(self.len)]
    }

    /// Appends a block; refused, returning `false`, when the list is full.
    #[inline]
    pub fn push(&mut self, block: SackBlock) -> bool {
        let Some(slot) = self.blocks.get_mut(usize::from(self.len)) else {
            return false;
        };
        *slot = block;
        self.len += 1;
        true
    }

    #[inline]
    pub fn as_mut_slice(&mut self) -> &mut [SackBlock] {
        &mut self.blocks[..usize::from(self.len)]
    }

    #[inline]
    pub fn clear(&mut self) {
        self.truncate(0);
    }

    #[inline]
    pub fn truncate(&mut self, len: usize) {
        if len < usize::from(self.len) {
            // The dropped blocks are cleared, so two lists of the same blocks
            // compare equal however they were built.
            self.blocks[len..usize::from(self.len)].fill(SackBlock::default());
            self.len = len as u8;
        }
    }

    #[inline]
    pub fn len(&self) -> usize {
        usize::from(self.len)
    }

    #[inline]
    pub fn is_empty(&self) -> bool {
        self.len == 0
    }
}

impl TcpOptions {
    /// The most bytes of options a TCP header holds: what its data offset can
    /// count beyond the fixed header.
    pub const MAX_LEN: usize = 40;

    #[inline]
    pub fn mss(&self) -> Option<u16> {
        self.mss
    }

    /// The window scale shift, at most [`WINDOW_SCALE_MAX`].
    #[inline]
    pub fn window_scale(&self) -> Option<u8> {
        self.window_scale
    }

    #[inline]
    pub fn sack_permitted(&self) -> bool {
        self.sack_permitted
    }

    #[inline]
    pub fn sack(&self) -> &Sack {
        &self.sack
    }

    #[inline]
    pub fn sack_mut(&mut self) -> &mut Sack {
        &mut self.sack
    }

    #[inline]
    pub fn timestamps(&self) -> Option<Timestamps> {
        self.timestamps
    }

    #[inline]
    #[must_use]
    pub fn with_mss(self, mss: Option<u16>) -> Self {
        Self { mss, ..self }
    }

    /// The options with this window scale, a larger shift than
    /// [`WINDOW_SCALE_MAX`] counting as it.
    #[inline]
    #[must_use]
    pub fn with_window_scale(self, shift: Option<u8>) -> Self {
        Self {
            window_scale: shift.map(|shift| shift.min(WINDOW_SCALE_MAX)),
            ..self
        }
    }

    #[inline]
    #[must_use]
    pub fn with_sack_permitted(self, sack_permitted: bool) -> Self {
        Self { sack_permitted, ..self }
    }

    #[inline]
    #[must_use]
    pub fn with_sack(self, sack: Sack) -> Self {
        Self { sack, ..self }
    }

    #[inline]
    #[must_use]
    pub fn with_timestamps(self, timestamps: Option<Timestamps>) -> Self {
        Self { timestamps, ..self }
    }

    /// The options of these that only a SYN carries: the maximum segment
    /// size, the window scale and SACK permitted.
    ///
    /// They are what a connection keeps of its handshake, such as in a SYN
    /// cookie, and what a SYN rebuilt from it starts from. The timestamps,
    /// negotiated on the SYN too, ride on every segment after it, so a SYN
    /// rebuilt from an ACK takes them from that ACK (RFC 7323, section 3.2).
    #[inline]
    #[must_use]
    pub fn handshake(&self) -> Self {
        Self {
            mss: self.mss,
            window_scale: self.window_scale,
            sack_permitted: self.sack_permitted,
            ..Self::default()
        }
    }

    /// The options of a SYN-ACK answering a SYN with these options, offering
    /// what `ours` offers.
    ///
    /// The maximum segment size is ours. The window scale and the timestamps
    /// are sent only when the SYN carried them (RFC 7323, sections 2.2 and
    /// 3.2), and our SACK permitted too, as common stacks answer: RFC 2018
    /// lets either side offer it alone, but a peer that offered none is taken
    /// to send no SACK. The timestamps are our clock and echo the SYN's.
    #[inline]
    #[must_use]
    pub fn answer_syn(&self, ours: &Self) -> Self {
        Self {
            mss: ours.mss,
            window_scale: self.window_scale.and(ours.window_scale),
            sack_permitted: self.sack_permitted && ours.sack_permitted,
            sack: Sack::default(),
            timestamps: match (self.timestamps, ours.timestamps) {
                (Some(theirs), Some(ours)) => Some(Timestamps {
                    value: ours.value,
                    echo: theirs.value,
                }),
                _ => None,
            },
        }
    }

    /// Reads the options of an options area.
    ///
    /// Of an option sent twice the last counts, as do the SACK blocks of
    /// every SACK option. A window scale above [`WINDOW_SCALE_MAX`] counts as
    /// it (RFC 7323, section 2.3). Options of other kinds are skipped, so
    /// options written back from what this reads lose them: a rewrite that
    /// must keep options it does not know edits them in place instead.
    ///
    /// # Errors
    ///
    /// [`ParseError::OptionLength`] for an option whose length cannot be
    /// trusted, or SACK blocks beyond [`SACK_BLOCKS_MAX`].
    #[inline]
    pub fn parse(area: &[u8]) -> Result<Self, ParseError> {
        // The same reading as [`options`], filling the options in place: a
        // packet path parses on every segment.
        const MSS: u8 = 1;
        const WINDOW_SCALE: u8 = 2;
        const SACK_PERMITTED: u8 = 4;
        const TIMESTAMPS: u8 = 8;
        let mut present = 0u8;
        let (mut mss, mut shift, mut value, mut echo) = (0u16, 0u8, 0u32, 0u32);
        let mut sack = Sack::default();
        let mut at = 0;
        while at < area.len() {
            let kind = area[at];
            match kind {
                KIND_EOL => break,
                KIND_NOP => {
                    at += 1;
                    continue;
                }
                _ => {}
            }
            // The two tests keep the option within the area.
            let refused = || ParseError::OptionLength { offset: at };
            if area.len() - at < 2 {
                return Err(refused());
            }
            let len = usize::from(area[at + 1]);
            if len < 2 || len > area.len() - at {
                return Err(refused());
            }
            let option = &area[at..at + len];
            // The length matched with the kind vouches for each field read.
            match (kind, len) {
                (KIND_MSS, LEN_MSS) => {
                    mss = be16(option, 2);
                    present |= MSS;
                }
                (KIND_WINDOW_SCALE, LEN_WINDOW_SCALE) => {
                    shift = option[2].min(WINDOW_SCALE_MAX);
                    present |= WINDOW_SCALE;
                }
                (KIND_SACK_PERMITTED, LEN_SACK_PERMITTED) => present |= SACK_PERMITTED,
                (KIND_TIMESTAMPS, LEN_TIMESTAMPS) => {
                    (value, echo) = (be32(option, 2), be32(option, 6));
                    present |= TIMESTAMPS;
                }
                (KIND_SACK, _) if len > 2 && (len - 2).is_multiple_of(8) => {
                    let blocks = (len - 2) / 8;
                    if sack.len() + blocks > SACK_BLOCKS_MAX {
                        return Err(refused());
                    }
                    for block in 0..blocks {
                        sack.push(SackBlock {
                            start: be32(option, 2 + 8 * block),
                            end: be32(option, 6 + 8 * block),
                        });
                    }
                }
                (KIND_MSS | KIND_WINDOW_SCALE | KIND_SACK_PERMITTED | KIND_TIMESTAMPS | KIND_SACK, _) => {
                    return Err(refused());
                }
                _ => {}
            }
            at += option.len();
        }
        Ok(Self {
            mss: (present & MSS != 0).then_some(mss),
            window_scale: (present & WINDOW_SCALE != 0).then_some(shift),
            sack_permitted: present & SACK_PERMITTED != 0,
            sack,
            timestamps: (present & TIMESTAMPS != 0).then_some(Timestamps { value, echo }),
        })
    }

    /// Bytes the options take when written, padded to whole 32-bit words.
    #[inline]
    pub fn len(&self) -> usize {
        let mut len = 0;
        if self.mss.is_some() {
            len += LEN_MSS;
        }
        if self.window_scale.is_some() {
            len += LEN_WINDOW_SCALE;
        }
        if self.sack_permitted {
            len += LEN_SACK_PERMITTED;
        }
        if !self.sack.is_empty() {
            len += 2 + 8 * self.sack.len();
        }
        if self.timestamps.is_some() {
            len += LEN_TIMESTAMPS;
        }
        len.next_multiple_of(4)
    }

    #[inline]
    pub fn is_empty(&self) -> bool {
        self.len() == 0
    }

    /// Drops SACK blocks, the latest first, until the options fit a TCP
    /// header.
    ///
    /// A block fewer costs the peer a retransmit it might have been spared;
    /// options that do not fit make the header unreadable, as the data
    /// offset cannot count them.
    #[inline]
    pub fn fit_to_header(&mut self) {
        while !self.sack.is_empty() && self.len() > Self::MAX_LEN {
            self.sack.truncate(self.sack.len() - 1);
        }
    }

    /// Writes the options at the start of an area and returns their length,
    /// [`TcpOptions::len`].
    ///
    /// The order is the maximum segment size, SACK permitted, SACK and
    /// timestamps, then no-operations to a word boundary, the window scale
    /// last when there is one.
    ///
    /// # Errors
    ///
    /// [`ParseError::Truncated`] when the area is shorter than the options;
    /// nothing is written then.
    #[inline]
    pub fn write(&self, area: &mut [u8]) -> Result<usize, ParseError> {
        let len = self.len();
        if area.len() < len {
            return Err(ParseError::Truncated { needed: len, available: area.len() });
        }
        let area = &mut area[..len];
        let mut at = 0;
        if let Some(mss) = self.mss {
            let [high, low] = mss.to_be_bytes();
            area[at..at + LEN_MSS].copy_from_slice(&[KIND_MSS, LEN_MSS as u8, high, low]);
            at += LEN_MSS;
        }
        if self.sack_permitted {
            area[at..at + LEN_SACK_PERMITTED].copy_from_slice(&[KIND_SACK_PERMITTED, LEN_SACK_PERMITTED as u8]);
            at += LEN_SACK_PERMITTED;
        }
        if !self.sack.is_empty() {
            area[at..at + 2].copy_from_slice(&[KIND_SACK, (2 + 8 * self.sack.len()) as u8]);
            at += 2;
            for block in self.sack.as_slice() {
                area[at..at + 4].copy_from_slice(&block.start.to_be_bytes());
                area[at + 4..at + 8].copy_from_slice(&block.end.to_be_bytes());
                at += 8;
            }
        }
        if let Some(Timestamps { value, echo }) = self.timestamps {
            area[at..at + 2].copy_from_slice(&[KIND_TIMESTAMPS, LEN_TIMESTAMPS as u8]);
            area[at + 2..at + 6].copy_from_slice(&value.to_be_bytes());
            area[at + 6..at + 10].copy_from_slice(&echo.to_be_bytes());
            at += LEN_TIMESTAMPS;
        }
        // Padding is under a word; the window scale, last, overwrites what of
        // it runs into its room.
        let rest = &mut area[at..];
        if let Some(pad) = rest.first_chunk_mut::<3>() {
            *pad = [KIND_NOP; 3];
        } else if let Some(pad) = rest.first_chunk_mut::<2>() {
            *pad = [KIND_NOP; 2];
        } else if let Some(pad) = rest.first_mut() {
            *pad = KIND_NOP;
        }
        if let Some(shift) = self.window_scale {
            area[len - LEN_WINDOW_SCALE..].copy_from_slice(&[KIND_WINDOW_SCALE, LEN_WINDOW_SCALE as u8, shift]);
        }
        Ok(len)
    }
}

/// One option of a header in a mutation, to read and change in place.
///
/// A change keeps the option's kind and length, so the options around it
/// keep their places; the changes are accounted to the mutation's checksum
/// once the option is handed back.
#[derive(Debug)]
pub struct TcpOptionMut<'a> {
    /// The whole option, its kind and length first.
    bytes: &'a mut [u8],
    /// The changes summed as if the option started a checksum word: every
    /// store starts at an even offset of the option.
    sum: u64,
}

impl TcpOptionMut<'_> {
    /// The option as it stands; one removed in place reads as an option of
    /// padding's kind, [`TcpOption::Other`] of kind 1.
    #[inline]
    pub fn option(&self) -> TcpOption<'_> {
        // The mutation checked every option it hands out, so the fallback is
        // never taken.
        let kind = self.bytes[0];
        decode(kind, self.bytes).unwrap_or(TcpOption::Other { kind, data: &[] })
    }

    /// Sets the maximum segment size; `false`, changing nothing, when the
    /// option is of another kind.
    #[inline]
    pub fn set_mss(&mut self, mss: u16) -> bool {
        if !self.is(KIND_MSS, LEN_MSS) {
            return false;
        }
        self.store(2, mss.to_be_bytes());
        true
    }

    /// Sets the window scale shift, capped at [`WINDOW_SCALE_MAX`]; `false`,
    /// changing nothing, when the option is of another kind.
    #[inline]
    pub fn set_window_scale(&mut self, shift: u8) -> bool {
        if !self.is(KIND_WINDOW_SCALE, LEN_WINDOW_SCALE) {
            return false;
        }
        self.store(2, [shift.min(WINDOW_SCALE_MAX)]);
        true
    }

    /// Sets the timestamps; `false`, changing nothing, when the option is of
    /// another kind.
    #[inline]
    pub fn set_timestamps(&mut self, Timestamps { value, echo }: Timestamps) -> bool {
        if !self.is(KIND_TIMESTAMPS, LEN_TIMESTAMPS) {
            return false;
        }
        self.store(2, value.to_be_bytes());
        self.store(6, echo.to_be_bytes());
        true
    }

    /// Replaces each SACK block by what `map` makes of it; `false`, changing
    /// nothing, when the option is of another kind.
    #[inline]
    pub fn map_sack(&mut self, mut map: impl FnMut(SackBlock) -> SackBlock) -> bool {
        if self.bytes[0] != KIND_SACK {
            return false;
        }
        for block in 0..(self.bytes.len() - 2) / 8 {
            let at = 2 + 8 * block;
            let SackBlock { start, end } = map(SackBlock {
                start: be32(self.bytes, at),
                end: be32(self.bytes, at + 4),
            });
            self.store(at, start.to_be_bytes());
            self.store(at + 4, end.to_be_bytes());
        }
        true
    }

    /// Removes the option, its bytes turned to padding.
    #[inline]
    pub fn remove(&mut self) {
        let len = self.bytes.len();
        for at in (0..len - 1).step_by(2) {
            self.store(at, [KIND_NOP; 2]);
        }
        if !len.is_multiple_of(2) {
            self.store(len - 1, [KIND_NOP]);
        }
    }

    #[inline]
    fn is(&self, kind: u8, len: usize) -> bool {
        self.bytes[0] == kind && self.bytes.len() == len
    }

    /// Stores bytes at an even offset of the option and sums the change.
    #[inline]
    fn store<const N: usize>(&mut self, at: usize, new: [u8; N]) {
        debug_assert!(at.is_multiple_of(2));
        let Some(slot) = self.bytes.get_mut(at..).and_then(<[u8]>::first_chunk_mut::<N>) else {
            return;
        };
        let old = core::mem::replace(slot, new);
        let (olds, old_last) = old.as_chunks::<2>();
        let (news, new_last) = new.as_chunks::<2>();
        for (&old, &new) in olds.iter().zip(news) {
            self.sum += u64::from(!u16::from_ne_bytes(old)) + u64::from(u16::from_ne_bytes(new));
        }
        if let (&[old], &[new]) = (old_last, new_last) {
            self.sum += u64::from(!mutation::first_byte(old)) + u64::from(mutation::first_byte(new));
        }
    }
}

impl<M> TcpMutation<'_, M>
where
    M: Mode,
{
    /// Hands each option of the header to `edit` in order, to read and
    /// change in place; padding and the end of the list are skipped.
    ///
    /// Options keep their kinds, lengths and places, so options `edit` does
    /// not change, of kinds this crate does not know included, stay as sent.
    /// A mutation of the fixed header alone holds no option.
    #[inline]
    pub fn edit_options(mut self, mut edit: impl FnMut(&mut TcpOptionMut<'_>)) -> Self {
        let mut at = 0;
        while let Some(&kind) = self.options.get(at) {
            match kind {
                KIND_EOL => break,
                KIND_NOP => {
                    at += 1;
                    continue;
                }
                _ => {}
            }
            // The mutation checked every option when it started.
            let Some(len) = self
                .options
                .get(at + 1)
                .map(|&len| usize::from(len))
                .filter(|&len| len >= 2 && len <= self.options.len() - at)
            else {
                break;
            };
            let mut option = TcpOptionMut {
                bytes: &mut self.options[at..at + len],
                sum: 0,
            };
            edit(&mut option);
            // The changes are summed as if the option started a checksum word,
            // so one in a word's second half swaps them (RFC 1071, section 2).
            let mut change = fold(option.sum);
            if !at.is_multiple_of(2) {
                change = change.swap_bytes();
            }
            // The complement of all ones is zero: the change is added alone.
            self.mode.add16(u16::MAX, change);
            at += len;
        }
        self
    }

    /// Lowers the maximum segment size to `max` when a SYN offers a larger
    /// one; other segments are left as they are.
    ///
    /// Only a SYN carries the option (RFC 9293, section 3.7.1); one on
    /// another segment is no offer and stays as sent.
    #[inline]
    pub fn clamp_mss(self, max: u16) -> Self {
        if self.header.flags() & TcpHeader::SYN == 0 {
            return self;
        }
        self.edit_options(|option| {
            if let TcpOption::Mss(mss) = option.option()
                && mss > max
            {
                option.set_mss(max);
            }
        })
    }
}

/// The big-endian word at an offset of bytes that hold it; zero past them,
/// which the callers' length checks rule out.
#[inline(always)]
fn be16(bytes: &[u8], at: usize) -> u16 {
    bytes
        .get(at..)
        .and_then(<[u8]>::first_chunk)
        .map_or(0, |&word| u16::from_be_bytes(word))
}

/// The big-endian double word at an offset, as [`be16`] reads a word.
#[inline(always)]
fn be32(bytes: &[u8], at: usize) -> u32 {
    bytes
        .get(at..)
        .and_then(<[u8]>::first_chunk)
        .map_or(0, |&word| u32::from_be_bytes(word))
}

#[cfg(test)]
mod tests {
    use core::{assert_matches, net::Ipv4Addr};

    use super::*;
    use crate::TcpPacketMut;

    /// Reads every option of the area, or the error that ends the reading.
    fn read(area: &[u8]) -> Vec<Result<TcpOption<'_>, ParseError>> {
        options(area).collect()
    }

    #[test]
    fn test_options_read_every_kind_and_stop_at_the_end_of_the_list() {
        let area = [
            KIND_MSS,
            4,
            0x05,
            0xb4,     //
            KIND_NOP, //
            KIND_WINDOW_SCALE,
            3,
            7, //
            KIND_SACK_PERMITTED,
            2, //
            KIND_TIMESTAMPS,
            10,
            0,
            0,
            0,
            1,
            0,
            0,
            0,
            2, //
            KIND_SACK,
            10,
            0,
            0,
            0,
            3,
            0,
            0,
            0,
            4, //
            34,
            4,
            0xaa,
            0xbb, //
            KIND_EOL,
            0xff,
            0xff,
        ];

        let read = read(&area);

        let sack = [SackBlock { start: 3, end: 4 }];
        assert_eq!(6, read.len());
        assert_eq!(Ok(TcpOption::Mss(1460)), read[0]);
        assert_eq!(Ok(TcpOption::WindowScale(7)), read[1]);
        assert_eq!(Ok(TcpOption::SackPermitted), read[2]);
        assert_eq!(Ok(TcpOption::Timestamps(Timestamps { value: 1, echo: 2 })), read[3]);
        assert_matches!(read[4], Ok(TcpOption::Sack(blocks)) if blocks.iter().eq(sack));
        assert_eq!(Ok(TcpOption::Other { kind: 34, data: &[0xaa, 0xbb][..] }), read[5]);
    }

    /// verifies that an option of an untrustworthy length ends the reading
    /// with an error at its offset, so a reader never stalls or overruns.
    #[test]
    fn test_options_refuse_an_unusable_length() {
        for (case, area, offset) in [
            ("a length below the header bytes", &[34, 1, 0, 0][..], 0),
            ("a zero length", &[34, 0, 0, 0][..], 0),
            ("a length past the area", &[34, 40, 0, 0][..], 0),
            ("a length byte past the area", &[KIND_NOP, 34][..], 1),
            ("an mss of another length", &[KIND_MSS, 3, 0, KIND_NOP][..], 0),
            (
                "a sack not of whole blocks",
                &[KIND_SACK, 9, 0, 0, 0, 0, 0, 0, 0][..],
                0,
            ),
            ("a sack of no block", &[KIND_SACK, 2, KIND_NOP, KIND_NOP][..], 0),
            (
                "a sack running past the area",
                &[KIND_NOP, KIND_NOP, KIND_SACK, 10, 0, 0][..],
                2,
            ),
        ] {
            let read = read(area);

            assert_eq!(Some(&Err(ParseError::OptionLength { offset })), read.last(), "{case}");
            assert_eq!(1, read.iter().filter(|option| option.is_err()).count(), "{case}");
        }
    }

    /// verifies that options sent with zero values, a zero shift or a zero
    /// clock as in every SYN's echo, read back as sent after a write.
    #[test]
    fn test_tcp_options_with_zero_values_survive_a_rewrite() {
        let options = TcpOptions {
            window_scale: Some(0),
            timestamps: Some(Timestamps { value: 0, echo: 0 }),
            ..TcpOptions::default()
        };
        let mut area = [0xee; TcpOptions::MAX_LEN];

        let len = options.write(&mut area).expect("the area holds the options");

        assert_eq!(Ok(options), TcpOptions::parse(&area[..len]));
    }

    #[test]
    fn test_tcp_options_write_their_kinds_in_order_and_pad_to_the_window_scale() {
        let options = TcpOptions {
            mss: Some(1460),
            window_scale: Some(7),
            sack_permitted: true,
            timestamps: Some(Timestamps { value: 1, echo: 2 }),
            ..TcpOptions::default()
        };
        let mut area = [0; 20];

        assert_eq!(Ok(20), options.write(&mut area));
        assert_eq!([2, 4, 5, 180, 4, 2, 8, 10, 0, 0, 0, 1, 0, 0, 0, 2, 1, 3, 3, 7], area);
    }

    #[test]
    fn test_tcp_options_count_a_larger_window_scale_as_fourteen() {
        let parsed = TcpOptions::parse(&[KIND_WINDOW_SCALE, 3, 20, KIND_NOP]);
        let built = TcpOptions::default().with_window_scale(Some(20));

        assert_eq!(Ok(Some(WINDOW_SCALE_MAX)), parsed.map(|options| options.window_scale()));
        assert_eq!(Some(WINDOW_SCALE_MAX), built.window_scale());
    }

    /// verifies that a SYN-ACK answer offers only what the SYN offered and
    /// echoes the SYN's clock.
    #[test]
    fn test_tcp_options_answer_syn_offers_only_what_the_syn_offered() {
        let ours = TcpOptions::default()
            .with_mss(Some(1400))
            .with_window_scale(Some(9))
            .with_sack_permitted(true)
            .with_timestamps(Some(Timestamps { value: 50, echo: 0 }));
        let full = TcpOptions::default()
            .with_mss(Some(1460))
            .with_window_scale(Some(7))
            .with_sack_permitted(true)
            .with_timestamps(Some(Timestamps { value: 10, echo: 0 }));
        for (case, syn, ours, answer) in [
            (
                "a syn offering everything",
                full,
                ours,
                ours.with_timestamps(Some(Timestamps { value: 50, echo: 10 })),
            ),
            (
                "a bare syn",
                TcpOptions::default(),
                ours,
                TcpOptions::default().with_mss(Some(1400)),
            ),
            (
                "our side offering nothing",
                full,
                TcpOptions::default(),
                TcpOptions::default(),
            ),
        ] {
            assert_eq!(answer, syn.answer_syn(&ours), "{case}");
        }
    }

    #[test]
    fn test_tcp_options_parse_refuses_sack_blocks_beyond_four() {
        let mut area = vec![KIND_SACK, 34];
        area.extend_from_slice(&[0; 32]);
        area.extend_from_slice(&[KIND_NOP, KIND_NOP, KIND_SACK, 10, 0, 0, 0, 0, 0, 0, 0, 0]);

        // The error names the second SACK option, behind the padding.
        assert_eq!(Err(ParseError::OptionLength { offset: 36 }), TcpOptions::parse(&area));
    }

    /// verifies that a timestamp added to four SACK blocks costs the latest
    /// block, so the options fit a header and the oldest blocks stay.
    #[test]
    fn test_tcp_options_fit_to_header_drops_the_latest_sack_blocks() {
        let mut options = TcpOptions::default();
        for (start, end) in [(1, 2), (3, 4), (5, 6), (7, 8)] {
            options.sack.push(SackBlock { start, end });
        }
        options.fit_to_header();
        assert_eq!(4, options.sack.len());

        options.timestamps = Some(Timestamps { value: 1, echo: 2 });
        options.fit_to_header();

        assert_eq!(
            [(1, 2), (3, 4), (5, 6)],
            core::array::from_fn(|idx| (options.sack.as_slice()[idx].start, options.sack.as_slice()[idx].end))
        );
        assert!(options.len() <= TcpOptions::MAX_LEN);
        // The dropped block leaves nothing behind: the options equal the same
        // ones built with three blocks.
        let mut fresh = TcpOptions {
            timestamps: options.timestamps,
            ..TcpOptions::default()
        };
        for (start, end) in [(1, 2), (3, 4), (5, 6)] {
            fresh.sack.push(SackBlock { start, end });
        }
        assert_eq!(fresh, options);
    }

    /// verifies that every option at once, more than a header holds, writes
    /// whole and reads back, as before trimming to a header.
    #[test]
    fn test_tcp_options_write_every_option_at_once() {
        let mut options = TcpOptions {
            mss: Some(1460),
            window_scale: Some(7),
            sack_permitted: true,
            timestamps: Some(Timestamps { value: 1, echo: 2 }),
            ..TcpOptions::default()
        };
        for (start, end) in [(1, 2), (3, 4), (5, 6), (7, 8)] {
            options.sack.push(SackBlock { start, end });
        }
        let mut area = [0; WRITTEN_MAX];

        assert_eq!(Ok(WRITTEN_MAX), options.write(&mut area));
        assert_eq!(Ok(options), TcpOptions::parse(&area));
    }

    #[test]
    fn test_tcp_options_write_to_a_short_area_is_refused_and_writes_nothing() {
        let options = TcpOptions {
            mss: Some(1460),
            ..TcpOptions::default()
        };
        let mut area = [0xee; 3];

        assert_eq!(
            Err(ParseError::Truncated { needed: 4, available: 3 }),
            options.write(&mut area)
        );
        assert_eq!([0xee; 3], area);
    }

    /// A TCP segment of the flags, options area and payload whose checksum
    /// is filled over an IPv4 pseudo header.
    fn segment(flags: u8, area: &[u8], payload: &[u8]) -> Vec<u8> {
        let mut segment = vec![0; 20];
        segment[0..4].copy_from_slice(&[0x9c, 0x40, 0x01, 0xbb]);
        segment[12] = (((20 + area.len()) / 4) << 4) as u8;
        segment[13] = flags;
        segment.extend_from_slice(area);
        segment.extend_from_slice(payload);
        TcpHeader::fill_checksum_ipv4(&mut segment, SRC, DST).expect("the segment holds a header");
        segment
    }

    const SRC: Ipv4Addr = Ipv4Addr::new(10, 0, 0, 1);
    const DST: Ipv4Addr = Ipv4Addr::new(10, 0, 0, 2);

    /// verifies that a mutation over options whose length cannot be trusted
    /// is refused before anything changes.
    #[test]
    fn test_tcp_mutation_refuses_unusable_options() {
        let mut segment = segment(0x10, &[KIND_MSS, 3, 0, 0], &[]);
        let before = segment.clone();
        let mut tcp = TcpPacketMut::parse(&mut segment).expect("the segment parses");

        assert_matches!(tcp.mutate(), Err(ParseError::OptionLength { offset: 0 }));
        assert_eq!(before, segment);
    }

    proptest::proptest! {
        #![proptest_config(crate::testing::config())]

        /// verifies that options edited in place at any offset keep the
        /// checksum valid, unknown options as sent, and clamp a SYN alone.
        #[test]
        fn test_tcp_mutation_edit_options_keeps_the_checksum(
            pad in 0usize..4,
            mss in proptest::num::u16::ANY,
            value in proptest::num::u32::ANY,
            echo in proptest::num::u32::ANY,
            shift in proptest::num::u32::ANY,
            remove in proptest::bool::ANY,
            syn in proptest::bool::ANY,
            payload in proptest::collection::vec(proptest::num::u8::ANY, 0..9),
        ) {
            let mut area = vec![KIND_NOP; pad];
            area.extend_from_slice(&[KIND_MSS, 4, 0x05, 0xb4, 34, 3, 0xab]);
            area.extend_from_slice(&[KIND_TIMESTAMPS, 10, 0, 0, 0, 1, 0, 0, 0, 2]);
            area.extend_from_slice(&[KIND_SACK, 10, 0, 0, 0, 3, 0, 0, 0, 4]);
            area.resize(area.len().next_multiple_of(4), KIND_EOL);
            let flags = if syn { 0x12 } else { 0x10 };
            let mut segment = segment(flags, &area, &payload);
            let mut tcp = TcpPacketMut::parse(&mut segment).expect("the segment parses");

            tcp.mutate()
                .expect("the options are usable")
                .set_ack(value)
                .clamp_mss(mss)
                .edit_options(|option| match option.option() {
                    TcpOption::Timestamps(_) => {
                        option.set_timestamps(Timestamps { value, echo });
                    }
                    TcpOption::Sack(_) if remove => option.remove(),
                    TcpOption::Sack(_) => {
                        option.map_sack(|block| block.wrapping_add(shift));
                    }
                    _ => {}
                })
                .finish();

            let mut filled = segment.clone();
            TcpHeader::fill_checksum_ipv4(&mut filled, SRC, DST).expect("the segment holds a header");
            proptest::prop_assert_eq!(&filled[16..18], &segment[16..18]);
            let unknown = TcpOption::Other { kind: 34, data: &[0xab] };
            let kept = options(&segment[20..20 + area.len()]).any(|option| option == Ok(unknown));
            proptest::prop_assert!(kept);
            let clamped = if syn { mss.min(1460) } else { 1460 };
            let offered = options(&segment[20..20 + area.len()]).any(|option| option == Ok(TcpOption::Mss(clamped)));
            proptest::prop_assert!(offered);
        }
    }

    /// The options the iterator reads, gathered as [`TcpOptions::parse`]
    /// gathers them, or the error that ends the reading.
    fn gathered(area: &[u8]) -> Result<TcpOptions, ParseError> {
        let mut gathered = TcpOptions::default();
        let mut iter = options(area);
        let mut at = 0;
        while let Some(option) = iter.next() {
            // The offset of this option: past the padding after the last one.
            while area.get(at) == Some(&KIND_NOP) {
                at += 1;
            }
            match option? {
                TcpOption::Mss(mss) => gathered.mss = Some(mss),
                TcpOption::WindowScale(shift) => gathered.window_scale = Some(shift.min(WINDOW_SCALE_MAX)),
                TcpOption::SackPermitted => gathered.sack_permitted = true,
                TcpOption::Sack(blocks) => {
                    if gathered.sack.len() + blocks.len() > SACK_BLOCKS_MAX {
                        return Err(ParseError::OptionLength { offset: at });
                    }
                    for block in blocks.iter() {
                        gathered.sack.push(block);
                    }
                }
                TcpOption::Timestamps(timestamps) => gathered.timestamps = Some(timestamps),
                TcpOption::Other { .. } => {}
            }
            at = iter.at;
        }
        Ok(gathered)
    }

    proptest::proptest! {
        #![proptest_config(crate::testing::config())]

        /// verifies that parsing an area gives what reading it option by
        /// option gives, options or error alike, for any bytes.
        #[test]
        fn test_tcp_options_parse_agrees_with_the_iterator(
            area in proptest::collection::vec(
                proptest::prop_oneof![0u8..=10, proptest::num::u8::ANY],
                0..48,
            )
        ) {
            proptest::prop_assert_eq!(gathered(&area), TcpOptions::parse(&area));
        }
    }
}
