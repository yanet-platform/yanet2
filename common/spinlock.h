#pragma once

#include <sched.h>
#include <stdatomic.h>
#include <stdbool.h>

struct spinlock {
	volatile int locked;
};

/* Initialize the spinlock to unlocked state */
static inline void
spinlock_init(struct spinlock *lock) {
	lock->locked = 0;
}

static inline void
spinlock_cpu_relax(void) {
#if defined(__x86_64__) || defined(__i386__)
	__asm__ __volatile__("pause");
#endif
}

/* Acquire the lock (blocking) */
static inline void
spinlock_lock(struct spinlock *sl) {
	int lock_val = 1;
	asm volatile("1:\n"
		     "xchg %[locked], %[lv]\n"
		     "test %[lv], %[lv]\n"
		     "jz 3f\n"
		     "2:\n"
		     "pause\n"
		     "cmpl $0, %[locked]\n"
		     "jnz 2b\n"
		     "jmp 1b\n"
		     "3:\n"
		     : [locked] "=m"(sl->locked), [lv] "=q"(lock_val)
		     : "[lv]"(lock_val)
		     : "memory");
}

/* Release the lock */
static inline void
spinlock_unlock(struct spinlock *sl) {
	int unlock_val = 0;
	asm volatile("xchg %[locked], %[ulv]\n"
		     : [locked] "=m"(sl->locked), [ulv] "=q"(unlock_val)
		     : "[ulv]"(unlock_val)
		     : "memory");
}
