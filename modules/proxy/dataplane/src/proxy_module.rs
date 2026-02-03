use std::ffi::c_char;
use std::ptr;

use crate::{module, MODULE_NAME_LEN};
use crate::dataplane::proxy_handle_packets;

#[repr(C)]
pub struct ProxyModule {
    pub module: module,
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