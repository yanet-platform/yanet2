#pragma once

// #include "lock.h"
// #include "ttlmap.h"

// #include <common/memory.h>

////////////////////////////////////////////////////////////////////////////////

// typedef struct dynamic_ttl_map {
//     ttlmap_t *current;
//     ttlmap_t *old;
//     struct memory_context mctx;
// } dynamic_ttlmap_t;

////////////////////////////////////////////////////////////////////////////////

#define __DYNAMIC_TTLMAP_INIT_INTERNAL(map_ptr, mctx_ptr, key_type, value_type, kv_entries) \
__extension__({ \
    __label__ __done; \
    __label__ __free; \
    int ret = 0; \
    (map_ptr)->max_old_deadline = 0; \
    (map_ptr)->old = NULL; \
    memory_context_init_from(&(map_ptr)->mctx, mctx_ptr, "dynamic_ttlmap"); \
    ttlmap_t *current = memory_balloc(&(map_ptr)->mctx, sizeof(ttlmap_t)); \
    if (current == NULL) { \
        ret = -1;   \
        goto __done;    \
    } \
    ret = __TTLMAP_INIT_INTERNAL(current, &(map_ptr)->mctx, key_type, value_type, kv_entries); \
    if (ret != 0) { \
        ret = -1; \
        goto __free; \
    } \
    SET_OFFSET_OF(&(map_ptr)->current, current); \
    goto __done; \
__free: \
    memory_bfree(mctx_ptr, current, sizeof(ttlmap_t)); \
__done: \
    ret; \
})

////////////////////////////////////////////////////////////////////////////////

#define __DYNAMIC_TTLMAP_GET_INTERNAL(map_ptr, key_ptr, value_ptr_ptr, lock_ptr_ptr, now, timeout) \
__extension__({ \
    int ret = TTLMAP_FAILED; \
    __label__ __done; \
    ttlmap_t *current = ADDR_OF(&(map_ptr)->current); \
    ret = __TTLMAP_GET_INTERNAL(current, key_ptr, value_ptr_ptr, lock_ptr_ptr, now, timeout); \
    if (ret == TTLMAP_FOUND || ret == TTLMAP_FAILED) { \
        goto __done; \
    } else if ((map_ptr)->old != NULL && (map_ptr)->max_old_deadline > (now)) { \
        ttlmap_t *old = ADDR_OF(&(map_ptr)->old); \
        if (__TTLMAP_LOOKUP_INTERNAL(old, key_ptr, *(value_ptr_ptr), now) == TTLMAP_FOUND) { \
            ret = TTLMAP_FOUND; \
        } \
    }\
__done: \
    ret; \
})