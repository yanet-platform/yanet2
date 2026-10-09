//! Decap dataplane module: strips IP-in-IP, IPv6-in-IPv6 and GRE tunnels
//! addressed to a configured prefix.
//!
//! Behaviourally equal to `modules/decap/dataplane/dataplane.c`: an outer
//! fragment is dropped, a destination outside both prefix sets passes
//! untouched, a matching IPv6 packet records its flow label, and a packet
//! the tunnel stripper refuses is dropped. The tunnel stripper itself is the
//! dataplane's C routine, reached through a safe `yanet-sys` call.

#![forbid(unsafe_code)]

mod config;

pub use config::DecapConfig;
use yanet_sdk::{
    ConfigView, LpmView, Module, Packet, Verdict, export_module,
    sys::ffi::{IPPROTO_FRAGMENT, RTE_ETHER_TYPE_IPV4, RTE_ETHER_TYPE_IPV6},
};

/// The decap module.
pub enum Decap {}

/// Views of both prefix sets for one handler invocation.
pub struct Prefixes<'g> {
    v4: LpmView<'g>,
    v6: LpmView<'g>,
}

impl Module for Decap {
    type Config = DecapConfig;
    type Views<'g> = Prefixes<'g>;

    fn attach<'g>(config: &ConfigView<'g, DecapConfig>) -> Prefixes<'g> {
        let body = config.body();
        Prefixes {
            v4: config.lpm(&body.prefixes4),
            v6: config.lpm(&body.prefixes6),
        }
    }

    fn handle_packet(prefixes: &Prefixes<'_>, packet: &mut Packet<'_>) -> Verdict {
        match packet.ether_type() {
            RTE_ETHER_TYPE_IPV4 => handle_v4(prefixes, packet),
            RTE_ETHER_TYPE_IPV6 => handle_v6(prefixes, packet),
            _ => Verdict::Output,
        }
    }
}

/// IPv4 header bytes the module reads: up to the destination address.
const IPV4_HEADER_LEN: usize = 20;
/// IPv6 fixed header length.
const IPV6_HEADER_LEN: usize = 40;
/// Fragment offset and "more fragments" bits of the IPv4 flags field.
const IPV4_FRAGMENT_MASK: u16 = 0x3fff;
/// Flow label bits of the first IPv6 header word.
const IPV6_FLOW_LABEL_MASK: u32 = 0x000f_ffff;

fn handle_v4(prefixes: &Prefixes<'_>, packet: &mut Packet<'_>) -> Verdict {
    // The parser guarantees the fixed header; a short one is not a packet
    // the dataplane would hand to a module.
    let Some(header) = packet.network_header::<IPV4_HEADER_LEN>() else {
        return Verdict::Drop;
    };
    if u16::from_be_bytes([header[6], header[7]]) & IPV4_FRAGMENT_MASK != 0 {
        return Verdict::Drop;
    }
    let destination = [header[16], header[17], header[18], header[19]];
    if !prefixes.v4.contains(&destination) {
        return Verdict::Output;
    }
    decap(packet)
}

fn handle_v6(prefixes: &Prefixes<'_>, packet: &mut Packet<'_>) -> Verdict {
    let Some(header) = packet.network_header::<IPV6_HEADER_LEN>() else {
        return Verdict::Drop;
    };
    if header[6] == IPPROTO_FRAGMENT {
        return Verdict::Drop;
    }
    let destination: [u8; 16] = header[24..40].try_into().expect("slice of 16 bytes");
    if !prefixes.v6.contains(&destination) {
        return Verdict::Output;
    }
    let label = u32::from_be_bytes([header[0], header[1], header[2], header[3]]) & IPV6_FLOW_LABEL_MASK;
    packet.set_flow_label(label);
    decap(packet)
}

fn decap(packet: &mut Packet<'_>) -> Verdict {
    match packet.decap() {
        Ok(()) => Verdict::Output,
        Err(_) => Verdict::Drop,
    }
}

export_module!(decap, Decap);

// The C fixtures are a dev-dependency of the differential tests in tests/;
// the library's own test build sees it too and must mark it as used.
#[cfg(test)]
use yanet_testkit as _;
