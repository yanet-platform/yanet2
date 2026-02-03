use std::sync::{LazyLock, Mutex};
use std::net::Ipv4Addr;

use crate::config::ProxyConfig;

pub mod checksum;
pub mod handle;
mod connections;
mod local_pool;

use connections::ConnectionsTable;
use local_pool::LocalPool;

pub struct Service {
    pub config: ProxyConfig,

    pub connections: ConnectionsTable,
    pub local_pool: LocalPool,
}

impl Service {
    pub fn new(config: &ProxyConfig) -> Self {
        let mut config = config.clone();
        config.upstream_addr = "10.0.1.1".parse::<Ipv4Addr>().unwrap().to_bits().swap_bytes();
        config.upstream_port = (80 as u16).swap_bytes();
        config.proxy_addr = "10.0.1.1".parse::<Ipv4Addr>().unwrap().to_bits().swap_bytes();
        config.proxy_port = (80 as u16).swap_bytes();
        config.upstream_net.addr = "10.0.0.0".parse::<Ipv4Addr>().unwrap().to_bits();
        config.upstream_net.mask = 30;

        Self {
            config,
            connections: ConnectionsTable::new(config.size_connections_table),
            local_pool: LocalPool::new(config.upstream_net.addr, config.upstream_net.mask),
        }
    }
}

pub static SERVICE : LazyLock<Mutex<Option<Service>>> = LazyLock::new(|| Mutex::new(None));