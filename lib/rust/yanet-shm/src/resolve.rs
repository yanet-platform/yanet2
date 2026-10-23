//! Resolution of shared-memory edges through a context.
//!
//! Edges resolve only through a context that holds the provenance root of
//! the mapping a published graph lives in. The edge types take the context
//! as an argument
//! ([`ShmBox::get`](crate::rel::ShmBox::get),
//! [`ShmSlice::as_slice`](crate::rel::ShmSlice::as_slice) and the lookups of
//! the crates that bind C data structures). A result lives for the
//! context's lifetime `'g`, and an edge reference must live for `'g` too.
//!
//! The lifetime does not prove that the slot belongs to this context's
//! graph. References are covariant, so a slot reference that outlives `'g`
//! type-checks, among them a slot of another graph reachable for `'g`, such
//! as a sibling view in the same scope. What the types refuse is a slot
//! whose reference is shorter than `'g`. Resolving another graph's slot is
//! still sound: the address comes from the slot itself, the root covers the
//! whole mapping, and the trait contract requires that graph to be published
//! and frozen for `'g` as well.

use crate::root::ShmRoot;

/// A context that resolves edges of published, frozen graphs: the root the
/// edge types read through.
///
/// # Safety
///
/// The lifetime `'g` does not tie a reference to this context's own graph
/// (see the module documentation), so the contract covers every reference
/// safe code can hold for `'g`: each `'g` reference to a shared-memory
/// value that safe code can pass to the resolving methods, whichever context or
/// graph it came from, must belong to a published graph that
/// stays frozen (except `Opaque` bytes and atomics) for `'g`, inside the
/// memory [`Self::shm_root`] covers. The implementor meets it by creating
/// the context only over a root that covers the whole mapping and by
/// handing safe code references only into published graphs of that
/// mapping.
pub unsafe trait Resolve<'g> {
    /// Provenance root of the graph.
    #[doc(hidden)]
    fn shm_root(&self) -> ShmRoot;
}
