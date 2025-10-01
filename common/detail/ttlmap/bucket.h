#pragma once

#include "key_value.h"

#include <rte_common.h>

////////////////////////////////////////////////////////////////////////////////

#define __TTLMAP_BUCKET_ENTRIES_EXP 4
#define __TTLMAP_BUCKET_ENTRIES (1 << __TTLMAP_BUCKET_ENTRIES_EXP)

#define __TTLMAP_BUCKET_DECLARE(key_type, value_type) \
typedef struct __bucket { \
    key_type keys[__TTLMAP_BUCKET_ENTRIES]; \
    value_type values[__TTLMAP_BUCKET_ENTRIES]; \
    uint32_t deadline[__TTLMAP_BUCKET_ENTRIES]; \
    ttlmap_lock_t lock; \
} __rte_cache_aligned __bucket_t

#define __TTLMAP_BUCKET_INIT(bucket_ptr, key_type, value_type) \
__extension__({ \
    __TTLMAP_BUCKET_DECLARE(key_type, value_type); \
    __bucket_t *__bucket = (__bucket_t *)bucket_ptr; \
    memset(__bucket->deadline, 0, sizeof(__bucket->deadline)); \
    __ttlmap_lock_init(&__bucket->lock); \
})

#define TTLMAP_FOUND 1
#define TTLMAP_INSERTED 0
#define TTLMAP_FAILED -1

// If value is found, returns 1.
// If value is not found, returns 0 and tries to insert. On insert success, sets `value_ptr_ptr`
// to the value pointer.
#define __TTLMAP_BUCKET_GET(bucket_ptr, key_ptr, value_ptr_ptr, lock_ptr_ptr, now, timeout, idx) \
__extension__({ \
    __label__ __done; \
    int __ret = TTLMAP_FAILED; \
    typedef typeof(*(key_ptr)) __key_type; \
    typedef typeof(**(value_ptr_ptr)) __value_type; \
    __TTLMAP_BUCKET_DECLARE(__key_type, __value_type); \
    __bucket_t *__bucket = (__bucket_t *)(bucket_ptr); \
    *(lock_ptr_ptr) = &__bucket->lock; \
    __ttlmap_lock(&__bucket->lock); \
    for (size_t __i = 0; __i < __TTLMAP_BUCKET_ENTRIES; ++__i) { \
        size_t __pos = (__i + (idx)) & (__TTLMAP_BUCKET_ENTRIES - 1); \
        if (__bucket->deadline[__pos] > (now) && __TTLMAP_KEYS_EQUAL((key_ptr), &__bucket->keys[__pos])) { \
            /* printf("ttlmap found: bucket_ptr=%lx, idx=%zu, i=%zu, pos=%zu\n", (uintptr_t)bucket_ptr, (size_t)idx, __i, __pos); */ \
            __bucket->deadline[__pos] = (now) + (timeout); \
            *(value_ptr_ptr) = &__bucket->values[__pos]; \
            __ret = TTLMAP_FOUND; \
            goto __done; \
        } \
    } \
    for (size_t __i = 0; __i < __TTLMAP_BUCKET_ENTRIES; ++__i) { \
        size_t __pos = (__i + (idx)) & (__TTLMAP_BUCKET_ENTRIES - 1); \
        if (__bucket->deadline[__pos] <= (now)) { \
            /* printf("ttlmap insert: bucket_ptr=%lx, idx=%zu, i=%zu, pos=%zu\n", (uintptr_t)bucket_ptr, (size_t)idx, __i, __pos); */\
            __bucket->deadline[__pos] = (now) + (timeout); \
            __TTLMAP_MEMORY_SET(&__bucket->keys[__pos], (key_ptr)); \
            *(value_ptr_ptr) = &__bucket->values[__pos]; \
            __ret = TTLMAP_INSERTED; \
            goto __done; \
        } \
    } \
    /* failed */ \
    __ttlmap_unlock(&__bucket->lock); \
__done: \
    __ret; \
})

////////////////////////////////////////////////////////////////////////////////

static inline size_t
__ttlmap_bucket_count(size_t kv_entries) { // NOLINT
    size_t buckets = (kv_entries + __TTLMAP_BUCKET_ENTRIES - 1) / __TTLMAP_BUCKET_ENTRIES;
    size_t max_bit = 63 - __builtin_clzll(buckets);
    size_t res = 1ull << (max_bit + 1);
    return res;
}

////////////////////////////////////////////////////////////////////////////////

#define __TTLMAP_BUCKET_FIND_WITH_ID(map_ptr, bucket_id, key_type, value_type) \
__extension__({ \
    __TTLMAP_BUCKET_DECLARE(key_type, value_type); \
    uint32_t __bucket = (bucket_id); \
    uint32_t __chunk = __bucket >> ((map_ptr)->buckets_per_chunk_exp); \
    uint32_t __buckets_per_chunk = 1 << ((map_ptr)->buckets_per_chunk_exp); \
    uint32_t __bucket_in_chunk = __bucket & (__buckets_per_chunk - 1); \
    __bucket_t *__buckets_array = ADDR_OF(&((map_ptr)->chunks[__chunk])); \
    (void *)&__buckets_array[__bucket_in_chunk]; \
})

#define __TTLMAP_BUCKET_FIND(map_ptr, key_ptr, value_type) \
__extension__({ \
    uint32_t __hash = __TTLMAP_KEY_HASH((key_ptr)); \
    uint32_t __buckets = 1 << ((map_ptr)->buckets_exp); \
    uint32_t __bucket_id = __hash & (__buckets - 1); \
    __TTLMAP_BUCKET_FIND_WITH_ID(map_ptr, __bucket_id, typeof(*(key_ptr)), value_type); \
})

////////////////////////////////////////////////////////////////////////////////

#define __TTLMAP_BUCKET_ELEMENTS_TOUCHED(map_ptr, bucket_id, key_type, value_type) \
__extension__({ \
    const void *__addr = __TTLMAP_BUCKET_FIND_WITH_ID(map_ptr, bucket_id, key_type, value_type); \
    __TTLMAP_BUCKET_DECLARE(key_type, value_type); \
    const __bucket_t *__bucket = (const __bucket_t *)__addr; \
    size_t count = 0; \
    for (size_t i = 0; i < __TTLMAP_BUCKET_ENTRIES; ++i) { \
        if (__bucket->deadline[i] > 0) { \
            ++count; \
        } \
    } \
    count; \
})
