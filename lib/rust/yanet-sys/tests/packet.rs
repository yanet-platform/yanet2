//! Packet operations over the invocation harness: allocating and copying
//! packets from the worker's pool, passing the whole input, walking it
//! without taking packets, and growing and shrinking a frame at either end.
//!
//! The headroom growth is the Rust twin of the C routine, so these cases
//! run under Miri too; the unit tests compare the twin with C.

use yanet_packet as _;
use yanet_sdk_testing::{
    BlockArena,
    dp::{Fate, Frame, Invocation, Outcome, POOL_PREPEND_ROOM, TestPacket, alloc_config},
};
use yanet_shm as _;
use yanet_sys::dp::{Batch, CommitCx, Cx, Module, Verdict};

/// A configuration of one plain word.
type Word = u64;

/// Runs `M` on the packets with a pool of the given capacity and returns
/// the outcome and the pool packets left outstanding.
fn run<M: Module<Config = Word>>(packets: Vec<TestPacket>, pool: usize) -> (Outcome, usize) {
    let arena = BlockArena::new();
    let config = alloc_config::<Word>(arena);
    // SAFETY: a plain word, never written.
    let mut invocation = unsafe { Invocation::<M>::new(arena, config, 0) };
    invocation.set_pool_capacity(pool);
    let outcome = invocation.run(packets);
    (outcome, invocation.outstanding())
}

/// Non-IP packets of 60 bytes, the first byte of packet `n` being `n`.
fn numbered(count: usize) -> Vec<TestPacket> {
    (0..count)
        .map(|idx| TestPacket::ether(0x0806, vec![idx as u8; 60]))
        .collect()
}

/// An IPv4/UDP frame behind 16 bytes of headroom, with the parsed offsets.
fn ipv4_udp() -> TestPacket {
    let mut frame = vec![0u8; 14 + 20 + 8];
    frame[12..14].copy_from_slice(&[0x08, 0x00]);
    frame[14] = 0x45;
    frame[14 + 3] = 28;
    frame[14 + 8] = 64;
    frame[14 + 9] = 17;
    frame[14 + 12..14 + 16].copy_from_slice(&[192, 0, 2, 1]);
    frame[14 + 16..14 + 20].copy_from_slice(&[192, 0, 2, 2]);
    TestPacket {
        headroom: 16,
        transport_type: 17,
        transport_offset: 34,
        ..TestPacket::ether(0x0800, frame)
    }
}

/// Prepends a 4-byte tag in front of every packet.
struct Tagger;

