//! Owner-bound capabilities issued only by the configuration builder.

use core::marker::PhantomData;

use super::{BlockAddr, Context, Header, Kind, MemoryContext, Owner, ShmLayout};

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

/// Exclusive release right for one initialized value in the issuing context.
///
/// The parent retains the storage of the value itself. Only its resources
/// are handed over to the release implementation.
#[must_use]
pub struct Taken<'a, T: ShmLayout, W: Owner> {
    addr: usize,
    memory: &'a MemoryContext<'a, W>,
    _value: PhantomData<fn(T) -> T>,
}

impl<'a, T: ShmLayout, W: Owner> Taken<'a, T, W> {
    /// # Safety
    ///
    /// The address holds one live, aligned and initialized value satisfying
    /// the release contract of `T` in this context. No other code can access
    /// the value or release its resources while this right is held.
    pub(crate) unsafe fn new(addr: usize, memory: &'a MemoryContext<'_, W>) -> Self {
        Self { addr, memory, _value: PhantomData }
    }

    /// Final address of the value.
    pub fn addr(&self) -> usize {
        self.addr
    }

    /// The issuing context and its provenance root.
    pub fn memory(&self) -> &'a MemoryContext<'a, W> {
        self.memory
    }

    /// Gives a reborrowed release right for one of this value's fields.
    ///
    /// # Safety
    ///
    /// The offset names an aligned field of exactly this type, written
    /// whole. Its resources must not already have been released or handed
    /// over. Even fields sharing an address because they are zero-sized
    /// are distinct fields and must each be released exactly once.
    pub unsafe fn field<U: ShmLayout>(&mut self, offset: usize) -> Taken<'_, U, W> {
        // SAFETY: the caller proves ownership of this field at its offset.
        unsafe { Taken::new(self.addr + offset, self.memory) }
    }

    pub(crate) fn get(&self) -> &T {
        // SAFETY: the capability holds an initialized, aligned value, and
        // the shared borrow prevents use of its exclusive release right.
        unsafe { &*self.memory.root().ptr(self.addr) }
    }
}

impl<T: ShmLayout, W: Owner> core::fmt::Debug for Taken<'_, T, W> {
    fn fmt(&self, f: &mut core::fmt::Formatter<'_>) -> core::fmt::Result {
        f.debug_struct("Taken")
            .field("addr", &self.addr)
            .finish_non_exhaustive()
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
