//! The mutex's exclusion and release, under Miri as natively.
//!
//! Under Miri a missing happens-before edge between two holders is a reported
//! data race, so these cases also check the memory ordering of the lock.

#![forbid(unsafe_code)]

use core::{
    assert_matches, hint,
    sync::atomic::{AtomicU32, Ordering},
};
use std::{panic, thread};

use yanet_shm::mutex::Mutex;

/// verifies that holders on several threads never overlap.
///
/// The count is read and written without atomics, so overlapping holders lose
/// increments, and under Miri any missing happens-before edge between them is
/// a reported data race.
#[test]
fn test_lock_excludes_concurrent_holders() {
    const THREADS: u32 = 4;
    const ROUNDS: u32 = if cfg!(miri) { 50 } else { 100_000 };

    let guarded = Mutex::new(0_u32);
    thread::scope(|scope| {
        let guarded = &guarded;
        for _ in 0..THREADS {
            scope.spawn(move || {
                for _ in 0..ROUNDS {
                    *guarded.lock() += 1;
                }
            });
        }
    });

    assert_eq!(THREADS * ROUNDS, *guarded.lock());
}

#[test]
fn test_lock_released_on_panic() {
    let lock = Mutex::new(0_u32);

    let result = panic::catch_unwind(panic::AssertUnwindSafe(|| {
        let _guard = lock.lock();
        panic!("panic while the lock is held");
    }));

    assert_matches!(result, Err(_));
    assert_matches!(lock.try_lock(), Some(_));
}

#[test]
fn test_try_lock_refuses_a_held_lock_and_leaves_it_held() {
    let lock = Mutex::new(0_u32);
    let guard = lock.lock();

    let refused = (lock.try_lock().is_none(), lock.try_lock().is_none());
    drop(guard);

    assert_eq!((true, true), refused);
    assert_matches!(lock.try_lock(), Some(_));
}

/// verifies that a successful nonblocking acquisition observes the previous
/// holder's writes, ordered by the mutex alone.
#[test]
fn test_try_lock_observes_the_previous_holders_writes() {
    let state = Mutex::new(0_u32);
    let waiting = AtomicU32::new(0);

    let seen = thread::scope(|scope| {
        let mut guard = state.lock();
        let waiter = scope.spawn(|| {
            loop {
                if let Some(guard) = state.try_lock() {
                    break *guard;
                }
                waiting.store(1, Ordering::Relaxed);
                thread::yield_now();
            }
        });
        while waiting.load(Ordering::Relaxed) == 0 {
            hint::spin_loop();
        }
        *guard = 7;
        drop(guard);
        waiter.join().expect("the waiter acquires the released mutex")
    });

    assert_eq!(7, seen);
}

#[test]
fn test_guard_can_be_used_and_released_on_another_thread() {
    let state = Mutex::new(7_u32);
    let mut guard = state.lock();

    thread::scope(|scope| {
        scope.spawn(move || *guard += 1).join().expect("the holder finishes");
    });

    assert_eq!(8, *state.try_lock().expect("released on the other thread"));
}

#[test]
fn test_lock_with_calls_the_hook_while_held() {
    let lock = Mutex::new(0_u32);
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

#[test]
fn test_lock_with_panicking_wait_hook_leaves_the_current_holder_locked() {
    let state = Mutex::new(7_u32);
    let guard = state.lock();

    let result = panic::catch_unwind(panic::AssertUnwindSafe(|| {
        drop(state.lock_with(|| panic!("stop waiting")));
    }));

    assert_matches!(result, Err(_));
    assert_matches!(state.try_lock(), None);
    assert_eq!(7, *guard);
    drop(guard);
    assert_matches!(state.try_lock(), Some(_));
}

/// verifies that a waiter that takes the lock after spinning sees what the
/// holder wrote before releasing it.
///
/// The hook runs only while spinning, so once it has run, the holder's write
/// and the waiter's read are ordered by the lock alone; under Miri a missing
/// acquire on that path is a reported data race, whatever the schedule.
#[test]
fn test_lock_with_contended_sees_the_holders_writes() {
    let guarded = Mutex::new(0_u32);
    let waits = AtomicU32::new(0);

    let seen = thread::scope(|scope| {
        let guarded = &guarded;
        let waits = &waits;
        let mut guard = guarded.lock();
        let waiter = scope.spawn(move || {
            let guard = guarded.lock_with(|| {
                waits.fetch_add(1, Ordering::Relaxed);
                thread::yield_now();
            });
            *guard
        });
        while waits.load(Ordering::Relaxed) == 0 {
            hint::spin_loop();
        }
        *guard = 7;
        drop(guard);
        waiter.join().expect("the waiter takes the released lock")
    });

    assert_eq!(7, seen);
}

/// A value whose drop panics, carried by a wait hook.
struct PanicsOnDrop;

impl Drop for PanicsOnDrop {
    fn drop(&mut self) {
        panic!("the wait hook's capture panics on drop");
    }
}

/// verifies that a wait hook whose drop panics after the lock is taken
/// leaves the lock free.
#[test]
fn test_lock_with_released_when_the_hook_panics_on_drop() {
    let lock = Mutex::new(0_u32);
    let capture = PanicsOnDrop;

    let result = panic::catch_unwind(panic::AssertUnwindSafe(|| {
        let _guard = lock.lock_with(move || {
            let _ = &capture;
        });
    }));

    assert_matches!(result, Err(_));
    assert_matches!(lock.try_lock(), Some(_));
}
