#pragma once

#include "key_value.h"

#include <rte_common.h>

////////////////////////////////////////////////////////////////////////////////

#define __TTLMAP_BUCKET_ENTRIES 16

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
    ttlmap_lock_init(&__bucket->lock); \
})

// Returns 1 on success and 0 on failure.

#define __TTLMAP_BUCKET_INSERT(bucket_ptr, key_ptr, value_ptr, now, timeout) \
__extension__({ \
    __label__ __done; \
    int __ret = -1; \
    typedef typeof(*(key_ptr)) __key_type; \
    typedef typeof(*(value_ptr)) __value_type; \
    __TTLMAP_BUCKET_DECLARE(__key_type, __value_type); \
    __bucket_t *__bucket = (__bucket_t *)(bucket_ptr); \
    ttlmap_lock(&__bucket->lock); \
    for (size_t __i = 0; __i < __TTLMAP_BUCKET_ENTRIES; ++__i) { \
        if (__TTLMAP_KEYS_EQUAL((key_ptr), &__bucket->keys[__i])) { \
            __bucket->deadline[__i] = (now) + (timeout); \
            __TTLMAP_MEMORY_SET(&__bucket->values[__i], (value_ptr)); \
            __ret = 0; \
            goto __done; \
        } \
    } \
    for (size_t __i = 0; __i < __TTLMAP_BUCKET_ENTRIES; ++__i) { \
        if (__bucket->deadline[__i] <= (now)) { \
            __bucket->deadline[__i] = (now) + (timeout); \
            __TTLMAP_MEMORY_SET(&__bucket->keys[__i], (key_ptr)); \
            __TTLMAP_MEMORY_SET(&__bucket->values[__i], (value_ptr)); \
            __ret = 0; \
            goto __done; \
        } \
    } \
__done: \
    ttlmap_unlock(&__bucket->lock); \
    __ret; \
})

#define __TTLMAP_BUCKET_LOOKUP(bucket_ptr, key_ptr, value_ptr_ptr, lock_ptr_ptr, now) \
__extension__({ \
    __label__ __done; \
    int __ret = -1; \
    typedef typeof(*(key_ptr)) __key_type; \
    typedef typeof(**(value_ptr_ptr)) __value_type; \
    __TTLMAP_BUCKET_DECLARE(__key_type, __value_type); \
    __bucket_t *__bucket = (__bucket_t *)(bucket_ptr); \
    ttlmap_lock(&__bucket->lock); \
    for (size_t __i = 0; __i < __TTLMAP_BUCKET_ENTRIES; ++__i) { \
        if (__bucket->deadline[__i] > (now) && __TTLMAP_KEYS_EQUAL((key_ptr), &__bucket->keys[__i])) { \
            *(value_ptr_ptr) = &__bucket->values[__i]; \
            *(lock_ptr_ptr) = &__bucket->lock; \
            __ret = 0; \
            goto __done; \
        } \
    } \
    ttlmap_unlock(&__bucket->lock); \
__done: \
    __ret; \
})

////////////////////////////////////////////////////////////////////////////////

static inline size_t
__ttlmap_bucket_count(size_t kv_entries) { // NOLINT
    size_t buckets = (kv_entries + __TTLMAP_BUCKET_ENTRIES - 1) / __TTLMAP_BUCKET_ENTRIES;
    size_t max_bit = 63 - __builtin_clzll(buckets);
    if (buckets == max_bit) {
        return buckets;
    }
    return 1ull << (max_bit + 1);
}

////////////////////////////////////////////////////////////////////////////////

#define __TTLMAP_BUCKET_GET(map_ptr, key_ptr) \
__extension__({ \
    uint32_t __hash = __TTLMAP_KEY_HASH((key_ptr)); \
    uint32_t __buckets = 1 << ((map_ptr)->buckets_exp); \
    uint32_t __bucket = __hash & (__buckets - 1); \
    uint32_t __chunk = __bucket >> ((map_ptr)->buckets_per_chunk_exp); \
    uint32_t __buckets_per_chunk = 1 << ((map_ptr)->buckets_per_chunk_exp); \
    uint32_t __bucket_in_chunk = bucket & (__buckets_per_chunk - 1); \
    (ADDR_OF(&((map_ptr)->chunks[__chunk])))[__bucket_in_chunk]; \
})