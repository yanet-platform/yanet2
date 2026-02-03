use crate::{
    proxy::{SERVICE, connections::Connection}, rte_ipv4_hdr, rte_tcp_hdr
};

pub fn client_syn(ip_header: &mut rte_ipv4_hdr, tcp_header: &mut rte_tcp_hdr) {
    let mut service = SERVICE.lock().unwrap();

    let local = service.as_mut().unwrap().local_pool.allocate(ip_header.src_addr, tcp_header.src_port);
    if local.is_none() {
        eprintln!("Failed to allocate local addr and port");
        return;
    }
    let (local_addr, local_port) = local.unwrap();

    if service.as_mut().unwrap().connections.insert(ip_header.src_addr, tcp_header.src_port, Connection{
        local_addr,
        local_port,
    }).is_err() {
        eprintln!("Failed to insert connection");
        return;
    }

    ip_header.src_addr = local_addr;
    tcp_header.src_port = local_port;
    ip_header.dst_addr = service.as_ref().unwrap().config.upstream_addr;
    tcp_header.dst_port = service.as_ref().unwrap().config.upstream_port;
}

pub fn server_synack(ip_header: &mut rte_ipv4_hdr, tcp_header: &mut rte_tcp_hdr) {
    let service = SERVICE.lock().unwrap();

    let client = service.as_ref().unwrap().local_pool.get_client(ip_header.dst_addr, tcp_header.dst_port);
    if client.is_none() {
        eprintln!("Failed to get client addr and port");
        return;
    }
    let (client_addr, client_port) = client.unwrap();

    ip_header.src_addr = service.as_ref().unwrap().config.proxy_addr;
    tcp_header.src_port = service.as_ref().unwrap().config.proxy_port;
    ip_header.dst_addr = client_addr;
    tcp_header.dst_port = client_port;
}

pub fn client_ack(ip_header: &mut rte_ipv4_hdr, tcp_header: &mut rte_tcp_hdr) {
    let service = SERVICE.lock().unwrap();

    let connection = service.as_ref().unwrap().connections.find(ip_header.src_addr, tcp_header.src_port);
    if connection.is_none() {
        eprintln!("Failed to get connection");
        return;
    }
    let connection = connection.unwrap();
    let (local_addr, local_port) = (connection.local_addr, connection.local_port);

    ip_header.src_addr = local_addr;
    tcp_header.src_port = local_port;
    ip_header.dst_addr = service.as_ref().unwrap().config.upstream_addr;
    tcp_header.dst_port = service.as_ref().unwrap().config.upstream_port;
}

pub fn server_ack(ip_header: &mut rte_ipv4_hdr, tcp_header: &mut rte_tcp_hdr) {
    let service = SERVICE.lock().unwrap();

    let client = service.as_ref().unwrap().local_pool.get_client(ip_header.dst_addr, tcp_header.dst_port);
    if client.is_none() {
        eprintln!("Failed to get client addr and port");
        return;
    }
    let (client_addr, client_port) = client.unwrap();

    ip_header.src_addr = service.as_ref().unwrap().config.proxy_addr;
    tcp_header.src_port = service.as_ref().unwrap().config.proxy_port;
    ip_header.dst_addr = client_addr;
    tcp_header.dst_port = client_port;
}