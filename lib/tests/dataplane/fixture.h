#pragma once

#include <stdlib.h>
#include <string.h>

#include "common/memory.h"
#include "common/memory_address.h"
#include "common/memory_block.h"
#include "common/test_assert.h"

#include "lib/dataplane/config/zone.h"

// Arena size backing every dp_config test fixture in this directory.
#define DATAPLANE_TEST_ARENA_SIZE (1 << 20)

// A dp_config wired up with an arena-backed memory context and a
// dp_topology.devices array of device_count slots.
struct dataplane_test_fixture {
	void *arena;
	struct block_allocator block_allocator;
	struct dp_config dp_config;
};

static inline int
dataplane_test_fixture_init(
	struct dataplane_test_fixture *fixture,
	const char *name,
	uint64_t device_count
) {
	struct dp_config *cfg = &fixture->dp_config;
	memset(cfg, 0, sizeof(*cfg));

	fixture->arena = malloc(DATAPLANE_TEST_ARENA_SIZE);
	TEST_ASSERT_NOT_NULL(fixture->arena, "failed to allocate arena");
	block_allocator_init(&fixture->block_allocator);
	block_allocator_put_arena(
		&fixture->block_allocator,
		fixture->arena,
		DATAPLANE_TEST_ARENA_SIZE
	);
	memory_context_init(
		&cfg->memory_context, name, &fixture->block_allocator
	);
	TEST_ASSERT_NOT_NULL(
		dp_topology_alloc_devices(cfg, device_count),
		"alloc_devices failed"
	);

	return TEST_SUCCESS;
}

static inline void
dataplane_test_fixture_fini(struct dataplane_test_fixture *fixture) {
	free(fixture->arena);
}
