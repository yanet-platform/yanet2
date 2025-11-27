#include "common/memory_block.h"
#include "lib/logging/log.h"
#include "rte_common.h"

#include <common/ttlmap/detail/bucket.h>
#include <common/ttlmap/detail/lock.h>

#include <common/ttlmap/ttlmap.h>

#include <assert.h>
#include <pthread.h>
#include <stdalign.h>
#include <unistd.h>

////////////////////////////////////////////////////////////////////////////////

void
bucket_basic() {
	alignas(64) uint8_t bucket[4096];
	[[maybe_unused]] void *bucket_ptr = bucket;
	__TTLMAP_BUCKET_INIT(bucket_ptr, size_t, size_t);
	int res;

	// fill full bucket
	for (size_t i = 0; i < __TTLMAP_BUCKET_ENTRIES; ++i) {
		size_t *value;
		ttlmap_lock_t *lock;
		res = __TTLMAP_BUCKET_GET(
			bucket_ptr, &i, &value, &lock, 0, 10, 0
		);
		assert(TTLMAP_STATUS(res) == TTLMAP_INSERTED);
		assert(value != NULL);
		assert(lock != NULL);
		*value = i;
		__ttlmap_unlock(lock);
	}

	// check values for full bucket
	for (size_t i = 0; i < __TTLMAP_BUCKET_ENTRIES; ++i) {
		size_t *value;
		ttlmap_lock_t *lock;
		res = __TTLMAP_BUCKET_GET(
			bucket_ptr, &i, &value, &lock, 0, 10, 0
		);
		assert(TTLMAP_STATUS(res) == TTLMAP_FOUND);
		assert(value != NULL);
		assert(lock != NULL);
		assert(*value == i);
		__ttlmap_unlock(lock);
	}

	// insert one more key, expect error
	{
		size_t key = 100;
		size_t *value = NULL;
		ttlmap_lock_t *lock;
		res = __TTLMAP_BUCKET_GET(
			bucket_ptr, &key, &value, &lock, 0, 10, 0
		);
		assert(TTLMAP_STATUS(res) == TTLMAP_FAILED);
		assert(value == NULL);
	}

	// try get value with expired timeout
	{
		size_t key = 0;
		size_t *value;
		ttlmap_lock_t *lock;
		res = __TTLMAP_BUCKET_GET(
			bucket_ptr, &key, &value, &lock, 10, 10, 0
		);
		assert(TTLMAP_STATUS(res) == TTLMAP_INSERTED ||
		       TTLMAP_STATUS(res) == TTLMAP_REPLACED);
		assert(value != NULL);
		assert(lock != NULL);
		__ttlmap_unlock(lock);
	}

	// try get value with almost expired timeout
	{
		size_t key = 1;
		size_t *value;
		ttlmap_lock_t *lock;
		res = __TTLMAP_BUCKET_GET(
			bucket_ptr, &key, &value, &lock, 9, 10, 0
		);
		assert(TTLMAP_STATUS(res) == TTLMAP_FOUND);
		assert(value != NULL);
		assert(lock != NULL);
		assert(*value == key);
		__ttlmap_unlock(lock);
	}

	// try insert with rewrite
	{
		size_t key = 0;
		size_t *value;
		ttlmap_lock_t *lock;
		res = __TTLMAP_BUCKET_GET(
			bucket_ptr, &key, &value, &lock, 11, 10, 0
		);
		assert(TTLMAP_STATUS(res) == TTLMAP_FOUND);
		assert(value != NULL);
		assert(lock != NULL);
		*value = 100;
		__ttlmap_unlock(lock);
	}

	// check rewrite worked
	{
		size_t key = 0;
		size_t *value;
		ttlmap_lock_t *lock;
		res = __TTLMAP_BUCKET_GET(
			bucket_ptr, &key, &value, &lock, 11, 10, 0
		);
		assert(TTLMAP_STATUS(res) == TTLMAP_FOUND);
		assert(value != NULL);
		assert(lock != NULL);
		assert(*value == 100);
		__ttlmap_unlock(lock);
	}

	// again
	{
		size_t key = __TTLMAP_BUCKET_ENTRIES / 2;
		size_t *value;
		ttlmap_lock_t *lock;
		res = __TTLMAP_BUCKET_GET(
			bucket_ptr, &key, &value, &lock, 15, 10, 0
		);
		assert(TTLMAP_STATUS(res) == TTLMAP_INSERTED ||
		       TTLMAP_STATUS(res) == TTLMAP_REPLACED);
		assert(*value != 100);
		assert(lock != NULL);
		*value = 500;
		__ttlmap_unlock(lock);
	}
}

////////////////////////////////////////////////////////////////////////////////

