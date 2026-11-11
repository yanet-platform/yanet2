/*
 * Registry lifecycle across configuration replacements.
 *
 * A replacement must not remember names forever: a registry keeps the
 * counters of the configuration it replaced for one further replacement
 * and then releases their value slots for reuse, so a configuration whose
 * counter names change over time keeps a bounded registry and storage.
 * The scenarios drive register, link and storage spawn in the order a
 * module replacement does, write counter values through the storages as
 * workers would, and pin the release, the reuse and the value continuity.
 */

#include "common/memory.h"
#include "common/memory_block.h"
#include "common/test_assert.h"
#include "lib/counters/counters.h"
#include "lib/errors/errors.h"

#include <inttypes.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define ARENA_SIZE (64u * 1024u * 1024u)

struct registry_fixture {
	struct block_allocator allocator;
	// Stands for the memory of the module owning the registry.
	struct memory_context registry_context;
	// Stands for the shared per-worker counter storage context.
	struct memory_context storage_context;
	void *arena;
};

static int
registry_fixture_init(struct registry_fixture *fixture) {
	memset(fixture, 0, sizeof(*fixture));
	fixture->arena =
		aligned_alloc(MEMORY_BLOCK_ALLOCATOR_MAX_ALIGN, ARENA_SIZE);
	TEST_ASSERT_NOT_NULL(fixture->arena, "failed to allocate arena");

	block_allocator_init(&fixture->allocator);
	block_allocator_put_arena(
		&fixture->allocator, fixture->arena, ARENA_SIZE
	);
	memory_context_init(
		&fixture->registry_context, "registry-test", &fixture->allocator
	);
	memory_context_init(
		&fixture->storage_context, "storage-test", &fixture->allocator
	);
	return TEST_SUCCESS;
}

static void
registry_fixture_fini(struct registry_fixture *fixture) {
	memory_context_fini(&fixture->storage_context);
	memory_context_fini(&fixture->registry_context);
	block_allocator_fini(&fixture->allocator);
	free(fixture->arena);
}

// One configuration generation: its registry and the storage its workers
// count through.
struct generation {
	struct counter_registry *registry;
	struct counter_storage *storage;
};

static void
generation_name(char *buffer, uint64_t idx) {
	snprintf(buffer, COUNTER_NAME_LEN, "c%" PRIu64, idx);
}

// Replaces the current generation with one that registers `fixed`
// permanent counters and a window of `window` counters starting at
// `first`, linking to the current registry and spawning from the current
// storage as a module replacement does.
static int
generation_replace(
	struct registry_fixture *fixture,
	uint64_t fixed,
	uint64_t first,
	uint64_t window,
	struct generation *current
) {
	struct counter_registry *registry = calloc(1, sizeof(*registry));
	TEST_ASSERT_NOT_NULL(registry, "failed to allocate registry");
	TEST_ASSERT_SUCCESS(
		counter_registry_init(registry, &fixture->registry_context),
		"failed to init registry"
	);

	char name[COUNTER_NAME_LEN];
	yanet_error *err = NULL;
	for (uint64_t idx = 0; idx < fixed; ++idx) {
		snprintf(name, COUNTER_NAME_LEN, "own%" PRIu64, idx);
		TEST_ASSERT(
			counter_registry_register(registry, name, 2, &err) !=
				COUNTER_INVALID,
			"failed to register permanent counter"
		);
	}
	for (uint64_t idx = first; idx < first + window; ++idx) {
		generation_name(name, idx);
		TEST_ASSERT(
			counter_registry_register(registry, name, 2, &err) !=
				COUNTER_INVALID,
			"failed to register window counter"
		);
	}

	TEST_ASSERT_SUCCESS(
		counter_registry_link(registry, current->registry, &err),
		"failed to link registry"
	);

	struct counter_storage *storage = counter_storage_spawn(
		&fixture->storage_context, current->storage, registry
	);
	TEST_ASSERT_NOT_NULL(storage, "failed to spawn counter storage");

	// The replaced storage reads its registry on release, so it goes
	// first.
	counter_storage_free(current->storage);
	if (current->registry != NULL) {
		counter_registry_fini(current->registry);
		free(current->registry);
	}

	current->registry = registry;
	current->storage = storage;
	return TEST_SUCCESS;
}

