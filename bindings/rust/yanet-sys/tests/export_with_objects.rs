//! The plugin exports `export_module!` produces for a module and the object
//! types its plugin carries, called through their C declarations as the
//! dataplane's loaders call them.

use core::ffi::{CStr, c_void};

use netip as _;
use proptest as _;
use yanet_packet as _;
use yanet_sdk_testing as _;
use yanet_shm as _;
use yanet_sys::{
    bindings,
    dp::{Batch, CommitCx, Cx, Module, Object, ObjectCommitCx, Verdict},
};

/// A module with no prepared value, which carries both object types below.
struct Carrier;

impl Module for Carrier {
    const NAME: &'static str = "carrier";
    type Config = u64;
    type Prepared<'p> = ();
    fn handle(_: &mut Cx<'_, Self>, batch: &mut Batch<'_>) {
        batch.dispatch(|_| Verdict::Pass);
    }
    fn prepare(_: &CommitCx<'_, Self>) {}
}

/// An object type of one word, with no prepared value.
struct Table;

impl Object for Table {
    const TYPE: &'static str = "carrier_table";
    type Config = u64;
    type Prepared<'p> = ();

    fn prepare(_: &ObjectCommitCx<'_, Self>) {}
}

/// An object type of one word, with a prepared value of two words.
struct Set;

impl Object for Set {
    const TYPE: &'static str = "carrier_set";
    type Config = u64;
    type Prepared<'p> = [u64; 2];

    fn prepare(cx: &ObjectCommitCx<'_, Self>) -> [u64; 2] {
        [*cx.config(), 0]
    }
}

yanet_sys::export_module!(
    name = carrier,
    module = Carrier,
    objects = [carrier_table = Table, carrier_set = Set],
);

unsafe extern "C" {
    fn new_module_carrier() -> *mut bindings::module;
    fn new_object_carrier_table() -> *mut bindings::object;
    fn new_object_carrier_set() -> *mut bindings::object;
    fn free(ptr: *mut c_void);
    static yanet_module_abi_version: u32;
}

/// verifies that the module's constructor describes the module beside the
/// object types it carries: the name, both handlers and no prepared buffer.
#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_new_module_with_objects_describes_the_module() {
    // SAFETY: the constructor takes no arguments and returns a descriptor
    // from the C allocator, read here and freed as the loader does.
    let (name, handler, commit, prepared_size) = unsafe {
        let module = new_module_carrier();
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

    assert_eq!(
        (c"carrier", true, true, 0),
        (name.as_c_str(), handler, commit, prepared_size)
    );
}

/// verifies that each object type the module carries is exported as
/// `export_object!` exports one: a constructor under its type name, which
/// registers the commit hook and the prepared size only for a prepared value.
#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_new_object_with_a_module_describes_each_type() {
    let constructors: [(unsafe extern "C" fn() -> *mut bindings::object, &CStr, bool, u64); 2] = [
        (new_object_carrier_table, c"carrier_table", false, 0),
        (new_object_carrier_set, c"carrier_set", true, 16),
    ];

    for (constructor, want_name, want_commit, want_size) in constructors {
        // SAFETY: the constructor takes no arguments and returns a descriptor
        // from the C allocator, read here and freed as the loader does.
        let (name, commit, prepared_size) = unsafe {
            let object = constructor();
            assert!(!object.is_null());
            let described = (
                CStr::from_ptr((&raw const (*object).name).cast()).to_owned(),
                (*object).commit_ectx_handler.is_some(),
                (*object).prepared_size,
            );
            free(object.cast());
            described
        };

        assert_eq!(
            (want_name, want_commit, want_size),
            (name.as_c_str(), commit, prepared_size)
        );
    }
}

/// verifies that the plugin of a module and its object types exports the ABI
/// version the bindings were generated against, where the loader looks for
/// it.
#[test]
fn test_plugin_with_objects_exports_the_abi_version() {
    // SAFETY: the static is the plugin's own `u32`.
    let version = unsafe { yanet_module_abi_version };

    assert_eq!(bindings::YANET_MODULE_ABI_VERSION, version);
}