static void *
thread_func(void *bucket) {
	for (size_t i = 0; i < 100000; ++i) {
		size_t key = 0;
		size_t *value;
		ttlmap_lock_t *lock;
		int res = __TTLMAP_BUCKET_GET(
			bucket, &key, &value, &lock, 0, 10, 0
		);
		if (TTLMAP_STATUS(res) != TTLMAP_FOUND) {
			return (void *)1;
		}
		assert(value != NULL);
		assert(lock != NULL);
		*value = *value + 1;
		__ttlmap_unlock(lock);
	}
	return (void *)0;
}

void
bucket_multithread() {
	alignas(64) uint8_t bucket[4096];
	[[maybe_unused]] void *bucket_ptr = bucket;
	__TTLMAP_BUCKET_INIT(bucket_ptr, size_t, size_t);
	size_t key = 0;
	size_t *value;
	ttlmap_lock_t *lock;
	int res =
		__TTLMAP_BUCKET_GET(bucket_ptr, &key, &value, &lock, 0, 10, 0);
	assert(TTLMAP_STATUS(res) == TTLMAP_INSERTED);
	assert(value != NULL);
	assert(lock != NULL);
	*value = 0;
	__ttlmap_unlock(lock);
	pthread_t threads[10];
	for (size_t i = 0; i < 10; ++i) {
		int res = pthread_create(
			&threads[i], NULL, thread_func, bucket_ptr
		);
		assert(res == 0);
	}
	for (size_t i = 0; i < 10; ++i) {
		void *ret;
		res = pthread_join(threads[i], &ret);
		assert(res == 0);
		assert(ret == (void *)0);
	}
	size_t *lookup_value;
	res = __TTLMAP_BUCKET_GET(
		bucket_ptr, &key, &lookup_value, &lock, 0, 10, 0
	);
	assert(TTLMAP_STATUS(res) == TTLMAP_FOUND);
	assert(lookup_value != NULL);
	assert(*lookup_value == 1000000);
	assert(lock != NULL);
	__ttlmap_unlock(lock);
}

////////////////////////////////////////////////////////////////////////////////

void
bucket_big_alignment() {
	typedef struct key {
		int x;
	} __rte_cache_aligned key_t;

	typedef struct value {
		int x;
	} __rte_cache_aligned value_t;

	alignas(64) uint8_t bucket[4096];
	[[maybe_unused]] void *bucket_ptr = bucket;
	__TTLMAP_BUCKET_INIT(bucket_ptr, key_t, value_t);

	key_t key = {1};
	value_t *value;
	ttlmap_lock_t *lock;
	int res =
		__TTLMAP_BUCKET_GET(bucket_ptr, &key, &value, &lock, 0, 10, 0);
	assert(TTLMAP_STATUS(res) == TTLMAP_INSERTED);
	assert(value != NULL);
	assert(lock != NULL);
	*value = (value_t){.x = 0};
	__ttlmap_unlock(lock);

	res = __TTLMAP_BUCKET_GET(bucket_ptr, &key, &value, &lock, 0, 10, 0);
	assert(TTLMAP_STATUS(res) == TTLMAP_FOUND);
	assert(value != NULL);
	assert(lock != NULL);
	assert(value->x == 0);
	value->x += 10;
	__ttlmap_unlock(lock);

	res = __TTLMAP_BUCKET_GET(bucket_ptr, &key, &value, &lock, 0, 10, 0);
	assert(TTLMAP_STATUS(res) == TTLMAP_FOUND);
	assert(value != NULL);
	assert(lock != NULL);
	value->x += 10;
	assert(value->x == 20);
	__ttlmap_unlock(lock);
}

////////////////////////////////////////////////////////////////////////////////

