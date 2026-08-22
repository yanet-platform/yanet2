/*
 * Tests for the dangling-reference free protocol on shared objects.
 *
 * An object's reference count is the number of registries — live
 * configuration generations — that registered it; construction takes no
 * reference of its own. Only a dangling object may be destroyed, and only
 * by its owner: the free entry point of the object's own type. While a
 * generation still holds the object, the free attempt is answered with
 * EAGAIN and the owner must retry later.
 */

#include "api/agent.h"

#include "common/memory.h"
#include "common/memory_block.h"
#include "common/test_assert.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_object.h"
#include "lib/controlplane/config/zone.h"

#include "objects/fwstate/api/fwstate_map_v4_object.h"
#include "objects/fwstate/api/fwstate_map_v6_object.h"

#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/errors/errors.h"

#include "lib/logging/log.h"

#include <errno.h>
#include <stdio.h>

#define DANGLING_TEST_MEMORY_LIMIT (2u * 1024u * 1024u)

// An object referenced by a registry must refuse the owner's free with
// EAGAIN and keep its memory; once the registry retires and the object
// turns dangling, the same free succeeds.
static int
test_referenced_free_eagain_then_destroy(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm,
		0,
		"dangling-obj-referenced",
		DANGLING_TEST_MEMORY_LIMIT,
		&err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	size_t baseline = block_allocator_free_size(&agent->block_allocator);

	struct cp_object *v4_0 =
		fwstate_map_v4_object_config_new(agent, "m0", &err);
	TEST_ASSERT_NOT_NULL(v4_0, "fwstate_map_v4_object_config_new failed");

	// Simulate a live generation holding v4_0: a registry reference,
	// exactly like an install would take.
	struct cp_object_registry reg;
	TEST_ASSERT_SUCCESS(
		cp_object_registry_init(&agent->memory_context, &reg, &err),
		"cp_object_registry_init failed"
	);
	TEST_ASSERT_SUCCESS(
		cp_object_registry_upsert(
			&reg, FWSTATE_MAP_V4_OBJECT_TYPE, "m0", v4_0, &err
		),
		"cp_object_registry_upsert failed"
	);
	TEST_ASSERT_EQUAL(
		agent->loaded_object_count,
		1,
		"the generation reference must be mirrored into the live count"
	);

	yanet_error *free_err = NULL;
	TEST_ASSERT(
		fwstate_map_v4_object_config_free(v4_0, &free_err) == -1 &&
			errno == EAGAIN,
		"freeing a generation-referenced object must fail with EAGAIN"
	);
	yanet_error_free(free_err);
	TEST_ASSERT(
		block_allocator_free_size(&agent->block_allocator) < baseline,
		"a refused free must leave the object's memory in place"
	);

	// Retire the generation: v4_0 turns dangling and the same free
	// succeeds.
	cp_object_registry_fini(&reg);
	TEST_ASSERT_EQUAL(
		agent->loaded_object_count,
		0,
		"loaded_object_count must balance to 0 once the registry "
		"retired"
	);
	free_err = NULL;
	TEST_ASSERT_SUCCESS(
		fwstate_map_v4_object_config_free(v4_0, &free_err),
		"freeing a dangling object after its last generation retired "
		"must destroy it"
	);
	TEST_ASSERT_EQUAL(
		(long)block_allocator_free_size(&agent->block_allocator),
		(long)baseline,
		"destroying the object must return the arena to its baseline"
	);

	agent_detach(agent);
	return TEST_SUCCESS;
}

