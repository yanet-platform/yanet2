use std::ptr;
use std::net::Ipv4Addr;

use crate::config::ProxyModuleConfig;
use crate::{
    container_of, cp_module, dp_worker, module_ectx, packet,
    packet_front, packet_front_output, packet_list_pop, packet_to_mbuf,
    rte_ipv4_hdr, rte_tcp_hdr, rte_mbuf,
    RTE_ETHER_TYPE_IPV4, RTE_TCP_SYN_FLAG, RTE_TCP_ACK_FLAG,
};
use crate::proxy;

unsafe fn get_cp_module_from_ectx(module_ectx: *mut module_ectx) -> *mut cp_module {
    if module_ectx.is_null() {
        return ptr::null_mut();
    }
    let cp_module_ptr = unsafe { &(*module_ectx).cp_module };
    if cp_module_ptr.is_null() {
        return ptr::null_mut();
    }
    let offset_val = *cp_module_ptr as usize;
    if offset_val == 0 {
        return ptr::null_mut();
    }
    (offset_val + cp_module_ptr as *const _ as usize) as *mut cp_module
}

#[inline]
unsafe fn mbuf_offset(mbuf: *mut rte_mbuf, offset: u16) -> *mut u8 {
    if mbuf.is_null() {
        return ptr::null_mut();
    }
    unsafe {
        let buf_addr = (*mbuf).buf_addr as *mut u8;
        let data_off = (*mbuf).data_off;
        buf_addr.add((data_off + offset) as usize)
    }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn proxy_handle_packets(
    dp_worker: *mut dp_worker,
    module_ectx: *mut module_ectx,
    packet_front: *mut packet_front,
) {
    if dp_worker.is_null() || module_ectx.is_null() || packet_front.is_null() {
        return;
    }

    let cp_module_ptr = unsafe { get_cp_module_from_ectx(module_ectx) };
    if cp_module_ptr.is_null() {
        return;
    }
    
    let module_config = container_of!(cp_module_ptr, ProxyModuleConfig, cp_module);
    if module_config.is_null() {
        eprintln!("Null module config");
        return;
    }

    if proxy::SERVICE.lock().unwrap().is_none() {
        unsafe { proxy::SERVICE.lock().unwrap().insert(proxy::Service::new(&(*module_config).proxy_config)) };
    }
    let config = proxy::SERVICE.lock().unwrap().as_ref().unwrap().config.clone();

    loop {
        let packet = unsafe { packet_list_pop(&mut (*packet_front).input) as *mut packet };
        if packet.is_null() {
            break;
        }
        let packet = unsafe { &mut *packet };

        let ipv4_type = (RTE_ETHER_TYPE_IPV4 as u16).swap_bytes();
        if packet.network_header.type_ == ipv4_type {
            let mbuf = unsafe { packet_to_mbuf(ptr::from_mut(packet)) };
            if mbuf.is_null() { continue; }

            let ipv4_header = unsafe { mbuf_offset(
                mbuf,
                packet.network_header.offset,
            ) as *mut rte_ipv4_hdr };
            if ipv4_header.is_null() {
                eprintln!("Null ip header");
                continue;
            }
            let ipv4_header = unsafe { &mut *ipv4_header };

            let tcp_header = unsafe { mbuf_offset(
                    mbuf,
                    packet.transport_header.offset,
            ) as *mut rte_tcp_hdr };
            if tcp_header.is_null() {
                eprintln!("Null tcp header");
                continue;
            }
            let tcp_header = unsafe { &mut *tcp_header };

            let syn = tcp_header.tcp_flags & (RTE_TCP_SYN_FLAG as u8) != 0;
            let ack = tcp_header.tcp_flags & (RTE_TCP_ACK_FLAG as u8) != 0;

            println!("SYN: {} ACK: {}", syn, ack);
            println!("DST: {:?} SRC: {:?}", Ipv4Addr::from_bits(ipv4_header.dst_addr.swap_bytes()), Ipv4Addr::from_bits(ipv4_header.src_addr.swap_bytes()));

            if ipv4_header.dst_addr == config.proxy_addr
                && ack {
                println!("CLIENT ACK");
                proxy::handle::client_ack(ipv4_header, tcp_header);
            } else if ipv4_header.src_addr == config.upstream_addr
                && ack && !syn {
                println!("SERVER ACK");
                proxy::handle::server_ack(ipv4_header, tcp_header);
            } else if ipv4_header.dst_addr == config.proxy_addr
                && syn {
                println!("CLIENT SYN");
                proxy::handle::client_syn(ipv4_header, tcp_header);
            } else if ipv4_header.src_addr == config.upstream_addr
                && syn && ack {
                println!("SERVER SYN ACK");
                proxy::handle::server_synack(ipv4_header, tcp_header);
            } else {
                println!("SKIP");
                continue;
            }

            tcp_header.cksum = 0;
            tcp_header.cksum = proxy::checksum::ipv4_udptcp_cksum(
                ipv4_header, tcp_header
            );
            ipv4_header.hdr_checksum = 0;
            ipv4_header.hdr_checksum = proxy::checksum::ipv4_cksum(ipv4_header);
        }

        unsafe { packet_front_output(packet_front, ptr::from_mut(packet)) };
    }
}
