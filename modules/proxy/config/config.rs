use std::mem::MaybeUninit;

use crate::cp_module;

#[repr(C)]
#[derive(Debug, Clone, Copy)]
pub struct IPv4Prefix {
    pub addr: u32,
    pub mask: u8,
}

#[repr(C)]
#[derive(Debug, Clone, Copy)]
pub struct ProxyConfig {
    pub upstream_addr: u32,
    pub upstream_port: u16,

    pub proxy_addr: u32,
    pub proxy_port: u16,

    pub upstream_net: IPv4Prefix,

    pub size_connections_table: u32,
}

#[repr(C)]
pub struct ProxyModuleConfig {
    pub cp_module: MaybeUninit<cp_module>,

    pub proxy_config: ProxyConfig,
}