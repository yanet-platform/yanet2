#pragma once

#include "cpu_pause.h"

#include <sched.h>
#include <stdatomic.h>
#include <stdint.h>

// A test-and-test-and-set spinlock, held while its word is one.
//
// The lock fills a whole aligned 32-bit word, so a process that shares
// the memory without this header takes it with a plain word-sized
// compare-and-swap.
struct spinlock {
	_Atomic uint32_t locked;
};

/* Initialize the spinlock to unlocked state */
static inline void
spinlock_init(struct spinlock *lock) {
	atomic_init(&lock->locked, 0);
}

/* Acquire the lock (blocking) */
static inline void
spinlock_lock(struct spinlock *lock) {
	uint32_t expected = 0;
	if (atomic_compare_exchange_strong_explicit(
		    &lock->locked,
		    &expected,
		    1,
		    memory_order_acquire,
		    memory_order_relaxed
	    )) {
		return;
	}

	int spins = 0;
	for (;;) {
		expected = 0;
		if (atomic_compare_exchange_weak_explicit(
			    &lock->locked,
			    &expected,
			    1,
			    memory_order_acquire,
			    memory_order_relaxed
		    )) {
			return;
		}

		while (atomic_load_explicit(&lock->locked, memory_order_relaxed)
		) {
			cpu_pause();
			if (++spins >= 1024) {
				sched_yield();
				spins = 0;
			}
		}
	}
}

/* Release the lock */
static inline void
spinlock_unlock(struct spinlock *lock) {
	atomic_store_explicit(&lock->locked, 0, memory_order_release);
}
