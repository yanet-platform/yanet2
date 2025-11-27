#include "common/interval_counter.h"
#include "common/memory.h"
#include "common/memory_block.h"
#include "lib/logging/log.h"
#include "modules/pdump/tests/helpers.h"
#include <assert.h>
#include <stdlib.h>

////////////////////////////////////////////////////////////////////////////////

struct stupid {
	int64_t *count;
	uint64_t max_time;
	uint64_t now;
	struct memory_context *mctx;
};

int
stupid_init(
	struct stupid *stupid,
	struct memory_context *mctx,
	uint64_t now,
	uint64_t max_time
) {
	stupid->count = memory_balloc(mctx, (max_time + 1) * sizeof(int64_t));
	TEST_ASSERT_NOT_NULL(stupid->count, "failed to create count array");
	memset(stupid->count, 0, (max_time + 1) * sizeof(int64_t));
	stupid->max_time = max_time;
	stupid->now = now;
	return TEST_SUCCESS;
}

static inline void
stupid_advance_time(struct stupid *stupid, uint32_t to) {
	assert(to <= stupid->max_time);
	stupid->now = to;
}

static inline uint64_t
stupid_current_count(struct stupid *stupid) {
	return stupid->count[stupid->now];
}

static inline void
stupid_put(
	struct stupid *stupid, uint32_t from, uint32_t timeout, int32_t cnt
) {
	for (uint64_t i = from; i < from + timeout; ++i) {
		stupid->count[i] += cnt;
	}
}

////////////////////////////////////////////////////////////////////////////////

#define ARENA_SIZE (1 << 20)

int
stress(void *arena, uint64_t now, uint64_t max_time, uint64_t max_timeout) {
	struct block_allocator alloc;
	block_allocator_init(&alloc);
	block_allocator_put_arena(&alloc, arena, ARENA_SIZE);
	struct memory_context mctx;
	memory_context_init(&mctx, "test", &alloc);
	struct stupid stupid;
	stupid_init(&stupid, &mctx, now, max_time);
	struct interval_counter counter;
	int res = interval_counter_init(&counter, now, max_timeout, &mctx);
	TEST_ASSERT_EQUAL(res, 0, "failed to init interval counter");
	(void)stupid_put;
	(void)stupid_current_count;
	(void)stupid_advance_time;
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////

int
basic(void *arena) {
	struct block_allocator alloc;
	block_allocator_init(&alloc);
	block_allocator_put_arena(&alloc, arena, ARENA_SIZE);
	struct memory_context mctx;
	memory_context_init(&mctx, "test", &alloc);
	struct interval_counter counter;
	int res = interval_counter_init(&counter, 0, 100, &mctx);
	TEST_ASSERT_EQUAL(res, 0, "failed to init interval counter");
	interval_counter_advance_time(&counter, 1000);
	interval_counter_put(&counter, 1002, 60, 1);
	interval_counter_advance_time(&counter, 1002);
	uint64_t cur = interval_counter_current_count(&counter);
	TEST_ASSERT_EQUAL(cur, 1, "must be one interval");
	interval_counter_advance_time(&counter, 1010);
	cur = interval_counter_current_count(&counter);
	TEST_ASSERT_EQUAL(cur, 1, "must be one interval");
	interval_counter_advance_time(&counter, 1062);
	cur = interval_counter_current_count(&counter);
	TEST_ASSERT_EQUAL(cur, 0, "interval ended here");
	interval_counter_free(&counter);
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////

int
main() {
	log_enable_name("debug");
	void *arena = malloc(ARENA_SIZE);

	LOG(INFO, "running test 'basic'...");
	int test_basic_result = basic(arena);
	TEST_ASSERT_EQUAL(
		test_basic_result, TEST_SUCCESS, "test 'basic' failed"
	);
	free(arena);

	LOG(INFO, "all tests succeeded");

	return 0;
}