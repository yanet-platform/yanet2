//! Shared-memory state accessed only while its spinlock is held.

use core::{
    cell::UnsafeCell,
    ops::{Deref, DerefMut},
};

use crate::{
    shm::{MAX_ALIGN, ShmLayout},
    spinlock::{RawSpinlock, RawSpinlockGuard},
};

/// A spin mutex protecting a plain shared-memory value.
///
/// Every access to the value takes a guard. Acquiring the lock observes the
/// previous holder's writes; dropping the guard publishes this holder's
/// writes. An unwinding panic releases the lock without poisoning it. A
/// holder that never returns, such as a killed process or a forgotten guard,
/// leaves it locked. The layout is independent of the C spinlock's layout.
/// Only copyable layout values are accepted, so the guard cannot move an
/// owning edge or an address-relative pointer out of its storage.
/// Storage is a 64-byte initialised header followed by the value, with the
/// value's alignment and no implicit padding.
///
/// ```
/// #![forbid(unsafe_code)]
/// use yanet_shm::mutex::SpinMutex;
/// let state = SpinMutex::new([0_u64; 2]);
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
/// use yanet_shm::{mutex::SpinMutex, rel::ShmBox};
/// fn move_edge(_: SpinMutex<ShmBox<u64>>) {}
/// ```
#[repr(C)]
pub struct SpinMutex<T>
where
    T: Copy + ShmLayout,
{
    lock: RawSpinlock,
    // A 64-byte header aligns every permitted payload without implicit
    // padding; byte-aligned fields also avoid tail padding for small values.
    padding: [u8; MAX_ALIGN - 1],
    value: UnsafeCell<T>,
}

impl<T> SpinMutex<T>
where
    T: Copy + ShmLayout,
{
    /// An unlocked mutex containing the initial value.
    pub const fn new(value: T) -> Self {
        Self {
            lock: RawSpinlock::new(),
            padding: [0; MAX_ALIGN - 1],
            value: UnsafeCell::new(value),
        }
    }

    /// Spins until the value can be borrowed exclusively.
    #[inline]
    pub fn lock(&self) -> SpinMutexGuard<'_, T> {
        self.guard(self.lock.lock())
    }

    /// Borrows the value exclusively if the mutex is free, without waiting.
    #[inline]
    pub fn try_lock(&self) -> Option<SpinMutexGuard<'_, T>> {
        self.lock.try_lock().map(|lock| self.guard(lock))
    }

    /// Borrows the value exclusively, calling the hook between rounds of
    /// spinning while another holder owns it.
    ///
    /// A caller sharing its CPU with a holder may yield from the hook. A
    /// hook that panics, including when dropped, never strands a lock this
    /// call acquired.
    #[inline]
    pub fn lock_with<F>(&self, wait: F) -> SpinMutexGuard<'_, T>
    where
        F: FnMut(),
    {
        self.guard(self.lock.lock_with(wait))
    }

    /// Borrows the payload while the guard of this mutex keeps it exclusive.
    #[inline]
    fn guard<'a>(&'a self, lock: RawSpinlockGuard<'a>) -> SpinMutexGuard<'a, T> {
        SpinMutexGuard {
            // SAFETY: every caller holds this mutex's lock, and the payload
            // is private. The exclusive borrow ends before the guard unlocks.
            value: unsafe { &mut *self.value.get() },
            _lock: lock,
        }
    }
}

// SAFETY: only a lock holder can reach the payload. The lock orders every
// holder's access, and the payload can be transferred between threads.
unsafe impl<T> Sync for SpinMutex<T> where T: Copy + ShmLayout + Send {}

// SAFETY: the header has only explicit bytes and is a multiple of every
// allowed payload alignment. The payload has no padding, invalid bit pattern
// or drop, so neither does the whole value; its alignment is the payload's.
unsafe impl<T> ShmLayout for SpinMutex<T> where T: Copy + ShmLayout {}

/// An exclusive borrow that releases the mutex when dropped.
///
/// A reference to the payload cannot outlive its guard:
///
/// ```compile_fail,E0515
/// use yanet_shm::mutex::SpinMutex;
/// fn escape(mutex: &SpinMutex<u64>) -> &u64 {
///     &*mutex.lock()
/// }
/// ```
///
/// Mutating through a shared guard is refused:
///
/// ```compile_fail,E0596
/// use yanet_shm::mutex::SpinMutexGuard;
/// fn write(guard: &SpinMutexGuard<'_, u64>) {
///     **guard = 1;
/// }
/// ```
///
/// Sharing a guard requires a shareable payload, even when the mutex itself
/// can be shared:
///
/// ```compile_fail,E0277
/// use core::{cell::Cell, marker::PhantomData};
/// use yanet_shm::{mutex::{SpinMutex, SpinMutexGuard}, shm::ShmLayout};
/// #[derive(Clone, Copy)]
/// #[repr(C)]
/// struct Local(u32, PhantomData<Cell<()>>);
/// // SAFETY: a word and a zero-sized marker, without padding or drop.
/// unsafe impl ShmLayout for Local {}
/// fn shared<T: Sync>() {}
/// shared::<SpinMutex<Local>>();
/// shared::<SpinMutexGuard<'_, Local>>();
/// ```
#[derive(Debug)]
#[must_use = "dropping the guard releases the mutex at once"]
pub struct SpinMutexGuard<'a, T>
where
    T: Copy + ShmLayout,
{
    // The mutable reference also bounds sharing of the guard by whether the
    // payload may be shared, independently of whether its mutex may be shared.
    value: &'a mut T,
    _lock: RawSpinlockGuard<'a>,
}

impl<T> Deref for SpinMutexGuard<'_, T>
where
    T: Copy + ShmLayout,
{
    type Target = T;

    #[inline]
    fn deref(&self) -> &T {
        self.value
    }
}

impl<T> DerefMut for SpinMutexGuard<'_, T>
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
    use core::{cell::Cell, marker::PhantomData};

    use super::{SpinMutex, SpinMutexGuard};
    use crate::shm::ShmLayout;

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

    /// Checks the layout promise for every byte, including the payload's
    /// start and the end of the containing value.
    fn check_layout<T: Copy + ShmLayout>() {
        assert_eq!(64, core::mem::offset_of!(SpinMutex<T>, value));
        assert_eq!(64 + size_of::<T>(), size_of::<SpinMutex<T>>());
        assert_eq!(align_of::<T>(), align_of::<SpinMutex<T>>());
        assert!(!core::mem::needs_drop::<SpinMutex<T>>());
    }

    #[test]
    fn test_mutex_layout_has_no_padding_for_small_empty_and_aligned_payloads() {
        check_layout::<()>();
        check_layout::<u8>();
        check_layout::<[u8; 3]>();
        check_layout::<u16>();
        check_layout::<u32>();
        check_layout::<u64>();
        check_layout::<Wide>();
    }

    #[test]
    fn test_mutex_shares_send_only_payloads_and_guards_follow_payload_traits() {
        fn send<T: Send>() {}
        fn share<T: Sync>() {}

        send::<SpinMutex<Local>>();
        share::<SpinMutex<Local>>();
        send::<SpinMutexGuard<'_, Local>>();
        share::<SpinMutexGuard<'_, u32>>();
    }
}
