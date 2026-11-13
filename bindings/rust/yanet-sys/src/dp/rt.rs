//! Handler runtime behind the generated plugin's exports.
//!
//! The runtime interprets a module header as this module's configuration on
//! the module's identity alone (attach: binding) and checks nothing per
//! generation or per packet.
//!
//! The descriptors a plugin's loader entries return carry the runtime's
//! handler and commit hooks, monomorphised for the exported type in the
//! plugin crate; nothing outside this module names a hook, and only
//! `module_hooks` and `object_hook` hand one out, after the export checks.

use core::{mem::size_of, ptr::NonNull};

use yanet_shm::root::ShmRoot;

use crate::{
    bindings,
    dp::{
        __private::{check_module, check_object},
        batch::Batch,
        cx::{CommitCx, Cx, ObjectCommitCx},
        module::{Module, ModuleConfig},
        object::{Object, ObjectConfig},
    },
};

/// Dataplane to module ABI version the bindings were generated against.
pub const ABI_VERSION: u32 = bindings::YANET_MODULE_ABI_VERSION;

/// The module handler the descriptor of `new_module` registers.
///
/// # Safety
///
/// Must be called with the arguments the dataplane passes to a module
/// handler, for a module registered as the type parameter. The
/// execution context pointer is the provenance root of the shared mapping: a
/// raw pointer, never derived from a Rust reference, whose provenance covers
/// every byte of the one contiguous mapping any pointer of the root is used
/// to access (in a product, C's shared mapping).
unsafe extern "C" fn handle<M: Module>(
    worker: *mut bindings::dp_worker,
    ectx: *mut bindings::module_ectx,
    front: *mut bindings::packet_front,
) {
    // SAFETY: the caller passes non-null worker, context and front (attach:
    // call).
    let (worker, ectx, front) = unsafe {
        (
            NonNull::new_unchecked(worker),
            NonNull::new_unchecked(ectx),
            NonNull::new_unchecked(front),
        )
    };
    // SAFETY: the context lies inside the shared mapping and its pointer
    // has mapping-wide provenance (attach: call, mapping).
    let root = unsafe { ShmRoot::from_mapping(ectx.cast()) };
    // SAFETY: the context is frozen once handed to the worker and its
    // absolute fields were derived before release (attach: call, frozen,
    // publish).
    let (config, prepared) = unsafe { ((*ectx.as_ptr()).abs_cp_module, (*ectx.as_ptr()).abs_module_prepared) };
    // SAFETY: the front, context and worker belong to this call (attach:
    // call).
    let mut batch = unsafe { Batch::new(front, ectx, worker) };
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

/// The execution-context commit hook the descriptor of `new_module`
/// registers.
///
/// # Safety
///
/// Must be called with the arguments the dataplane passes to a commit
/// handler of a module registered as the type parameter. The execution
/// context pointer is the provenance root of the shared mapping: a raw
/// pointer, never derived from a Rust reference, whose provenance covers
/// every byte of the one contiguous mapping any pointer of the root is used
/// to access (in a product, C's shared mapping).
unsafe extern "C" fn commit_ectx<M: Module>(ectx: *mut bindings::module_ectx, cp_module: *mut bindings::cp_module) {
    if size_of::<M::Prepared<'_>>() == 0 {
        // C allocates no buffer for a module without a prepared size.
        return;
    }
    // SAFETY: the caller passes a non-null context inside the mapping with
    // mapping-wide provenance (attach: call, mapping).
    let ectx = unsafe { NonNull::new_unchecked(ectx) };
    // SAFETY: as above.
    let root = unsafe { ShmRoot::from_mapping(ectx.cast()) };
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
    // allocator (attach: align; only `module_hooks` hands out this hook, and
    // its `check_module` bounds the alignment by `MAX_ALIGN` and refuses a
    // value that needs a drop) and exclusively owned by this pass; a value
    // needing no drop leaks nothing if the buffer is overwritten.
    unsafe { out.write(M::prepare(&cx)) };
}

