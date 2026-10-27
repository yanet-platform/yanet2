#include "common/big_array.h"
#include "common/memory.h"
#include "common/memory_block.h"
#include "common/test_assert.h"
#include "lib/logging/log.h"

#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

// Arena large enough for an array of two full chunks and a remainder.
#define MULTI_CHUNK_ARENA_SIZE ((size_t)1 << 29)
#define SMALL_ARENA_SIZE ((size_t)1 << 26)

struct fixture {
	struct block_allocator ba;
	struct memory_context mctx;
	void *arena;
};

// Sets up a block allocator over a heap arena of the given size.
static int
setup_allocator(struct block_allocator *ba, void **raw_mem, size_t size) {
	TEST_ASSERT(
		block_allocator_init(ba) == 0, "block_allocator_init failed"
	);

	*raw_mem = malloc(size);
	TEST_ASSERT(*raw_mem != NULL, "failed to allocate raw memory");

	block_allocator_put_arena(ba, *raw_mem, size);
	return TEST_SUCCESS;
}

// Sets up an allocator and a memory context on it; fixture_fini releases both.
static int
fixture_init(struct fixture *fixture, size_t arena_size) {
	TEST_ASSERT_SUCCESS(
		setup_allocator(&fixture->ba, &fixture->arena, arena_size),
		"setup_allocator failed"
	);
	TEST_ASSERT(
		memory_context_init(
			&fixture->mctx, "big_array_test", &fixture->ba
		) == 0,
		"memory_context_init failed"
	);
	return TEST_SUCCESS;
}

static void
fixture_fini(struct fixture *fixture) {
	memory_context_fini(&fixture->mctx);
	free(fixture->arena);
}

// Returns the largest power of two that fits one allocator block, which is
// the chunk size the array must split its storage into.
static size_t
expected_chunk_size(void) {
	size_t chunk = 1;
	while (chunk * 2 <= MEMORY_BLOCK_ALLOCATOR_MAX_SIZE) {
		chunk *= 2;
	}
	return chunk;
}

// Verifies that an array of two full chunks and a remainder keeps the chunk
// layout its consumer relies on.
//
// The chunks are powers of two, each chunk is contiguous, every chunk
// boundary maps to its own byte, the last chunk takes only the remainder and
// the release returns every block.
static int
test_spans_chunks_with_remainder(void) {
	struct fixture fixture;
	TEST_ASSERT_SUCCESS(
		fixture_init(&fixture, MULTI_CHUNK_ARENA_SIZE),
		"fixture setup failed"
	);
	size_t free_before = block_allocator_free_size(&fixture.ba);

	const size_t chunk = expected_chunk_size();
	// A remainder under half a chunk takes a smaller block than a full
	// chunk, so the release check also covers the size it is freed with.
	const size_t remainder = chunk / 4 + 1;
	const size_t size = 2 * chunk + remainder;
	struct big_array array;
	TEST_ASSERT(
		big_array_init(&array, size, &fixture.mctx) == 0,
		"big_array_init failed"
	);
	TEST_ASSERT(
		((size_t)1 << array.subarray_len_exp) == chunk,
		"chunk size is %zu, expected %zu",
		(size_t)1 << array.subarray_len_exp,
		chunk
	);
	TEST_ASSERT(
		array.subarrays_count == 3,
		"expected 3 chunks, got %zu",
		array.subarrays_count
	);
	TEST_ASSERT(
		array.mctx.balloc_size == size + 3 * sizeof(void *),
		"allocated %zu bytes, expected %zu",
		(size_t)array.mctx.balloc_size,
		size + 3 * sizeof(void *)
	);

	uint8_t *first = big_array_get(&array, 0);
	uint8_t *third = big_array_get(&array, 2 * chunk);
	TEST_ASSERT(
		(uint8_t *)big_array_get(&array, chunk - 1) ==
			first + chunk - 1,
		"first chunk is not contiguous"
	);
	TEST_ASSERT(
		(uint8_t *)big_array_get(&array, size - 1) ==
			third + remainder - 1,
		"remainder chunk is not contiguous"
	);

	const size_t edges[] = {
		0, chunk - 1, chunk, 2 * chunk - 1, 2 * chunk, size - 1
	};
	const size_t edge_count = sizeof(edges) / sizeof(edges[0]);
	for (size_t idx = 0; idx < edge_count; ++idx) {
		*(uint8_t *)big_array_get(&array, edges[idx]) =
			(uint8_t)(idx + 1);
	}
	for (size_t idx = 0; idx < edge_count; ++idx) {
		uint8_t value = *(uint8_t *)big_array_get(&array, edges[idx]);
		TEST_ASSERT(
			value == (uint8_t)(idx + 1),
			"byte at index %zu reads %u, expected %u",
			edges[idx],
			value,
			(unsigned)(idx + 1)
		);
	}

	big_array_fini(&array);
	TEST_ASSERT(
		block_allocator_free_size(&fixture.ba) == free_before,
		"release did not return every block"
	);
	TEST_ASSERT(
		fixture.mctx.first_child == NULL,
		"release left the child context attached"
	);
	fixture_fini(&fixture);
	return TEST_SUCCESS;
}

