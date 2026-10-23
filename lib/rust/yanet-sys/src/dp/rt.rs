//! Handler runtime behind the generated plugin's exports.
//!
//! The runtime interprets a module header as this module's configuration on
//! the module's identity alone (attach: binding) and checks nothing per
//! generation or per packet.

use core::{mem::size_of, ptr::NonNull};

use yanet_shm::root::ShmRoot;

use crate::{
    bindings,
    dp::{
        batch::Batch,
        cx::{CommitCx, Cx},
        module::{Module, ModuleConfig},
    },
};

/// Dataplane to module ABI version the bindings were generated against.
pub const ABI_VERSION: u32 = bindings::YANET_MODULE_ABI_VERSION;

unsafe extern "C" {
    fn malloc(size: usize) -> *mut core::ffi::c_void;
}

/// Body of the exported handler.
///
/// # Safety
///
/// Must be called with the arguments the dataplane passes to a module
/// handler, for a module registered as the type parameter. The
/// execution context pointer is the provenance root of the shared mapping, so
/// it must come straight from C.
#[inline(always)]
pub unsafe fn handle<M: Module>(
    worker: *mut bindings::dp_worker,
    ectx: *mut bindings::module_ectx,
    front: *mut bindings::packet_front,
) {
    // SAFETY: C passes a non-null context inside the shared mapping with
    // mapping-wide provenance (attach: call, mapping).
    let root = unsafe { ShmRoot::from_mapping(NonNull::new_unchecked(ectx).cast()) };
    // SAFETY: forwarded caller contract.
    unsafe { invoke::<M>(root, worker, ectx, front) };
}

/// Runs one handler call over an explicit root.
///
/// # Safety
///
/// As for the handler body, with the root covering the memory the context
/// reaches.
#[inline(always)]
pub unsafe fn invoke<M: Module>(
    root: ShmRoot,
    worker: *mut bindings::dp_worker,
    ectx: *mut bindings::module_ectx,
    front: *mut bindings::packet_front,
) {
    // SAFETY: C passes non-null worker, context and front (attach: call).
    let (worker, ectx, front) = unsafe {
        (
            NonNull::new_unchecked(worker),
            NonNull::new_unchecked(ectx),
            NonNull::new_unchecked(front),
        )
    };
    // SAFETY: the context is frozen once handed to the worker and its
    // absolute fields were derived before release (attach: call, frozen,
    // publish).
    let (config, prepared) = unsafe { ((*ectx.as_ptr()).abs_cp_module, (*ectx.as_ptr()).abs_module_prepared) };
    // SAFETY: the front and context belong to this call (attach: call).
    let mut batch = unsafe { Batch::new(front, ectx) };
    let prepared = if size_of::<M::Prepared<'_>>() == 0 {
        // C allocates no buffer for a module without a prepared size.
        NonNull::dangling()
    } else {
        // SAFETY: the control plane allocates a buffer for every context of a
        // module declaring a size (attach: call).
        unsafe { NonNull::new_unchecked(root.ptr::<M::Prepared<'_>>(prepared.addr())) }
    };
    debug_assert!(prepared.is_aligned(), "misaligned prepared buffer");
    // SAFETY: the context's configuration is set and is this module's
    // (attach: call, binding); its C header bytes stay opaque and the body
    // is frozen and live for the call (attach: frozen, reclaim).
    let config = unsafe { (*root.ptr::<ModuleConfig<M::Config>>(config.addr())).body() };
    // SAFETY: every argument comes from the dataplane call (attach: call).
    let mut cx = unsafe { Cx::<M>::new(root, ectx, worker, config, prepared) };
    M::handle(&mut cx, &mut batch);
}

/// Body of the exported execution-context commit handler.
///
/// # Safety
///
/// Must be called with the arguments the dataplane passes to a commit
/// handler of a module registered as the type parameter, straight from C.
#[inline(always)]
pub unsafe fn commit_ectx<M: Module>(ectx: *mut bindings::module_ectx, cp_module: *mut bindings::cp_module) {
    // SAFETY: as for the handler.
    let root = unsafe { ShmRoot::from_mapping(NonNull::new_unchecked(ectx).cast()) };
    // SAFETY: forwarded caller contract.
    unsafe { commit_with::<M>(root, ectx, cp_module) };
}

/// Makes and stores the prepared value of one context over an explicit root.
///
/// # Safety
///
/// As for the commit handler body, with the root covering the memory the
/// context reaches.
#[inline(always)]
pub unsafe fn commit_with<M: Module>(
    root: ShmRoot,
    ectx: *mut bindings::module_ectx,
    cp_module: *mut bindings::cp_module,
) {
    if size_of::<M::Prepared<'_>>() == 0 {
        // C allocates no buffer for a module without a prepared size.
        return;
    }
    // SAFETY: C passes a non-null context (attach: call).
    let ectx = unsafe { NonNull::new_unchecked(ectx) };
    // SAFETY: the context is frozen except for the prepared buffer, which
    // the single commit pass owns until release to the worker, and the pass
    // runs only for a context whose buffer is allocated (attach: call,
    // frozen, publish).
    let out = root.ptr::<M::Prepared<'_>>(unsafe { (*ectx.as_ptr()).abs_module_prepared }.addr());
    // SAFETY: the header is this module's configuration (attach: binding).
    let config = unsafe { (*root.ptr::<ModuleConfig<M::Config>>(cp_module.addr())).body() };
    // SAFETY: root, context and configuration as stated above; the
    // context's counter storage was derived before the pass (attach: call).
    let cx = unsafe { CommitCx::<M>::new(root, ectx, config) };
    // SAFETY: the buffer holds the prepared value's size, aligned by the
    // allocator (attach: align; the export check bounds the alignment by
    // `MAX_ALIGN` and refuses a value that needs a drop) and exclusively
    // owned by this pass; a value needing no drop leaks nothing if the
    // buffer is overwritten.
    unsafe { out.write(M::prepare(&cx)) };
}

