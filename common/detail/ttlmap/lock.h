#pragma once

#include "generic/rte_spinlock.h"
#include <rte_spinlock.h>

////////////////////////////////////////////////////////////////////////////////

typedef struct ttmap_lock {
    rte_spinlock_t lock;
} ttlmap_lock_t;

static inline void
ttlmap_lock_init(ttlmap_lock_t *lock) {
    rte_spinlock_init(&lock->lock);
}

static inline void 
ttlmap_lock(ttlmap_lock_t *lock) {
    rte_spinlock_lock(&lock->lock);
}

static inline void 
ttlmap_unlock(ttlmap_lock_t *lock) {
    rte_spinlock_unlock(&lock->lock);
}