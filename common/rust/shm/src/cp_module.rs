//! The `struct cp_module` mirror and typed config access.

use core::ffi::c_char;

use crate::{counter::CounterRegistry, memory::MemoryContext, offset::OffsetPtr};

/// Module and device name capacity, counting the terminating zero.
pub const CP_MODULE_NAME_LEN: usize = 80;

/// Object type and name capacity, counting the terminating zero.
pub const CP_OBJECT_NAME_LEN: usize = 80;

/// Mirror of `struct registry_item` (`lib/controlplane/config/registry.h`).
#[repr(C)]
pub struct RegistryItem {
    pub refcnt: u64,
    pub destroying: u64,
}

/// Mirror of `struct cp_module_device`.
#[repr(C)]
pub struct CpModuleDevice {
    pub name: [u8; CP_MODULE_NAME_LEN],
}

/// Mirror of `struct cp_module_counter_registry`.
#[repr(C)]
pub struct CpModuleCounterRegistry {
    pub tag: [c_char; crate::counter::COUNTER_NAME_LEN],
    pub registry: CounterRegistry,
}

/// Mirror of `struct cp_module_object` — a declared link to a cp_object.
#[repr(C)]
pub struct CpModuleObject {
    pub type_: [c_char; CP_MODULE_NAME_LEN],
    pub name: [c_char; CP_OBJECT_NAME_LEN],
}

/// Opaque `struct agent` — control-plane ownership data.
#[repr(C)]
pub struct AgentOpaque {
    _private: [u8; 0],
}

/// Mirror of `struct cp_module` (`lib/controlplane/config/cp_module.h`).
///
/// The shared prefix of every module configuration: a typed config
/// embeds it as its first member, which is what
/// [`CpModule::container_of`] relies on. Its size is ABI-pinned.
#[repr(C)]
pub struct CpModule {
    pub config_item: RegistryItem,
    pub dp_module_idx: u64,
    pub type_: [c_char; CP_MODULE_NAME_LEN],
    pub name: [c_char; CP_MODULE_NAME_LEN],
    pub generation: u64,
    pub counter_registry: CounterRegistry,
    pub runtime_counter_registry_count: u64,
    pub runtime_counter_registries: OffsetPtr<OffsetPtr<CpModuleCounterRegistry>>,
    pub rx_counter_id: u64,
    pub tx_counter_id: u64,
    pub drop_counter_id: u64,
    pub pending_input_counter_id: u64,
    pub pending_output_counter_id: u64,
    pub prev: OffsetPtr<CpModule>,
    pub agent: OffsetPtr<AgentOpaque>,
    pub memory_context: MemoryContext,
    pub device_count: u64,
    pub devices: OffsetPtr<CpModuleDevice>,
    pub object_count: u64,
    pub objects: OffsetPtr<CpModuleObject>,
}

impl Default for CpModule {
    fn default() -> Self {
        // SAFETY: all-zero is the valid pre-init state of every embedded
        // member (null offsets, empty registries and contexts).
        unsafe { core::mem::zeroed() }
    }
}

/// A typed module configuration whose first member is a [`CpModule`].
///
/// # Safety
///
/// Implementors must place `cp_module` as the struct's first field —
/// the container repo convention enforced by reviewers for C configs —
/// because [`CpModule::container_of`] recovers the config by casting.
pub unsafe trait ModuleConfig {
    /// The shared module prefix.
    fn cp_module(&self) -> &CpModule;
}

impl CpModule {
    /// Recover the enclosing typed config from a shared-module pointer.
    ///
    /// # Safety
    ///
    /// `T` must be the configuration type the pointer actually lives in;
    /// module code reading its own config through its own ectx satisfies
    /// this by construction.
    pub unsafe fn container_of<'a, T: ModuleConfig>(&self) -> &'a T {
        // SAFETY: the contract of ModuleConfig places cp_module first,
        // so the prefix pointer is the config pointer.
        unsafe { &*(self as *const Self as *const T) }
    }
}

/// Mirror of `struct cp_object` (`lib/controlplane/config/cp_object.h`).
///
/// The shared prefix of every named shared object (a FIB, a session
/// table): a typed object embeds it as its first member, exactly like a
/// module config embeds [`CpModule`].
#[repr(C)]
pub struct CpObject {
    pub config_item: RegistryItem,
    pub type_: [c_char; CP_MODULE_NAME_LEN],
    pub name: [c_char; CP_OBJECT_NAME_LEN],
    pub dp_object_idx: u64,
    pub counter_registry: CounterRegistry,
    pub link_counter_registry: CounterRegistry,
    pub agent: OffsetPtr<AgentOpaque>,
    pub memory_context: MemoryContext,
}

impl Default for CpObject {
    fn default() -> Self {
        // SAFETY: all-zero is the valid pre-init state of every embedded
        // member.
        unsafe { core::mem::zeroed() }
    }
}

/// A typed shared object whose first member is a [`CpObject`].
///
/// # Safety
///
/// Implementors must place `cp_object` as the struct's first field.
pub unsafe trait ObjectConfig {
    /// The shared object prefix.
    fn cp_object(&self) -> &CpObject;
}

impl CpObject {
    /// Recover the enclosing typed object from a shared-object pointer.
    ///
    /// # Safety
    ///
    /// `T` must be the object type the pointer actually lives in.
    pub unsafe fn container_of<'a, T: ObjectConfig>(&self) -> &'a T {
        // SAFETY: the contract of ObjectConfig places cp_object first.
        unsafe { &*(self as *const Self as *const T) }
    }
}

const _: () = assert!(core::mem::size_of::<RegistryItem>() == 16);
const _: () = assert!(core::mem::size_of::<CpModuleDevice>() == 80);
const _: () = assert!(core::mem::size_of::<CpModuleObject>() == 160);
const _: () = assert!(core::mem::size_of::<CpModule>() == 544);
const _: () = assert!(core::mem::size_of::<CpObject>() == 560);
const _: () = assert!(core::mem::offset_of!(CpModule, dp_module_idx) == 16);
const _: () = assert!(core::mem::offset_of!(CpModule, counter_registry) == 192);
const _: () = assert!(core::mem::offset_of!(CpModule, rx_counter_id) == 328);
const _: () = assert!(core::mem::offset_of!(CpModule, prev) == 368);
const _: () = assert!(core::mem::offset_of!(CpModule, memory_context) == 384);
const _: () = assert!(core::mem::offset_of!(CpModule, device_count) == 512);

#[cfg(test)]
mod tests {
    use std::prelude::v1::*;

    use super::*;

    struct DemoConfig {
        cp_module: CpModule,
        extra: u64,
    }

    // SAFETY: cp_module is the first field, as the C convention requires.
    unsafe impl ModuleConfig for DemoConfig {
        fn cp_module(&self) -> &CpModule {
            &self.cp_module
        }
    }

    #[test]
    fn container_of_recovers_first_field_embedding() {
        let config = DemoConfig {
            cp_module: CpModule::default(),
            extra: 42,
        };
        let config_ptr = &config as *const DemoConfig;
        let recovered =
        // SAFETY: the pointer does live in a DemoConfig.
            unsafe { (*config_ptr).cp_module.container_of::<DemoConfig>() };
        assert_eq!(recovered.extra, 42);
        assert_eq!(recovered as *const DemoConfig, config_ptr);
    }
}
