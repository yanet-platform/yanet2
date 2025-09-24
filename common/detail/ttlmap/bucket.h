#pragma once

#include "key_value.h"
#include "lock.h"

////////////////////////////////////////////////////////////////////////////////

#define __TTLMAP_BUCKET_ENTRIES 16

#define __TTLMAP_BUCKET_DECLARE(key_type, value_type) \
typedef struct bucket { \
    key_type keys[__TTLMAP_BUCKET_ENTRIES]; \
    value_type values[__TTLMAP_BUCKET_ENTRIES]; \
    uint32_t deadline[__TTLMAP_BUCKET_ENTRIES]; \
    ttlmap_lock_t lock; \
} bucket_t

#define __TTLMAP_BUCKET_INIT(bucket_ptr, key_type, value_type) \
__extension__({ \
    __TTLMAP_BUCKET_DECLARE(key_type, value_type); \
    bucket_t *bucket = (bucket_t *)bucket_ptr; \
    memset(bucket->deadline, 0, sizeof(bucket->deadline)); \
    ttlmap_lock_init(&bucket->lock); \
})

// Returns 1 on success and 0 on failure.

#define __TTLMAP_BUCKET_INSERT(bucket_ptr, key_ptr, value_ptr, now, timeout) \
__extension__({ \
    __label__ done; \
    int ret = -1; \
    typedef typeof(*(key_ptr)) key_type; \
    typedef typeof(*(value_ptr)) value_type; \
    __TTLMAP_BUCKET_DECLARE(key_type, value_type); \
    bucket_t *bucket = (bucket_t *)bucket_ptr; \
    ttlmap_lock(&bucket->lock); \
    for (size_t i = 0; i < __TTLMAP_BUCKET_ENTRIES; ++i) { \
        if (bucket->deadline[i] > now && __TTLMAP_KEYS_EQUAL((key_ptr), &bucket->keys[i])) { \
            bucket->deadline[i] = now + timeout; \
            __TTLMAP_MEMORY_SET(&bucket->values[i], (value_ptr)); \
            ret = 0; \
            goto done; \
        } \
    } \
    for (size_t i = 0; i < __TTLMAP_BUCKET_ENTRIES; ++i) { \
        if (bucket->deadline[i] <= now) { \
            bucket->deadline[i] = now + timeout; \
            __TTLMAP_MEMORY_SET(&bucket->keys[i], (key_ptr)); \
            __TTLMAP_MEMORY_SET(&bucket->values[i], (value_ptr)); \
            ret = 0; \
            goto done; \
        } \
    } \
done: \
    ttlmap_unlock(&bucket->lock); \
    ret; \
})

#define __TTLMAP_BUCKET_LOOKUP(bucket_ptr, key_ptr, value_ptr_ptr, lock_ptr_ptr, now) \
__extension__({ \
    __label__ done; \
    int ret = -1; \
    typedef typeof(*(key_ptr)) key_type; \
    typedef typeof(**(value_ptr_ptr)) value_type; \
    __TTLMAP_BUCKET_DECLARE(key_type, value_type); \
    bucket_t *bucket = (bucket_t *)bucket_ptr; \
    ttlmap_lock(&bucket->lock); \
    for (size_t i = 0; i < __TTLMAP_BUCKET_ENTRIES; ++i) { \
        if (bucket->deadline[i] > now && __TTLMAP_KEYS_EQUAL((key_ptr), &bucket->keys[i])) { \
            *value_ptr_ptr = &bucket->values[i]; \
            *lock_ptr_ptr = &bucket->lock; \
            ret = 0; \
            goto done; \
        } \
    } \
    ttlmap_unlock(&bucket->lock); \
done: \
    ret; \
})