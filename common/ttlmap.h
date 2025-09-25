#pragma once

#include <stddef.h>

#include "detail/ttlmap/ttlmap.h"

////////////////////////////////////////////////////////////////////////////////

typedef struct ttlmap ttlmap_t;

////////////////////////////////////////////////////////////////////////////////

#define TTLMAP_INIT(map_ptr, mctx_ptr, key_type, value_type, kv_entries) \
    __TTLMAP_INIT_INTERNAL(map_ptr, mctx_ptr, key_type, value_type, kv_entries)

#define TTLMAP_INSERT(map_ptr, key_ptr, value_ptr, now, timeout) \
__extension__({ \
    void *__bucket = __TTLMAP_BUCKET_GET((map_ptr), (key_ptr)); \
    __TTLMAP_BUCKET_INSERT(__bucket, (key_ptr), (value_ptr), (now), (timeout)); \
})

#define TTLMAP_LOOKUP(map_ptr, key_ptr, value_ptr_ptr, lock_ptr_ptr, now) \
__extension__({ \
    void *__bucket = __TTLMAP_BUCKET_GET((map_ptr), (key_ptr)); \
    __TTLMAP_BUCKET_LOOKUP(__bucket, (key_ptr), (value_ptr_ptr), (lock_ptr_ptr), (now)); \
})