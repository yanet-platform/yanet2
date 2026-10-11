//! Module descriptor, handler trampoline and the export macro.
//!
//! Panic policy: a panic inside a handler aborts the dataplane process. The
//! handler runs between a C caller and C-owned packet lists; unwinding into
//! C is undefined, and returning early would leave popped packets neither
//! output nor dropped, so no consistent state exists to continue from.
//!
//! Configuration binding: the descriptor declares the module's
//! configuration layout, the loader copies it into the dataplane module
//! table, and the C module init refuses to create a configuration for this
//! module unless the creator names the same layout. The dataplane
//! interprets a module header as `ModuleConfig<M::Config>` only when the
//! header's dataplane module index points at this module, so every header
//! it reads was created for this layout (as kernel modversions bind a
//! module to the symbols it was built against). The packet path checks
//! nothing per generation or per packet. The argument assumes the SDK
//! builder is the only caller naming a non-zero layout: it validates the
//! graph before handing the configuration over, while C code calling the
//! layout-aware init with a Rust module's layout would bypass that.

use core::ptr::NonNull;

use crate::{
    bindings,
    packet::PacketFront,
    rel::{FrozenView, MapResolver},
    shm::{Module, ModuleConfig, Shm},
    views::ModuleEctx,
};

/// Dataplane to module ABI version this crate was generated against.
pub const ABI_VERSION: u32 = bindings::YANET_MODULE_ABI_VERSION;

/// Handler signature a module provides.
pub type Handler<M> = for<'r, 'g> fn(&mut PacketFront<'r>, Shm<'g, <M as Module>::Config, MapResolver<'g>>);

#[doc(hidden)]
pub mod rt {
    use super::*;

    unsafe extern "C" {
        fn malloc(size: usize) -> *mut core::ffi::c_void;
    }

    /// Runs `body`, aborting the process if it panics.
    #[inline(always)]
    pub fn abort_on_panic<T>(body: impl FnOnce() -> T) -> T {
        match std::panic::catch_unwind(core::panic::AssertUnwindSafe(body)) {
            Ok(value) => value,
            Err(_) => std::process::abort(),
        }
    }

    /// Body of the exported handler trampoline.
    ///
    /// A context without a configuration drops every input packet.
    ///
    /// # Safety
    ///
    /// Must be called with the arguments the dataplane passes to a module
    /// handler, for a module registered with `M`'s layout. The execution
    /// context pointer is the provenance root of the shared-memory mapping,
    /// so it must come straight from C.
    #[inline(always)]
    pub unsafe fn handle<M: Module>(
        ectx: *mut bindings::module_ectx,
        front: *mut bindings::packet_front,
        handler: Handler<M>,
    ) {
        abort_on_panic(|| {
            let (Some(ectx), Some(front)) = (NonNull::new(ectx), NonNull::new(front)) else {
                std::process::abort();
            };
            // SAFETY: C passes a context living in the shared mapping and
            // frozen once handed to the worker, with mapping-wide provenance,
            // and the worker-owned front of this invocation.
            let (view, mut front) = unsafe {
                let res = MapResolver::new(ectx.cast());
                (ModuleEctx::from_raw(res, ectx), PacketFront::from_raw(front))
            };
            let config = view.abs_cp_module().addr();
            if config == 0 {
                while let Some(packet) = front.pop() {
                    front.drop(packet);
                }
                return;
            }
            let res = view.resolver();
            // SAFETY: the context names a module header created by the C
            // module init with this module's layout (see the module docs) and
            // validated by the control-plane api before publish; the C header
            // bytes stay opaque.
            let config = unsafe { Shm::from_raw(res, res.at::<ModuleConfig<M::Config>>(config)) };
            handler(&mut front, config.map(ModuleConfig::body));
        });
    }

    /// Allocates the module descriptor the loader copies and frees.
    pub fn new_module(name: &str, handler: bindings::module_handler, config_layout: u64) -> *mut bindings::module {
        let size = core::mem::size_of::<bindings::module>();
        // SAFETY: the loader releases the descriptor with C free, so it is
        // allocated with C malloc and fully initialised before return.
        unsafe {
            let module = malloc(size).cast::<bindings::module>();
            if module.is_null() {
                return module;
            }
            core::ptr::write_bytes(module.cast::<u8>(), 0, size);
            let dst = &raw mut (*module).name;
            let len = name.len().min((*dst).len() - 1);
            core::ptr::copy_nonoverlapping(name.as_ptr(), dst.cast::<u8>(), len);
            (*module).handler = handler;
            (*module).config_layout = config_layout;
            module
        }
    }
}

/// Exports a module to the dataplane plugin loader.
///
/// Emits `new_module_<name>` and `yanet_module_abi_version`, and a C
/// handler trampoline that builds the configuration view and calls
/// `handler`. The descriptor declares `config_layout::<Module>()`; `name`
/// must equal the module's `Module::NAME`, or the build fails. The input
/// grammar is part of the safety boundary: identifiers and `::`-separated
/// identifier paths only, used outside the macro's `unsafe` blocks.
#[macro_export]
macro_rules! register_module {
    (
        name: $name:ident,
        module: $($module:ident)::+,
        handler: $($handler:ident)::+ $(,)?
    ) => {
        const _: () = {
            #[unsafe(export_name = "yanet_module_abi_version")]
            pub static YANET_MODULE_ABI_VERSION: u32 = $crate::module::ABI_VERSION;

            type M = $($module)::+;
            const HANDLER: $crate::module::Handler<M> = $($handler)::+;

            const _: () = ::core::assert!(
                $crate::shm::same_name(<M as $crate::shm::Module>::NAME, ::core::stringify!($name)),
                "register_module!: the module is exported under another name than its Module::NAME",
            );

            unsafe extern "C" fn trampoline(
                _dp_worker: *mut $crate::bindings::dp_worker,
                ectx: *mut $crate::bindings::module_ectx,
                front: *mut $crate::bindings::packet_front,
            ) {
                // SAFETY: the dataplane calls this with handler arguments.
                unsafe { $crate::module::rt::handle::<M>(ectx, front, HANDLER) }
            }

            #[unsafe(export_name = ::core::concat!("new_module_", ::core::stringify!($name)))]
            pub extern "C" fn new_module() -> *mut $crate::bindings::module {
                $crate::module::rt::new_module(
                    ::core::stringify!($name),
                    ::core::option::Option::Some(trampoline),
                    $crate::shm::config_layout::<M>(),
                )
            }
        };
    };
}
