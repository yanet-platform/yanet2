// verifies that exhausted counter arenas fail without corrupting cleanup.
//
// Poisoned single-block and zeroed multi-block sweeps reach both unsafe paths.

#include "common/asan.h"
#include "common/memory.h"
#include "common/memory_block.h"
#include "common/test_assert.h"
#include "lib/counters/counters.h"
#include "lib/errors/errors.h"
#include "lib/logging/log.h"

#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/wait.h>
#include <unistd.h>

#define ARENA_BUFFER_SIZE MEMORY_BLOCK_ALLOCATOR_MAX_ALIGN
#define MIN_SWEPT_ARENA_SIZE 256U
#define MAX_SWEPT_ARENA_SIZE (32U * 1024U)
#define SWEPT_ARENA_SIZE_STEP 8U

#define SINGLE_BLOCK_COUNTER_COUNT 1U
#define MULTI_BLOCK_COUNTER_COUNT 9U
#define EXPECTED_SINGLE_BLOCK_COUNT 1U
#define EXPECTED_MULTI_BLOCK_COUNT 2U
#define POISONED_ARENA_FILL 0xa5U
#define ZEROED_ARENA_FILL 0U
#define TEST_COUNTER_SIZE 64U

#define BLOCK_ARRAY_OOM_POINT (UINT64_C(1) << 1)
#define FIRST_BLOCK_OOM_POINT (UINT64_C(1) << 2)
#define SECOND_BLOCK_OOM_POINT (UINT64_C(1) << 4)

struct registry_fixture {
	struct block_allocator allocator;
	struct memory_context memory_context;
	struct counter_registry registry;
	void *arena;
};

// Builds a linked registry in an arena outside the allocation sweep.
static int
registry_fixture_init(struct registry_fixture *fixture, size_t counter_count) {
	memset(fixture, 0, sizeof(*fixture));
	fixture->arena = aligned_alloc(ARENA_BUFFER_SIZE, ARENA_BUFFER_SIZE);
	TEST_ASSERT_NOT_NULL(
		fixture->arena, "failed to allocate registry arena"
	);

	block_allocator_init(&fixture->allocator);
	block_allocator_put_arena(
		&fixture->allocator, fixture->arena, ARENA_BUFFER_SIZE
	);
	memory_context_init(
		&fixture->memory_context,
		"counter-registry",
		&fixture->allocator
	);
	TEST_ASSERT_SUCCESS(
		counter_registry_init(
			&fixture->registry, &fixture->memory_context, 1
		),
		"failed to initialize counter registry"
	);

	yanet_error *err = NULL;
	for (size_t idx = 0; idx < counter_count; ++idx) {
		char name[COUNTER_NAME_LEN];
		snprintf(name, sizeof(name), "counter-%zu", idx);
		TEST_ASSERT(
			counter_registry_register(
				&fixture->registry,
				name,
				TEST_COUNTER_SIZE,
				&err
			) != COUNTER_INVALID,
			"failed to register counter"
		);
	}
	TEST_ASSERT_SUCCESS(
		counter_registry_link(&fixture->registry, NULL, &err),
		"failed to link counter registry"
	);
	yanet_error_free(err);
	return TEST_SUCCESS;
}

// Releases the registry fixture and its dedicated arena.
static void
registry_fixture_fini(struct registry_fixture *fixture) {
	counter_registry_fini(&fixture->registry);
	memory_context_fini(&fixture->memory_context);
	block_allocator_fini(&fixture->allocator);
	free(fixture->arena);
}

// Checks allocation accounting and proves the restored arena accepts work.
static bool
allocator_restored_and_reusable(
	struct memory_context *memory_context,
	struct block_allocator *allocator,
	size_t initial_free_size
) {
	bool accounting_balanced =
		block_allocator_free_size(allocator) == initial_free_size &&
		memory_context->balloc_count == memory_context->bfree_count &&
		memory_context->balloc_size == memory_context->bfree_size;

	void *reuse_probe = memory_balloc(memory_context, 1);
	if (reuse_probe == NULL) {
		return false;
	}
	memory_bfree(memory_context, reuse_probe, 1);

	return accounting_balanced &&
	       block_allocator_free_size(allocator) == initial_free_size &&
	       memory_context->balloc_count == memory_context->bfree_count &&
	       memory_context->balloc_size == memory_context->bfree_size;
}

