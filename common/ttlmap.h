#pragma once

#include "rte_common.h"
#include <assert.h>
#include <stdalign.h>
#include <stdatomic.h>
#include <stddef.h>
#include <stdint.h>
#include <stdalign.h>

#include <common/memory.h>

#include <rte_hash_crc.h>
#include <rte_spinlock.h>

#include "detail/ttlmap/lock.h"

////////////////////////////////////////////////////////////////////////////////

#define FURRYTABLE_ENTRIES_PER_BUCKET 16

////////////////////////////////////////////////////////////////////////////////

#define define_bucket(key_type, value_type) struct bucket { \
    key_type key[FURRYTABLE_ENTRIES_PER_BUCKET]; \
    size_t epoch[FURRYTABLE_ENTIRES_PER_BUCKET]; \
    value_type value[FURRYTABLE_ENTRIES_PER_BUCKET]; \
    uint8_t occupied; \
    rte_spinlock_t lock; \
} __rte_cache_aligned \

#define furrytable_bucket_alignment(key_type, value_type) alignof(define_bucket(key_type, value_type))
#define furrytable_bucket_size(key_type, value_type) sizeof(define_bucket(key_type, value_type))

////////////////////////////////////////////////////////////////////////////////


// #define furrytable_lookup(table_ptr, key_ptr, value_ptr)

struct furrytable {
    void **bucket_arrays; // every chain[] has size 64MB
    size_t *bucket_shifts;
    size_t bucket_count;
    struct memory_context *mctx;
};

////////////////////////////////////////////////////////////////////////////////

inline static void furrytable_free(struct furrytable *table) {
    if (!table->bucket_arrays) {
        return;
    }
    if (!table->bucket_shifts) {
        memory_bfree(table->mctx, table->bucket_arrays, table->bucket_count * sizeof(void *));
        return;   
    }
    for (size_t i = 0; i < table->bucket_count; ++i) {
        uintptr_t bucket_array_addr = (uintptr_t)table->bucket_arrays[i];
        bucket_array_addr -= table->bucket_shifts[i];
        memory_bfree(table->mctx, (void *)bucket_array_addr, MEMORY_BLOCK_ALLOCATOR_MAX_SIZE);
    }
    memory_bfree(table->mctx, table->bucket_arrays, table->bucket_count * sizeof(void *));
    memory_bfree(table->mctx, table->bucket_shifts, table->bucket_count * sizeof(size_t));
}

////////////////////////////////////////////////////////////////////////////////

inline static int furrytable_init(struct furrytable *table, struct memory_context *mctx, size_t bucket_count, size_t bucket_size, size_t bucket_align) {
    assert((bucket_count & (bucket_count - 1)) == 0); // `bucket_count` must be power of 2
    table->mctx = mctx;
    size_t buckets_per_array = (MEMORY_BLOCK_ALLOCATOR_MAX_SIZE - bucket_align) / bucket_size;
    assert(buckets_per_array > 0);
    size_t bucket_arrays = (bucket_count + buckets_per_array - 1) / buckets_per_array;
    table->bucket_arrays = memory_balloc(mctx, bucket_arrays * sizeof(void *)); // 8 bytes aligned
    if (table->bucket_arrays == NULL) {
        furrytable_free(table);
        return -1;
    }
    table->bucket_shifts = memory_balloc(mctx, bucket_arrays * sizeof(size_t)); // 8 bytes aligned
    if (table->bucket_shifts == NULL) {
        furrytable_free(table);
        return -1;
    }
    for (size_t i = 0; i < bucket_arrays; ++i) {
        size_t cur_array_buckets = buckets_per_array;
        if (bucket_count < cur_array_buckets) {
            cur_array_buckets = bucket_count;
        }
        void *cur_array = memory_balloc(mctx, MEMORY_BLOCK_ALLOCATOR_MAX_SIZE);
        if (cur_array == NULL) {
            furrytable_free(table);
            return -1;
        }
        uintptr_t cur_array_addr = (uintptr_t)cur_array;
        size_t shift = (bucket_align - cur_array_addr % bucket_align) % bucket_align;
        table->bucket_shifts[i] = shift;
        table->bucket_arrays[i] = (void *)(cur_array_addr + shift);
        cur_array_addr += shift;
        bucket_count -= cur_array_buckets;
    }
    return 0;
}

#define FURRYTABLE_INIT(table_ptr, mctx_ptr, key_type, value_type, table_entries) \
__extension__({ \
    define_bucket(key_type, value_type); \
    size_t bucket_count = ((table_entries) + FURRYTABLE_ENTRIES_PER_BUCKET - 1) / FURRYTABLE_ENTRIES_PER_BUCKET; \
    furrytable_init((table_ptr), (mctx_ptr), bucket_count, sizeof(struct bucket), alignof(struct __furrytable_bucket)); \
})

#define FURRYTABLE_GET(table_ptr, gen, key_ptr, value_ptr, lock_ptr) \
__extension__({ \
    typedef typeof(*key_ptr) key_type; \
    typedef typeof(**value_ptr) value_type; \
    typeof(gen) _gen = (gen); \
    key_type *key = (key_ptr); \
    define_bucket(key_type, value_type); \
    uint32_t hash = rte_hash_crc(key_ptr, sizeof(key_type), 0); \
    uint32_t bucket_id = hash & (table_ptr->bucket_count - 1); \
    size_t buckets_per_array = (MEMORY_BLOCK_ALLOCATOR_MAX_SIZE - alignof(struct bucket)) / sizeof(struct bucket); \
    size_t array = bucket_count / buckets_per_array; \
    void *array_ptr = table_ptr->bucket_arrays[array]; \
    struct bucket *bucket = ((struct bucket *)array_ptr) + bucket_id % buckets_per_array; \
    for (size_t i = 0; i < bucket->occupied; ++i) { \
        \
    }   \
})

////////////////////////////////////////////////////////////////////////////////

