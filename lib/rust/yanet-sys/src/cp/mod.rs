//! Mirror of `lib/controlplane/`: the control-plane boundary of the Rust
//! module SDK.
//!
//! Builds module configurations in the shared memory of an [`Owner`]. The
//! owner is the seam to the C control plane (agent memory, the module header
//! and its registries); this module calls C only for the objects a
//! configuration embeds, such as the LPM, through the shims of `common/`.
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

mod addr;
mod build;
mod error;
mod owner;
mod record;

pub use addr::{BlockAddr, Context, Header};
pub use build::{Build, Builder, Built, Owned, OwnedSlice, Registered, Scope};
#[doc(hidden)]
pub use build::{MemoryContext, Partial};
pub use error::{BuildError, Refusal};
pub use owner::Owner;
pub use record::FfiRecord;
