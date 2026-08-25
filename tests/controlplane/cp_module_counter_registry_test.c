/*
 * Regression test for GH#2159: growing a module's runtime counter
 * registry array must never move a populated registry.
 *
 * A counter registry embeds shared-memory offsets relative to its own
 * address, so a registry whose bytes are copied to a new array slot
 * resolves its names and string index to shifted, bogus targets. The
 * growth path therefore allocates each registry out-of-line and
 * reallocates only the array of pointers to them; these scenarios drive
 * a second tag through that growth and then keep using the first
 * registry, including under swept arena exhaustion. The exhaustion
 * sweep needs a private allocator per round, fork isolation and
 * free-size introspection, which the shared in-process harnesses
 * cannot provide, hence a plain C test.
 */

#include "common/asan.h"
#include "common/memory.h"
#include "common/memory_block.h"
#include "common/test_assert.h"

#include "lib/controlplane/config/cp_module.h"
#include "lib/counters/counters.h"
#include "lib/errors/errors.h"

#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/wait.h>
#include <unistd.h>

#define ARENA_BUFFER_SIZE MEMORY_BLOCK_ALLOCATOR_MAX_ALIGN
#define FULL_ARENA_SIZE (64u * 1024u)
#define SWEEP_MIN_ARENA_SIZE 256u
#define SWEEP_MAX_ARENA_SIZE (16u * 1024u)
#define SWEEP_STEP 8u

// Builds a module whose runtime registry allocations come from a fresh
// poisoned arena of the given size carved out of arena_buffer.
static void
module_init(
	struct cp_module *module,
	struct block_allocator *allocator,
	void *arena_buffer,
	size_t arena_size
) {
	memset(module, 0, sizeof(*module));
	// Round N+1 reuses a buffer the previous round's frees left
	// poisoned; lift the poison before overwriting it.
	asan_unpoison_memory_region(arena_buffer, arena_size);
	memset(arena_buffer, 0xa5, arena_size);
	block_allocator_init(allocator);
	block_allocator_put_arena(allocator, arena_buffer, arena_size);
	memory_context_init(
		&module->memory_context, "cp-module-test", allocator
	);
	strtcpy(module->type, "test", sizeof(module->type));
	strtcpy(module->name, "test0", sizeof(module->name));
}

// Tears the module down and requires the arena to come back whole.
//
// Every block the module and its registries borrowed must return, so
// the restored free size catches a growth or failure path that strands
// an allocation; the context's own counters are unreadable here because
// the teardown zeroes them.
static int
module_fini(
	struct cp_module *module,
	struct block_allocator *allocator,
	size_t initial_free_size
) {
	cp_module_fini(module);

	TEST_ASSERT_EQUAL(
		block_allocator_free_size(allocator),
		initial_free_size,
		"arena not fully restored after teardown"
	);
	block_allocator_fini(allocator);
	return TEST_SUCCESS;
}

// verifies that a registry registered before an array growth keeps
// resolving counters and backing per-worker storage afterwards
static int
run_first_registry_survives_growth_test(void) {
	void *arena_buffer =
		aligned_alloc(ARENA_BUFFER_SIZE, ARENA_BUFFER_SIZE);
	TEST_ASSERT_NOT_NULL(arena_buffer, "failed to allocate arena buffer");

	struct block_allocator allocator;
	struct cp_module module;
	module_init(&module, &allocator, arena_buffer, FULL_ARENA_SIZE);
	size_t initial_free_size = block_allocator_free_size(&allocator);

	yanet_error *err = NULL;

	uint64_t first_idx = UINT64_MAX;
	struct counter_registry *first =
		cp_module_counter_registry(&module, "first", &first_idx, &err);
	TEST_ASSERT_NOT_NULL(first, "first tag registration failed");
	TEST_ASSERT_EQUAL(first_idx, 0, "unexpected first registry index");

	uint64_t c0 = counter_registry_register(first, "c0", 2, &err);
	TEST_ASSERT(c0 != COUNTER_INVALID, "c0 registration failed");

	uint64_t second_idx = UINT64_MAX;
	struct counter_registry *second = cp_module_counter_registry(
		&module, "second", &second_idx, &err
	);
	TEST_ASSERT_NOT_NULL(second, "second tag registration failed");
	TEST_ASSERT_EQUAL(second_idx, 1, "unexpected second registry index");

	// The first registry must resolve through its own offsets after the
	// array growth: re-registering the pre-growth counter resolves to
	// its existing id, and a fresh counter goes through the same names
	// and string index.
	TEST_ASSERT(
		counter_registry_register(first, "c0", 2, &err) == c0,
		"pre-growth counter no longer resolves"
	);
	uint64_t c1 = counter_registry_register(first, "c1", 2, &err);
	TEST_ASSERT(c1 != COUNTER_INVALID, "c1 registration failed");

	TEST_ASSERT_SUCCESS(
		counter_registry_link(first, NULL, &err),
		"failed to link the first registry"
	);

	struct counter_storage *storage =
		counter_storage_spawn(&module.memory_context, NULL, first);
	TEST_ASSERT_NOT_NULL(
		storage, "failed to spawn storage for the first registry"
	);

	uint64_t *c0_value =
		counter_handle_get_value(counter_get_value_handle(c0, storage));
	uint64_t *c1_value =
		counter_handle_get_value(counter_get_value_handle(c1, storage));
	*c0_value += 1;
	*c1_value += 2;
	TEST_ASSERT_EQUAL(*c0_value, 1, "c0 storage round-trip failed");
	TEST_ASSERT_EQUAL(*c1_value, 2, "c1 storage round-trip failed");

	counter_storage_free(storage);

	uint64_t idx = UINT64_MAX;
	TEST_ASSERT(
		cp_module_counter_registry(&module, "first", &idx, &err) ==
				first &&
			idx == 0,
		"first tag no longer resolves to its registry"
	);
	TEST_ASSERT(
		cp_module_counter_registry(&module, "second", &idx, &err) ==
				second &&
			idx == 1,
		"second tag no longer resolves to its registry"
	);

	int fini_result = module_fini(&module, &allocator, initial_free_size);
	free(arena_buffer);
	return fini_result;
}

