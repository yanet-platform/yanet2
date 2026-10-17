//! Runs a module handler over packets parsed by the real C parser.
//!
//! Handlers are passed as untyped function addresses so this crate does not
//! depend on the layout crate it helps to test.

use core::ffi::c_void;

use crate::CImage;

/// Packet state a handler may change, as read back from C.
#[repr(C)]
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub struct PacketState {
    pub data_len: u16,
    pub network_type: u16,
    pub network_offset: u16,
    pub transport_type: u16,
    pub transport_offset: u16,
    pub flags: u16,
    pub flow_label: u32,
}

/// Output and drop counters of the front after a handler ran.
#[repr(C)]
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub struct FrontTotals {
    pub output_count: u64,
    pub output_bytes: u64,
    pub drop_count: u64,
    pub drop_bytes: u64,
}

/// Opaque C packet.
enum Packet {}

unsafe extern "C" {
    fn tk_c_decap_handler() -> *const c_void;
    fn tk_packet_new(frame: *const u8, len: u16) -> *mut Packet;
    fn tk_packet_free(packet: *mut Packet);
    fn tk_packet_data(packet: *mut Packet, len: *mut u16) -> *const u8;
    fn tk_packet_state(packet: *const Packet, state: *mut PacketState);
    fn tk_run_handler(
        handler: *const c_void,
        cp_module: *mut c_void,
        packets: *const *mut Packet,
        count: usize,
        verdicts: *mut u8,
        totals: *mut FrontTotals,
    ) -> i32;
}

/// Address of the C decap handler compiled from `modules/decap/dataplane`.
pub fn c_decap_handler() -> *const c_void {
    // SAFETY: returns a function address, no preconditions.
    unsafe { tk_c_decap_handler() }
}

/// What a handler did with one packet.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Verdict {
    Output,
    Drop,
}

/// A packet after a handler ran: verdict, frame bytes and descriptor state.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Outcome {
    pub verdict: Verdict,
    pub frame: Vec<u8>,
    pub state: PacketState,
}

/// Parses each frame and runs the module handler at `handler` over the
/// parsed ones in one front, with the image's decap configuration.
///
/// Returns the front counters and, per frame, `None` when the C parser
/// rejects it, like the receive path would before any module sees it.
///
/// # Safety
///
/// `handler` must be the address of a function with the C module handler
/// signature that accepts a decap configuration.
pub unsafe fn run(handler: *const c_void, image: &CImage, frames: &[Vec<u8>]) -> (FrontTotals, Vec<Option<Outcome>>) {
    let packets: Vec<*mut Packet> = frames
        .iter()
        .map(|frame| {
            let len = u16::try_from(frame.len()).expect("frame fits a segment");
            // SAFETY: the frame is a live byte slice of `len` bytes.
            unsafe { tk_packet_new(frame.as_ptr(), len) }
        })
        .collect();
    let parsed: Vec<*mut Packet> = packets.iter().copied().filter(|p| !p.is_null()).collect();
    let mut verdicts = vec![0u8; parsed.len()];
    let mut totals = FrontTotals::default();
    // SAFETY: every packet is live and parsed, the caller vouches for the
    // handler, and the verdict buffer has one slot per packet.
    let rc = unsafe {
        tk_run_handler(
            handler,
            image.config_ptr().as_ptr().cast(),
            parsed.as_ptr(),
            parsed.len(),
            verdicts.as_mut_ptr(),
            &mut totals,
        )
    };
    assert_eq!(0, rc, "handler lost or duplicated packets");

    let mut verdicts = verdicts.into_iter();
    let outcomes = packets
        .into_iter()
        .map(|packet| {
            if packet.is_null() {
                return None;
            }
            let verdict = match verdicts.next().expect("one verdict per parsed packet") {
                1 => Verdict::Output,
                2 => Verdict::Drop,
                other => panic!("unknown verdict {other}"),
            };
            let mut len = 0;
            let mut state = PacketState::default();
            // SAFETY: the packet is live until freed below; the data pointer
            // covers `len` bytes of its single segment.
            let outcome = unsafe {
                let data = tk_packet_data(packet, &mut len);
                tk_packet_state(packet, &mut state);
                Outcome {
                    verdict,
                    frame: core::slice::from_raw_parts(data, usize::from(len)).to_vec(),
                    state,
                }
            };
            // SAFETY: allocated by the harness and freed exactly once.
            unsafe { tk_packet_free(packet) };
            Some(outcome)
        })
        .collect();
    (totals, outcomes)
}