// Sweeps arena sizes and requires failed construction to leave reusable memory.
static int
run_oom_sweep(
	size_t counter_count,
	unsigned char arena_fill,
	uint64_t expected_block_count
) {
	struct registry_fixture fixture;
	TEST_ASSERT_SUCCESS(
		registry_fixture_init(&fixture, counter_count),
		"failed to initialize registry fixture"
	);

	void *arena = aligned_alloc(ARENA_BUFFER_SIZE, ARENA_BUFFER_SIZE);
	TEST_ASSERT_NOT_NULL(arena, "failed to allocate storage arena");

	bool saw_oom = false;
	bool saw_success = false;
	uint64_t observed_oom_points = 0;
	for (size_t arena_size = MIN_SWEPT_ARENA_SIZE;
	     arena_size <= MAX_SWEPT_ARENA_SIZE;
	     arena_size += SWEPT_ARENA_SIZE_STEP) {
		asan_unpoison_memory_region(arena, arena_size);
		memset(arena, arena_fill, arena_size);

		struct block_allocator allocator;
		block_allocator_init(&allocator);
		block_allocator_put_arena(&allocator, arena, arena_size);

		struct memory_context memory_context;
		memory_context_init(
			&memory_context, "counter-storage", &allocator
		);
		size_t initial_free_size =
			block_allocator_free_size(&allocator);

		struct counter_storage *storage = counter_storage_spawn(
			&memory_context, NULL, &fixture.registry
		);
		if (storage == NULL) {
			saw_oom = true;
			observed_oom_points |= UINT64_C(1)
					       << memory_context.balloc_count;
		} else {
			saw_success = true;
			TEST_ASSERT_EQUAL(
				storage->pools[COUNTER_MAX_SIZE_EXP]
					.block_count,
				expected_block_count,
				"unexpected block count"
			);
			counter_storage_free(storage);
		}

		TEST_ASSERT(
			allocator_restored_and_reusable(
				&memory_context, &allocator, initial_free_size
			),
			"allocator corrupted at arena size %zu",
			arena_size
		);
		memory_context_fini(&memory_context);
		block_allocator_fini(&allocator);
	}

	free(arena);
	registry_fixture_fini(&fixture);

	TEST_ASSERT(saw_oom, "OOM sweep did not reach an allocation failure");
	TEST_ASSERT(saw_success, "OOM sweep did not reach a success");
	TEST_ASSERT(
		observed_oom_points & BLOCK_ARRAY_OOM_POINT,
		"blocks array OOM missed"
	);
	TEST_ASSERT(
		observed_oom_points & FIRST_BLOCK_OOM_POINT,
		"first block OOM missed"
	);
	TEST_ASSERT(
		expected_block_count < EXPECTED_MULTI_BLOCK_COUNT ||
			(observed_oom_points & SECOND_BLOCK_OOM_POINT),
		"second block OOM missed"
	);
	return TEST_SUCCESS;
}

// Isolates one sweep so a crash is reported as a test failure.
static int
run_oom_scenario(
	size_t counter_count,
	unsigned char arena_fill,
	uint64_t expected_block_count
) {
	pid_t pid = fork();
	TEST_ASSERT(pid >= 0, "fork failed");
	if (pid == 0) {
		int sweep_result = run_oom_sweep(
			counter_count, arena_fill, expected_block_count
		);
		_exit(sweep_result == TEST_SUCCESS ? EXIT_SUCCESS : EXIT_FAILURE
		);
	}

	int child_status = 0;
	TEST_ASSERT(waitpid(pid, &child_status, 0) == pid, "waitpid failed");
	TEST_ASSERT(
		WIFEXITED(child_status) && WEXITSTATUS(child_status) == 0,
		"OOM sweep failed with status %d",
		child_status
	);
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("debug");

	int poisoned_single_block_result = run_oom_scenario(
		SINGLE_BLOCK_COUNTER_COUNT,
		POISONED_ARENA_FILL,
		EXPECTED_SINGLE_BLOCK_COUNT
	);
	int zeroed_multi_block_result = run_oom_scenario(
		MULTI_BLOCK_COUNTER_COUNT,
		ZEROED_ARENA_FILL,
		EXPECTED_MULTI_BLOCK_COUNT
	);
	if (poisoned_single_block_result != TEST_SUCCESS ||
	    zeroed_multi_block_result != TEST_SUCCESS) {
		return EXIT_FAILURE;
	}
	return EXIT_SUCCESS;
}