// Two object types share one agent — v4 and v6 fwstate maps, as one
// fwstate control plane owns both in production. After the shared
// generation retires, each type's owner destroys its own object without
// any knowledge of the other type.
static int
test_typed_destroy_is_per_owner(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm,
		0,
		"dangling-obj-per-owner",
		DANGLING_TEST_MEMORY_LIMIT,
		&err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	struct cp_object *v4_0 =
		fwstate_map_v4_object_config_new(agent, "m0", &err);
	TEST_ASSERT_NOT_NULL(v4_0, "fwstate_map_v4_object_config_new failed");
	struct cp_object *v6_0 =
		fwstate_map_v6_object_config_new(agent, "m1", &err);
	TEST_ASSERT_NOT_NULL(v6_0, "fwstate_map_v6_object_config_new failed");

	struct cp_object_registry reg;
	TEST_ASSERT_SUCCESS(
		cp_object_registry_init(&agent->memory_context, &reg, &err),
		"cp_object_registry_init failed"
	);
	TEST_ASSERT_SUCCESS(
		cp_object_registry_upsert(
			&reg, FWSTATE_MAP_V4_OBJECT_TYPE, "m0", v4_0, &err
		),
		"cp_object_registry_upsert(v4) failed"
	);
	TEST_ASSERT_SUCCESS(
		cp_object_registry_upsert(
			&reg, FWSTATE_MAP_V6_OBJECT_TYPE, "m1", v6_0, &err
		),
		"cp_object_registry_upsert(v6) failed"
	);

	// Both frees are refused while the generation holds the objects.
	yanet_error *free_err = NULL;
	TEST_ASSERT(
		fwstate_map_v4_object_config_free(v4_0, &free_err) == -1 &&
			errno == EAGAIN,
		"freeing a generation-referenced v4 object must fail with "
		"EAGAIN"
	);
	yanet_error_free(free_err);
	free_err = NULL;
	TEST_ASSERT(
		fwstate_map_v6_object_config_free(v6_0, &free_err) == -1 &&
			errno == EAGAIN,
		"freeing a generation-referenced v6 object must fail with "
		"EAGAIN"
	);
	yanet_error_free(free_err);

	// Retire the generation; the v6 owner destroys its object only.
	cp_object_registry_fini(&reg);
	size_t after_v6 = block_allocator_free_size(&agent->block_allocator);
	free_err = NULL;
	TEST_ASSERT_SUCCESS(
		fwstate_map_v6_object_config_free(v6_0, &free_err),
		"v6's owner must destroy its dangling object"
	);
	TEST_ASSERT(
		block_allocator_free_size(&agent->block_allocator) > after_v6,
		"destroying the v6 object must return its memory"
	);

	// v4_0 is still intact: its owner has not asked to destroy it, and
	// nothing the v6 owner did may touch it.
	free_err = NULL;
	TEST_ASSERT_SUCCESS(
		fwstate_map_v4_object_config_free(v4_0, &free_err),
		"v4's owner must still be able to destroy its object"
	);

	agent_detach(agent);
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("debug");

	const char *port_names[] = {"01:00.0"};
	const char *devs_to_load[] = {"plain"};
	const char *objs_to_load[] = {
		FWSTATE_MAP_V4_OBJECT_TYPE,
		FWSTATE_MAP_V6_OBJECT_TYPE,
	};

	struct dataplane_ut_config cfg = {
		.cp_memory = 1u << 24,
		.dp_memory = 1u << 20,
		.worker_count = 1,
		.devices = port_names,
		.device_count = 1,
		.modules = NULL,
		.module_count = 0,
		.devices_to_load = devs_to_load,
		.devices_to_load_count = 1,
		.objects_to_load = objs_to_load,
		.objects_to_load_count = 2,
	};

	struct dataplane_ut *ut = dataplane_ut_new(&cfg);
	if (ut == NULL) {
		fprintf(stderr, "dataplane_ut_new failed\n");
		return 1;
	}

	struct yanet_shm *shm = dataplane_ut_shm(ut);
	if (shm == NULL) {
		fprintf(stderr, "dataplane_ut_shm returned NULL\n");
		dataplane_ut_free(ut);
		return 1;
	}

	int res = test_referenced_free_eagain_then_destroy(shm);
	if (res == TEST_SUCCESS) {
		res = test_typed_destroy_is_per_owner(shm);
	}

	dataplane_ut_free(ut);

	return (res == TEST_SUCCESS) ? 0 : 1;
}
