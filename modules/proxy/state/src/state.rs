use std::net::Ipv4Addr;

use bindings::memory_context;
use crate::{Config, ConnectionsTable, LocalPool};

pub struct State {
    pub mctx: *mut memory_context,
    pub memory_shift: usize,

    pub config: Config,

    pub connections: ConnectionsTable,
    pub local_pool: LocalPool,
}

impl State {
    pub fn new(mctx: *mut memory_context, memory_shift: usize, size_conn_table: u32) -> Result<State, &'static str>  {
        let mut config = Config::default();
        config.size_connections_table = size_conn_table;
        config.upstream_addr = "10.0.1.1".parse::<Ipv4Addr>().unwrap().to_bits().swap_bytes();
        config.upstream_port = (80 as u16).swap_bytes();
        config.proxy_addr = "10.0.1.1".parse::<Ipv4Addr>().unwrap().to_bits().swap_bytes();
        config.proxy_port = (80 as u16).swap_bytes();
        config.upstream_net.addr = "10.0.0.0".parse::<Ipv4Addr>().unwrap().to_bits();
        config.upstream_net.mask = 30;

        let mctx_ref = unsafe { &mut *mctx };

        let connections = ConnectionsTable::new(mctx_ref, config.size_connections_table as usize)?;
        let local_pool = LocalPool::new(mctx_ref, config.upstream_net)?;
        Ok(Self {
            mctx,
            memory_shift,
            config,
            connections,
            local_pool,
        })
    }

    pub fn adjust_pointers(&mut self) {
        self.connections.adjust_pointers();
        self.local_pool.adjust_pointers();
    }
}