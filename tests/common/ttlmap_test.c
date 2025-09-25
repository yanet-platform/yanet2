#include "common/memory_block.h"
#include <assert.h>

#include <common/detail/ttlmap/lock.h>
#include <common/detail/ttlmap/bucket.h>
#include <common/ttlmap.h>

#include <pthread.h>

////////////////////////////////////////////////////////////////////////////////

void bucket_basic() {
    alignas(4096) uint8_t bucket[4096];
    [[maybe_unused]] void *bucket_ptr = bucket;
    __TTLMAP_BUCKET_INIT(bucket_ptr, size_t, size_t);
    int res;

    // fill full bucket
    for (size_t i = 0; i < __TTLMAP_BUCKET_ENTRIES; ++i) {
        size_t value = i;
        res = __TTLMAP_BUCKET_INSERT(bucket_ptr, &i, &value, 0, 10);
        assert(res == 0);
    }

    // check values for full bucket
    for (size_t i = 0; i < __TTLMAP_BUCKET_ENTRIES; ++i) {
        size_t *value;
        ttlmap_lock_t *lock;
        res = __TTLMAP_BUCKET_LOOKUP(bucket_ptr, &i, &value, &lock, 0);
        assert(res == 0);
        assert(*value == i);
        __ttlmap_unlock(lock);
    }

    // insert one more key, expect error
    {
        size_t key = 100;
        size_t value = 0;
        res = __TTLMAP_BUCKET_INSERT(bucket_ptr, &key, &value, 0, 10);
        assert(res < 0);
    }

    // try get value with expired timeout
    {
        size_t key = 0;
        size_t *value;
        ttlmap_lock_t *lock;
        res = __TTLMAP_BUCKET_LOOKUP(bucket_ptr, &key, &value, &lock, 10);
        assert(res < 0);
    }

    // try get value with almost expired timeout
    {
        size_t key = 0;
        size_t *value;
        ttlmap_lock_t *lock;
        res = __TTLMAP_BUCKET_LOOKUP(bucket_ptr, &key, &value, &lock, 9);
        assert(res == 0);
        assert(*value == key);
        __ttlmap_unlock(lock);
    }

    // try insert with rewrite
    {
        size_t key = 0;
        size_t value = 100;
        res = __TTLMAP_BUCKET_INSERT(bucket_ptr, &key, &value, 9, 10);
        assert(res == 0);
    }

    // check rewrite worked
    {
        size_t key = 0;
        size_t *value;
        ttlmap_lock_t *lock;
        res = __TTLMAP_BUCKET_LOOKUP(bucket_ptr, &key, &value, &lock, 9);
        assert(res == 0);
        assert(*value == 100);
        __ttlmap_unlock(lock);                
    }

    // again
    {
        size_t key = __TTLMAP_BUCKET_ENTRIES / 2;
        size_t value = 500;
        res = __TTLMAP_BUCKET_INSERT(bucket_ptr, &key, &value, 15, 10);
        assert(res == 0);
    }

    // check rewrite worked
    {
        size_t key = __TTLMAP_BUCKET_ENTRIES / 2;
        size_t *value;
        ttlmap_lock_t *lock;
        res = __TTLMAP_BUCKET_LOOKUP(bucket_ptr, &key, &value, &lock, 15);
        assert(res == 0);
        assert(*value == 500);
        __ttlmap_unlock(lock);      
        
        key = 0;
        res = __TTLMAP_BUCKET_LOOKUP(bucket_ptr, &key, &value, &lock, 15);
        assert(res == 0);
        assert(*value == 100);
        __ttlmap_unlock(lock);
    }

    // check buckets again
    for (size_t i = 0; i < __TTLMAP_BUCKET_ENTRIES; ++i) {
        size_t *value;
        ttlmap_lock_t *lock;
        res = __TTLMAP_BUCKET_LOOKUP(bucket_ptr, &i, &value, &lock, 0);
        assert(res == 0);
        if (i != 0 && i != __TTLMAP_BUCKET_ENTRIES / 2) {
            assert(*value == i);
        } else if (i == 0) {
            assert(*value == 100);
        } else {
            // i == __TTLMAP_BUCKET_ENTRIES / 2
            assert(*value == 500);
        }
        __ttlmap_unlock(lock);
    }
}

////////////////////////////////////////////////////////////////////////////////