/// Allocates the module descriptor the loader copies and frees, with the
/// runtime's handler and commit hook for the type parameter; null when C
/// calloc fails.
///
/// A module that `check_module` refuses fails the build where the function
/// is instantiated, as one whose name does not fit the C module type field:
///
/// ```compile_fail,E0080
/// use yanet_sys::dp::{Batch, CommitCx, Cx, Module, Verdict};
/// struct Mine;
/// impl Module for Mine {
///     const NAME: &'static str =
///         "module_of_a_name_long_enough_to_overflow_the_eighty_bytes_of_the_c_module_type_field";
///     type Config = u64;
///     type Prepared<'p> = ();
///     fn handle(_: &mut Cx<Self>, batch: &mut Batch) {
///         batch.dispatch(|_| Verdict::Pass);
///     }
///     fn prepare(_: &CommitCx<'_, Self>) {}
/// }
/// let _ = yanet_sys::dp::__private::new_module::<Mine>();
/// ```
///
/// A name that fits is accepted:
///
/// ```no_run
/// use yanet_sys::dp::{Batch, CommitCx, Cx, Module, Verdict};
/// struct Mine;
/// impl Module for Mine {
///     const NAME: &'static str = "module_of_a_name_short_enough_to_fit_the_c_module_type_field";
///     type Config = u64;
///     type Prepared<'p> = ();
///     fn handle(_: &mut Cx<Self>, batch: &mut Batch) {
///         batch.dispatch(|_| Verdict::Pass);
///     }
///     fn prepare(_: &CommitCx<'_, Self>) {}
/// }
/// let _ = yanet_sys::dp::__private::new_module::<Mine>();
/// ```
pub fn new_module<M: Module>() -> *mut bindings::module {
    let (handler, commit_ectx) = module_hooks::<M>();
    // SAFETY: the shim reads the name's bytes, live for the call, and stores
    // the hooks and the size in a descriptor it allocates with C calloc,
    // which the loader releases with C free; `check_module`, run by
    // `module_hooks`, proved the name fits the field with its terminator.
    unsafe {
        bindings::yanet_sys_dp_module_new(
            M::NAME.as_ptr().cast(),
            M::NAME.len(),
            handler,
            commit_ectx,
            size_of::<M::Prepared<'static>>() as u64,
        )
    }
}

/// The handler and commit hook the descriptor of `new_module` registers for
/// a module, as the loader reads them from it.
///
/// Fails the build, as `new_module` does, for a module `check_module`
/// refuses:
///
/// ```compile_fail,E0080
/// use yanet_sys::dp::{Batch, CommitCx, Cx, Module, Verdict};
/// struct Mine;
/// impl Module for Mine {
///     const NAME: &'static str =
///         "module_of_a_name_long_enough_to_overflow_the_eighty_bytes_of_the_c_module_type_field";
///     type Config = u64;
///     type Prepared<'p> = ();
///     fn handle(_: &mut Cx<Self>, batch: &mut Batch) {
///         batch.dispatch(|_| Verdict::Pass);
///     }
///     fn prepare(_: &CommitCx<'_, Self>) {}
/// }
/// let _ = yanet_sys::dp::__private::module_hooks::<Mine>();
/// ```
pub fn module_hooks<M: Module>() -> (bindings::module_handler, bindings::module_commit_ectx_handler) {
    const { check_module::<M>() };
    (Some(handle::<M>), Some(commit_ectx::<M>))
}

/// The object execution-context commit hook the descriptor of `new_object`
/// registers.
///
/// # Safety
///
/// Must be called with the arguments the dataplane passes to the commit
/// handler of an object type registered as the type parameter, which runs it
/// only for a context whose prepared buffer is allocated. The execution
/// context pointer is the provenance root of the shared mapping, as for a
/// module's commit handler.
unsafe extern "C" fn commit_object_ectx<O: Object>(
    ectx: *mut bindings::object_ectx,
    cp_object: *mut bindings::cp_object,
) {
    if size_of::<O::Prepared<'_>>() == 0 {
        // The descriptor of such a type registers no hook.
        return;
    }
    // SAFETY: the caller passes a non-null context inside the mapping with
    // mapping-wide provenance (attach: call, mapping).
    let root = unsafe { ShmRoot::from_mapping(NonNull::new_unchecked(ectx).cast()) };
    // SAFETY: the context is frozen except for the prepared buffer, which the
    // single commit pass owns, and the pass runs only for a context whose
    // buffer is allocated (attach: call, frozen, publish).
    let out = root.ptr::<O::Prepared<'_>>(unsafe { (*ectx).abs_object_prepared }.addr());
    // SAFETY: the header is an object of this type, its body frozen but for
    // its atomics and mutex payloads (attach: binding, frozen).
    let config = unsafe { (*root.ptr::<ObjectConfig<O::Config>>(cp_object.addr())).body() };
    // SAFETY: root, context and configuration as stated above.
    let cx = unsafe { ObjectCommitCx::<O>::new(root, NonNull::new_unchecked(ectx), config) };
    // SAFETY: the buffer holds the prepared value's size, aligned by the
    // allocator, and is owned by this pass, as for a module (attach: align;
    // only `object_hook` hands out this hook, after its `check_object`).
    unsafe { out.write(O::prepare(&cx)) };
}

