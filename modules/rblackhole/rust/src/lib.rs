//! rblackhole: the reference Rust-implemented module dataplane.
//!
//! Packets whose network-layer destination matches a configured prefix
//! are dropped and counted; everything else passes through untouched.
//! The control-plane side stays in C (`modules/rblackhole/api`), which
//! builds and publishes the config this handler reads.

use yanet_dp::prelude::*;

/// Byte-for-byte mirror of `struct rblackhole_module_config`
/// (modules/rblackhole/dataplane/config.h); the two must change together.
#[repr(C)]
struct RBlackholeConfig {
    cp_module: yanet_shm::CpModule,
    prefixes4: yanet_shm::Lpm,
    prefixes6: yanet_shm::Lpm,
    pass_device_idx: u64,
    dropped_counter_id: u64,
}

// SAFETY: cp_module is the first field, matching the C config.
unsafe impl ModuleConfig for RBlackholeConfig {
    fn cp_module(&self) -> &yanet_shm::CpModule {
        &self.cp_module
    }
}

const _: () = assert!(
    core::mem::size_of::<RBlackholeConfig>()
        == core::mem::size_of::<yanet_shm::CpModule>()
            + 2 * core::mem::size_of::<yanet_shm::Lpm>()
            + 2 * core::mem::size_of::<u64>()
);

/// One blackhole decision for a packet's destination.
enum Verdict {
    Drop,
    Pass,
}

fn classify_v4(prefixes: &yanet_shm::Lpm, packet: &Packet<'_>) -> Verdict {
    let Some(ip) = packet.ipv4() else {
        return Verdict::Pass;
    };
    if ip.is_fragmented() {
        // Only the first fragment carries a usable header; a blackhole
        // swallows none of a fragmented datagram's parts selectively.
        return Verdict::Pass;
    }
    if yanet_shm::lpm4_lookup(prefixes, &ip.dst()) != yanet_shm::LPM_VALUE_INVALID {
        return Verdict::Drop;
    }
    Verdict::Pass
}

fn classify_v6(prefixes: &yanet_shm::Lpm, packet: &Packet<'_>) -> Verdict {
    let Some(ip) = packet.ipv6() else {
        return Verdict::Pass;
    };
    if ip.next_header() == yanet_packet::IpProtocol::Fragment as u8 {
        return Verdict::Pass;
    }
    if yanet_shm::lpm16_lookup(prefixes, &ip.dst()) != yanet_shm::LPM_VALUE_INVALID {
        return Verdict::Drop;
    }
    Verdict::Pass
}

fn handle<'a>(ectx: &mut Ectx<'a>, front: &mut PacketFront<'a>) {
    let config = ectx.config::<RBlackholeConfig>();
    let Some(mut dropped) = ectx.counter(config.dropped_counter_id) else {
        // No storage means no published registry: drop everything so a
        // misconfigured module fails closed instead of forwarding.
        while let Some(packet) = front.pop_input() {
            front.drop_packet(packet);
        }
        return;
    };

    while let Some(mut packet) = front.pop_input() {
        let verdict = match packet.network_type_known() {
            Some(yanet_packet::EtherType::Ipv4) => classify_v4(&config.prefixes4, &packet),
            Some(yanet_packet::EtherType::Ipv6) => classify_v6(&config.prefixes6, &packet),
            _ => Verdict::Pass,
        };
        match verdict {
            Verdict::Drop => {
                dropped.add_packet(packet.len());
                front.drop_packet(packet);
            }
            Verdict::Pass => {
                // An input-entry module's bare output is dropped by the
                // framework: a surviving packet must be routed into a
                // device entry. Route to the configured pass device's
                // output; index zero names the "any" fallback, so an
                // unset config fails the routing and drops.
                match ectx.device_target(config.pass_device_idx as usize) {
                    Some(target) => {
                        packet.set_tx_device_id(target.device_id());
                        ectx.route_output(front, target, packet);
                    }
                    None => {
                        front.drop_packet(packet);
                    }
                }
            }
        }
    }
}

yanet_dp::define_module! {
    /// Loaded by the dataplane through the standard module registry.
    loader: new_module_rblackhole,
    name: "rblackhole",
    handler: handle,
}