static void *thread_func(void *bucket) {
    for (size_t i = 0; i < 100000; ++i) {
        size_t key = 0;
        size_t *value;
        ttlmap_lock_t *lock;
        int res = __TTLMAP_BUCKET_LOOKUP(bucket, &key, &value, &lock, 0);
        if (res != 0) {
            return (void *)1;
        }
        *value = *value + 1;
        __ttlmap_unlock(lock);
    }
    return (void *)0;
}

void bucket_multithread_lookup() {
     alignas(4096) uint8_t bucket[4096];
    [[maybe_unused]] void *bucket_ptr = bucket;
    __TTLMAP_BUCKET_INIT(bucket_ptr, size_t, size_t);
    size_t key = 0;
    size_t value = 0;
    __TTLMAP_BUCKET_INSERT(bucket_ptr, &key, &value, 0, 10);
    pthread_t threads[10];
    for (size_t i = 0; i < 10; ++i) {
        int res = pthread_create(&threads[i], NULL, thread_func, bucket_ptr);
        assert(res == 0);
    }
    for (size_t i = 0; i < 10; ++i) {
        void *ret;
        int res = pthread_join(threads[i], &ret);
        assert(res == 0);
        assert(ret == (void *)0);
    }
    size_t *lookup_value;
    ttlmap_lock_t *lock;
    int res = __TTLMAP_BUCKET_LOOKUP(bucket_ptr, &key, &lookup_value, &lock, 0);
    assert(res == 0);
    assert(*lookup_value == 1000000);
}

////////////////////////////////////////////////////////////////////////////////

void bucket_big_alignment() {
    typedef struct key {
        int x;
    } __rte_cache_aligned key_t;

    typedef struct value {
        int x;
    } __rte_cache_aligned value_t;

    alignas(4096) uint8_t bucket[4096];
    [[maybe_unused]] void *bucket_ptr = bucket;
    __TTLMAP_BUCKET_INIT(bucket_ptr, key_t, value_t);

    key_t key = {1};
    value_t value = {.x = 0};
    int res = __TTLMAP_BUCKET_INSERT(bucket_ptr, &key, &value, 0, 10);
    assert(res == 0);

    value_t *lookup_value;
    ttlmap_lock_t *lock;
    res = __TTLMAP_BUCKET_LOOKUP(bucket_ptr, &key, &lookup_value, &lock, 0);
    assert(res == 0);
    assert(lookup_value->x == 0);
    lookup_value->x += 10;
    __ttlmap_unlock(lock);

    res = __TTLMAP_BUCKET_LOOKUP(bucket_ptr, &key, &lookup_value, &lock, 0);
    assert(res == 0);
    lookup_value->x += 10;
    assert(lookup_value->x == 20);
    __ttlmap_unlock(lock);
}

////////////////////////////////////////////////////////////////////////////////

void bucket_alignment() {
    __TTLMAP_BUCKET_DECLARE(uint8_t, uint8_t);
    static_assert(alignof(__bucket_t) >= 64, "not cache aligned");
}

////////////////////////////////////////////////////////////////////////////////

typedef struct test_key {
    size_t ip_src;
    size_t ip_dst;
    uint8_t proto;
    uint16_t port_src;
    uint16_t port_dst;
    size_t tcp_flags; // 64 bits just to be
} test_key_t;

typedef struct test_value {
    size_t session_id;
    size_t counter1;
    size_t counter2;
} test_value_t;

void ttlmap_init(void *memory, size_t memory_size, size_t kv_entries) {
    int res;

    struct block_allocator alloc;
    res = block_allocator_init(&alloc);
    assert(res == 0);
    block_allocator_put_arena(&alloc, memory, memory_size);
    
    struct memory_context mctx;
    res = memory_context_init(&mctx, "test", &alloc);
    assert(res == 0);

    ttlmap_t map;
    res = TTLMAP_INIT(&map, &mctx, test_key_t, test_value_t, kv_entries);
    assert(res == 0);

    TTLMAP_FREE(&map);
}