/// Allocates the object type descriptor the loader copies and frees, with
/// the runtime's commit hook for the type parameter; null when C calloc
/// fails.
///
/// A type with a zero-sized prepared value registers no commit hook and no
/// prepared buffer, so C runs no hook for it.
///
/// A type that `check_object` refuses fails the build where the function is
/// instantiated, as one whose name does not fit the C object type fields:
///
/// ```compile_fail,E0080
/// use yanet_sys::dp::{Object, ObjectCommitCx};
/// struct Mine;
/// impl Object for Mine {
///     const TYPE: &'static str =
///         "state_of_a_name_long_enough_to_overflow_the_eighty_bytes_of_the_c_object_type_field";
///     type Config = u64;
///     type Prepared<'p> = ();
///     fn prepare(_: &ObjectCommitCx<'_, Self>) {}
/// }
/// let _ = yanet_sys::dp::__private::new_object::<Mine>();
/// ```
///
/// A name that fits is accepted:
///
/// ```no_run
/// use yanet_sys::dp::{Object, ObjectCommitCx};
/// struct Mine;
/// impl Object for Mine {
///     const TYPE: &'static str = "state_of_a_name_short_enough_to_fit_the_c_object_type_field";
///     type Config = u64;
///     type Prepared<'p> = ();
///     fn prepare(_: &ObjectCommitCx<'_, Self>) {}
/// }
/// let _ = yanet_sys::dp::__private::new_object::<Mine>();
/// ```
pub fn new_object<O: Object>() -> *mut bindings::object {
    let commit_ectx = object_hook::<O>();
    // SAFETY: as for `new_module`: the shim reads the name's bytes and fills
    // a descriptor it allocates for the object loader's C free;
    // `check_object`, run by `object_hook`, proved the name fits the field.
    unsafe {
        bindings::yanet_sys_dp_object_new(
            O::TYPE.as_ptr().cast(),
            O::TYPE.len(),
            commit_ectx,
            size_of::<O::Prepared<'static>>() as u64,
        )
    }
}

/// The commit hook the descriptor of `new_object` registers for an object
/// type, as the loader reads it from it: none for a zero-sized prepared
/// value.
///
/// Fails the build, as `new_object` does, for a type `check_object` refuses:
///
/// ```compile_fail,E0080
/// use yanet_sys::dp::{Object, ObjectCommitCx};
/// struct Mine;
/// impl Object for Mine {
///     const TYPE: &'static str =
///         "state_of_a_name_long_enough_to_overflow_the_eighty_bytes_of_the_c_object_type_field";
///     type Config = u64;
///     type Prepared<'p> = ();
///     fn prepare(_: &ObjectCommitCx<'_, Self>) {}
/// }
/// let _ = yanet_sys::dp::__private::object_hook::<Mine>();
/// ```
pub fn object_hook<O: Object>() -> bindings::object_commit_ectx_handler {
    const { check_object::<O>() };
    if size_of::<O::Prepared<'static>>() == 0 {
        None
    } else {
        Some(commit_object_ectx::<O>)
    }
}

