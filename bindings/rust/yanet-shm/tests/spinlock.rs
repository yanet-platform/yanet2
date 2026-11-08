//! The spinlock's exclusion and release, under Miri as natively.
//!
//! Under Miri a missing happens-before edge between two holders is a reported
//! data race, so these cases also check the memory ordering of the lock.

use core::{
    assert_matches,
    cell::UnsafeCell,
    hint,
    sync::atomic::{AtomicU32, Ordering},
};
use std::{panic, thread};

use yanet_sdk_testing as _;
use yanet_shm::spinlock::RawSpinlock;

/// Data kept beside the lock that guards it, as a shared-memory struct keeps
/// it.
struct Guarded {
    lock: RawSpinlock,
    count: UnsafeCell<u32>,
}

// SAFETY: the count is reached only while the lock is held.
unsafe impl Sync for Guarded {}

/// verifies that holders on several threads never overlap.
///
/// The count is read and written without atomics, so overlapping holders lose
/// increments, and under Miri any missing happens-before edge between them is
/// a reported data race.
#[test]
fn test_lock_excludes_concurrent_holders() {
    const THREADS: u32 = 4;
    const ROUNDS: u32 = if cfg!(miri) { 50 } else { 100_000 };

    let guarded = Guarded {
        lock: RawSpinlock::new(),
        count: UnsafeCell::new(0),
    };
    thread::scope(|scope| {
        // Shares the whole struct: a closure would otherwise capture the
        // count alone, which is not shareable by itself.
        let guarded = &guarded;
        for _ in 0..THREADS {
            scope.spawn(move || {
                for _ in 0..ROUNDS {
                    let _guard = guarded.lock.lock();
                    // SAFETY: the guard above makes this the only holder.
                    unsafe { *guarded.count.get() += 1 };
                }
            });
        }
    });

    assert_eq!(THREADS * ROUNDS, guarded.count.into_inner());
}

#[test]
fn test_lock_released_on_panic() {
    let lock = RawSpinlock::new();

    let result = panic::catch_unwind(|| {
        let _guard = lock.lock();
        panic!("panic while the lock is held");
    });

    assert_matches!(result, Err(_));
    assert_matches!(lock.try_lock(), Some(_));
}

#[test]
fn test_try_lock_refuses_a_held_lock_and_leaves_it_held() {
    let lock = RawSpinlock::new();
    let guard = lock.lock();

    let refused = (lock.try_lock().is_none(), lock.try_lock().is_none());
    drop(guard);

    assert_eq!((true, true), refused);
    assert_matches!(lock.try_lock(), Some(_));
}

#[test]
fn test_lock_with_calls_the_hook_while_held() {
    let lock = RawSpinlock::new();
    let waits = AtomicU32::new(0);

    thread::scope(|scope| {
        let guard = lock.lock();
        let waiter = scope.spawn(|| {
            drop(lock.lock_with(|| {
                waits.fetch_add(1, Ordering::Relaxed);
                thread::yield_now();
            }));
        });
        while waits.load(Ordering::Relaxed) == 0 {
            hint::spin_loop();
        }
        drop(guard);
        waiter.join().expect("the waiter takes the released lock");
    });
}