static void
generation_retire(struct generation *generation) {
	counter_storage_free(generation->storage);
	if (generation->registry != NULL) {
		counter_registry_fini(generation->registry);
		free(generation->registry);
	}
	memset(generation, 0, sizeof(*generation));
}

// Resolves a counter of the current generation to its value slot.
static uint64_t *
counter_value_of(struct generation *generation, const char *name) {
	uint64_t idx =
		counter_registry_lookup_index(generation->registry, name);
	if (idx == COUNTER_INVALID) {
		return NULL;
	}
	return counter_handle_get_value(
		counter_get_value_handle(idx, generation->storage)
	);
}

static int
counter_value(
	struct generation *generation, const char *name, uint64_t *value
) {
	uint64_t *slot = counter_value_of(generation, name);
	TEST_ASSERT_NOT_NULL(slot, "counter '%s' not in registry", name);
	*value = slot[0];
	return TEST_SUCCESS;
}

static int
counter_value_add(
	struct generation *generation, const char *name, uint64_t add
) {
	uint64_t *slot = counter_value_of(generation, name);
	TEST_ASSERT_NOT_NULL(slot, "counter '%s' not in registry", name);
	slot[0] += add;
	return TEST_SUCCESS;
}

static int
counter_slot_offset(
	struct generation *generation, const char *name, uint64_t *offset
) {
	uint64_t idx =
		counter_registry_lookup_index(generation->registry, name);
	TEST_ASSERT(
		idx != COUNTER_INVALID, "counter '%s' not in registry", name
	);
	struct counter *counter = ADDR_OF(&generation->registry->names) + idx;
	*offset = counter->offset;
	return TEST_SUCCESS;
}

// verifies that a registry forgets names the configuration dropped and
// stays bounded while every replacement renames every window counter
static int
run_renamed_counters_released_test(void) {
	struct registry_fixture fixture;
	TEST_ASSERT_SUCCESS(
		registry_fixture_init(&fixture), "fixture init failed"
	);

	struct generation current = {0};
	TEST_ASSERT_SUCCESS(
		generation_replace(&fixture, 5, 0, 256, &current),
		"first replacement failed"
	);

	// The window slides by its whole length: every replacement renames
	// every window counter, the heaviest churn a registry can see.
	for (uint64_t gen_idx = 1; gen_idx < 32; ++gen_idx) {
		TEST_ASSERT_SUCCESS(
			generation_replace(
				&fixture, 5, gen_idx * 256, 256, &current
			),
			"replacement %" PRIu64 " failed",
			gen_idx
		);

		// The registry holds its own counters plus the unreplaced ones
		// of the configuration it replaced: the permanent counters, the
		// current window and the replaced window.
		TEST_ASSERT_EQUAL(
			current.registry->count,
			5 + 2 * 256,
			"registry not bounded to own and replaced names at "
			"generation %" PRIu64,
			gen_idx
		);
	}

	generation_retire(&current);
	registry_fixture_fini(&fixture);
	return TEST_SUCCESS;
}

// verifies that many distinct names through one configuration never
// refuse a replacement while the current names fit
static int
run_many_distinct_names_accepted_test(void) {
	struct registry_fixture fixture;
	TEST_ASSERT_SUCCESS(
		registry_fixture_init(&fixture), "fixture init failed"
	);

	struct generation current = {0};
	TEST_ASSERT_SUCCESS(
		generation_replace(&fixture, 5, 0, 4096, &current),
		"first replacement failed"
	);

	// More distinct names than a registry can hold at once; without the
	// release this sequence refuses its 64th replacement.
	for (uint64_t gen_idx = 1; gen_idx < 66; ++gen_idx) {
		TEST_ASSERT_SUCCESS(
			generation_replace(
				&fixture, 5, gen_idx * 4096, 4096, &current
			),
			"replacement %" PRIu64 " failed",
			gen_idx
		);
	}

	TEST_ASSERT_EQUAL(
		current.registry->count,
		5 + 2 * 4096,
		"registry did not settle at own and replaced names"
	);

	generation_retire(&current);
	registry_fixture_fini(&fixture);
	return TEST_SUCCESS;
}

