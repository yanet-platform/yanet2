//! Module registration: from a safe handler to the C loader symbol.

use core::ffi::c_void;

use crate::{ectx::Ectx, front::PacketFront, raw};

/// A module's per-batch packet processing, written in safe code.
///
/// The contract mirrors the C handler: pop the front's input and push
/// every packet exactly once to the output or the drop list. Per-worker
/// state lives in the prepared buffer ([`Ectx::prepared`]), not in the
/// implementor.
pub trait ModuleHandler {
    /// Process one front.
    ///
    /// The context and the front share one round lifetime, so a packet
    /// popped from the front feeds straight back into it.
    fn handle<'a>(ectx: &mut Ectx<'a>, front: &mut PacketFront<'a>);
}

/// A commit hook: generation-invariant derivation over the module's own
/// config, run once per published generation from one thread.
pub trait ModuleCommit {
    /// Derive config-internal state.
    fn commit(dp_config: &raw::DpConfig, config: &yanet_shm::CpModule);
}

/// An ectx commit hook: per-worker derivation into the module's prepared
/// buffer, run once per worker context per generation.
///
/// Worker services (allocation, worker time) are unavailable here, the
/// same as in C: the absolutization pass carries no worker pointer.
pub trait ModuleCommitEctx {
    /// Fill the prepared buffer.
    fn commit_ectx(ectx: &mut Ectx<'_>, config: &yanet_shm::CpModule);
}

// The loader copies the returned descriptor's fields and frees the
// block, so the allocation must come from the C allocator rather than
// the Rust global one.
unsafe extern "C" {
    fn calloc(nmemb: usize, size: usize) -> *mut c_void;
}

/// Generic C-ABI trampoline adapting the dataplane's handler call to
/// [`ModuleHandler::handle`].
///
/// A panic inside `handle` aborts the dataplane in release builds (the
/// workspace pins `panic = "abort"`), matching a C crash: no unwind
/// ever crosses the FFI boundary.
///
/// # Safety
///
/// The dataplane calls the trampoline with live pointers of one worker
/// round, exactly once per front.
pub unsafe extern "C" fn handler_trampoline<H: ModuleHandler>(
    worker: *mut raw::DpWorker,
    ectx: *mut raw::ModuleEctx,
    front: *mut raw::PacketFront,
) {
    // SAFETY: the dataplane invokes the trampoline with live pointers of
    // one worker round; the wrappers borrow them for the call only.
    let mut ectx = unsafe { Ectx::new(worker, ectx) };
    // SAFETY: the front is live for this module call.
    let mut front = unsafe { PacketFront::from_raw(&mut *front) };
    H::handle(&mut ectx, &mut front);
}

/// Generic C-ABI trampoline for [`ModuleCommit`].
///
/// # Safety
///
/// The commit pass hands live pointers for one commit run.
pub unsafe extern "C" fn commit_trampoline<C: ModuleCommit>(
    dp_config: *mut raw::DpConfig,
    cp_module: *mut yanet_shm::CpModule,
) {
    // SAFETY: the commit pass hands live pointers for one commit run.
    C::commit(unsafe { &*dp_config }, unsafe { &*cp_module });
}

/// Generic C-ABI trampoline for [`ModuleCommitEctx`].
///
/// # Safety
///
/// The absolutization pass hands a live context and config.
pub unsafe extern "C" fn commit_ectx_trampoline<E: ModuleCommitEctx>(
    ectx: *mut raw::ModuleEctx,
    cp_module: *mut yanet_shm::CpModule,
) {
    // SAFETY: the absolutization pass hands a live context; it carries
    // no worker, so worker services are off inside the hook.
    let mut ectx = unsafe { Ectx::new_for_commit(ectx) };
    E::commit_ectx(&mut ectx, unsafe { &*cp_module });
}

/// Allocate and fill the module descriptor the loader copies from.
///
/// The name must fit `MODULE_TYPE_LEN` bytes and the loader symbol must
/// follow the dataplane's `new_module_<name>` convention. Returns NULL
/// only when the descriptor allocation fails.
#[doc(hidden)]
pub fn module_descriptor(
    name: &str,
    handler: raw::ModuleHandler,
    commit: Option<raw::ModuleCommitHandler>,
    commit_ectx: Option<raw::ModuleCommitEctxHandler>,
    prepared_size: u64,
) -> *mut raw::Module {
    debug_assert!(name.len() < raw::MODULE_TYPE_LEN);

    // SAFETY: calloc zeroes one module-sized block, matching the loader's
    // expectation that unset fields carry no heap garbage.
    let module = unsafe { calloc(1, core::mem::size_of::<raw::Module>()) } as *mut raw::Module;
    if module.is_null() {
        return core::ptr::null_mut();
    }

    // SAFETY: the block is module-sized and freshly zeroed.
    let module = unsafe { &mut *module };
    module.name[..name.len()].copy_from_slice(name.as_bytes());
    module.name[name.len()] = 0;
    module.handler = Some(handler);
    module.commit_handler = commit;
    module.commit_ectx_handler = commit_ectx;
    module.prepared_size = prepared_size;
    module as *mut raw::Module
}

/// Declare a module dataplane and export its loader symbol.
///
/// The loader symbol name must follow the dataplane's
/// `new_module_<name>` convention, and `name` must match the module type
/// the control plane configures. `handler` is a function
/// `fn(&mut Ectx, &mut PacketFront)`; `commit`, `commit_ectx` and
/// `prepared` are optional and compose freely:
///
/// ```no_run
/// use yanet_dp::prelude::*;
///
/// fn handle<'a>(ectx: &mut Ectx<'a>, front: &mut PacketFront<'a>) {
///     while let Some(packet) = front.pop_input() {
///         front.drop_packet(packet);
///     }
/// }
///
/// struct Prepared {
///     hits: u64,
/// }
///
/// fn commit_ectx(ectx: &mut Ectx<'_>, _config: &yanet_shm::CpModule) {
///     if let Some(prepared) = ectx.prepared::<Prepared>() {
///         prepared.hits = 0;
///     }
/// }
///
/// yanet_dp::define_module! {
///     loader: new_module_example,
///     name: "example",
///     handler: handle,
///     commit_ectx: commit_ectx,
///     prepared: Prepared,
/// }
/// ```
#[macro_export]
macro_rules! define_module {
    (
        $(#[$outer:meta])*
        loader: $loader:ident,
        name: $name:literal,
        handler: $handler:path
        $(, commit: $commit:path)?
        $(, commit_ectx: $commit_ectx:path)?
        $(, prepared: $prepared:ty)?
        $(,)?
    ) => {
        $(#[$outer])*
        #[unsafe(no_mangle)]
        pub extern "C" fn $loader() -> *mut $crate::raw::Module {
            // Zero-sized wrappers carry the user functions into the
            // generic trampolines without changing the C signatures.
            struct __Handler;
            impl $crate::ModuleHandler for __Handler {
                fn handle<'a>(
                    ectx: &mut $crate::Ectx<'a>,
                    front: &mut $crate::PacketFront<'a>,
                ) {
                    $handler(ectx, front)
                }
            }

            struct __Commit;
            impl $crate::ModuleCommit for __Commit {
                fn commit(
                    dp_config: &$crate::raw::DpConfig,
                    config: &yanet_shm::CpModule,
                ) {
                    $($commit(dp_config, config);)?
                    #[allow(unreachable_code)]
                    {
                        let _ = (dp_config, config);
                    }
                }
            }

            struct __CommitEctx;
            impl $crate::ModuleCommitEctx for __CommitEctx {
                fn commit_ectx(
                    ectx: &mut $crate::Ectx<'_>,
                    config: &yanet_shm::CpModule,
                ) {
                    $($commit_ectx(ectx, config);)?
                    #[allow(unreachable_code)]
                    {
                        let _ = (ectx, config);
                    }
                }
            }

            $crate::module_descriptor(
                $name,
                $crate::handler_trampoline::<__Handler>,
                Some($crate::commit_trampoline::<__Commit>),
                Some($crate::commit_ectx_trampoline::<__CommitEctx>),
                0u64
                    $(+ core::mem::size_of::<$prepared>() as u64)?,
            )
        }
    };
}
