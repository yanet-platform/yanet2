//! The plugin exports `export_object!` produces, called through their C
//! declarations as the dataplane's loader calls them.
//!
//! A test binary of its own: a module and an object export in one binary
//! collide on the ABI version, as they would in one plugin.

use core::ffi::{CStr, c_void};

use netip as _;
use yanet_sdk_testing as _;
use yanet_shm as _;
use yanet_sys::{bindings, dp::Object};

/// An object type of one word.
struct Exported;

impl Object for Exported {
    const TYPE: &'static str = "exported_state";
    type Config = u64;
}

yanet_sys::export_object!(name = exported_state, object = Exported);

unsafe extern "C" {
    fn new_object_exported_state() -> *mut bindings::object;
    fn free(ptr: *mut c_void);
    static yanet_module_abi_version: u32;
}

/// verifies that the exported constructor returns the object type descriptor
/// the loader copies: the type name, no commit hook and no prepared buffer.
#[test]
fn test_new_object_describes_the_object_type() {
    // SAFETY: the constructor takes no arguments and returns a malloc'd
    // descriptor, read here and released with C free as the loader does.
    let (name, commit, prepared_size) = unsafe {
        let object = new_object_exported_state();
        assert!(!object.is_null());
        let described = (
            CStr::from_ptr((&raw const (*object).name).cast()).to_owned(),
            (*object).commit_ectx_handler.is_some(),
            (*object).prepared_size,
        );
        free(object.cast());
        described
    };

    assert_eq!((c"exported_state", false, 0), (name.as_c_str(), commit, prepared_size));
}

/// verifies that the plugin exports the ABI version the bindings were
/// generated against, where the loader looks for it.
#[test]
fn test_object_plugin_exports_the_abi_version() {
    // SAFETY: the static is the plugin's own `u32`.
    let version = unsafe { yanet_module_abi_version };

    assert_eq!(bindings::YANET_MODULE_ABI_VERSION, version);
}
