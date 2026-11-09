//! The private lock byte of a shared-memory mutex.

#![forbid(unsafe_code)]

use core::{
    hint,
    sync::atomic::{AtomicU8, Ordering},
};

/// How many times a waiter spins between calls of its wait hook.
const SPINS_PER_WAIT: u32 = 1024;

/// A test-and-test-and-set spinlock over one shared-memory byte.
///
/// Zero is free and any other value held, so every bit pattern is valid.
/// Byte alignment lets the enclosing mutex place any payload immediately
/// after its explicit header without implicit padding.
#[repr(C)]
#[derive(Debug, Default)]
pub struct RawSpinlock {
    word: AtomicU8,
}

impl RawSpinlock {
    /// A free lock.
    pub const fn new() -> Self {
        Self { word: AtomicU8::new(0) }
    }

    /// Spins until the lock is free, takes it and returns the guard that
    /// releases it.
    ///
    /// Taking the lock acquires everything the previous holder wrote before
    /// releasing it.
    #[inline]
    pub fn lock(&self) -> RawSpinlockGuard<'_> {
        self.lock_with(|| {})
    }

    /// Takes the lock if it is free, without waiting.
    #[inline]
    pub fn try_lock(&self) -> Option<RawSpinlockGuard<'_>> {
        // A held lock is refused on a plain load, which leaves the cache line
        // shared with the waiters.
        if self.word.load(Ordering::Relaxed) == 0 && self.word.swap(1, Ordering::Acquire) == 0 {
            Some(RawSpinlockGuard { lock: self })
        } else {
            None
        }
    }

    /// Takes the lock as [`Self::lock`] does, calling the hook between rounds
    /// of spinning while the lock is held by someone else.
    ///
    /// A caller that may share its CPU with the holder passes a hook that
    /// gives the CPU up, such as a thread yield; a waiter pinned to a core of
    /// its own needs none.
    #[inline]
    pub fn lock_with<F>(&self, mut wait: F) -> RawSpinlockGuard<'_>
    where
        F: FnMut(),
    {
        if self.word.swap(1, Ordering::Acquire) != 0 {
            self.lock_contended(&mut wait);
        }
        let guard = RawSpinlockGuard { lock: self };
        // The hook is dropped while the guard lives, so a drop that panics
        // releases the lock as it unwinds.
        drop(wait);
        guard
    }

    #[cold]
    fn lock_contended<F>(&self, wait: &mut F)
    where
        F: FnMut(),
    {
        let mut spins = 0;
        loop {
            // Waiting on plain loads keeps the cache line shared among the
            // waiters until the holder writes it.
            while self.word.load(Ordering::Relaxed) != 0 {
                hint::spin_loop();
                spins += 1;
                if spins == SPINS_PER_WAIT {
                    wait();
                    spins = 0;
                }
            }
            if self.word.swap(1, Ordering::Acquire) == 0 {
                return;
            }
        }
    }
}

/// Holds a [`RawSpinlock`] and releases it when dropped.
#[derive(Debug)]
#[must_use = "dropping the guard releases the lock at once"]
pub struct RawSpinlockGuard<'a> {
    lock: &'a RawSpinlock,
}

impl Drop for RawSpinlockGuard<'_> {
    #[inline]
    fn drop(&mut self) {
        // Publishes the holder's writes to whoever takes the lock next.
        self.lock.word.store(0, Ordering::Release);
    }
}

const _: () = {
    assert!(size_of::<RawSpinlock>() == 1);
    assert!(align_of::<RawSpinlock>() == 1);
};
