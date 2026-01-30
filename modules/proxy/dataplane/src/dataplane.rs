use std::ffi::{c_char, c_void};
use std::ptr;

use crate::config::ProxyModuleConfig;
use crate::{
    container_of, dp_worker, module, module_ectx, packet_front, packet_front_output,
    packet_list_pop, packet_to_mbuf, rte_ipv4_hdr, rte_tcp_hdr, rte_mbuf,
    rte_ipv4_cksum, rte_ipv4_hdr_len, rte_raw_cksum,
    MODULE_NAME_LEN, RTE_ETHER_TYPE_IPV4, IPPROTO_UDP,
};

#[repr(C)]
pub struct ProxyModule {
    pub module: module,
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

unsafe fn get_cp_module_from_ectx(module_ectx: *mut module_ectx) -> *mut crate::cp_module {
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
    (offset_val + cp_module_ptr as *const _ as usize) as *mut crate::cp_module
}

struct ipv4_psd_header {
    src_addr : u32, /* IP address of source host. */
    dst_addr : u32, /* IP address of destination host. */
    zero     : u8,  /* zero. */
    proto    : u8,  /* L4 protocol type. */
    len      : u16  /* L4 length. */
}

unsafe fn ipv4_phdr_cksum(ip_header: *const rte_ipv4_hdr) -> u16 {
    unsafe {
        let l3_len = ((*ip_header).total_length as u16).to_le();
        
        let mut psd_hdr = ipv4_psd_header{
            src_addr: (*ip_header).src_addr,
            dst_addr: (*ip_header).dst_addr,
            zero: 0,
            proto: (*ip_header).next_proto_id,
            len: (l3_len - rte_ipv4_hdr_len(ip_header) as u16).to_be(),
        };
    
        return rte_raw_cksum(ptr::from_ref(&psd_hdr) as *const c_void, size_of::<ipv4_psd_header>());
    }
}

unsafe fn ipv4_udptcp_cksum(ip_header: *const rte_ipv4_hdr, tcp_header: *const rte_tcp_hdr) -> u16 {
	let ip_hdr_len = rte_ipv4_hdr_len(ip_header) as u16;
	let l3_len = unsafe { ((*ip_header).total_length).to_le() };
	if l3_len < ip_hdr_len {
        return 0;
    }

	let l4_len = l3_len - ip_hdr_len;

	let mut cksum = rte_raw_cksum(tcp_header as *const c_void, l4_len as usize) as u32;
	cksum += unsafe { ipv4_phdr_cksum(ip_header) as u32 };

	let mut cksum = (((cksum & 0xffff0000) >> 16) + (cksum & 0xffff)) as u16;

    cksum = !cksum;

    if cksum == 0 && unsafe {(*ip_header).next_proto_id == IPPROTO_UDP as u8 } {
		cksum = 0xffff;
    }

    return cksum;
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn proxy_handle_packets(
    _dp_worker: *mut dp_worker,
    module_ectx: *mut module_ectx,
    packet_front: *mut packet_front,
) {
    if module_ectx.is_null() || packet_front.is_null() {
        return;
    }

    let cp_module_ptr = unsafe { get_cp_module_from_ectx(module_ectx) };
    if cp_module_ptr.is_null() {
        return;
    }

    let module_config = container_of!(cp_module_ptr, ProxyModuleConfig, cp_module);

    loop {
        let packet = unsafe { packet_list_pop(&mut (*packet_front).input) };
        if packet.is_null() {
            break;
        }

        let ipv4_type = (RTE_ETHER_TYPE_IPV4 as u16).to_be();
        unsafe {
            if (*packet).network_header.type_ == ipv4_type {
                let mbuf = packet_to_mbuf(packet);
                if mbuf.is_null() { continue; }

                let ipv4_header = mbuf_offset(
                    mbuf,
                    (*packet).network_header.offset,
                ) as *mut rte_ipv4_hdr;
                if ipv4_header.is_null() {
                    eprintln!("Null ip header");
                    continue;
                }

                (*ipv4_header).src_addr =
                    (*module_config).proxy_config.addr as u32;

                let tcp_header = mbuf_offset(
						mbuf,
						(*packet).transport_header.offset,
				) as *mut rte_tcp_hdr;
				if tcp_header.is_null() {
                    eprintln!("Null tcp header");
					continue;
				}

				(*tcp_header).cksum = 0;
				(*tcp_header).cksum = ipv4_udptcp_cksum(
					ipv4_header, tcp_header
				);
                (*ipv4_header).hdr_checksum = 0;
                (*ipv4_header).hdr_checksum = rte_ipv4_cksum(ipv4_header);
            }
    
            packet_front_output(packet_front, packet);
        }
    }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn new_module_proxy() -> *mut module {
    let module_ptr = unsafe { libc::malloc(std::mem::size_of::<ProxyModule>()) as *mut ProxyModule };

    if module_ptr.is_null() {
        return ptr::null_mut();
    }

    unsafe {
        let proxy_module = &mut *module_ptr;
    
        let name = b"proxy\0";
        let name_len = name.len().min(MODULE_NAME_LEN as usize);
        ptr::copy_nonoverlapping(
            name.as_ptr() as *const c_char,
            proxy_module.module.name.as_mut_ptr(),
            name_len,
        );
        
        proxy_module.module.handler = Some(proxy_handle_packets);
        
        &mut proxy_module.module as *mut module
    }
}
