#pragma once

#include <sched.h>
#include <stdatomic.h>
#include <stdbool.h>
#include <unistd.h>

#include <sys/syscall.h>

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

struct recursive_spinlock {
	atomic_int owner;
	uint32_t recursion;
};

static inline void
recursive_spinlock_init(struct recursive_spinlock *lock) {
	atomic_init(&lock->owner, 0);
	lock->recursion = 0;
}

static inline int
tid() {
	return syscall(SYS_gettid);
}

static inline int
recursive_spinlock_try_lock(struct recursive_spinlock *lock) {
	int me = tid();
	if (atomic_load_explicit(&lock->owner, memory_order_relaxed) == me) {
		lock->recursion++;
		return 1;
	}

	int expected = 0;
	if (atomic_compare_exchange_weak_explicit(
		    &lock->owner,
		    &expected,
		    me,
		    memory_order_acquire,
		    memory_order_relaxed
	    )) {
		lock->recursion = 1;
		return 1;
	}

	return 0;
}

static inline void
recursive_spinlock_lock(struct recursive_spinlock *lock) {
	int me = tid();
	if (atomic_load_explicit(&lock->owner, memory_order_relaxed) == me) {
		++lock->recursion;
		return;
	}

	for (;;) {
		int expected = 0;
		if (atomic_compare_exchange_weak_explicit(
			    &lock->owner,
			    &expected,
			    me,
			    memory_order_acquire,
			    memory_order_relaxed
		    )) {
			lock->recursion = 1;
			return;
		}
		spinlock_cpu_relax();
	}
}

static inline void
recursive_spinlock_unlock(struct recursive_spinlock *lock) {
	if (--lock->recursion == 0) {
		atomic_store_explicit(&lock->owner, 0, memory_order_release);
	}
}