#include "chunk.h"

#include <stddef.h>

#include "common/memory_address.h"
#include "common/memory_block.h"
#include "common/memory.h"

#include "bucket.h"

////////////////////////////////////////////////////////////////////////////////

typedef struct ttlmap {
    struct memory_context *mctx; // relative pointer
    void *chunks[__TTLMAP_MAX_CHUNKS]; // relative pointers
    size_t chunk_shifts[__TTLMAP_MAX_CHUNKS];
    size_t buckets_per_chunk_exp; // buckets_per_chunk = 2**buckets_per_chunk_exp
    size_t buckets_exp; // buckets = 2**buckets_exp
} ttlmap_t;

////////////////////////////////////////////////////////////////////////////////

static inline int
__ttlmap_init_internal(ttlmap_t *map, struct memory_context *mctx, size_t bucket_align, size_t bucket_size, size_t bucket_count) { // NOLINT
    if ((bucket_count & (bucket_count - 1)) != 0) { // bucket count must be power of 2
        return -1;
    }

    SET_OFFSET_OF(&map->mctx, mctx);

    map->buckets_exp = 63 - __builtin_clzll(bucket_count);

    size_t buckets_per_chunk = (MEMORY_BLOCK_ALLOCATOR_MAX_SIZE - bucket_align) / bucket_size;
    // coarse to the closest power of two
    map->buckets_per_chunk_exp = 63 - __builtin_clzll(buckets_per_chunk);
    
    memset(&map->chunks, 0, sizeof(map->chunks));
    for (size_t i = 0; i < __TTLMAP_MAX_CHUNKS; ++i) {
        if (bucket_count == 0) {
            break;
        }
        size_t need_size = bucket_count * bucket_size + bucket_align;
        if (need_size > MEMORY_BLOCK_ALLOCATOR_MAX_SIZE) {
            need_size = MEMORY_BLOCK_ALLOCATOR_MAX_SIZE;
        }
        void *chunk = memory_balloc(mctx, need_size);\
        if (chunk == NULL) {
            break;
        }
        uintptr_t chunk_ptr = (uintptr_t)chunk;
        size_t need_add_offset = (bucket_align - chunk_ptr % bucket_align) % bucket_align;
        map->chunk_shifts[i] = need_add_offset;
        chunk_ptr += need_add_offset;
        SET_OFFSET_OF(&map->chunks[i], (void *)chunk_ptr);
    }
    
    if (bucket_count != 0) {
        for (size_t i = 0; i < __TTLMAP_MAX_CHUNKS; ++i) {
            memory_bfree(mctx, ADDR_OF(&map->chunks[i]), MEMORY_BLOCK_ALLOCATOR_MAX_SIZE);
        }
        return -1;
    }

    return 0;
}

////////////////////////////////////////////////////////////////////////////////

#define __TTLMAP_INIT_INTERNAL(map_ptr, mctx_ptr, key_type, value_type, entries) \
__extension__({ \
    __TTLMAP_BUCKET_DECLARE(key_type, value_type); \
    size_t bucket_count = __ttlmap_bucket_count(entries); \
    if (bucket_count == 0) { \
        bucket_count = 1; \
    } \
    __ttlmap_init_internal(map_ptr, mctx_ptr, alignof(__bucket_t), sizeof(__bucket_t), bucket_count); \
})
