#[repr(C)]
#[derive(Debug, Default, Clone, Copy)]
pub struct IPv4Prefix {
    pub addr: u32,
    pub mask: u8,
}

#[repr(C)]
#[derive(Debug, Default, Clone, Copy)]
pub struct Config {
    pub upstream_addr: u32,
    pub upstream_port: u16,

    pub proxy_addr: u32,
    pub proxy_port: u16,

    pub upstream_net: IPv4Prefix,

    pub size_connections_table: u32,
}
