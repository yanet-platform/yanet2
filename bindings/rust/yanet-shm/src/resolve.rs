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
//! whole mapping, and the graph token's contract requires that graph to be
//! published and frozen for `'g` as well.

use core::marker::PhantomData;

use crate::root::ShmRoot;

/// The provenance root of published, frozen graphs, invariant in `'g`.
///
/// A token can be copied and delegated, but its lifetime cannot be changed.
/// Safe code cannot create one from a raw root:
///
/// ```compile_fail,E0133
/// use yanet_shm::{resolve::Graph, root::ShmRoot};
/// fn forge(root: ShmRoot) -> Graph<'static> {
///     Graph::new(root)
/// }
/// ```
///
/// Nor can a token outlive the lifetime it was issued for:
///
/// ```compile_fail
/// use yanet_shm::resolve::Graph;
/// fn widen<'g>(graph: Graph<'g>) -> Graph<'static> {
///     graph
/// }
/// ```
///
/// Shortening the lifetime is also refused, preserving the invariant brand:
///
/// ```compile_fail
/// use yanet_shm::resolve::Graph;
/// fn shorten<'long: 'short, 'short>(graph: Graph<'long>) -> Graph<'short> {
///     graph
/// }
/// ```
///
/// A token is neither sent nor shared between threads:
///
/// ```compile_fail,E0277
/// use yanet_shm::resolve::Graph;
/// fn send<T: Send>() {}
/// send::<Graph<'static>>();
/// ```
///
/// ```compile_fail,E0277
/// use yanet_shm::resolve::Graph;
/// fn share<T: Sync>() {}
/// share::<Graph<'static>>();
/// ```
#[derive(Clone, Copy)]
pub struct Graph<'g> {
    root: ShmRoot,
    _brand: PhantomData<fn(&'g ()) -> &'g ()>,
}

impl<'g> Graph<'g> {
    /// Issues a token over a published graph's mapping.
    ///
    /// # Safety
    ///
    /// The root must retain a raw pointer's provenance over the whole mapping,
    /// never derived from a Rust reference.
    ///
    /// The lifetime `'g` does not tie a reference to this context's own graph
    /// (see the module documentation), so the contract covers every reference
    /// safe code can hold for `'g`: each `'g` reference to a shared-memory
    /// value that safe code can pass to the resolving methods, whichever
    /// context or graph it came from, must belong to a published graph that
    /// stays frozen (except opaque bytes, atomics and mutex-protected
    /// payloads) for `'g`, inside the memory the root covers. Mutex
    /// payloads are reached only through a live lock guard. Per-worker
    /// slots and regions are exempt: only the worker owning one reaches it,
    /// through the handle in its prepared value, and a reference into it
    /// lives only while its call context is borrowed, shorter than `'g`, so
    /// safe code cannot pass it to a resolving method. The issuer meets
    /// this contract by creating the context only over a root that covers
    /// the whole mapping and by handing safe code references only into
    /// published graphs of that mapping.
    #[inline(always)]
    pub const unsafe fn new(root: ShmRoot) -> Self {
        Self { root, _brand: PhantomData }
    }

    /// The token's provenance root.
    #[doc(hidden)]
    #[inline(always)]
    pub const fn root(self) -> ShmRoot {
        self.root
    }
}

/// A context that resolves edges of the graphs its token covers.
///
/// Implementations delegate a token from a legitimate context of the same
/// invariant brand. Only issuing the token requires an unsafe contract.
pub trait Resolve<'g> {
    /// The published-graph token.
    #[doc(hidden)]
    fn graph(&self) -> Graph<'g>;
}

impl<'g> Resolve<'g> for Graph<'g> {
    #[inline(always)]
    fn graph(&self) -> Graph<'g> {
        *self
    }
}