void ttlmap_init_and_get_buckets(void *memory, size_t memory_size, size_t kv_entries) {
    int res;

    struct block_allocator alloc;
    res = block_allocator_init(&alloc);
    assert(res == 0);
    block_allocator_put_arena(&alloc, memory, memory_size);
    
    struct memory_context mctx;
    res = memory_context_init(&mctx, "test", &alloc);
    assert(res == 0);

    ttlmap_t map;
    res = TTLMAP_INIT(&map, &mctx, test_key_t, test_value_t, kv_entries);
    assert(res == 0);

    for (size_t i = 0; i < (1 << map.buckets_exp); ++i) {
        void *bucket = __TTLMAP_BUCKET_GET_WITH_ID(&map, i, test_key_t, test_value_t);
        assert((uintptr_t)bucket % 64 == 0);
        
        test_key_t key = {
            .ip_dst = i + 0x01010,
            .ip_src = i + 0x10101,
            .port_dst = 10,
            .port_src = 20,
            .proto = 55,
            .tcp_flags = i
        };
        test_value_t value = {
            .counter1 = i,
            .counter2 = i + 1,
            .session_id = 0
        };
        res = __TTLMAP_BUCKET_INSERT(bucket, &key, &value, 0, 10);
        assert(res == 0);
    }

    for (size_t i = 0; i < (1 << map.buckets_exp); ++i) {
        void *bucket = __TTLMAP_BUCKET_GET_WITH_ID(&map, i, test_key_t, test_value_t);
        assert((uintptr_t)bucket % 64 == 0);

        test_key_t key = {
            .ip_dst = i + 0x01010,
            .ip_src = i + 0x10101,
            .port_dst = 10,
            .port_src = 20,
            .proto = 55,
            .tcp_flags = i
        };
        test_value_t value = {
            .counter1 = i,
            .counter2 = i + 1,
            .session_id = 0
        };
        test_value_t *lookup_value;
        ttlmap_lock_t *lock;
        res = __TTLMAP_BUCKET_LOOKUP(bucket, &key, &lookup_value, &lock, 5);
        assert(res == 0);
        assert(memcmp(lookup_value, &value, sizeof(value)) == 0);
        __ttlmap_unlock(lock);
    }

    int fd = 0;
    TTLMAP_PRINT_STAT(&map, test_key_t, test_value_t, fd);
    fprintf(fdopen(fd, "w"), "\tPer-entry memory overhead: %.2lf%%\n", 100.0 * (double)(map.mctx.balloc_size) / (kv_entries * (sizeof(test_key_t) + sizeof(test_value_t))));

    TTLMAP_FREE(&map);
}

////////////////////////////////////////////////////////////////////////////////

void ttlmap_strike_entries(void *memory, size_t memory_size, size_t kv_entries) {
    int res;

    struct block_allocator alloc;
    res = block_allocator_init(&alloc);
    assert(res == 0);
    block_allocator_put_arena(&alloc, memory, memory_size);
    
    struct memory_context mctx;
    res = memory_context_init(&mctx, "test", &alloc);
    assert(res == 0);

    ttlmap_t map;
    res = TTLMAP_INIT(&map, &mctx, test_key_t, test_value_t, kv_entries);
    assert(res == 0);
    
    size_t inserted = 0;
    for (size_t i = 0; i < kv_entries; ++i) {
        test_key_t key = {
            .ip_dst = i + 0x01010,
            .ip_src = i + 0x10101,
            .port_dst = 10,
            .port_src = 20,
            .proto = 55,
            .tcp_flags = i
        };
        test_value_t value = {
            .counter1 = i,
            .counter2 = i + 1,
            .session_id = 0
        };
        int res = TTLMAP_INSERT(&map, &key, &value, 0, 10);
        if (res == 0) {
            ++inserted;
        }
    }

    size_t lookup = 0;
    for (size_t i = 0; i < kv_entries; ++i) {
        test_key_t key = {
            .ip_dst = i + 0x01010,
            .ip_src = i + 0x10101,
            .port_dst = 10,
            .port_src = 20,
            .proto = 55,
            .tcp_flags = i
        };
        test_value_t ref_value = {
            .counter1 = i,
            .counter2 = i + 1,
            .session_id = 0
        };
        test_value_t *value;
        ttlmap_lock_t *lock;

        int res = TTLMAP_LOOKUP(&map, &key, &value, &lock, 5);
        if (res == 0) {
            ++lookup;
            assert(memcmp(&ref_value, value, sizeof(ref_value)) == 0);
            ttlmap_release(lock);
        }
    }
    assert(inserted == lookup);

    printf("- Inserted: %lu/%lu entries (%.2lf%%)\n", inserted, kv_entries, 100.0 * (double)inserted / kv_entries);
    TTLMAP_PRINT_STAT(&map, test_key_t, test_value_t, 0);
}

