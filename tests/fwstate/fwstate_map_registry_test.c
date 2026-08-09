// Verify that fwstate_map_v4/v6 cp_objects can be created, inserted,
// looked up, and deleted from the object registry.
//
// cp_object_init resolves the type name via dp_config_lookup_object, so
// the test dp_config must have the fwstate_map_v4 and fwstate_map_v6
// object types pre-registered.

#include "common/memory.h"
#include "common/memory_address.h"
#include "common/test_assert.h"

#include "controlplane/agent/agent.h"
#include "controlplane/config/cp_object.h"
#include "controlplane/config/zone.h"
#include "dataplane/config/zone.h"
#include "lib/dataplane/object/object.h"
#include "lib/errors/errors.h"

#include "modules/fwstate/objects/fwstate_map_object.h"

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#define DP_MEMORY (1 << 20)
#define CP_MEMORY (1 << 20)
#define MAP_NAME "test-map"

// Register the two fwstate_map object types in dp_config so
// dp_config_lookup_object succeeds during cp_object_init.
static int
register_object_types(struct dp_config *dp) {
	struct dp_object *dp_objects = (struct dp_object *)memory_balloc(
		&dp->memory_context, 2 * sizeof(struct dp_object)
	);
	if (dp_objects == NULL) {
		return -1;
	}

	strtcpy(dp_objects[0].name,
		FWSTATE_MAP_V4_OBJECT_TYPE,
		sizeof(dp_objects[0].name));
	strtcpy(dp_objects[1].name,
		FWSTATE_MAP_V6_OBJECT_TYPE,
		sizeof(dp_objects[1].name));

	SET_OFFSET_OF(&dp->dp_objects, dp_objects);
	dp->object_count = 2;
	return 0;
}

static int
test_create_and_table_accessor(struct agent *agent) {
	yanet_error *err = NULL;

	struct cp_object *obj = fwstate_map_object_config_new(
		agent,
		FWSTATE_MAP_V4_OBJECT_TYPE,
		MAP_NAME,
		FWTABLE_KIND_V4,
		&err
	);
	TEST_ASSERT_NOT_NULL(obj, "fwstate_map_object_config_new must succeed");
	TEST_ASSERT_NULL(err, "no error expected on success");

	TEST_ASSERT(
		strncmp(obj->type, FWSTATE_MAP_V4_OBJECT_TYPE, sizeof(obj->type)
		) == 0,
		"cp_object type must be 'fwstate_map_v4'"
	);
	TEST_ASSERT(
		strncmp(obj->name, MAP_NAME, sizeof(obj->name)) == 0,
		"cp_object name must match"
	);

	fwtable_t *table = fwstate_map_object_table(obj);
	TEST_ASSERT_NOT_NULL(table, "table accessor must return non-NULL");

	TEST_ASSERT(
		fwstate_map_object_kind(obj) == FWTABLE_KIND_V4,
		"kind must be V4"
	);

	fwstate_map_object_config_free(obj);
	yanet_error_free(err);
	return TEST_SUCCESS;
}

static int
test_registry_upsert_lookup_delete(
	struct cp_config_gen *gen, struct agent *agent
) {
	yanet_error *err = NULL;

	struct cp_object *obj = fwstate_map_object_config_new(
		agent,
		FWSTATE_MAP_V6_OBJECT_TYPE,
		MAP_NAME,
		FWTABLE_KIND_V6,
		&err
	);
	TEST_ASSERT_NOT_NULL(obj, "failed to create fwstate_map_v6 object");

	TEST_ASSERT(
		cp_object_registry_upsert(
			&gen->object_registry, obj->type, obj->name, obj, &err
		) == 0,
		"cp_object_registry_upsert must succeed"
	);

	struct cp_object *found = cp_object_registry_lookup(
		&gen->object_registry, FWSTATE_MAP_V6_OBJECT_TYPE, MAP_NAME
	);
	TEST_ASSERT_NOT_NULL(found, "lookup must find the inserted object");
	TEST_ASSERT(found == obj, "lookup must return the same pointer");

	TEST_ASSERT(
		cp_object_registry_delete(
			&gen->object_registry,
			FWSTATE_MAP_V6_OBJECT_TYPE,
			MAP_NAME
		) == 0,
		"cp_object_registry_delete must succeed"
	);

	found = cp_object_registry_lookup(
		&gen->object_registry, FWSTATE_MAP_V6_OBJECT_TYPE, MAP_NAME
	);
	TEST_ASSERT_NULL(found, "lookup must return NULL after delete");

	yanet_error_free(err);
	return TEST_SUCCESS;
}

