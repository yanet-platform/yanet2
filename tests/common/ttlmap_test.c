#include <assert.h>

#include <common/detail/ttlmap/bucket.h>
#include <common/ttlmap.h>

////////////////////////////////////////////////////////////////////////////////

int main() {
    alignas(4096) uint8_t bucket[4096];
    [[maybe_unused]] void *bucket_ptr = bucket;
    __TTLMAP_BUCKET_INIT(bucket_ptr, size_t, size_t);
    size_t key = 0;
    size_t value = 15;
    int res = __TTLMAP_BUCKET_INSERT(bucket_ptr, &key, &value, 0, 10);
    assert(res == 0);
    key = 5;
    value = 1;
    res = __TTLMAP_BUCKET_INSERT(bucket_ptr, &key, &value, 0, 10);
    size_t *value_ptr;
    ttlmap_lock_t *lock;
    res = __TTLMAP_BUCKET_LOOKUP(bucket_ptr, &key, &value_ptr, &lock, 0);
    assert(res == 0);
    assert(*value_ptr == 1);
    ttlmap_unlock(lock);
    puts("OK!");
    return 0;
}