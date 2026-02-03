use std::ffi::c_void;
use std::ptr;

use crate::{
    rte_ipv4_hdr, rte_tcp_hdr,
    rte_ipv4_cksum, rte_ipv4_hdr_len, rte_raw_cksum,
    IPPROTO_UDP
};

struct ipv4_psd_header {
    src_addr : u32, /* IP address of source host. */
    dst_addr : u32, /* IP address of destination host. */
    zero     : u8,  /* zero. */
    proto    : u8,  /* L4 protocol type. */
    len      : u16  /* L4 length. */
}

#[inline]
fn ipv4_phdr_cksum(ip_header: &rte_ipv4_hdr) -> u16 {
    unsafe {
        let l3_len = (*ip_header).total_length.swap_bytes();
        
        let psd_hdr = ipv4_psd_header{
            src_addr: ip_header.src_addr,
            dst_addr: ip_header.dst_addr,
            zero: 0,
            proto: ip_header.next_proto_id,
            len: (l3_len - rte_ipv4_hdr_len(ptr::from_ref(ip_header)) as u16).swap_bytes(),
        };
    
        return rte_raw_cksum(ptr::from_ref(&psd_hdr) as *const c_void, size_of::<ipv4_psd_header>());
    }
}

#[inline]
pub fn ipv4_udptcp_cksum(ip_header: &rte_ipv4_hdr, tcp_header: &rte_tcp_hdr) -> u16 {
	let ip_hdr_len = unsafe { rte_ipv4_hdr_len(ptr::from_ref(ip_header)) as u16 };
	let l3_len = ip_header.total_length.swap_bytes();
	if l3_len < ip_hdr_len {
        return 0;
    }

	let l4_len = l3_len - ip_hdr_len;

	let mut cksum = unsafe { rte_raw_cksum(ptr::from_ref(tcp_header) as *const c_void, l4_len as usize) as u32 };
	cksum += ipv4_phdr_cksum(ip_header) as u32;

	let mut cksum = (((cksum & 0xffff0000) >> 16) + (cksum & 0xffff)) as u16;

    cksum = !cksum;

    if cksum == 0 && ip_header.next_proto_id == IPPROTO_UDP as u8 {
		cksum = 0xffff;
    }

    return cksum;
}

#[inline]
pub fn ipv4_cksum(ip_header: &rte_ipv4_hdr) -> u16 {
    unsafe { rte_ipv4_cksum(ptr::from_ref(ip_header)) }
}