//! The plugin exports `export_module!` produces, called through their C
//! declarations as the dataplane's loader calls them.

use core::{
    ffi::{CStr, c_void},
    mem::size_of,
};

use netip as _;
use yanet_packet as _;
use yanet_sdk_testing as _;
use yanet_shm as _;
use yanet_sys::{
    bindings,
    dp::{__private::new_module, Batch, CommitCx, Cx, Module, Verdict},
};

/// A module with a prepared value of eight bytes.
struct Exported;

impl Module for Exported {
    const NAME: &'static str = "exported";
    type Config = u64;
    type Prepared<'p> = u64;
    fn handle(_: &mut Cx<'_, Self>, batch: &mut Batch<'_>) {
        batch.dispatch(|_| Verdict::Pass);
    }
    fn prepare(_: &CommitCx<'_, Self>) -> u64 {
        0
    }
}

yanet_sys::export_module!(name = exported, module = Exported);

/// A module whose name is `MODULE_TYPE_LEN - 1` bytes long, the longest that
/// fits the C field with its terminator.
struct Longest;

impl Module for Longest {
    const NAME: &'static str = "module_of_the_longest_name_that_fits_the_c_module_type_field_with_a_terminator_";
    type Config = u64;
    type Prepared<'p> = ();
    fn handle(_: &mut Cx<'_, Self>, batch: &mut Batch<'_>) {
        batch.dispatch(|_| Verdict::Pass);
    }
    fn prepare(_: &CommitCx<'_, Self>) {}
}

unsafe extern "C" {
    fn new_module_exported() -> *mut bindings::module;
    fn free(ptr: *mut c_void);
    static yanet_module_abi_version: u32;
}

/// verifies that the exported constructor returns the module descriptor the
/// loader copies: the name, both handlers and the size of the prepared
/// buffer.
#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_new_module_describes_the_module() {
    // SAFETY: the constructor takes no arguments and returns a descriptor
    // allocated with C calloc, read here and released with C free as the
    // loader does.
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

/// verifies that the descriptor stores a name of `MODULE_TYPE_LEN - 1` bytes
/// whole and NUL-terminated, and pads a shorter one with zeros to the end of
/// the field.
#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_new_module_terminates_the_name_and_zeroes_the_rest_of_the_field() {
    const LEN: usize = bindings::MODULE_TYPE_LEN as usize;
    assert_eq!(LEN - 1, Longest::NAME.len());
    let names = [
        (new_module::<Longest>(), Longest::NAME),
        (new_module::<Exported>(), Exported::NAME),
    ];

    for (module, want) in names {
        assert!(!module.is_null());
        // SAFETY: the descriptor was just allocated with C calloc; it is read
        // here and released with C free as the loader does.
        let field = unsafe {
            let field = (*module).name.map(|byte| byte as u8);
            free(module.cast());
            field
        };

        let mut padded = [0u8; LEN];
        padded[..want.len()].copy_from_slice(want.as_bytes());
        assert_eq!(padded, field, "{want}");
    }
}

/// verifies that the plugin exports the ABI version the bindings were
/// generated against, where the loader looks for it.
#[test]
fn test_plugin_exports_the_abi_version() {
    // SAFETY: the static is the plugin's own `u32`.
    let version = unsafe { yanet_module_abi_version };

    assert_eq!(bindings::YANET_MODULE_ABI_VERSION, version);
}