// Verifies that an array of exactly two chunks takes two full chunks and
// returns both on release.
static int
test_exact_chunk_multiple(void) {
	struct fixture fixture;
	TEST_ASSERT_SUCCESS(
		fixture_init(&fixture, MULTI_CHUNK_ARENA_SIZE),
		"fixture setup failed"
	);
	size_t free_before = block_allocator_free_size(&fixture.ba);

	const size_t chunk = expected_chunk_size();
	struct big_array array;
	TEST_ASSERT(
		big_array_init(&array, 2 * chunk, &fixture.mctx) == 0,
		"big_array_init failed"
	);
	TEST_ASSERT(
		array.subarrays_count == 2,
		"expected 2 chunks, got %zu",
		array.subarrays_count
	);
	TEST_ASSERT(
		array.mctx.balloc_size == 2 * chunk + 2 * sizeof(void *),
		"allocated %zu bytes, expected %zu",
		(size_t)array.mctx.balloc_size,
		2 * chunk + 2 * sizeof(void *)
	);
	uint8_t *second = big_array_get(&array, chunk);
	TEST_ASSERT(
		(uint8_t *)big_array_get(&array, 2 * chunk - 1) ==
			second + chunk - 1,
		"last chunk is not contiguous"
	);

	big_array_fini(&array);
	TEST_ASSERT(
		block_allocator_free_size(&fixture.ba) == free_before,
		"release did not return every block"
	);
	fixture_fini(&fixture);
	return TEST_SUCCESS;
}

// Verifies that an empty array initializes without allocating anything.
static int
test_zero_size(void) {
	struct fixture fixture;
	TEST_ASSERT_SUCCESS(
		fixture_init(&fixture, SMALL_ARENA_SIZE), "fixture setup failed"
	);
	size_t free_before = block_allocator_free_size(&fixture.ba);

	struct big_array array;
	TEST_ASSERT(
		big_array_init(&array, 0, &fixture.mctx) == 0,
		"big_array_init failed for an empty array"
	);
	TEST_ASSERT(
		array.subarrays == NULL && array.subarrays_count == 0,
		"an empty array holds chunks"
	);
	TEST_ASSERT(
		block_allocator_free_size(&fixture.ba) == free_before,
		"an empty array allocated memory"
	);

	big_array_fini(&array);
	fixture_fini(&fixture);
	return TEST_SUCCESS;
}

// Verifies that releasing an array twice leaves it cleared and frees nothing
// the first release did not.
static int
test_double_fini(void) {
	struct fixture fixture;
	TEST_ASSERT_SUCCESS(
		fixture_init(&fixture, SMALL_ARENA_SIZE), "fixture setup failed"
	);
	size_t free_before = block_allocator_free_size(&fixture.ba);

	struct big_array array;
	TEST_ASSERT(
		big_array_init(&array, 1000, &fixture.mctx) == 0,
		"big_array_init failed"
	);
	big_array_fini(&array);
	big_array_fini(&array);
	TEST_ASSERT(
		array.subarrays == NULL && array.subarrays_count == 0,
		"a released array is not cleared"
	);
	TEST_ASSERT(
		block_allocator_free_size(&fixture.ba) == free_before,
		"releasing twice changed the free memory"
	);

	fixture_fini(&fixture);
	return TEST_SUCCESS;
}

// Verifies that exhausted arenas leave no owned blocks or child context.
static int
test_init_allocation_failure(size_t arena_size) {
	struct block_allocator ba;
	void *raw_mem = NULL;
	if (arena_size != 0) {
		TEST_ASSERT(
			setup_allocator(&ba, &raw_mem, arena_size) ==
				TEST_SUCCESS,
			"setup_allocator failed"
		);
	} else {
		TEST_ASSERT(
			block_allocator_init(&ba) == 0, "allocator init failed"
		);
	}

	struct memory_context mctx;
	memory_context_init(&mctx, "big_array_allocation_failure", &ba);
	size_t free_before = block_allocator_free_size(&ba);
	struct big_array array;
	memset(&array, 0xa5, sizeof(array));

	int res = big_array_init(&array, 4096, &mctx);
	bool cleared = array.subarrays == NULL && array.subarrays_count == 0 &&
		       array.size == 0;
	bool detached = mctx.first_child == NULL;
	size_t free_after = block_allocator_free_size(&ba);
	size_t pointer_pool = block_allocator_pool_index(
		&ba, sizeof(void *) + 2 * ASAN_RED_ZONE
	);
	uint64_t metadata_allocations = ba.pools[pointer_pool].allocate;
	big_array_fini(&array);
	memory_context_fini(&mctx);
	free(raw_mem);

	TEST_ASSERT(
		res == -1, "initialization should fail with exhausted arena"
	);
	TEST_ASSERT(cleared, "failed initialization should clear array state");
	TEST_ASSERT(
		detached, "failed initialization should detach child context"
	);
	TEST_ASSERT(free_after == free_before, "partial allocations leaked");
	TEST_ASSERT(
		metadata_allocations == (arena_size != 0 ? 1u : 0u),
		"unexpected number of successful metadata allocations"
	);
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("info");

	if (test_init_allocation_failure(0) != TEST_SUCCESS) {
		LOG(ERROR, "first allocation failure cleanup failed");
		return -1;
	}

	if (test_init_allocation_failure(1024) != TEST_SUCCESS) {
		LOG(ERROR, "later allocation failure cleanup failed");
		return -1;
	}

	if (test_spans_chunks_with_remainder() != TEST_SUCCESS) {
		LOG(ERROR, "test_spans_chunks_with_remainder failed");
		return -1;
	}

	if (test_exact_chunk_multiple() != TEST_SUCCESS) {
		LOG(ERROR, "test_exact_chunk_multiple failed");
		return -1;
	}

	if (test_zero_size() != TEST_SUCCESS) {
		LOG(ERROR, "test_zero_size failed");
		return -1;
	}

	if (test_double_fini() != TEST_SUCCESS) {
		LOG(ERROR, "test_double_fini failed");
		return -1;
	}

	LOG(INFO, "All big_array tests passed!");
	return 0;
}