/// Exports a module to the dataplane plugin loader, with the object types its
/// plugin carries.
///
/// Emits `new_module_<name>`, which returns the module's descriptor with the
/// runtime's handler and commit hook, and `yanet_module_abi_version`. The
/// name is the module's declared name with each `-` written as `_`, as the
/// dataplane's module list names the loader (`route-mpls` by
/// `new_module_route_mpls`), or the build fails.
///
/// `objects` lists the object types the plugin carries beside the module,
/// each as `<type> = <path>`: exported and checked as by `export_object!`, but
/// without a second ABI version, which a plugin exports once. A module and the
/// object types it links share a plugin this way, as a C module's plugin
/// carries its object types.
///
/// Generated into each plugin crate as its only line.
///
/// The module is exported under its own name only; the check is a const
/// assertion, which rustc reports as E0080:
///
/// ```compile_fail,E0080
/// use yanet_sys::dp::{Batch, CommitCx, Cx, Module, Verdict};
/// struct Mine;
/// impl Module for Mine {
///     const NAME: &'static str = "mine";
///     type Config = u64;
///     type Prepared<'p> = ();
///     fn handle(_: &mut Cx<Self>, batch: &mut Batch) {
///         batch.dispatch(|_| Verdict::Pass);
///     }
///     fn prepare(_: &CommitCx<'_, Self>) {}
/// }
/// yanet_sys::export_module!(name = other, module = Mine);
/// ```
///
/// Under its declared name, with a `_` in place of each `-`, it is accepted:
///
/// ```
/// use yanet_sys::dp::{Batch, CommitCx, Cx, Module, Verdict};
/// struct RouteMpls;
/// impl Module for RouteMpls {
///     const NAME: &'static str = "route-mpls";
///     type Config = u64;
///     type Prepared<'p> = ();
///     fn handle(_: &mut Cx<Self>, batch: &mut Batch) {
///         batch.dispatch(|_| Verdict::Pass);
///     }
///     fn prepare(_: &CommitCx<'_, Self>) {}
/// }
/// yanet_sys::export_module!(name = route_mpls, module = RouteMpls);
/// ```
///
/// So is a module with the object types its plugin carries, each under its
/// type name:
///
/// ```
/// use yanet_sys::dp::{Batch, CommitCx, Cx, Module, Object, ObjectCommitCx, Verdict};
/// struct Mine;
/// impl Module for Mine {
///     const NAME: &'static str = "mine";
///     type Config = u64;
///     type Prepared<'p> = ();
///     fn handle(_: &mut Cx<Self>, batch: &mut Batch) {
///         batch.dispatch(|_| Verdict::Pass);
///     }
///     fn prepare(_: &CommitCx<'_, Self>) {}
/// }
/// struct Table;
/// impl Object for Table {
///     const TYPE: &'static str = "mine_table";
///     type Config = u64;
///     type Prepared<'p> = ();
///     fn prepare(_: &ObjectCommitCx<'_, Self>) {}
/// }
/// struct Set;
/// impl Object for Set {
///     const TYPE: &'static str = "mine_set";
///     type Config = u64;
///     type Prepared<'p> = ();
///     fn prepare(_: &ObjectCommitCx<'_, Self>) {}
/// }
/// yanet_sys::export_module!(
///     name = mine,
///     module = Mine,
///     objects = [mine_table = Table, mine_set = Set]
/// );
/// ```
///
/// An object type it carries is checked as `export_object!` checks one, so a
/// type listed under another name is refused:
///
/// ```compile_fail,E0080
/// use yanet_sys::dp::{Batch, CommitCx, Cx, Module, Object, ObjectCommitCx, Verdict};
/// struct Mine;
/// impl Module for Mine {
///     const NAME: &'static str = "mine";
///     type Config = u64;
///     type Prepared<'p> = ();
///     fn handle(_: &mut Cx<Self>, batch: &mut Batch) {
///         batch.dispatch(|_| Verdict::Pass);
///     }
///     fn prepare(_: &CommitCx<'_, Self>) {}
/// }
/// struct Table;
/// impl Object for Table {
///     const TYPE: &'static str = "mine_table";
///     type Config = u64;
///     type Prepared<'p> = ();
///     fn prepare(_: &ObjectCommitCx<'_, Self>) {}
/// }
/// yanet_sys::export_module!(name = mine, module = Mine, objects = [table = Table]);
/// ```
#[macro_export]
macro_rules! export_module {
    (
        name = $name:ident,
        module = $($module:ident)::+
        $(, objects = [$($object_name:ident = $($object:ident)::+),* $(,)?])?
        $(,)?
    ) => {
        const _: () = {
            type __ExportedModule = $($module)::+;

            const _: () = ::core::assert!(
                $crate::dp::__private::names_module(
                    ::core::stringify!($name),
                    <__ExportedModule as $crate::dp::Module>::NAME,
                ),
                "export_module!: the name is not Module::NAME with each '-' written as '_'",
            );
            // `new_module` asserts it too, but only once instantiated, which a
            // check build never does.
            const _: () = $crate::dp::__private::check_module::<__ExportedModule>();

            #[unsafe(export_name = "yanet_module_abi_version")]
            pub static YANET_MODULE_ABI_VERSION: u32 = $crate::dp::__private::ABI_VERSION;

            #[unsafe(export_name = ::core::concat!("new_module_", ::core::stringify!($name)))]
            pub extern "C" fn new_module() -> *mut $crate::dp::__private::bindings::module {
                $crate::dp::__private::new_module::<__ExportedModule>()
            }
        };
        $($($crate::__export_object_type!(name = $object_name, object = $($object)::+);)*)?
    };
}

