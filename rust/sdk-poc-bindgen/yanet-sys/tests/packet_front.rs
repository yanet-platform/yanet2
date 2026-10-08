//! Packet front list handling over Rust-allocated packets.

use core::ptr::NonNull;

use yanet_sys::{bindings, packet::PacketFront};

/// One packet with a single-segment mbuf of `len` bytes.
struct TestPacket {
    packet: *mut bindings::packet,
    mbuf: *mut bindings::rte_mbuf,
    data: *mut [u8],
}

impl TestPacket {
    fn new(len: u16, fill: u8) -> Self {
        let data = Box::into_raw(vec![fill; usize::from(len) + 16].into_boxed_slice());
        // SAFETY: zero is a valid bit pattern for both C structs.
        let (packet, mbuf) = unsafe {
            (
                Box::into_raw(Box::new(core::mem::zeroed::<bindings::packet>())),
                Box::into_raw(Box::new(core::mem::zeroed::<bindings::rte_mbuf>())),
            )
        };
        // SAFETY: freshly allocated, exclusively owned.
        unsafe {
            (*mbuf).buf_addr = data.cast();
            (*mbuf).data_off = 16;
            (*mbuf).data_len = len;
            (*mbuf).pkt_len = u32::from(len);
            (*packet).mbuf = mbuf;
            (*packet).data_len = len;
        }
        Self { packet, mbuf, data }
    }
}

impl Drop for TestPacket {
    fn drop(&mut self) {
        // SAFETY: allocated in `new`, freed once.
        unsafe {
            drop(Box::from_raw(self.packet));
            drop(Box::from_raw(self.mbuf));
            drop(Box::from_raw(self.data));
        }
    }
}

fn front_with(packets: &[TestPacket]) -> Box<bindings::packet_front> {
    // SAFETY: zero is a valid, empty front.
    let mut front = Box::new(unsafe { core::mem::zeroed::<bindings::packet_front>() });
    let mut last: *mut *mut bindings::packet = &raw mut front.input.first;
    for packet in packets {
        // SAFETY: test-owned list.
        unsafe { *last = packet.packet };
        last = unsafe { &raw mut (*packet.packet).next };
    }
    front.input.last = if packets.is_empty() {
        core::ptr::null_mut()
    } else {
        last
    };
    front
}

/// Collects a list as packet pointers.
fn list(list: &bindings::packet_list) -> Vec<*mut bindings::packet> {
    let mut out = Vec::new();
    let mut cursor = list.first;
    while !cursor.is_null() {
        out.push(cursor);
        // SAFETY: test-owned list.
        cursor = unsafe { (*cursor).next };
    }
    out
}

/// Verifies that packets keep their order through pop and output or drop,
/// that the lists are linked as the C list add links them, and that the
/// counters add up.
#[test]
fn test_packet_front_pop_output_drop_order_and_counters() {
    let packets: Vec<_> = (0..5).map(|idx| TestPacket::new(60 + idx, idx as u8)).collect();
    let mut raw = front_with(&packets);
    {
        // SAFETY: the front and its packets are exclusively ours.
        let mut front = unsafe { PacketFront::from_raw(NonNull::from(&mut *raw)) };
        let mut idx = 0;
        while let Some(packet) = front.pop() {
            assert_eq!(idx as u8, packet.data()[0]);
            if idx % 2 == 0 {
                front.output(packet);
            } else {
                front.drop(packet);
            }
            idx += 1;
        }
        assert_eq!(5, idx);
    }
    assert!(raw.input.first.is_null() && raw.input.last.is_null());
    let expected_output: Vec<_> = packets.iter().step_by(2).map(|p| p.packet).collect();
    let expected_drop: Vec<_> = packets.iter().skip(1).step_by(2).map(|p| p.packet).collect();
    assert_eq!(expected_output, list(&raw.output));
    assert_eq!(expected_drop, list(&raw.drop));
    let tail = packets[4].packet;
    // SAFETY: the last-next pointer of a non-empty list is the tail's link.
    assert_eq!(unsafe { &raw mut (*tail).next }, raw.output.last);
    assert_eq!((3, 60 + 62 + 64), (raw.output_count, raw.output_bytes));
    assert_eq!((2, 61 + 63), (raw.drop_count, raw.drop_bytes));
}

/// Verifies that packet data and the network bytes are the first segment
/// from the data offset, bounded by the segment length.
#[test]
fn test_packet_data_is_first_segment() {
    let packets = [TestPacket::new(40, 7)];
    // SAFETY: test-owned packet.
    unsafe {
        (*packets[0].packet).network_header.offset = 14;
        (*packets[0].packet).network_header.type_ = 0x0008;
        (*packets[0].data)[16 + 14] = 0x45;
    }
    let mut raw = front_with(&packets);
    // SAFETY: the front and its packet are exclusively ours.
    let mut front = unsafe { PacketFront::from_raw(NonNull::from(&mut *raw)) };
    let mut packet = front.pop().unwrap();
    assert_eq!(40, packet.data().len());
    assert_eq!(0x0800, packet.network_type());
    assert_eq!(26, packet.network_data().len());
    assert_eq!(0x45, packet.network_data()[0]);
    packet.set_flow_label(0xabcde);
    assert_eq!(0xabcde, packet.flow_label());
    front.output(packet);
}
