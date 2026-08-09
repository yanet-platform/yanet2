// Lifetime test for fwstate_map_object: create, create_map, table
// accessor, idempotent fini, and NULL-safe free.
//
// Under ASan+UBSan (build-asan), any use-after-free or heap corruption
// during the create -> create_map -> free-object sequence is caught
// immediately.

#include "common/container_of.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "common/test_assert.h"

#include "controlplane/agent/agent.h"
#include "controlplane/config/zone.h"
#include "dataplane/config/zone.h"
#include "lib/dataplane/object/object.h"
#include "lib/errors/errors.h"
#include "lib/fwstate/fwmap.h"
#include "lib/fwstate/fwtable.h"

#include "modules/fwstate/objects/fwstate_map_object.h"

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#define DP_MEMORY (1 << 22)
#define CP_MEMORY (1 << 22)
#define MAP_NAME_V4 "test-map-v4"
#define MAP_NAME_V6 "test-map-v6"
#define WORKER_COUNT 1
#define INDEX_SIZE 1024
#define EXTRA_BUCKETS 64

static struct agent test_agent;

static void *
build_stack(void) {
	void *storage = aligned_alloc(64, DP_MEMORY + CP_MEMORY);
	memset(storage, 0, DP_MEMORY + CP_MEMORY);

	struct dp_config *dp = (struct dp_config *)storage;
	block_allocator_init(&dp->block_allocator);
	block_allocator_put_arena(
		&dp->block_allocator,
		storage + sizeof(struct dp_config),
		DP_MEMORY - sizeof(struct dp_config)
	);
	memory_context_init(&dp->memory_context, "dp", &dp->block_allocator);
	dp->module_count = 0;
	dp->dp_modules = NULL;

	// Register the fwstate_map_v4 and fwstate_map_v6 object types so
	// cp_object_init can resolve them via dp_config_lookup_object.
	struct dp_object *dp_objects = (struct dp_object *)memory_balloc(
		&dp->memory_context, 2 * sizeof(struct dp_object)
	);
	if (dp_objects == NULL) {
		free(storage);
		return NULL;
	}
	strtcpy(dp_objects[0].name,
		FWSTATE_MAP_V4_OBJECT_TYPE,
		sizeof(dp_objects[0].name));
	strtcpy(dp_objects[1].name,
		FWSTATE_MAP_V6_OBJECT_TYPE,
		sizeof(dp_objects[1].name));
	SET_OFFSET_OF(&dp->dp_objects, dp_objects);
	dp->object_count = 2;

	struct cp_config *cp =
		(struct cp_config *)((uintptr_t)storage + DP_MEMORY);
	block_allocator_init(&cp->block_allocator);
	block_allocator_put_arena(
		&cp->block_allocator,
		storage + DP_MEMORY + sizeof(struct cp_config),
		CP_MEMORY - sizeof(struct cp_config)
	);
	memory_context_init(&cp->memory_context, "cp", &cp->block_allocator);

	SET_OFFSET_OF(&dp->cp_config, cp);
	SET_OFFSET_OF(&cp->dp_config, dp);

	memset(&test_agent, 0, sizeof(test_agent));
	memory_context_init_from(
		&test_agent.memory_context, &cp->memory_context, "test-agent"
	);
	SET_OFFSET_OF(&test_agent.dp_config, dp);
	SET_OFFSET_OF(&test_agent.cp_config, cp);

	yanet_error *err = NULL;
	struct cp_config_gen *gen = cp_config_gen_new(&test_agent, &err);
	if (gen == NULL) {
		yanet_error_free(err);
		free(storage);
		return NULL;
	}
	yanet_error_free(err);

	return storage;
}

