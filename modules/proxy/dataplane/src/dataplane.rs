use std::ffi::c_char;
use std::ptr;

use crate::config::ProxyModuleConfig;
use crate::{
    container_of, dp_worker, module, module_ectx, packet_front, packet_front_output,
    packet_list_pop, packet_to_mbuf, rte_ipv4_hdr, rte_mbuf, MODULE_NAME_LEN, RTE_ETHER_TYPE_IPV4,
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
                if !mbuf.is_null() {
                    let ipv4_header = mbuf_offset(
                        mbuf,
                        (*packet).network_header.offset,
                    ) as *mut rte_ipv4_hdr;
    
                    if !ipv4_header.is_null() {
                        (*ipv4_header).src_addr =
                            (*module_config).proxy_config.addr as u32;
                    }
                }
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
