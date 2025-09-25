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
        ttlmap_unlock(lock);
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
        ttlmap_unlock(lock);
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
        ttlmap_unlock(lock);                
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
        ttlmap_unlock(lock);      
        
        key = 0;
        res = __TTLMAP_BUCKET_LOOKUP(bucket_ptr, &key, &value, &lock, 15);
        assert(res == 0);
        assert(*value == 100);
        ttlmap_unlock(lock);
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
        ttlmap_unlock(lock);
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
        ttlmap_unlock(lock);
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
    ttlmap_unlock(lock);

    res = __TTLMAP_BUCKET_LOOKUP(bucket_ptr, &key, &lookup_value, &lock, 0);
    assert(res == 0);
    lookup_value->x += 10;
    assert(lookup_value->x == 20);
    ttlmap_unlock(lock);
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

static size_t ttlmap_init(void *memory, size_t memory_size, size_t kv_entries) {
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

    return mctx.balloc_size;
}

////////////////////////////////////////////////////////////////////////////////

int main() {
    puts("Test bucket_basic...");
    bucket_basic();

    puts("Test bucket_multithread_lookup...");
    bucket_multithread_lookup();

    puts("Test bucket_big_alignment...");
    bucket_big_alignment();

    puts("Test bucket_alignment...");
    bucket_alignment();

    // ttlmap
    void *memory = malloc(1 << 20);
    puts("Test ttlmap_init...");
    size_t memory_used = ttlmap_init(memory, 1 << 20, 100);
    printf("Used %lu bytes for 100 key-value entries (key size is %lu, value size is %lu)\n", memory_used, sizeof(test_key_t), sizeof(test_value_t));

    puts("OK!");
    return 0;
}