/// Exports an object type to the dataplane object loader, as a plugin of its
/// own.
///
/// Emits `new_object_<name>`, which returns the type's descriptor with the
/// runtime's commit hook for a non-zero-sized prepared value, and
/// `yanet_module_abi_version`, which the plugin loader asks of every plugin. A
/// plugin exports the ABI version once, so an object type that ships with a
/// module is listed in the module's `export_module!` instead. The name is the
/// object's type name as it is, as the loader looks a type up by
/// `new_object_<type>`, or the build fails. Generated into each plugin crate as
/// its only line.
///
/// The type is exported under its own name only; the check is a const
/// assertion, which rustc reports as E0080:
///
/// ```compile_fail,E0080
/// use yanet_sys::dp::{Object, ObjectCommitCx};
/// struct Mine;
/// impl Object for Mine {
///     const TYPE: &'static str = "mine";
///     type Config = u64;
///     type Prepared<'p> = ();
///     fn prepare(_: &ObjectCommitCx<'_, Self>) {}
/// }
/// yanet_sys::export_object!(name = other, object = Mine);
/// ```
///
/// A type name that no identifier spells, as one with a `-`, is refused:
///
/// ```compile_fail,E0080
/// use yanet_sys::dp::{Object, ObjectCommitCx};
/// struct Mine;
/// impl Object for Mine {
///     const TYPE: &'static str = "my-state";
///     type Config = u64;
///     type Prepared<'p> = ();
///     fn prepare(_: &ObjectCommitCx<'_, Self>) {}
/// }
/// yanet_sys::export_object!(name = my_state, object = Mine);
/// ```
///
/// A type name that does not fit the C type field with its terminator is
/// refused, not truncated:
///
/// ```compile_fail,E0080
/// use yanet_sys::dp::{Object, ObjectCommitCx};
/// struct Mine;
/// impl Object for Mine {
///     const TYPE: &'static str =
///         "state_of_a_name_long_enough_to_overflow_the_eighty_bytes_of_the_c_object_type_field";
///     type Config = u64;
///     type Prepared<'p> = ();
///     fn prepare(_: &ObjectCommitCx<'_, Self>) {}
/// }
/// yanet_sys::export_object!(
///     name = state_of_a_name_long_enough_to_overflow_the_eighty_bytes_of_the_c_object_type_field,
///     object = Mine
/// );
/// ```
///
/// Under its type name it is accepted:
///
/// ```
/// use yanet_sys::dp::{Object, ObjectCommitCx};
/// struct Mine;
/// impl Object for Mine {
///     const TYPE: &'static str = "my_state";
///     type Config = u64;
///     type Prepared<'p> = ();
///     fn prepare(_: &ObjectCommitCx<'_, Self>) {}
/// }
/// yanet_sys::export_object!(name = my_state, object = Mine);
/// ```
#[macro_export]
macro_rules! export_object {
    (name = $name:ident, object = $($object:ident)::+ $(,)?) => {
        const _: () = {
            #[unsafe(export_name = "yanet_module_abi_version")]
            pub static YANET_MODULE_ABI_VERSION: u32 = $crate::dp::__private::ABI_VERSION;
        };
        $crate::__export_object_type!(name = $name, object = $($object)::+);
    };
}

/// Emits an object type's loader entry `new_object_<name>`, after the checks
/// of its name and prepared value.
///
/// What `export_object!` exports besides the ABI version, and what
/// `export_module!` exports for each object type it lists.
#[doc(hidden)]
#[macro_export]
macro_rules! __export_object_type {
    (name = $name:ident, object = $($object:ident)::+) => {
        const _: () = {
            type __ExportedObject = $($object)::+;

            const _: () = ::core::assert!(
                $crate::dp::__private::names_object(
                    ::core::stringify!($name),
                    <__ExportedObject as $crate::dp::Object>::TYPE,
                ),
                "an object type is exported under a name other than its Object::TYPE",
            );
            // `new_object` asserts it too, but only once instantiated, which a
            // check build never does.
            const _: () = $crate::dp::__private::check_object::<__ExportedObject>();

            #[unsafe(export_name = ::core::concat!("new_object_", ::core::stringify!($name)))]
            pub extern "C" fn new_object() -> *mut $crate::dp::__private::bindings::object {
                $crate::dp::__private::new_object::<__ExportedObject>()
            }
        };
    };
}