void
bucket_alignment() {
	__TTLMAP_BUCKET_DECLARE(uint8_t, uint8_t);
	static_assert(alignof(__bucket_t) == 64, "not cache aligned");
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

void
ttlmap_init(void *memory, size_t memory_size, size_t kv_entries) {
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

void
ttlmap_init_and_get_buckets(
	void *memory, size_t memory_size, size_t kv_entries
) {
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

	for (size_t i = 0; i < ((size_t)1 << map.buckets_exp); ++i) {
		void *bucket = __TTLMAP_BUCKET_FIND_WITH_ID(
			&map, i, test_key_t, test_value_t
		);
		assert((uintptr_t)bucket % 64 == 0);

		test_key_t key = {
			.ip_dst = i + 0x01010,
			.ip_src = i + 0x10101,
			.port_dst = 10,
			.port_src = 20,
			.proto = 55,
			.tcp_flags = i
		};
		test_value_t *value;
		ttlmap_lock_t *lock;
		res = __TTLMAP_BUCKET_GET(
			bucket, &key, &value, &lock, 0, 10, 0
		);
		assert(TTLMAP_STATUS(res) == TTLMAP_INSERTED);
		*value = (test_value_t
		){.counter1 = i, .counter2 = i + 1, .session_id = 0};
		__ttlmap_unlock(lock);
	}

	LOG(DEBUG, "print stat...");

	TTLMAP_PRINT_STAT(&map, test_key_t, test_value_t, STDERR_FILENO);
	LOG(INFO,
	    "\tPer-entry memory overhead: %.2lf%%\n",
	    100.0 * (double)(map.mctx.balloc_size) /
		    (kv_entries * (sizeof(test_key_t) + sizeof(test_value_t))));

	TTLMAP_FREE(&map);
}

////////////////////////////////////////////////////////////////////////////////

void
ttlmap_strike_entries(void *memory, size_t memory_size, size_t kv_entries) {
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
		test_value_t *value;
		ttlmap_lock_t *lock;
		int res = TTLMAP_GET(&map, &key, &value, &lock, 0, 10);
		if (TTLMAP_STATUS(res) == TTLMAP_INSERTED) {
			++inserted;
			*value = (test_value_t
			){.counter1 = i, .counter2 = i + 1, .session_id = 0};
			ttlmap_release_lock(lock);
		} else {
			assert(TTLMAP_STATUS(res) == TTLMAP_FAILED);
		}
	}

	size_t found = 0;
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
			.counter1 = i, .counter2 = i + 1, .session_id = 0
		};
		test_value_t *value;
		ttlmap_lock_t *lock;

		int res = TTLMAP_GET(&map, &key, &value, &lock, 5, 10);
		if (TTLMAP_STATUS(res) == TTLMAP_FOUND) {
			++found;
			assert(memcmp(&ref_value, value, sizeof(ref_value)) == 0
			);
			ttlmap_release_lock(lock);
		} else {
			assert(TTLMAP_STATUS(res) == TTLMAP_FAILED);
		}
	}
	assert(inserted == found);

	LOG(DEBUG, "print stat...");

	LOG(INFO,
	    "- Inserted: %lu/%lu entries (%.2lf%%)\n",
	    inserted,
	    kv_entries,
	    100.0 * (double)inserted / kv_entries);
	TTLMAP_PRINT_STAT(&map, test_key_t, test_value_t, STDERR_FILENO);

	TTLMAP_FREE(&map);
	assert(map.mctx.balloc_size == map.mctx.bfree_size);
}

////////////////////////////////////////////////////////////////////////////////

void
ttlmap_init_and_get_buckets_many_entries(void *memory, size_t memory_size) {
	ttlmap_init_and_get_buckets(memory, memory_size, 1000000);
}

////////////////////////////////////////////////////////////////////////////////

void
ttlmap_strike_many_entries(void *memory, size_t memory_size) {
	ttlmap_strike_entries(memory, memory_size, 1000000);
}

////////////////////////////////////////////////////////////////////////////////

int
main() {
	log_enable_name("debug");

	// buckets
	LOG(INFO, "test bucket_basic...");
	bucket_basic();

	LOG(INFO, "test bucket_multithread...");
	bucket_multithread();

	LOG(INFO, "test bucket_alignment...");
	bucket_alignment();

	LOG(INFO, "test bucket_big_alignment...");
	bucket_big_alignment();

	// ttlmap
	size_t memory_size = 1 << 30;
	void *memory = malloc(memory_size);
	LOG(INFO, "test ttlmap_init...");
	ttlmap_init(memory, memory_size, 100);

	for (size_t entries = 1; entries <= 10000;
	     entries = (size_t)((double)(entries + 1) * 1.6)) {
		LOG(INFO,
		    "test ttlmap_init_and_get_buckets [entries=%zu]...",
		    entries);
		ttlmap_init_and_get_buckets(memory, memory_size, entries);
	}

	for (size_t entries = 1; entries <= 10000;
	     entries = (size_t)((double)(entries + 1) * 1.6)) {
		LOG(INFO, "test ttlmap_strike_entries [entries=%zu]...", entries
		);
		ttlmap_strike_entries(memory, memory_size, entries);
	}

	LOG(INFO, "test ttlmap_init_and_get_buckets_many_entries...");
	ttlmap_init_and_get_buckets_many_entries(memory, memory_size);

	LOG(INFO, "test ttlmap_strike_many_entries...");
	ttlmap_strike_many_entries(memory, memory_size);

	LOG(INFO, "free memory");
	free(memory);

	LOG(INFO, "all tests have been passed");
	return 0;
}