//! Rust port of the decap dataplane module.
//!
//! Decapsulates IP-in-IP and GRE packets whose outer destination falls into
//! a configured prefix; outer fragments are dropped, everything else passes
//! through unchanged. Behaviour follows `modules/decap/dataplane/dataplane.c`.
//! The crate also declares the module's configuration layout, which the
//! control-plane api crate builds.

#![forbid(unsafe_code)]

use yanet_sdk::{Lpm4, Lpm6, Module, ShmLayout};

/// Configuration body of the decap module: the prefixes whose tunnels are
/// removed, one LPM per address family.
#[derive(ShmLayout)]
#[repr(C)]
pub struct DecapConfig {
    pub prefixes4: Lpm4,
    pub prefixes6: Lpm6,
}

/// The decap module: its name and configuration body, shared by the
/// dataplane export and the control-plane api.
pub struct Decap;

impl Module for Decap {
    const NAME: &'static str = "decap";
    type Config = DecapConfig;
}

#[cfg(feature = "dp")]
mod dataplane {
    use yanet_sdk::{Config, Packet, PacketFront, lpm, register_module};

    use super::{Decap, DecapConfig};

    const ETHER_TYPE_IPV4: u16 = 0x0800;
    const ETHER_TYPE_IPV6: u16 = 0x86dd;
    const IPPROTO_FRAGMENT: u8 = 44;
    /// Fragment offset and more-fragments bits of the IPv4 header.
    const IPV4_FRAGMENT_MASK: u16 = 0x3fff;
    const IPV6_FLOW_LABEL_MASK: u32 = 0x000f_ffff;

    /// Verdict for one packet.
    #[derive(Debug, PartialEq, Eq)]
    enum Verdict {
        Pass,
        Drop,
    }

    fn handle_v4(config: Config<'_, DecapConfig>, packet: &mut Packet<'_>) -> Verdict {
        let header = packet.network_data();
        // The C module reads the header unchecked, trusting the parser; a
        // first segment too short for it is dropped here instead of read past.
        let (Some(fragment), Some(dst)) = (header.get(6..8), header.get(16..20)) else {
            return Verdict::Drop;
        };
        if u16::from_be_bytes([fragment[0], fragment[1]]) & IPV4_FRAGMENT_MASK != 0 {
            return Verdict::Drop;
        }
        let dst: [u8; 4] = [dst[0], dst[1], dst[2], dst[3]];
        if !lpm::contains(config.map(|c| &c.prefixes4), &dst) {
            return Verdict::Pass;
        }
        match packet.decap() {
            Ok(()) => Verdict::Pass,
            Err(_) => Verdict::Drop,
        }
    }

    fn handle_v6(config: Config<'_, DecapConfig>, packet: &mut Packet<'_>) -> Verdict {
        let header = packet.network_data();
        // As for IPv4: a first segment too short for the header is dropped.
        let (Some(vtc_flow), Some(&next_header), Some(dst)) = (header.get(0..4), header.get(6), header.get(24..40))
        else {
            return Verdict::Drop;
        };
        if next_header == IPPROTO_FRAGMENT {
            return Verdict::Drop;
        }
        let mut key = [0u8; 16];
        key.copy_from_slice(dst);
        if !lpm::contains(config.map(|c| &c.prefixes6), &key) {
            return Verdict::Pass;
        }
        let flow_label =
            u32::from_be_bytes([vtc_flow[0], vtc_flow[1], vtc_flow[2], vtc_flow[3]]) & IPV6_FLOW_LABEL_MASK;
        packet.set_flow_label(flow_label);
        match packet.decap() {
            Ok(()) => Verdict::Pass,
            Err(_) => Verdict::Drop,
        }
    }

    /// Handles every input packet of the front.
    pub fn handle_packets(front: &mut PacketFront<'_>, config: Config<'_, DecapConfig>) {
        while let Some(mut packet) = front.pop() {
            let verdict = match packet.network_type() {
                ETHER_TYPE_IPV4 => handle_v4(config, &mut packet),
                ETHER_TYPE_IPV6 => handle_v6(config, &mut packet),
                _ => Verdict::Pass,
            };
            match verdict {
                Verdict::Pass => front.output(packet),
                Verdict::Drop => front.drop(packet),
            }
        }
    }

    register_module! {
        name: decap,
        module: Decap,
        handler: handle_packets,
    }
}

// Test-only dependencies are visible to the library's own test target.
#[cfg(test)]
use {decap_oracle as _, yanet_sys as _};
