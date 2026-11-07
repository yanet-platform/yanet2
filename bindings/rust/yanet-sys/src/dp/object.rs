//! The object trait: one impl per object type binds its type name and its
//! configuration.

use core::mem::{align_of, size_of};

use yanet_shm::opaque::Opaque;

use crate::{bindings, cp::Build, dp::cx::ObjectCommitCx};

/// A shared object written in Rust: named state that outlives the module
/// configurations linking it, as C's `cp_object`.
///
/// An object is built once by the control plane, published into a
/// generation by name, and replaced by publishing another of the same type
/// and name; module configurations come and go around it. One impl feeds both
/// the object's plugin, which registers the type with the dataplane, and its
/// api crate, which builds it.
///
/// An object type ships in the plugin of a module that lists it in its
/// `export_module!`, or in a plugin of its own through `export_object!`, as
/// every plugin exports one `yanet_module_abi_version`.
pub trait Object: Sized + 'static {
    /// Object type name, as the dataplane registers the type and as the
    /// object's plugin exports it.
    const TYPE: &'static str;
    /// Configuration body, the object's state; the control plane builds it
    /// with [`Build`] and may read it while it is published, so it is `Sync`.
    type Config: Build + Sync;

    /// Value of each worker context of the object in a generation, made by
    /// the prepare step and read by the modules linking the object; the unit
    /// type when unused.
    ///
    /// Each worker context of the object has a prepared value of its own,
    /// which the prepare step makes from the configuration and the context's
    /// worker, so it may hold a handle on that worker's per-worker slot. A
    /// module linking the object reads, through the link, the prepared value
    /// of the object context of its own worker.
    ///
    /// The same rules as a module's prepared value: made on the assigner
    /// thread and read on the worker, so it is `Send`; it needs no drop and
    /// is aligned within what the block allocator guarantees, which
    /// `export_object!` checks:
    ///
    /// ```compile_fail,E0080
    /// use yanet_sys::dp::{Object, ObjectCommitCx};
    /// #[repr(align(128))]
    /// struct Wide(u64);
    /// struct Mine;
    /// impl Object for Mine {
    ///     const TYPE: &'static str = "mine";
    ///     type Config = u64;
    ///     type Prepared<'p> = Wide;
    ///     fn prepare(_: &ObjectCommitCx<'_, Self>) -> Wide {
    ///         Wide(0)
    ///     }
    /// }
    /// yanet_sys::export_object!(name = mine, object = Mine);
    /// ```
    ///
    /// A zero-sized prepared value is never made, so its type must have one:
    ///
    /// ```compile_fail,E0080
    /// use yanet_sys::dp::{Object, ObjectCommitCx};
    /// enum Never {}
    /// struct Mine;
    /// impl Object for Mine {
    ///     const TYPE: &'static str = "mine";
    ///     type Config = u64;
    ///     type Prepared<'p> = Never;
    ///     fn prepare(_: &ObjectCommitCx<'_, Self>) -> Never {
    ///         panic!()
    ///     }
    /// }
    /// yanet_sys::export_object!(name = mine, object = Mine);
    /// ```
    ///
    /// A prepared value within the allocator's alignment is accepted:
    ///
    /// ```
    /// use yanet_sys::dp::{Object, ObjectCommitCx};
    /// #[repr(align(64))]
    /// struct Line(u64);
    /// struct Mine;
    /// impl Object for Mine {
    ///     const TYPE: &'static str = "mine";
    ///     type Config = u64;
    ///     type Prepared<'p> = Line;
    ///     fn prepare(_: &ObjectCommitCx<'_, Self>) -> Line {
    ///         Line(0)
    ///     }
    /// }
    /// yanet_sys::export_object!(name = mine, object = Mine);
    /// ```
    type Prepared<'p>: Send;

    /// Makes the prepared value from the configuration and the context's
    /// worker.
    ///
    /// Runs on the dataplane's assigner thread once per worker context of the
    /// object in a generation, before the module contexts linking it are
    /// prepared. A zero-sized prepared value has no buffer and no commit hook,
    /// so for it this is not called.
    fn prepare<'p>(cx: &ObjectCommitCx<'p, Self>) -> Self::Prepared<'p>;
}

/// The root block of an object: the C object header, then the body the
/// object type declares.
///
/// The header is C's `cp_object`, which the control plane initialises and
/// finalises; its bytes stay opaque to Rust. Its entity base sits at offset
/// zero, as a module header's does, so the memory context of the build is at
/// the same offset in both.
#[repr(C)]
pub struct ObjectConfig<B> {
    header: Opaque<bindings::cp_object>,
    body: B,
}

impl<B> ObjectConfig<B> {
    /// Offset of the body inside the block.
    pub const BODY_OFFSET: usize = core::mem::offset_of!(Self, body);

    /// Object body.
    #[inline(always)]
    pub fn body(&self) -> &B {
        &self.body
    }
}

const _: () = assert!(core::mem::offset_of!(ObjectConfig<u64>, header) == 0);
const _: () = assert!(size_of::<Opaque<bindings::cp_object>>() == size_of::<bindings::cp_object>());
const _: () = assert!(align_of::<Opaque<bindings::cp_object>>() == align_of::<bindings::cp_object>());
