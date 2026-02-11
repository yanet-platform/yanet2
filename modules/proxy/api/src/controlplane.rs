use std::{os::raw::c_void, ptr};

use libc::c_char;

use memory::{addr_of, container_of, set_offset_of};
use state::{Module, State};
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
    state: *mut State
) -> *mut cp_module {
    if agent.is_null() || state.is_null() {
        return ptr::null_mut();
    }

    let module = unsafe { memory_balloc(
        std::ptr::addr_of_mut!((*agent).memory_context),
        size_of::<Module>(),
    ) as *mut Module };
    if module.is_null() {
        return ptr::null_mut();
    }
    let module_ref = unsafe { &mut *module };

    let module_type = c"proxy";
    if unsafe { cp_module_init(
        module_ref.cp_module.as_mut_ptr(),
        agent,
        module_type.as_ptr(),
        name,
    ) } != 0
    {
        proxy_module_config_free(module_ref.cp_module.as_mut_ptr());
        return ptr::null_mut();
    }

    set_offset_of!(std::ptr::addr_of_mut!(module_ref.state), state);

    module_ref.cp_module.as_mut_ptr()
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

    let module_config = container_of!(module, Module, cp_module);
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
                size_of::<Module>(),
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

#[unsafe(no_mangle)]
pub extern "C" fn proxy_state_create(
    agent: *mut agent,
    size_conn_table: u32
) -> *mut State {
    if agent.is_null() {
        return ptr::null_mut()
    }
    let agent = unsafe { &mut *agent };

    let mctx = &raw mut agent.memory_context;

	let align = align_of::<State>();
	let mut memory =
		unsafe { memory_balloc(mctx, size_of::<State>() + align) as *mut u8 };
	if memory.is_null() {
        eprintln!("failed to allocate memory for state object");
		return ptr::null_mut();
	}
	let shift = memory.align_offset(align);
	memory = unsafe { memory.add(shift)};
	assert!((memory as usize) % align == 0);
	let state_ptr = memory as *mut State;

    match State::new(mctx, shift, size_conn_table) {
        Ok(state) => {
            unsafe { *state_ptr = state };
            unsafe { (*state_ptr).adjust_pointers() };
            state_ptr
        },
        Err(e) => {
            eprintln!("failed to create new state: {}", e);
            unsafe { memory_bfree(mctx, memory as *mut c_void, size_of::<State>() + align) };
            ptr::null_mut()
        }
    }
}

#[unsafe(no_mangle)]
pub extern "C" fn proxy_state_destroy(state: *mut State) {
    unsafe {
        let mut mem = state as usize;
        mem -= (*state).memory_shift;
        memory_bfree(
            (*state).mctx,
            mem as *mut c_void,
            size_of::<State>() + align_of::<State>()
        );
    }
}