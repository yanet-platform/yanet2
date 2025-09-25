#pragma once

#include <rte_spinlock.h>

////////////////////////////////////////////////////////////////////////////////

typedef struct ttlmap_lock {
    rte_spinlock_t lock;
} ttlmap_lock_t;

static inline void
__ttlmap_lock_init(ttlmap_lock_t *lock) { // NOLINT
    rte_spinlock_init(&lock->lock);
}

static inline void 
__ttlmap_lock(ttlmap_lock_t *lock) { // NOLINT
    rte_spinlock_lock(&lock->lock);
}

static inline void 
__ttlmap_unlock(ttlmap_lock_t *lock) { // NOLINT
    rte_spinlock_unlock(&lock->lock);
}