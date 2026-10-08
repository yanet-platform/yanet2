//! Module descriptor, handler trampoline and the export macro.
//!
//! Panic policy: a panic inside a handler aborts the dataplane process. The
//! handler runs between a C caller and C-owned packet lists; unwinding into
//! C is undefined, and returning early would leave popped packets neither
//! output nor dropped, so no consistent state exists to continue from. The
//! workspace also builds with `panic = "abort"`; the explicit abort keeps the
//! policy independent of the profile.

use core::{ffi::c_void, ptr::NonNull};

use crate::{
    bindings,
    packet::PacketFront,
    rel::{FrozenView, MapResolver},
    views::{DecapConfig, ModuleEctx},
};

/// Dataplane to module ABI version this crate was generated against.
pub const ABI_VERSION: u32 = bindings::YANET_MODULE_ABI_VERSION;

/// Configuration a module reads from its execution context.
pub trait ModuleConfig {
    /// View of one published generation of the configuration.
    type View<'g>;

    /// Locates the configuration, `None` when the context carries none.
    fn from_ectx<'g>(ectx: &ModuleEctx<'g, MapResolver<'g>>) -> Option<Self::View<'g>>;
}

/// The C decap module configuration: a module header followed by two LPMs.
pub struct Decap;

impl ModuleConfig for Decap {
    type View<'g> = DecapConfig<'g, MapResolver<'g>>;

    fn from_ectx<'g>(ectx: &ModuleEctx<'g, MapResolver<'g>>) -> Option<Self::View<'g>> {
        // The module header is the first field, so the configuration starts
        // at the header address, as the C container_of computes.
        const _: () = assert!(core::mem::offset_of!(bindings::decap_module_config, cp_module) == 0);
        let addr = ectx.abs_cp_module().addr();
        if addr == 0 {
            return None;
        }
        let res = ectx.resolver();
        // SAFETY: the absolutized module header of a published context
        // points at this module's configuration inside the mapping.
        Some(unsafe { DecapConfig::from_raw(res, res.at(addr)) })
    }
}

/// Handler signature a module provides.
pub type Handler<C> = for<'r, 'g> fn(&mut PacketFront<'r>, &<C as ModuleConfig>::View<'g>);

#[doc(hidden)]
pub mod rt {
    use super::*;

    unsafe extern "C" {
        fn malloc(size: usize) -> *mut c_void;
    }

    /// Runs `body`, aborting the process if it panics.
    #[inline(always)]
    pub fn abort_on_panic(body: impl FnOnce()) {
        if std::panic::catch_unwind(core::panic::AssertUnwindSafe(body)).is_err() {
            std::process::abort();
        }
    }

    /// Body of the exported handler trampoline.
    ///
    /// A context without a configuration drops every input packet.
    ///
    /// # Safety
    ///
    /// Must be called with the arguments the dataplane passes to a module
    /// handler. The execution context pointer is the provenance root of the
    /// shared-memory mapping, so it must come straight from C.
    #[inline(always)]
    pub unsafe fn handle<C: ModuleConfig>(
        ectx: *mut bindings::module_ectx,
        front: *mut bindings::packet_front,
        handler: Handler<C>,
    ) {
        abort_on_panic(|| {
            let (Some(ectx), Some(front)) = (NonNull::new(ectx), NonNull::new(front)) else {
                std::process::abort();
            };
            // SAFETY: C passes a context living in the shared mapping and
            // frozen once handed to the worker, with mapping-wide provenance,
            // and the worker-owned front of this invocation.
            let (ectx, mut front) = unsafe {
                let res = MapResolver::new(ectx.cast());
                (ModuleEctx::from_raw(res, ectx), PacketFront::from_raw(front))
            };
            match C::from_ectx(&ectx) {
                Some(config) => handler(&mut front, &config),
                None => {
                    while let Some(packet) = front.pop() {
                        front.drop(packet);
                    }
                }
            }
        });
    }

    /// Allocates the module descriptor the loader copies and frees.
    pub fn new_module(name: &str, handler: bindings::module_handler) -> *mut bindings::module {
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
            module
        }
    }
}

/// Exports a module to the dataplane plugin loader.
///
/// Emits `new_module_<name>` and `yanet_module_abi_version`, and a C handler
/// trampoline that builds the views and calls `handler`. The input grammar is
/// part of the safety boundary: it accepts identifiers and `::`-separated
/// identifier paths only, and the caller's tokens are only ever used outside
/// the macro's `unsafe` block (the handler as a safe function value, the
/// configuration as a type), so the module crate's `forbid(unsafe_code)`
/// cannot be bypassed through the macro input.
#[macro_export]
macro_rules! register_module {
    (
        name: $name:ident,
        config: $($config:ident)::+,
        handler: $($handler:ident)::+ $(,)?
    ) => {
        const _: () = {
            #[unsafe(export_name = "yanet_module_abi_version")]
            pub static YANET_MODULE_ABI_VERSION: u32 = $crate::module::ABI_VERSION;

            type Config = $($config)::+;
            const HANDLER: $crate::module::Handler<Config> = $($handler)::+;

            unsafe extern "C" fn trampoline(
                _dp_worker: *mut $crate::bindings::dp_worker,
                ectx: *mut $crate::bindings::module_ectx,
                front: *mut $crate::bindings::packet_front,
            ) {
                // SAFETY: the dataplane calls this with handler arguments.
                unsafe { $crate::module::rt::handle::<Config>(ectx, front, HANDLER) }
            }

            #[unsafe(export_name = ::core::concat!("new_module_", ::core::stringify!($name)))]
            pub extern "C" fn new_module() -> *mut $crate::bindings::module {
                $crate::module::rt::new_module(::core::stringify!($name), ::core::option::Option::Some(trampoline))
            }
        };
    };
}
