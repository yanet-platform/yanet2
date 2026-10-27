//! The plugin exports `export_module!` produces, called through their C
//! declarations as the dataplane's loader calls them.

use core::{
    ffi::{CStr, c_void},
    mem::size_of,
};

use yanet_sdk_testing as _;
use yanet_shm as _;
use yanet_sys::{
    bindings,
    dp::{Batch, CommitCx, Cx, Module, Verdict},
};

/// A module with a prepared value of eight bytes.
struct Exported;

impl Module for Exported {
    const NAME: &'static str = "exported";
    type Config = u64;
    type Prepared<'p> = u64;
    fn handle(_: &mut Cx<Self>, batch: &mut Batch) {
        batch.dispatch(|_| Verdict::Pass);
    }
    fn prepare(_: &CommitCx<'_, Self>) -> u64 {
        0
    }
}

yanet_sys::export_module!(name = exported, module = Exported);

unsafe extern "C" {
    fn new_module_exported() -> *mut bindings::module;
    fn free(ptr: *mut c_void);
    static yanet_module_abi_version: u32;
}

/// verifies that the exported constructor returns the module descriptor the
/// loader copies: the name, both handlers and the size of the prepared
/// buffer.
#[test]
fn test_new_module_describes_the_module() {
    // SAFETY: the constructor takes no arguments and returns a malloc'd
    // descriptor, read here and released with C free as the loader does.
    let (name, handler, commit, prepared_size) = unsafe {
        let module = new_module_exported();
        assert!(!module.is_null());
        let described = (
            CStr::from_ptr((&raw const (*module).name).cast()).to_owned(),
            (*module).handler.is_some(),
            (*module).commit_ectx_handler.is_some(),
            (*module).prepared_size,
        );
        free(module.cast());
        described
    };

    assert_eq!(c"exported", name.as_c_str());
    assert!(handler && commit);
    assert_eq!(size_of::<u64>() as u64, prepared_size);
}

/// verifies that the plugin exports the ABI version the bindings were
/// generated against, where the loader looks for it.
#[test]
fn test_plugin_exports_the_abi_version() {
    // SAFETY: the static is the plugin's own `u32`.
    let version = unsafe { yanet_module_abi_version };

    assert_eq!(bindings::YANET_MODULE_ABI_VERSION, version);
}