////////////////////////////////////////////////////////////////////////////////

void ttlmap_free_works(void *memory, size_t memory_size) {
    const size_t kv_entries = 100000;
    int res;

    struct block_allocator alloc;
    res = block_allocator_init(&alloc);
    assert(res == 0);
    block_allocator_put_arena(&alloc, memory, memory_size);
    
    struct memory_context mctx;
    res = memory_context_init(&mctx, "test", &alloc);
    assert(res == 0);

    ttlmap_t map;
    res = TTLMAP_INIT(&map, &mctx, test_key_t, test_value_t, kv_entries);
    assert(res == 0);

    size_t inserted = 0;
    for (size_t i = 0; i < kv_entries; ++i) {
        test_key_t key = {
            .ip_dst = i + 0x01010,
            .ip_src = i + 0x10101,
            .port_dst = 10,
            .port_src = 20,
            .proto = 55,
            .tcp_flags = i
        };
        test_value_t value = {
            .counter1 = i,
            .counter2 = i + 1,
            .session_id = 0
        };
        int res = TTLMAP_INSERT(&map, &key, &value, 0, 10);
        if (res == 0) {
            ++inserted;
        }
    }

    size_t lookup = 0;
    for (size_t i = 0; i < kv_entries; ++i) {
        test_key_t key = {
            .ip_dst = i + 0x01010,
            .ip_src = i + 0x10101,
            .port_dst = 10,
            .port_src = 20,
            .proto = 55,
            .tcp_flags = i
        };
        test_value_t ref_value = {
            .counter1 = i,
            .counter2 = i + 1,
            .session_id = 0
        };
        test_value_t *value;
        ttlmap_lock_t *lock;

        int res = TTLMAP_LOOKUP(&map, &key, &value, &lock, 5);
        if (res == 0) {
            ++lookup;
            assert(memcmp(&ref_value, value, sizeof(ref_value)) == 0);
            ttlmap_release(lock);
        }
    }
    assert(inserted == lookup);

    TTLMAP_FREE(&map);
    assert(map.mctx.balloc_size == map.mctx.bfree_size);
}

////////////////////////////////////////////////////////////////////////////////

void ttlmap_init_and_get_buckets_many_entries(void *memory, size_t memory_size) {
    ttlmap_init_and_get_buckets(memory, memory_size, 1000000);
}

////////////////////////////////////////////////////////////////////////////////

void ttlmap_strike_many_entries(void *memory, size_t memory_size) {
    ttlmap_strike_entries(memory, memory_size, 1000000);
}

////////////////////////////////////////////////////////////////////////////////

int main() {
    // buckets
    puts("Test bucket_basic...");
    bucket_basic();

    puts("Test bucket_multithread_lookup...");
    bucket_multithread_lookup();

    puts("Test bucket_big_alignment...");
    bucket_big_alignment();

    puts("Test bucket_alignment...");
    bucket_alignment();

    // ttlmap
    size_t memory_size = 1 << 30;
    void *memory = malloc(memory_size);
    puts("Test ttlmap_init...");
    ttlmap_init(memory, memory_size, 100);

    for (size_t entries = 1; entries <= 10000; entries = (size_t)((double)(entries + 1) * 1.6)) {
        printf("\nTest ttlmap_init_and_get_buckets [entries=%zu]...\n", entries);
        ttlmap_init_and_get_buckets(memory, memory_size, entries);
    }

    for (size_t entries = 1; entries <= 10000; entries = (size_t)((double)(entries + 1) * 1.6)) {
        printf("\nTest ttlmap_strike_entries [entries=%zu]...\n", entries);
        ttlmap_strike_entries(memory, memory_size, entries);
    }

    puts("Test ttlmap_free_works...");
    ttlmap_free_works(memory, memory_size);

    puts("Test ttlmap_init_and_get_buckets_many_entries...");
    ttlmap_init_and_get_buckets_many_entries(memory, memory_size);

    puts("Test ttlmap_strike_many_entries...");
    ttlmap_strike_many_entries(memory, memory_size);

    free(memory);

    puts("OK!");
    return 0;
}