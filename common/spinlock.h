#pragma once

#include <sched.h>
#include <stdatomic.h>
#include <stdbool.h>

struct spinlock {
	atomic_bool locked;
};

/* Initialize the spinlock to unlocked state */
static inline void
spinlock_init(struct spinlock *lock) {
	atomic_init(&lock->locked, false);
}

static inline void
spinlock_cpu_relax(void) {
#if defined(__x86_64__) || defined(__i386__)
	__asm__ __volatile__("pause");
#endif
}

/* Acquire the lock (blocking) */
static inline void
spinlock_lock(struct spinlock *lock) {
	/* Fast path: try to acquire immediately */
	bool expected = false;
	if (atomic_compare_exchange_strong_explicit(
		    &lock->locked,
		    &expected,
		    true,
		    memory_order_acquire,
		    memory_order_relaxed
	    )) {
		return;
	}

	/* Slow path: spin with backoff */
	int spins = 0;
	for (;;) {
		/* Try to acquire */
		expected = false;
		if (atomic_compare_exchange_weak_explicit(
			    &lock->locked,
			    &expected,
			    true,
			    memory_order_acquire,
			    memory_order_relaxed
		    )) {
			return;
		}

		/* Busy wait while the lock is observed as held */
		while (atomic_load_explicit(&lock->locked, memory_order_relaxed)
		) {
			spinlock_cpu_relax();
			if (++spins >= 1024) {
				/* Be nice to the scheduler under high
				 * contention */
				sched_yield();
				spins = 0;
			}
		}
	}
}

/* Release the lock */
static inline void
spinlock_unlock(struct spinlock *lock) {
	atomic_store_explicit(&lock->locked, false, memory_order_release);
}