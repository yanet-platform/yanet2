#pragma once

#include "common/spinlock.h"
#include <stdatomic.h>

////////////////////////////////////////////////////////////////////////////////

typedef struct spinlock ttlmap_lock_t;

static inline void
__ttlmap_lock_init(ttlmap_lock_t *lock) { // NOLINT
	spinlock_init(lock);
}

static inline void
__ttlmap_lock(ttlmap_lock_t *lock) { // NOLINT
	spinlock_lock(lock);
}

static inline void
__ttlmap_unlock(ttlmap_lock_t *lock) { // NOLINT
	spinlock_unlock(lock);
}