// Verify that upserting a second object with the same (type, name)
// replaces the first.
static int
test_registry_replace(struct cp_config_gen *gen, struct agent *agent) {
	yanet_error *err = NULL;

	struct cp_object *first = fwstate_map_object_config_new(
		agent,
		FWSTATE_MAP_V4_OBJECT_TYPE,
		MAP_NAME,
		FWTABLE_KIND_V4,
		&err
	);
	TEST_ASSERT_NOT_NULL(first, "failed to create first object");

	TEST_ASSERT(
		cp_object_registry_upsert(
			&gen->object_registry,
			first->type,
			first->name,
			first,
			&err
		) == 0,
		"first upsert must succeed"
	);

	struct cp_object *second = fwstate_map_object_config_new(
		agent,
		FWSTATE_MAP_V4_OBJECT_TYPE,
		MAP_NAME,
		FWTABLE_KIND_V4,
		&err
	);
	TEST_ASSERT_NOT_NULL(second, "failed to create second object");

	TEST_ASSERT(
		cp_object_registry_upsert(
			&gen->object_registry,
			second->type,
			second->name,
			second,
			&err
		) == 0,
		"second upsert must succeed"
	);

	struct cp_object *found = cp_object_registry_lookup(
		&gen->object_registry, FWSTATE_MAP_V4_OBJECT_TYPE, MAP_NAME
	);
	TEST_ASSERT_NOT_NULL(found, "lookup after replace must succeed");
	TEST_ASSERT(
		found == second,
		"lookup must return the replacement, not the original"
	);

	// Clean up the remaining registry entry.
	cp_object_registry_delete(
		&gen->object_registry, FWSTATE_MAP_V4_OBJECT_TYPE, MAP_NAME
	);
	// The first object was retired by the replace; free it explicitly.
	fwstate_map_object_config_free(first);

	yanet_error_free(err);
	return TEST_SUCCESS;
}

int
main(void) {
	void *storage = aligned_alloc(64, DP_MEMORY + CP_MEMORY);
	TEST_ASSERT_NOT_NULL(storage, "failed to allocate storage");
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
	dp->object_count = 0;
	dp->dp_objects = NULL;

	TEST_ASSERT(
		register_object_types(dp) == 0,
		"failed to register object types"
	);

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

	struct agent agent;
	memset(&agent, 0, sizeof(agent));
	memory_context_init_from(
		&agent.memory_context, &cp->memory_context, "test-agent"
	);
	SET_OFFSET_OF(&agent.dp_config, dp);
	SET_OFFSET_OF(&agent.cp_config, cp);

	yanet_error *err = NULL;
	struct cp_config_gen *gen = cp_config_gen_new(&agent, &err);
	TEST_ASSERT_NOT_NULL(gen, "cp_config_gen_new failed");

	size_t tests = 0;
	size_t failures = 0;

	++tests;
	if (test_create_and_table_accessor(&agent) != TEST_SUCCESS) {
		++failures;
	}

	++tests;
	if (test_registry_upsert_lookup_delete(gen, &agent) != TEST_SUCCESS) {
		++failures;
	}

	++tests;
	if (test_registry_replace(gen, &agent) != TEST_SUCCESS) {
		++failures;
	}

	memory_context_fini(&agent.memory_context);
	memory_context_fini(&cp->memory_context);
	memory_context_fini(&dp->memory_context);
	yanet_error_free(err);
	free(storage);

	if (failures != 0) {
		fprintf(stderr, "%zu/%zu tests failed\n", failures, tests);
		return 1;
	}
	return 0;
}