impl Module for Tagger {
    const NAME: &'static str = "tagger";
    type Config = Word;
    type Prepared<'p> = ();
    fn prepare(_: &CommitCx<'_, Self>) {}

    fn handle(_: &mut Cx<Self>, batch: &mut Batch) {
        batch.dispatch(|packet| match packet.prepend(4) {
            Ok(tag) => {
                tag.copy_from_slice(b"TAG!");
                Verdict::Pass
            }
            Err(_) => Verdict::Drop,
        });
    }
}

/// verifies that a prepend writes in front of the frame and moves the parsed
/// offsets with it, so they name the same headers.
#[test]
fn test_packet_prepend_shifts_parsed_offsets() {
    let sent = ipv4_udp();
    let mut expected = b"TAG!".to_vec();
    expected.extend_from_slice(&sent.frame);

    let (outcome, _) = run::<Tagger>(vec![sent], 0);

    assert_eq!(vec![Fate::Output], outcome.fates);
    assert_eq!(
        vec![Frame {
            bytes: expected,
            network_offset: 18,
            transport_offset: 38,
            pkt_len: 4 + 14 + 20 + 8,
        }],
        outcome.frames
    );
}

/// Prepends a number of bytes to every packet: passes it when accepted,
/// with the new bytes set to 0xaa, drops it when refused.
struct PrependBy<const N: usize>;

impl<const N: usize> Module for PrependBy<N> {
    const NAME: &'static str = "prepend_by";
    type Config = Word;
    type Prepared<'p> = ();
    fn prepare(_: &CommitCx<'_, Self>) {}

    fn handle(_: &mut Cx<Self>, batch: &mut Batch) {
        batch.dispatch(|packet| match packet.prepend(N) {
            Ok(room) => {
                room.fill(0xaa);
                Verdict::Pass
            }
            Err(_) => Verdict::Drop,
        });
    }
}

/// verifies that a prepend takes exactly the room the input packet's
/// headroom holds beyond the descriptor that shares its buffer.
#[test]
fn test_packet_prepend_up_to_input_headroom_succeeds() {
    let sent = ipv4_udp();
    let mut expected = vec![0xaa; 16];
    expected.extend_from_slice(&sent.frame);

    let (outcome, _) = run::<PrependBy<16>>(vec![sent], 0);

    assert_eq!(vec![Fate::Output], outcome.fates);
    assert_eq!(expected, outcome.frames[0].bytes);
}

/// verifies that a prepend beyond the headroom is refused and leaves the
/// frame and its offsets as they were.
#[test]
fn test_packet_prepend_beyond_headroom_is_refused() {
    let sent = ipv4_udp();
    let frame = sent.frame.clone();
    let pkt_len = frame.len() as u32;

    let (outcome, _) = run::<PrependBy<17>>(vec![sent], 0);

    assert_eq!(vec![Fate::Drop], outcome.fates);
    assert_eq!(
        vec![Frame {
            bytes: frame,
            network_offset: 14,
            transport_offset: 34,
            pkt_len,
        }],
        outcome.frames
    );
}

/// Prepends the whole room of a pool packet to one, and one byte more to
/// another.
struct PoolPrepender;

impl Module for PoolPrepender {
    const NAME: &'static str = "pool_prepender";
    type Config = Word;
    type Prepared<'p> = ();
    fn prepare(_: &CommitCx<'_, Self>) {}

    fn handle(_: &mut Cx<Self>, batch: &mut Batch) {
        batch.pass_all();
        if let Some(mut exact) = batch.alloc() {
            match exact.prepend(POOL_PREPEND_ROOM) {
                Ok(room) => {
                    room.fill(0xbb);
                    batch.pass(exact);
                }
                Err(_) => batch.drop(exact),
            }
        }
        if let Some(mut past) = batch.alloc() {
            match past.prepend(POOL_PREPEND_ROOM + 1) {
                Ok(_) => batch.pass(past),
                Err(_) => batch.drop(past),
            }
        }
    }
}

/// verifies that a pool packet takes a prepend up to the headroom beyond
/// its descriptor, as C checks it, and refuses one byte past it, leaving
/// the packet empty and its headers unparsed.
#[test]
fn test_packet_prepend_pool_packet_limit_is_the_headroom_beyond_the_descriptor() {
    let (outcome, outstanding) = run::<PoolPrepender>(vec![ipv4_udp()], 4);

    let generated: Vec<_> = outcome
        .generated
        .into_iter()
        .map(|(fate, frame)| (fate, frame.bytes, frame.network_offset, frame.transport_offset))
        .collect();
    assert_eq!(
        vec![
            (Fate::Output, vec![0xbb; POOL_PREPEND_ROOM], 0, 0),
            (Fate::Drop, vec![], 0, 0)
        ],
        generated
    );
    assert_eq!(0, outstanding);
}

/// Appends a trailer and trims it again, then appends two bytes.
struct Trailer;

impl Module for Trailer {
    const NAME: &'static str = "trailer";
    type Config = Word;
    type Prepared<'p> = ();
    fn prepare(_: &CommitCx<'_, Self>) {}

    fn handle(_: &mut Cx<Self>, batch: &mut Batch) {
        batch.dispatch(|packet| {
            let grown = packet.append(6).map(|tail| tail.fill(0xee)).is_ok();
            let trimmed = packet.trim(6).is_ok();
            let tail = packet.append(2).map(|tail| tail.copy_from_slice(b"ok")).is_ok();
            if grown && trimmed && tail {
                Verdict::Pass
            } else {
                Verdict::Drop
            }
        });
    }
}

/// verifies that appends and trims resize the tail within the tailroom and
/// keep the mbuf's packet length in step.
#[test]
fn test_packet_append_and_trim_resize_the_tail() {
    let sent = TestPacket { tailroom: 8, ..ipv4_udp() };
    let mut expected = sent.frame.clone();
    expected.extend_from_slice(b"ok");

    let (outcome, _) = run::<Trailer>(vec![sent], 0);

    assert_eq!(vec![Fate::Output], outcome.fates);
    assert_eq!(
        (expected.len() as u32, expected),
        (outcome.frames[0].pkt_len, outcome.frames[0].bytes.clone())
    );
}

/// Appends one byte: passes the packet when refused, drops it otherwise.
struct Overflow;

impl Module for Overflow {
    const NAME: &'static str = "overflow";
    type Config = Word;
    type Prepared<'p> = ();
    fn prepare(_: &CommitCx<'_, Self>) {}

    fn handle(_: &mut Cx<Self>, batch: &mut Batch) {
        batch.dispatch(|packet| match packet.append(1) {
            Ok(_) => Verdict::Drop,
            Err(_) => Verdict::Pass,
        });
    }
}

/// verifies that appending past the tailroom is refused and leaves the
/// frame as it was.
#[test]
fn test_packet_append_beyond_tailroom_is_refused() {
    let sent = ipv4_udp();
    let frame = sent.frame.clone();

    let (outcome, _) = run::<Overflow>(vec![sent], 0);

    assert_eq!(vec![Fate::Output], outcome.fates);
    assert_eq!(frame, outcome.frames[0].bytes);
}

/// Trims as many bytes off every packet as its first byte says, and passes
/// it when the trim is accepted, drops it when refused.
struct TrimBy;

impl Module for TrimBy {
    const NAME: &'static str = "trim_by";
    type Config = Word;
    type Prepared<'p> = ();
    fn prepare(_: &CommitCx<'_, Self>) {}

    fn handle(_: &mut Cx<Self>, batch: &mut Batch) {
        batch.dispatch(|packet| {
            let len = usize::from(packet.data()[0]);
            match packet.trim(len) {
                Ok(()) => Verdict::Pass,
                Err(_) => Verdict::Drop,
            }
        });
    }
}

/// A frame of the given network type and header length, then a parsed
/// transport header of the given tag and length when it has one, then four
/// payload bytes; its first byte is the trim to ask for.
fn layered(network_type: u16, network_len: usize, transport: Option<(u16, usize)>, cut: u8) -> TestPacket {
    let transport_len = transport.map_or(0, |(_, len)| len);
    let mut packet = TestPacket::ether(network_type, vec![0; 14 + network_len + transport_len + 4]);
    packet.frame[0] = cut;
    if let Some((tag, _)) = transport {
        packet.transport_type = tag;
        packet.transport_offset = (14 + network_len) as u16;
    }
    packet
}

/// verifies that a trim takes the payload behind the parsed headers and
/// refuses one byte more, which would cut into the header the case names or
/// move a parsed header start past the frame, leaving the frame unchanged.
#[test]
fn test_packet_trim_keeps_the_parsed_headers() {
    let unavailable = 0x100;
    for (case, network_type, network_len, transport, room) in [
        ("udp header", 0x0800, 20, Some((17, 8)), 4),
        ("tcp header", 0x0800, 20, Some((6, 20)), 4),
        ("ipv4 header", 0x0800, 20, None, 4),
        ("ipv6 header", 0x86dd, 40, None, 4),
        (
            "unavailable udp starts in the frame",
            0x0800,
            20,
            Some((17 | unavailable, 8)),
            12,
        ),
        ("non-ip network starts in the frame", 0x0806, 28, None, 32),
    ] {
        let sent = vec![
            layered(network_type, network_len, transport, room),
            layered(network_type, network_len, transport, room + 1),
        ];
        let len = sent[0].frame.len();

        let (outcome, _) = run::<TrimBy>(sent, 0);

        assert_eq!(vec![Fate::Output, Fate::Drop], outcome.fates, "{case}");
        let lens: Vec<_> = outcome.frames.iter().map(|frame| frame.bytes.len()).collect();
        assert_eq!(vec![len - usize::from(room), len], lens, "{case}");
    }
}

/// verifies that a trim of an unparsed packet takes up to the whole frame
/// and refuses one byte past it.
#[test]
fn test_packet_trim_of_unparsed_packet_up_to_the_frame_length() {
    let unparsed = |cut: u8| TestPacket {
        network_type: 0,
        network_offset: 0,
        ..TestPacket::ether(0, vec![cut; 42])
    };

    let (outcome, _) = run::<TrimBy>(vec![unparsed(42), unparsed(43)], 0);

    assert_eq!(vec![Fate::Output, Fate::Drop], outcome.fates);
    assert_eq!(
        vec![0, 42],
        outcome.frames.iter().map(|frame| frame.bytes.len()).collect::<Vec<_>>()
    );
}

/// Allocates two packets per call: emits one with a byte of payload and
/// drops the other.
struct Emitter;

impl Module for Emitter {
    const NAME: &'static str = "emitter";
    type Config = Word;
    type Prepared<'p> = ();
    fn prepare(_: &CommitCx<'_, Self>) {}

    fn handle(_: &mut Cx<Self>, batch: &mut Batch) {
        batch.pass_all();
        let Some(mut packet) = batch.alloc() else {
            return;
        };
        if let Ok(byte) = packet.append(1) {
            byte[0] = 0x5a;
        }
        if let Some(other) = batch.alloc() {
            batch.drop(other);
        }
        batch.pass(packet);
    }
}

/// verifies that an allocated packet the handler passes leaves after the
/// input packets, that one it drops ends on the drop list, and that the
/// pool has nothing outstanding afterwards.
#[test]
fn test_batch_alloc_ends_by_verdict() {
    let (outcome, outstanding) = run::<Emitter>(vec![ipv4_udp()], 4);

    let generated: Vec<_> = outcome
        .generated
        .into_iter()
        .map(|(fate, frame)| (fate, frame.bytes))
        .collect();
    assert_eq!(vec![(Fate::Output, vec![0x5a]), (Fate::Drop, vec![])], generated);
    assert_eq!(
        (2, 1, 0),
        (outcome.front.output_count, outcome.front.drop_count, outstanding)
    );
}

#[test]
fn test_batch_alloc_from_exhausted_pool_is_none() {
    let (outcome, outstanding) = run::<Emitter>(vec![ipv4_udp()], 0);

    assert_eq!(
        (0, 1, 0, 0),
        (
            outcome.generated.len(),
            outcome.front.output_count,
            outcome.front.drop_count,
            outstanding
        )
    );
}

/// Copies every packet, marks the copy's first byte and passes both.
struct Cloner;

impl Module for Cloner {
    const NAME: &'static str = "cloner";
    type Config = Word;
    type Prepared<'p> = ();
    fn prepare(_: &CommitCx<'_, Self>) {}

    fn handle(_: &mut Cx<Self>, batch: &mut Batch) {
        while let Some(packet) = batch.next() {
            if let Some(mut copy) = batch.clone(&packet) {
                copy.data_mut()[0] = 0xff;
                batch.pass(copy);
            }
            batch.pass(packet);
        }
    }
}

/// verifies that a copy carries the source's frame and parsed offsets,
/// that writing it leaves the source unchanged, and that it ends by its
/// verdict with nothing left in the pool.
#[test]
fn test_batch_clone_copies_frame_and_offsets() {
    let source = ipv4_udp();
    let frame = Frame {
        bytes: source.frame.clone(),
        network_offset: 14,
        transport_offset: 34,
        pkt_len: source.frame.len() as u32,
    };
    let mut copied = frame.clone();
    copied.bytes[0] = 0xff;

    let (outcome, outstanding) = run::<Cloner>(vec![source], 4);

    assert_eq!(
        (vec![Fate::Output], vec![frame], vec![(Fate::Output, copied)]),
        (outcome.fates, outcome.frames, outcome.generated)
    );
    assert_eq!((2, 0), (outcome.front.output_count, outstanding));
}

#[test]
fn test_batch_clone_from_exhausted_pool_is_none() {
    let (outcome, outstanding) = run::<Cloner>(vec![ipv4_udp()], 0);

    assert_eq!(
        (vec![Fate::Output], 0, 1, 0),
        (
            outcome.fates,
            outcome.generated.len(),
            outcome.front.output_count,
            outstanding
        )
    );
}

/// Passes the whole input at once.
struct PassAll;

impl Module for PassAll {
    const NAME: &'static str = "pass_all";
    type Config = Word;
    type Prepared<'p> = ();
    fn prepare(_: &CommitCx<'_, Self>) {}

    fn handle(_: &mut Cx<Self>, batch: &mut Batch) {
        batch.pass_all();
    }
}

/// verifies that every input packet is passed in order with the input
/// counters moved to the output counters.
#[test]
fn test_batch_pass_all_moves_input_to_output() {
    let (outcome, _) = run::<PassAll>(numbered(3), 0);

    assert_eq!(vec![Fate::Output; 3], outcome.fates);
    assert_eq!(
        (0, 0, 3, 180),
        (
            outcome.front.input_count,
            outcome.front.input_bytes,
            outcome.front.output_count,
            outcome.front.output_bytes
        )
    );
}

/// Peeks at every packet for the largest first byte, then passes the
/// packets carrying it and drops the others.
struct Peeker;

impl Module for Peeker {
    const NAME: &'static str = "peeker";
    type Config = Word;
    type Prepared<'p> = ();
    fn prepare(_: &CommitCx<'_, Self>) {}

    fn handle(_: &mut Cx<Self>, batch: &mut Batch) {
        let max = batch
            .peek()
            .filter(|view| view.network_offset() == 14)
            .map(|view| view.data()[0])
            .max();
        batch.dispatch(|packet| {
            if Some(packet.data()[0]) == max {
                Verdict::Pass
            } else {
                Verdict::Drop
            }
        });
    }
}

/// verifies that peeking reads every input packet in order without taking
/// one, so the packets taken afterwards are the whole input.
#[test]
fn test_batch_peek_reads_without_taking() {
    let (outcome, _) = run::<Peeker>(numbered(3), 0);

    assert_eq!(vec![Fate::Drop, Fate::Drop, Fate::Output], outcome.fates);
}

yanet_sdk_testing::define_pool_services!();
