//! Owner-bound capabilities issued only by the configuration builder.

use core::marker::PhantomData;

use super::{BlockAddr, Context, Header, Kind};

/// A fresh root block large enough for a header of kind `K`.
///
/// Borrowed from its allocation guard. Consuming it permits one initialization
/// attempt, not ownership of the allocation.
pub struct Fresh<'a, W: ?Sized, K: Kind> {
    owner: &'a W,
    addr: BlockAddr,
    _kind: PhantomData<K>,
    _local: PhantomData<*const ()>,
}

impl<'a, W: ?Sized, K: Kind> Fresh<'a, W, K> {
    pub(super) fn new(owner: &'a W, addr: BlockAddr) -> Self {
        Self {
            owner,
            addr,
            _kind: PhantomData,
            _local: PhantomData,
        }
    }

    /// The owner whose root block is being initialized.
    pub fn owner(&self) -> &'a W {
        self.owner
    }

    /// The header at the start of the root block.
    pub fn header(&self) -> Header {
        Header::at(self.addr)
    }
}

/// An initialized, unpublished header borrowed from its live guard.
///
/// Its kind selects the registries available to an Owner implementation.
pub struct Live<'a, W: ?Sized, K: Kind> {
    owner: &'a W,
    header: Header,
    _kind: PhantomData<K>,
    _local: PhantomData<*const ()>,
}

impl<'a, W: ?Sized, K: Kind> Live<'a, W, K> {
    pub(super) fn new(owner: &'a W, header: Header) -> Self {
        Self {
            owner,
            header,
            _kind: PhantomData,
            _local: PhantomData,
        }
    }

    /// The owner that initialized the header.
    pub fn owner(&self) -> &'a W {
        self.owner
    }

    /// The initialized header of this kind.
    pub fn header(&self) -> Header {
        self.header
    }
}

/// A memory context borrowed from a live module or object header.
///
/// Unlike registrations, allocation does not depend on the header kind.
pub struct LiveContext<'a, W: ?Sized> {
    owner: &'a W,
    context: Context,
    _local: PhantomData<*const ()>,
}

impl<'a, W: ?Sized> LiveContext<'a, W> {
    pub(super) fn new(owner: &'a W, context: Context) -> Self {
        Self { owner, context, _local: PhantomData }
    }

    /// The owner of this initialized memory context.
    pub fn owner(&self) -> &'a W {
        self.owner
    }

    /// The initialized header's embedded memory context.
    pub fn context(&self) -> Context {
        self.context
    }
}

/// An unreferenced initialized header, ready for one finalization.
pub struct Retiring<'a, W: ?Sized, K: Kind> {
    owner: &'a W,
    header: Header,
    _kind: PhantomData<K>,
    _local: PhantomData<*const ()>,
}

impl<'a, W: ?Sized, K: Kind> Retiring<'a, W, K> {
    pub(super) fn new(owner: &'a W, header: Header) -> Self {
        Self {
            owner,
            header,
            _kind: PhantomData,
            _local: PhantomData,
        }
    }

    /// The owner that initialized the header.
    pub fn owner(&self) -> &'a W {
        self.owner
    }

    /// The header whose finalization consumes this capability.
    pub fn header(&self) -> Header {
        self.header
    }
}

/// An unreferenced allocation ready to return once, with its original size.
///
/// The default context `()` denotes a root allocation. A [`Context`] denotes
/// an allocation in a live header, so the two freeing operations cannot mix.
pub struct Freed<'a, W: ?Sized, C = ()> {
    owner: &'a W,
    addr: BlockAddr,
    size: usize,
    context: C,
    _local: PhantomData<*const ()>,
}

impl<'a, W: ?Sized> Freed<'a, W> {
    pub(super) fn root(owner: &'a W, addr: BlockAddr, size: usize) -> Self {
        Self {
            owner,
            addr,
            size,
            context: (),
            _local: PhantomData,
        }
    }
}

impl<'a, W: ?Sized> Freed<'a, W, Context> {
    /// Issues a return capability for an allocation in a live context.
    ///
    /// # Safety
    ///
    /// The block was allocated in this live context with this size, is still
    /// owned, and is no longer referenced. No second capability names it.
    pub(super) unsafe fn in_context(context: &'a LiveContext<'_, W>, addr: BlockAddr, size: usize) -> Self {
        Self {
            owner: context.owner,
            addr,
            size,
            context: context.context,
            _local: PhantomData,
        }
    }

    /// The live context from which the allocation came.
    pub fn context(&self) -> Context {
        self.context
    }
}

impl<'a, W: ?Sized, C> Freed<'a, W, C> {
    /// The owner whose allocation is returned.
    pub fn owner(&self) -> &'a W {
        self.owner
    }

    /// The original allocation address.
    pub fn addr(&self) -> BlockAddr {
        self.addr
    }

    /// The original allocation size in bytes.
    pub fn size(&self) -> usize {
        self.size
    }
}
