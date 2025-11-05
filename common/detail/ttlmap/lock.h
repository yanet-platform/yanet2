#pragma once

#include <stdatomic.h>

////////////////////////////////////////////////////////////////////////////////

typedef struct ttlmap_lock {
	atomic_flag flag;
} ttlmap_lock_t;

static inline void
__ttlmap_lock_init(ttlmap_lock_t *lock) { // NOLINT
	atomic_flag_clear(&lock->flag);
}

static inline void
__ttlmap_lock(ttlmap_lock_t *lock) { // NOLINT
	while (atomic_flag_test_and_set(&lock->flag)) {
		;
	}
}

static inline void
__ttlmap_unlock(ttlmap_lock_t *lock) { // NOLINT
	atomic_flag_clear(&lock->flag);
}