/// Allocates the module descriptor the loader copies and frees.
///
/// # Safety
///
/// The handler and the commit hook must be the runtime trampolines of the
/// type parameter.
pub unsafe fn new_module<M: Module>(
    handler: bindings::module_handler,
    commit: bindings::module_commit_ectx_handler,
) -> *mut bindings::module {
    let size = size_of::<bindings::module>();
    // SAFETY: the loader releases the descriptor with C free, so it is
    // allocated with C malloc and fully initialised before return.
    unsafe {
        let module = malloc(size).cast::<bindings::module>();
        if module.is_null() {
            return module;
        }
        core::ptr::write_bytes(module.cast::<u8>(), 0, size);
        let name = &raw mut (*module).name;
        let len = M::NAME.len().min(bindings::MODULE_TYPE_LEN as usize - 1);
        core::ptr::copy_nonoverlapping(M::NAME.as_ptr(), name.cast::<u8>(), len);
        (*module).handler = handler;
        (*module).commit_ectx_handler = commit;
        (*module).prepared_size = size_of::<M::Prepared<'static>>() as u64;
        module
    }
}

/// Exports a module to the dataplane plugin loader.
///
/// Emits `new_module_<name>`, `yanet_module_abi_version` and the C handler
/// and commit trampolines. The name must equal the module's declared name, or
/// the build fails. Generated into each plugin crate as its only line; the
/// input grammar (identifiers and `::`-separated paths) is part of the safety
/// boundary, used outside the macro's `unsafe`.
///
/// The module is exported under its own name only:
///
/// ```compile_fail,E0080
/// # use yanet_sdk_testing::dp::Model;
/// # type Mine = Model<u64>;
/// yanet_sys::export_module!(name = other, module = Mine);
/// ```
///
/// Under its declared name it is accepted:
///
/// ```
/// # use yanet_sdk_testing::dp::Model;
/// # type Mine = Model<u64>;
/// yanet_sys::export_module!(name = mine, module = Mine);
/// ```
#[macro_export]
macro_rules! export_module {
    (name = $name:ident, module = $($module:ident)::+ $(,)?) => {
        const _: () = {
            type __ExportedModule = $($module)::+;

            const _: () = ::core::assert!(
                $crate::dp::__private::same_name(
                    <__ExportedModule as $crate::dp::Module>::NAME,
                    ::core::stringify!($name),
                ),
                "export_module!: the module is exported under another name than its Module::NAME",
            );
            const _: () = $crate::dp::__private::check_prepared::<__ExportedModule>();
            const _: () = ::core::assert!(
                $crate::dp::__private::fits_module_type(<__ExportedModule as $crate::dp::Module>::NAME),
                "export_module!: Module::NAME must be non-empty, without NUL, and fit the C module type field",
            );

            #[unsafe(export_name = "yanet_module_abi_version")]
            pub static YANET_MODULE_ABI_VERSION: u32 = $crate::dp::rt::ABI_VERSION;

            unsafe extern "C" fn handler(
                worker: *mut $crate::dp::__private::bindings::dp_worker,
                ectx: *mut $crate::dp::__private::bindings::module_ectx,
                front: *mut $crate::dp::__private::bindings::packet_front,
            ) {
                // SAFETY: the dataplane calls this with handler arguments.
                unsafe { $crate::dp::rt::handle::<__ExportedModule>(worker, ectx, front) }
            }

            unsafe extern "C" fn commit_ectx(
                ectx: *mut $crate::dp::__private::bindings::module_ectx,
                cp_module: *mut $crate::dp::__private::bindings::cp_module,
            ) {
                // SAFETY: the dataplane calls this with commit arguments.
                unsafe { $crate::dp::rt::commit_ectx::<__ExportedModule>(ectx, cp_module) }
            }

            #[unsafe(export_name = ::core::concat!("new_module_", ::core::stringify!($name)))]
            pub extern "C" fn new_module() -> *mut $crate::dp::__private::bindings::module {
                // SAFETY: the trampolines above are the runtime's for this module.
                unsafe {
                    $crate::dp::rt::new_module::<__ExportedModule>(
                        ::core::option::Option::Some(handler),
                        ::core::option::Option::Some(commit_ectx),
                    )
                }
            }
        };
    };
}
