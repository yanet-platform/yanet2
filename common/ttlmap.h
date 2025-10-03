#pragma once

#include <stddef.h>

#include "detail/ttlmap/ttlmap.h"
#include "detail/ttlmap/bucket.h"
#include "detail/ttlmap/lock.h"

////////////////////////////////////////////////////////////////////////////////

typedef struct ttlmap ttlmap_t;
typedef struct ttlmap_lock ttlmap_lock_t;

////////////////////////////////////////////////////////////////////////////////

#define TTLMAP_INIT(map_ptr, mctx_ptr, key_type, value_type, kv_entries) \
    __TTLMAP_INIT_INTERNAL(map_ptr, mctx_ptr, key_type, value_type, kv_entries)

#define TTLMAP_FREE(map_ptr) \
    __TTLMAP_FREE_INTERNAL(map_ptr)

#define TTLMAP_GET(map_ptr, key_ptr, value_ptr_ptr, lock_ptr_ptr, now /* uint32_t */, timeout /* uint32_t */) \
    __TTLMAP_GET_INTERNAL(map_ptr, key_ptr, value_ptr_ptr, lock_ptr_ptr, now, timeout)

#define TTLMAP_LOOKUP(map_ptr, key_ptr, value_ptr, now) \
    __TTLMAP_LOOKUP_INTERNAL(map_ptr, key_ptr, value_ptr, now)

#define TTLMAP_REMOVE(key_type, value_ptr) \
    __TTLMAP_INVALIDATE_INTERNAL(key_type, value_ptr)

#define TTLMAP_PRINT_STAT(map_ptr, key_type, value_type, fd) \
    __TTLMAP_PRINT_STAT_INTERNAL(map_ptr, key_type, value_type, fd)

////////////////////////////////////////////////////////////////////////////////

static inline void
ttlmap_release_lock(ttlmap_lock_t *lock) {
    __ttlmap_unlock(lock);
}

////////////////////////////////////////////////////////////////////////////////

