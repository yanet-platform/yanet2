//! Shared-memory state accessed only while its spinlock is held.

use core::{
    cell::UnsafeCell,
    mem::offset_of,
    ops::{Deref, DerefMut},
    sync::atomic::AtomicU32,
};

use crate::{
    layout::{Field, Kind, Node, Param},
    shm::ShmLayout,
    spinlock::{Spinlock, SpinlockGuard},
};

/// A spin mutex protecting a plain shared-memory value.
///
/// Every access to the value takes a guard. Acquiring the lock observes the
/// previous holder's writes; dropping the guard publishes this holder's
/// writes. An unwinding panic releases the lock without poisoning it. A
/// holder that never returns, such as a killed process or a forgotten guard,
/// leaves it locked.
/// Only copyable layout values are accepted, so the guard cannot move an
/// owning edge or an address-relative pointer out of its storage.
/// Storage follows the C representation: a 32-bit lock word, alignment padding
/// and the value, which starts at the larger of 4 and its own alignment.
/// Padding is not initialized data and must never be read as bytes.
///
/// ```
/// #![forbid(unsafe_code)]
/// use yanet_shm::mutex::Mutex;
/// let state = Mutex::new([0_u64; 2]);
/// {
///     let mut guard = state.lock();
///     guard[0] += 1;
///     guard[1] += 64;
/// }
/// assert_eq!([1, 64], *state.lock());
/// ```
///
/// Owning edges cannot be protected and then moved through the guard:
///
/// ```compile_fail,E0277
/// use yanet_shm::{mutex::Mutex, rel::ShmBox};
/// fn move_edge(_: Mutex<ShmBox<u64>>) {}
/// ```
#[repr(C)]
pub struct Mutex<T>
where
    T: Copy + ShmLayout,
{
    lock: Spinlock,
    value: UnsafeCell<T>,
}

impl<T> Mutex<T>
where
    T: Copy + ShmLayout,
{
    /// An unlocked mutex containing the initial value.
    pub const fn new(value: T) -> Self {
        Self {
            lock: Spinlock::new(),
            value: UnsafeCell::new(value),
        }
    }

    /// Spins until the value can be borrowed exclusively.
    #[inline]
    pub fn lock(&self) -> MutexGuard<'_, T> {
        self.guard(self.lock.lock())
    }

    /// Borrows the value exclusively if the mutex is free, without waiting.
    #[inline]
    pub fn try_lock(&self) -> Option<MutexGuard<'_, T>> {
        self.lock.try_lock().map(|lock| self.guard(lock))
    }

    /// Borrows the value exclusively, calling the hook between rounds of
    /// spinning while another holder owns it.
    ///
    /// A caller sharing its CPU with a holder may yield from the hook. A
    /// hook that panics, including when dropped, never strands a lock this
    /// call acquired.
    #[inline]
    pub fn lock_with<F>(&self, wait: F) -> MutexGuard<'_, T>
    where
        F: FnMut(),
    {
        self.guard(self.lock.lock_with(wait))
    }

    /// Borrows the payload while the guard of this mutex keeps it exclusive.
    #[inline]
    fn guard<'a>(&'a self, lock: SpinlockGuard<'a>) -> MutexGuard<'a, T> {
        MutexGuard {
            // SAFETY: every caller holds this mutex's lock, and the payload
            // is private. The exclusive borrow ends before the guard unlocks.
            value: unsafe { &mut *self.value.get() },
            _lock: lock,
        }
    }
}

// SAFETY: only a lock holder can reach the payload. The lock orders every
// holder's access, and the payload can be transferred between threads.
unsafe impl<T> Sync for Mutex<T> where T: Copy + ShmLayout + Send {}

// SAFETY: the lock word and payload accept every bit pattern and need no
// drop. C representation aligns the payload; padding is never accessed as
// data, and the whole value has the payload's permitted alignment.
unsafe impl<T> ShmLayout for Mutex<T>
where
    T: Copy + ShmLayout,
{
    // The lock as the atomic word it is, and the payload as the type argument.
    //
    // The payload's offset and the mutex's size follow the payload's
    // alignment, so each instance describes its own; a Go view places the
    // payload by the alignment of the argument's view.
    const LAYOUT: &'static Node = &Node::of::<Self>(Kind::Edge {
        kind: "mutex",
        members: &[
            Field {
                name: "lock",
                offset: offset_of!(Self, lock),
                ty: AtomicU32::LAYOUT,
            },
            Field {
                name: "value",
                offset: offset_of!(Self, value),
                ty: <Param<0>>::LAYOUT,
            },
        ],
        args: &[T::LAYOUT],
    });
}

