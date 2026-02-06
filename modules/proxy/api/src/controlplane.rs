use std::ptr;

use libc::c_char;

use memory::{addr_of, container_of};
use state::config::ProxyModuleConfig;
use bindings::{
    agent, agent_delete_module,
    cp_module, cp_module_init,
    memory_balloc, memory_bfree,
};

/// Initialize a new proxy module configuration.
///
/// # Safety
/// - `agent` must be a valid pointer to an initialized agent structure
/// - `name` must be a valid null-terminated C string
#[unsafe(no_mangle)]
pub extern "C" fn proxy_module_config_init(
    agent: *mut agent,
    name: *const c_char,
) -> *mut cp_module {
    if agent.is_null() {
        return ptr::null_mut();
    }

    unsafe {
        let agent_ref = &mut *agent;

        let config = memory_balloc(
            ptr::from_mut(&mut agent_ref.memory_context),
            size_of::<ProxyModuleConfig>(),
        ) as *mut ProxyModuleConfig;
        if config.is_null() {
            return ptr::null_mut();
        }
        let config_ref = &mut *config;

        let module_type = c"proxy";
        if cp_module_init(
            config_ref.cp_module.as_mut_ptr(),
            agent_ref,
            module_type.as_ptr(),
            name,
        ) != 0
        {
            proxy_module_config_free(config_ref.cp_module.as_mut_ptr());
            return ptr::null_mut();
        }

        config_ref.proxy_config.size_connections_table = 0;
        config_ref.proxy_config.upstream_addr = 0;
        config_ref.proxy_config.upstream_port = 0;
        config_ref.proxy_config.proxy_addr = 0;
        config_ref.proxy_config.proxy_port = 0;
        config_ref.proxy_config.upstream_net.addr = 0;
        config_ref.proxy_config.upstream_net.mask = 0;

        config_ref.cp_module.as_mut_ptr()
    }
}

/// Free a proxy module configuration.
///
/// # Safety
/// - `module` must be a valid pointer to a cp_module that was created by proxy_module_config_init
#[unsafe(no_mangle)]
pub extern "C" fn proxy_module_config_free(module: *mut cp_module) {
    if module.is_null() {
        return;
    }

    let module_config = container_of!(module, ProxyModuleConfig, cp_module);
    if module_config.is_null() {
        eprintln!("Null module config");
        return;
    }

    unsafe {
        let agent_ptr: *mut agent = addr_of!(ptr::addr_of_mut!((*module).agent));

        // FIXME: remove the check as agent should be assigned
        if !agent_ptr.is_null() {
            let agent_ref = &mut *agent_ptr;
            memory_bfree(
                ptr::from_mut(&mut agent_ref.memory_context),
                module_config as *mut libc::c_void,
                size_of::<ProxyModuleConfig>(),
            );
        }
    }
}

/// Delete a proxy module from the agent.
///
/// # Safety
/// - `module` must be a valid pointer to a cp_module
#[unsafe(no_mangle)]
pub extern "C" fn proxy_module_config_delete(module: *mut cp_module) -> libc::c_int {
    if module.is_null() {
        return -1;
    }

    unsafe {
        let agent_ptr: *mut agent = addr_of!(ptr::addr_of_mut!((*module).agent));
        let module_type = c"proxy";
        agent_delete_module(agent_ptr, module_type.as_ptr(), (*module).name.as_ptr())
    }
}

/// Set the connection table size for a proxy module.
///
/// # Safety
/// - `module` must be a valid pointer to a cp_module that was created by proxy_module_config_init
#[unsafe(no_mangle)]
pub extern "C" fn proxy_module_config_set_conn_table_size(
    module: *mut cp_module,
    size: u32,
) -> libc::c_int {
    if module.is_null() {
        return -1;
    }

    let module_config = container_of!(module, ProxyModuleConfig, cp_module);
    if module_config.is_null() {
        return -1;
    }

    unsafe {
        let config_ref = &mut *module_config;
        config_ref.proxy_config.size_connections_table = size;
    }

    0
}