//! Unit tests over synthetic packets and fronts.
//!
//! The fixtures allocate the mbuf-shaped backing store by hand, so the
//! list, geometry and metadata mechanics run under `cargo test` without
//! the dataplane binary; the dataplane_ut suite covers the real path.

use std::{prelude::v1::*, vec};

use crate::{
    front::{packet_list_add, packet_list_pop},
    packet::Packet,
    raw,
};

const DATA_OFF: usize = 192;

/// 64-byte-aligned storage: the mbuf mirror carries DPDK's cacheline
/// alignment, so the fixture block must match it.
#[repr(align(64))]
struct AlignedBlock([u8; 512]);

/// A minimal mbuf-shaped fixture: one aligned block holding the mbuf
/// (with the packet descriptor in its headroom) and the packet bytes.
struct TestPacket {
    // Owns the block; the heap allocation never moves, so the raw
    // pointers below stay valid for the fixture's life.
    _storage: Box<AlignedBlock>,
    packet: *mut raw::Packet,
}

impl TestPacket {
    fn new(data: &[u8]) -> Self {
        let storage = Box::new(AlignedBlock([0u8; 512]));
        let base = storage.0.as_ptr() as *mut u8;
        let mbuf = base as *mut raw::RteMbuf;
        let packet;
        // SAFETY: the block is 512 bytes; the mbuf takes the first 128,
        // the descriptor 48 bytes of the headroom and the data the tail,
        // all disjoint for the fixture sizes used here.
        unsafe {
            (*mbuf).buf_addr = base as *mut core::ffi::c_void;
            (*mbuf).data_off = DATA_OFF as u16;
            (*mbuf).data_len = data.len() as u16;
            (*mbuf).pkt_len = data.len() as u32;
            (*mbuf).nb_segs = 1;
            core::ptr::copy_nonoverlapping(data.as_ptr(), base.add(DATA_OFF), data.len());

            packet = base.add(DATA_OFF - 48) as *mut raw::Packet;
            // SAFETY: any bit pattern is valid for the descriptor's
            // tail; only the named fields are read by the tests.
            let mut descriptor: raw::Packet = core::mem::zeroed();
            descriptor.mbuf = mbuf;
            descriptor.data_len = data.len() as u16;
            core::ptr::write(packet, descriptor);
        }
        Self { _storage: storage, packet }
    }

    fn packet_mut(&mut self) -> Packet<'_> {
        // SAFETY: the descriptor is live and unlinked for the test.
        unsafe { Packet::from_raw(self.packet) }
    }
}

#[test]
fn list_add_pop_round_trip() {
    let first = TestPacket::new(&[1, 2, 3]);
    let second = TestPacket::new(&[4]);
    let (first_raw, second_raw) = (first.packet, second.packet);
    let mut list = raw::PacketList {
        first: core::ptr::null_mut(),
        last: core::ptr::null_mut(),
    };

    // SAFETY: the packets are unlinked and the list starts empty.
    unsafe {
        packet_list_add(&mut list, first_raw);
        packet_list_add(&mut list, second_raw);

        assert_eq!(packet_list_pop(&mut list), Some(first_raw));
        assert_eq!(packet_list_pop(&mut list), Some(second_raw));
        assert_eq!(packet_list_pop(&mut list), None);
    }
}

#[test]
fn front_counts_and_views() {
    let mut tp = TestPacket::new(&[
        0x01, 0x02, 0x03, 0x04, 0x05, 0x06, // dst mac
        0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, // src mac
        0x08, 0x00, // IPv4
        0x45, 0x00, 0x00, 0x14, // IPv4 header start
    ]);
    let mut front = raw::PacketFront {
        input: empty_list(),
        output: empty_list(),
        drop: empty_list(),
        pending_input_count: 0,
        pending_input_bytes: 0,
        pending_output_count: 0,
        pending_output_bytes: 0,
        input_count: 0,
        input_bytes: 0,
        output_count: 0,
        output_bytes: 0,
        drop_count: 0,
        drop_bytes: 0,
    };

    tp.packet_mut().set_tx_device_id(7);
    // Queue the packet the way a framework switch would: listed as
    // input with its byte accounting.
    // SAFETY: the packet is unlinked and the front starts empty.
    unsafe {
        packet_list_add(&mut front.input, tp.packet);
        front.input_count = 1;
        front.input_bytes = 18;
    }
    {
        // SAFETY: the front is live and uniquely wrapped.
        let mut front = unsafe { crate::PacketFront::from_raw(&mut front) };
        let packet = front.pop_input().expect("input present");
        assert_eq!(packet.tx_device_id(), 7);
        assert_eq!(packet.len(), 18);
        assert_eq!(packet.total_len(), 18);
        // The fixture left the network offset at the Ethernet header.
        let eth = packet.eth().expect("eth parses");
        assert_eq!(eth.ether_type(), 0x0800);
        front.output(packet);
        assert_eq!(front.output_count(), 1);
        assert_eq!(front.output_bytes(), 18);
    }
    // SAFETY: the front is well-formed after the wrapper.
    assert!(unsafe { packet_list_pop(&mut front.output).is_some() });
}

fn empty_list() -> raw::PacketList {
    raw::PacketList {
        first: core::ptr::null_mut(),
        last: core::ptr::null_mut(),
    }
}

#[test]
fn recirc_budget_semantics() {
    let mut tp = TestPacket::new(&[9]);
    let mut packet = tp.packet_mut();

    // A fresh lineage seeds from the limit, then drains one per call.
    assert!(packet.recirc_try_redirect(3));
    assert!(packet.recirc_try_redirect(3));
    assert!(packet.recirc_try_redirect(3));
    assert!(!packet.recirc_try_redirect(3));
}

#[test]
fn strip_and_prepend_geometry() {
    let mut tp = TestPacket::new(&[0x08, 0x00, 0x45, 0x00, 1, 2, 3, 4]);
    {
        let mut packet = tp.packet_mut();
        assert!(packet.strip_head(2));
        assert_eq!(packet.len(), 6);
        let data = packet.data().to_vec();
        assert_eq!(data, vec![0x45, 0x00, 1, 2, 3, 4]);

        // Prepending the same bytes restores the geometry.
        let head = packet.prepend_head(2).expect("headroom available");
        head[..2].copy_from_slice(&[0x08, 0x00]);
        assert_eq!(packet.len(), 8);
        assert_eq!(packet.data()[..4], [0x08, 0x00, 0x45, 0x00]);
    }
    // The descriptor cache stays in sync with the mbuf.
    // SAFETY: the packet mirror is live for the test.
    let raw = unsafe { &*tp.packet };
    // SAFETY: the mbuf is live for the test.
    unsafe {
        assert_eq!(raw.data_len, (*raw.mbuf).data_len);
    }
}
