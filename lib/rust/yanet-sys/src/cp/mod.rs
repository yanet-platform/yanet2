//! Mirror of `lib/controlplane/`: the control-plane boundary of the Rust
//! module SDK.
//!
//! Builds module configurations and objects in the shared memory of an
//! [`Owner`]. The owner is the seam to the C control plane (agent memory, the
//! module and object headers and the module's registries); this module calls C
//! only for the objects a configuration embeds, such as the LPM, through the
//! shims of `common/`, and the classifiers of `lib/classify`.
//! `no_std` without `alloc`: the owner supplies the memory.
//!
//! # Construction
//!
//! A configuration body is a `#[repr(C)]` struct deriving `ShmLayout` (the
//! derive lives in `yanet-sdk-derive`), which also makes it [`Build`]: the
//! derive generates a `<Type>Parts` struct with one field per field of the
//! body. [`Builder::build`] runs the author's closure with a [`Scope`], whose
//! methods allocate the owned blocks ([`Owned`], [`OwnedSlice`]) and register
//! counters and devices ([`Registered`]); the closure returns the parts of the
//! body, which the builder writes in place and hands over as a [`Built`]. A
//! block is exactly its payload, and a configuration is released by walking its
//! owning edges, as C does; a block not yet written into its parent frees
//! itself when dropped.
//!
//! Writing a value is fallible, as embedded C objects that allocate are: a
//! refused write returns the blocks it took and the parts it was given, so the
//! refusal leaves nothing behind.
//!
//! # Objects
//!
//! An object, C's `cp_object`, is built the same way by
//! [`ObjectBuilder::build`], over a scope of [`ObjectKind`] that registers no
//! counter and links no device, and handed over as a [`BuiltObject`]. It is
//! state that outlives the module configurations linking it: the control
//! plane publishes it by name before any module links it and releases it once
//! no generation holds it.

mod addr;
mod builder;
mod error;
mod owner;
mod record;

pub use addr::{BlockAddr, Context, Header};
pub use builder::{
    Build, Builder, Built, BuiltObject, Kind, ModuleKind, ObjectBuilder, ObjectKind, Owned, OwnedSlice, Registered,
    Scope, Stage, Staged,
};
#[doc(hidden)]
pub use builder::{MemoryContext, Partial};
pub use error::{BuildError, Refusal};
pub use owner::Owner;
pub use record::FfiRecord;