enum sweep_outcome {
	OUTCOME_FIRST_FAILED,
	OUTCOME_GROWTH_FAILED,
	OUTCOME_GROWN,
};

// One exhaustion round: registers the first tag and one counter, then
// drives the array growth, and checks whichever state the arena leaves.
static int
run_sweep_round(
	void *arena_buffer, size_t arena_size, enum sweep_outcome *outcome
) {
	struct block_allocator allocator;
	struct cp_module module;
	module_init(&module, &allocator, arena_buffer, arena_size);
	size_t initial_free_size = block_allocator_free_size(&allocator);

	yanet_error *err = NULL;

	struct counter_registry *first =
		cp_module_counter_registry(&module, "first", NULL, &err);
	// The first counter registered on a fresh registry always gets id
	// zero; anything else means the registry resolves to the wrong
	// arena address.
	if (first == NULL ||
	    counter_registry_register(first, "c0", 2, &err) != 0) {
		*outcome = OUTCOME_FIRST_FAILED;
		return module_fini(&module, &allocator, initial_free_size);
	}

	struct counter_registry *second =
		cp_module_counter_registry(&module, "second", NULL, &err);
	if (second == NULL) {
		*outcome = OUTCOME_GROWTH_FAILED;

		// The failed growth must leave the previous array in place:
		// the first registry still answers by tag and keeps resolving
		// and re-registering its counter through its own offsets.
		uint64_t idx = UINT64_MAX;
		TEST_ASSERT(
			cp_module_counter_registry(
				&module, "first", &idx, &err
			) == first &&
				idx == 0,
			"first registry lost after a failed growth at arena "
			"size %zu",
			arena_size
		);
		TEST_ASSERT(
			counter_registry_register(first, "c0", 2, &err) == 0,
			"counter lookup broke after a failed growth at arena "
			"size %zu",
			arena_size
		);
		TEST_ASSERT(
			counter_registry_register(first, "c0", 2, &err) == 0,
			"counter re-registration broke after a failed growth "
			"at arena size %zu",
			arena_size
		);
	} else {
		*outcome = OUTCOME_GROWN;
	}

	return module_fini(&module, &allocator, initial_free_size);
}

// verifies that an allocation failure while growing the registry array
// leaves the previous array intact, usable and fully reclaimable
static int
run_growth_failure_sweep_test(void) {
	void *arena_buffer =
		aligned_alloc(ARENA_BUFFER_SIZE, ARENA_BUFFER_SIZE);
	TEST_ASSERT_NOT_NULL(arena_buffer, "failed to allocate arena buffer");

	bool saw_growth_failed = false;
	bool saw_grown = false;

	for (size_t arena_size = SWEEP_MIN_ARENA_SIZE;
	     arena_size <= SWEEP_MAX_ARENA_SIZE;
	     arena_size += SWEEP_STEP) {
		enum sweep_outcome outcome;
		TEST_ASSERT_SUCCESS(
			run_sweep_round(arena_buffer, arena_size, &outcome),
			"sweep round failed at arena size %zu",
			arena_size
		);
		saw_growth_failed |= outcome == OUTCOME_GROWTH_FAILED;
		saw_grown |= outcome == OUTCOME_GROWN;
	}

	free(arena_buffer);

	TEST_ASSERT(
		saw_growth_failed,
		"sweep never hit an allocation failure during growth"
	);
	TEST_ASSERT(saw_grown, "sweep never grew the registry array");
	return TEST_SUCCESS;
}

// Isolates one scenario so a crash is reported as a test failure.
static int
run_isolated(int (*scenario)(void)) {
	pid_t pid = fork();
	TEST_ASSERT(pid >= 0, "fork failed");
	if (pid == 0) {
		_exit(scenario() == TEST_SUCCESS ? EXIT_SUCCESS : EXIT_FAILURE);
	}

	int child_status = 0;
	TEST_ASSERT(waitpid(pid, &child_status, 0) == pid, "waitpid failed");
	TEST_ASSERT(
		WIFEXITED(child_status) && WEXITSTATUS(child_status) == 0,
		"scenario failed with status %d",
		child_status
	);
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("debug");

	if (run_isolated(run_first_registry_survives_growth_test) !=
	    TEST_SUCCESS) {
		return EXIT_FAILURE;
	}
	if (run_isolated(run_growth_failure_sweep_test) != TEST_SUCCESS) {
		return EXIT_FAILURE;
	}
	return EXIT_SUCCESS;
}
