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

	int spins = 0;
	for (;;) {
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

		while (atomic_load_explicit(&lock->locked, memory_order_relaxed)
		) {
			spinlock_cpu_relax();
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
	atomic_store_explicit(&lock->locked, false, memory_order_release);
}