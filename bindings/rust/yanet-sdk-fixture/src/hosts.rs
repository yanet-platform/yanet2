//! A module that counts each IPv4 packet by its destination address, through
//! an exact-match index: the index bindings wired through as a module
//! composes them.

use core::net::Ipv4Addr;

use yanet_sdk::{Batch, CommitCx, Counter, Cx, HashIndex, Module, ShmLayout, ShmSlice, Traffic, Verdict};

/// The configuration of [`Hosts`]: the index of the destination addresses,
/// each naming its position, and a counter per position.
#[derive(ShmLayout)]
#[repr(C)]
pub struct HostsConfig {
    pub hosts: HashIndex<Ipv4Addr>,
    pub counters: ShmSlice<Counter<Traffic>>,
}

/// Passes an IPv4 packet whose destination address the index holds and counts
/// it in the counter of the position the address names; drops a packet of an
/// address the index lacks, of a position without a counter, and one that is
/// not IPv4.
pub struct Hosts;

impl Module for Hosts {
    const NAME: &'static str = "hosts";
    type Config = HostsConfig;
    type Prepared<'p> = ();

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let config = cx.config();
        let hosts = config.hosts.resolve(cx);
        let counters = config.counters.as_slice(cx);
        batch.dispatch(|packet| {
            let data = packet.data();
            let dst = usize::from(packet.network_offset()) + 16;
            let counter = (packet.network_type() == 0x0800)
                .then(|| data.get(dst..dst.checked_add(4)?)?.first_chunk::<4>().copied())
                .flatten()
                .and_then(|addr| hosts.get(Ipv4Addr::from(addr)))
                .and_then(|position| counters.get(usize::try_from(position).ok()?));
            let Some(counter) = counter else {
                return Verdict::Drop;
            };
            counter.add(cx, Traffic::packet(data.len() as u64));
            Verdict::Pass
        });
    }

    fn prepare(_: &CommitCx<'_, Self>) {}
}