/// An exclusive borrow that releases the mutex when dropped.
///
/// A reference to the payload cannot outlive its guard:
///
/// ```compile_fail,E0515
/// use yanet_shm::mutex::Mutex;
/// fn escape(mutex: &Mutex<u64>) -> &u64 {
///     &*mutex.lock()
/// }
/// ```
///
/// Mutating through a shared guard is refused:
///
/// ```compile_fail,E0596
/// use yanet_shm::mutex::MutexGuard;
/// fn write(guard: &MutexGuard<'_, u64>) {
///     **guard = 1;
/// }
/// ```
///
/// Sharing a guard requires a shareable payload, even when the mutex itself
/// can be shared:
///
/// ```compile_fail,E0277
/// use core::{cell::Cell, marker::PhantomData};
/// use yanet_shm::{mutex::{Mutex, MutexGuard}, shm::ShmLayout};
/// #[derive(Clone, Copy)]
/// #[repr(C)]
/// struct Local(u32, PhantomData<Cell<()>>);
/// // SAFETY: a word and a zero-sized marker, without padding or drop.
/// unsafe impl ShmLayout for Local {}
/// fn shared<T: Sync>() {}
/// shared::<Mutex<Local>>();
/// shared::<MutexGuard<'_, Local>>();
/// ```
#[derive(Debug)]
#[must_use = "dropping the guard releases the mutex at once"]
pub struct MutexGuard<'a, T>
where
    T: Copy + ShmLayout,
{
    // The mutable reference also bounds sharing of the guard by whether the
    // payload may be shared, independently of whether its mutex may be shared.
    value: &'a mut T,
    _lock: SpinlockGuard<'a>,
}

impl<T> Deref for MutexGuard<'_, T>
where
    T: Copy + ShmLayout,
{
    type Target = T;

    #[inline]
    fn deref(&self) -> &T {
        self.value
    }
}

impl<T> DerefMut for MutexGuard<'_, T>
where
    T: Copy + ShmLayout,
{
    #[inline]
    fn deref_mut(&mut self) -> &mut T {
        self.value
    }
}

#[cfg(test)]
mod tests {
    use core::{assert_matches, cell::Cell, marker::PhantomData, mem::offset_of};

    use super::{Mutex, MutexGuard};
    use crate::{layout::Kind, shm::ShmLayout};

    #[derive(Clone, Copy)]
    #[repr(C, align(64))]
    struct Wide([u8; 64]);

    // SAFETY: the bytes fill the alignment, with no padding or drop.
    unsafe impl ShmLayout for Wide {}

    #[derive(Clone, Copy)]
    #[repr(C)]
    struct Local(u32, PhantomData<Cell<()>>);

    // SAFETY: a word and a zero-sized marker, without padding or drop.
    unsafe impl ShmLayout for Local {}

    /// Checks that the payload follows the lock word at the larger of 4 and
    /// its alignment, and that the mutex takes that alignment.
    fn check_layout<T: Copy + ShmLayout>() {
        let align = align_of::<T>().max(4);
        assert_eq!(align, offset_of!(Mutex<T>, value));
        assert_eq!((align + size_of::<T>()).next_multiple_of(align), size_of::<Mutex<T>>());
        assert_eq!(align, align_of::<Mutex<T>>());
        assert!(!core::mem::needs_drop::<Mutex<T>>());
    }

    #[test]
    fn test_mutex_layout_places_small_empty_and_aligned_payloads_after_the_lock_word() {
        check_layout::<()>();
        check_layout::<u8>();
        check_layout::<[u8; 3]>();
        check_layout::<u16>();
        check_layout::<u32>();
        check_layout::<u64>();
        check_layout::<Wide>();
    }

    /// The offset of the payload member and the type argument a mutex's
    /// layout description gives.
    fn described<T: Copy + ShmLayout>() -> (usize, usize) {
        let Kind::Edge { kind, members, args } = &<Mutex<T>>::LAYOUT.kind else {
            panic!("a mutex is a field type");
        };
        assert_eq!("mutex", *kind);
        let [lock, value] = members else {
            panic!("a mutex has two members");
        };
        assert_eq!(("lock", 0, 4), (lock.name, lock.offset, lock.ty.size));
        assert_matches!(lock.ty.kind, Kind::Int { signed: false, atomic: true });
        assert_eq!("value", value.name);
        assert_matches!(value.ty.kind, Kind::Param { index: 0 });
        let [arg] = args else {
            panic!("a mutex has one type argument");
        };
        (value.offset, arg.size)
    }

    #[test]
    fn test_mutex_layout_describes_the_lock_word_and_the_payload_offset() {
        assert_eq!((4, 1), described::<u8>());
        assert_eq!((4, 4), described::<u32>());
        assert_eq!((8, 8), described::<u64>());
        assert_eq!((64, 64), described::<Wide>());
    }

    #[test]
    fn test_mutex_shares_send_only_payloads_and_guards_follow_payload_traits() {
        fn send<T: Send>() {}
        fn share<T: Sync>() {}

        send::<Mutex<Local>>();
        share::<Mutex<Local>>();
        send::<MutexGuard<'_, Local>>();
        share::<MutexGuard<'_, u32>>();
    }
}