// Create a fwstate_map_object of the requested kind with one initial
// layer. Returns the cp_object pointer or NULL on failure.
static struct cp_object *
create_named_object(
	struct agent *agent,
	const char *type,
	const char *name,
	enum fwtable_kind kind
) {
	yanet_error *err = NULL;
	struct cp_object *obj =
		fwstate_map_object_config_new(agent, type, name, kind, &err);
	yanet_error_free(err);
	if (obj == NULL) {
		return NULL;
	}

	struct fwstate_map_object *map_obj =
		container_of(obj, struct fwstate_map_object, cp_object);
	if (fwstate_map_object_create_map(
		    map_obj, INDEX_SIZE, EXTRA_BUCKETS, WORKER_COUNT
	    ) != 0) {
		fwstate_map_object_config_free(obj);
		return NULL;
	}
	return obj;
}

// Test 1: a v4 and v6 object are created, their tables are populated,
// and freeing them cleans up the table chain.
static int
test_create_and_populate(void) {
	struct agent *agent = &test_agent;

	struct cp_object *v4_obj = create_named_object(
		agent, FWSTATE_MAP_V4_OBJECT_TYPE, MAP_NAME_V4, FWTABLE_KIND_V4
	);
	TEST_ASSERT_NOT_NULL(v4_obj, "failed to create v4 object");

	struct cp_object *v6_obj = create_named_object(
		agent, FWSTATE_MAP_V6_OBJECT_TYPE, MAP_NAME_V6, FWTABLE_KIND_V6
	);
	TEST_ASSERT_NOT_NULL(v6_obj, "failed to create v6 object");

	fwtable_t *v4_table = fwstate_map_object_table(v4_obj);
	fwtable_t *v6_table = fwstate_map_object_table(v6_obj);
	TEST_ASSERT_NOT_NULL(v4_table, "v4 table must resolve");
	TEST_ASSERT_NOT_NULL(v6_table, "v6 table must resolve");

	fwmap_t *fw4 = ADDR_OF(&v4_table->head);
	fwmap_t *fw6 = ADDR_OF(&v6_table->head);
	TEST_ASSERT_NOT_NULL(fw4, "v4 head fwmap must resolve");
	TEST_ASSERT_NOT_NULL(fw6, "v6 head fwmap must resolve");

	struct fwmap_stats stats4 = fwmap_get_stats(fw4);
	TEST_ASSERT(
		stats4.memory_used > 0, "v4 fwmap memory_used must be nonzero"
	);

	fwstate_map_object_config_free(v4_obj);
	fwstate_map_object_config_free(v6_obj);
	return TEST_SUCCESS;
}

// Test 2: idempotent fini and NULL-safe free. A double-fini must not
// double-free the fwmap chain; a NULL config must be a no-op.
static int
test_idempotent_fini_and_null_safe_free(void) {
	struct agent *agent = &test_agent;

	struct cp_object *obj = create_named_object(
		agent,
		FWSTATE_MAP_V4_OBJECT_TYPE,
		"test-idempotent",
		FWTABLE_KIND_V4
	);
	TEST_ASSERT_NOT_NULL(obj, "failed to create fwstate_map object");

	struct fwstate_map_object *map_obj =
		container_of(obj, struct fwstate_map_object, cp_object);

	fwstate_map_object_fini(map_obj);
	fwstate_map_object_fini(map_obj);

	fwstate_map_object_free(map_obj, agent);

	fwstate_map_object_free(NULL, agent);
	fwstate_map_object_fini(NULL);

	return TEST_SUCCESS;
}

int
main(void) {
	void *storage = build_stack();
	if (storage == NULL) {
		fprintf(stderr, "setup failed\n");
		return 1;
	}

	size_t tests = 0;
	size_t failures = 0;

	++tests;
	if (test_create_and_populate() != TEST_SUCCESS) {
		++failures;
	}

	++tests;
	if (test_idempotent_fini_and_null_safe_free() != TEST_SUCCESS) {
		++failures;
	}

	memory_context_fini(&test_agent.memory_context);

	struct cp_config *cp = ADDR_OF(&test_agent.cp_config);
	struct dp_config *dp = ADDR_OF(&test_agent.dp_config);
	memory_context_fini(&cp->memory_context);
	memory_context_fini(&dp->memory_context);
	free(storage);

	if (failures != 0) {
		fprintf(stderr, "%zu/%zu tests failed\n", failures, tests);
		return 1;
	}
	return 0;
}