// verifies that a released slot serves a new counter from zero while a
// kept counter keeps its values, including counts written through the
// replaced storages
static int
run_released_slot_reused_from_zero_test(void) {
	struct registry_fixture fixture;
	TEST_ASSERT_SUCCESS(
		registry_fixture_init(&fixture), "fixture init failed"
	);

	struct generation current = {0};
	uint64_t value = 0;
	uint64_t first_slot = 0;
	uint64_t second_slot = 0;
	TEST_ASSERT_SUCCESS(
		generation_replace(&fixture, 0, 0, 2, &current),
		"first replacement failed"
	);
	TEST_ASSERT_SUCCESS(
		counter_slot_offset(&current, "c0", &first_slot),
		"failed to read the first slot"
	);
	TEST_ASSERT_SUCCESS(
		counter_slot_offset(&current, "c1", &second_slot),
		"failed to read the second slot"
	);
	counter_value_of(&current, "c0")[0] = 11;
	counter_value_of(&current, "c1")[0] = 22;

	// Drop c0: it survives one replacement so its slot stays reserved.
	TEST_ASSERT_SUCCESS(
		generation_replace(&fixture, 0, 1, 2, &current),
		"second replacement failed"
	);
	TEST_ASSERT_SUCCESS(
		counter_value(&current, "c1", &value),
		"failed to read the kept counter"
	);
	TEST_ASSERT_EQUAL(
		value, 22, "kept counter lost its value across the replacement"
	);
	TEST_ASSERT_SUCCESS(
		counter_slot_offset(&current, "c0", &value),
		"failed to read the dropped counter's slot"
	);
	TEST_ASSERT_EQUAL(
		value,
		first_slot,
		"dropped counter did not hold its slot through the grace "
		"replacement"
	);

	// A worker of the replaced generation may keep counting into the
	// dropped slot until this second replacement begins.
	TEST_ASSERT_SUCCESS(
		counter_value_add(&current, "c0", 5),
		"failed to count into the dropped counter"
	);

	// c0 leaves the registry; the slot it held through its grace
	// replacement serves the new counter from zero.
	TEST_ASSERT_SUCCESS(
		generation_replace(&fixture, 0, 2, 2, &current),
		"third replacement failed"
	);
	TEST_ASSERT_SUCCESS(
		counter_slot_offset(&current, "c3", &value),
		"failed to read the new counter's slot"
	);
	TEST_ASSERT_EQUAL(
		value,
		first_slot,
		"released slot was not reused for the new counter"
	);
	TEST_ASSERT_SUCCESS(
		counter_value(&current, "c3", &value),
		"failed to read the counter on the released slot"
	);
	TEST_ASSERT_EQUAL(
		value, 0, "counter on the released slot did not start from zero"
	);
	TEST_ASSERT_SUCCESS(
		counter_value(&current, "c1", &value),
		"failed to read the kept counter"
	);
	TEST_ASSERT_EQUAL(
		value,
		22,
		"kept counter lost its value across the second replacement"
	);

	// The carried c1 ages out while holding a non-zero value; the
	// counter that takes its slot starts from zero despite it.
	TEST_ASSERT_SUCCESS(
		counter_value_add(&current, "c1", 7),
		"failed to count into the carried counter"
	);
	TEST_ASSERT_SUCCESS(
		generation_replace(&fixture, 0, 3, 2, &current),
		"fourth replacement failed"
	);
	TEST_ASSERT_SUCCESS(
		counter_slot_offset(&current, "c4", &value),
		"failed to read the second new counter's slot"
	);
	TEST_ASSERT_EQUAL(
		value,
		second_slot,
		"second released slot was not reused for the new counter"
	);
	TEST_ASSERT_SUCCESS(
		counter_value(&current, "c4", &value),
		"failed to read the counter on the second released slot"
	);
	TEST_ASSERT_EQUAL(
		value,
		0,
		"counter on the second released slot did not start from zero"
	);

	generation_retire(&current);
	registry_fixture_fini(&fixture);
	return TEST_SUCCESS;
}

int
main(void) {
	int results[] = {
		run_renamed_counters_released_test(),
		run_many_distinct_names_accepted_test(),
		run_released_slot_reused_from_zero_test(),
	};
	for (size_t idx = 0; idx < sizeof(results) / sizeof(results[0]);
	     ++idx) {
		if (results[idx] != TEST_SUCCESS) {
			return EXIT_FAILURE;
		}
	}
	return EXIT_SUCCESS;
}
