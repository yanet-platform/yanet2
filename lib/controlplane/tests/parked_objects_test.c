/*
 * Regression test for the object-parking mechanism: a construction call
 * must reclaim only parked entries of its own type, and parking an
 * already-parked object must not extend the list into a cycle.
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

#include <stdio.h>

#define PARKED_TEST_MEMORY_LIMIT (2u * 1024u * 1024u)

// Reproduces a cross-type parking scenario: releasing one object type's
// handle while another type shares the same agent.
//
// In production, one control plane can own v4 and v6 fwstate-map objects
// through a single agent. A released v4 handle parks once its last
// reference drops, and an unrelated v6 construction must leave it parked.
// A release no longer reclaims by itself, so whichever object each type
// releases last here stays parked until a later construction of that same
// type reclaims it.
static int
test_drain_parked_filters_by_type(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm,
		0,
		"parked-obj-drain-filter",
		PARKED_TEST_MEMORY_LIMIT,
		&err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	struct cp_object *v4_0 =
		fwstate_map_v4_object_config_new(agent, "m0", &err);
	TEST_ASSERT_NOT_NULL(v4_0, "fwstate_map_v4_object_config_new failed");

	// Simulate a live generation holding v4_0: a manual registry
	// reference, exactly like an install would take.
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

	// Release the creator's own reference, exactly as the fwstate-map
	// control plane does on free.
	//
	// The generation reference above still holds v4_0 alive, so
	// nothing parks yet.
	fwstate_map_v4_object_config_free(v4_0);
	TEST_ASSERT_NULL(
		ADDR_OF(&agent->parked_objects),
		"nothing must be parked while a generation still holds v4_0"
	);

	// Retire the generation: its last reference drops and v4_0 parks.
	cp_object_registry_fini(&reg);
	TEST_ASSERT(
		ADDR_OF(&agent->parked_objects) == v4_0,
		"v4_0 must be parked once its last reference drops"
	);
	TEST_ASSERT_EQUAL(
		agent->loaded_object_count,
		0,
		"the live count must reach zero with the last reference"
	);

	// An unrelated v6 construction must not touch a parked v4 object.
	struct cp_object *v6_0 =
		fwstate_map_v6_object_config_new(agent, "m0", &err);
	TEST_ASSERT_NOT_NULL(v6_0, "fwstate_map_v6_object_config_new failed");
	TEST_ASSERT(
		ADDR_OF(&agent->parked_objects) == v4_0,
		"v6's own construction must leave a parked v4 object untouched"
	);

	// A v4 construction reaches its own type's parked list and destroys
	// v4_0.
	struct cp_object *v4_1 =
		fwstate_map_v4_object_config_new(agent, "m1", &err);
	TEST_ASSERT_NOT_NULL(v4_1, "fwstate_map_v4_object_config_new failed");
	TEST_ASSERT_NULL(
		ADDR_OF(&agent->parked_objects),
		"v4's own construction must reclaim the parked v4 object"
	);

	// Steady state: exactly one live v4 and one live v6 object.
	// Reconstructing each type below must return to this same byte count.
	size_t checkpoint = block_allocator_free_size(&agent->block_allocator);

	// Freeing the only live instance of each type parks it instead of
	// destroying it: nothing constructs that type again yet to reclaim it.
	fwstate_map_v4_object_config_free(v4_1);
	fwstate_map_v6_object_config_free(v6_0);
	TEST_ASSERT(
		ADDR_OF(&agent->parked_objects) == v6_0,
		"the last release of each type must park it, not destroy it"
	);
	TEST_ASSERT(
		ADDR_OF(&v6_0->parked_next) == v4_1,
		"parking must chain onto the existing list head"
	);
	TEST_ASSERT(
		ADDR_OF(&v4_1->parked_next) == v4_1,
		"the list tail stays self-referential"
	);
	TEST_ASSERT_EQUAL(
		(long)block_allocator_free_size(&agent->block_allocator),
		(long)checkpoint,
		"parking must not itself move memory"
	);

	// A later construction of each type reclaims what its own release
	// left parked, exactly as v4_1 reclaimed v4_0 above.
	struct cp_object *v4_2 =
		fwstate_map_v4_object_config_new(agent, "m2", &err);
	TEST_ASSERT_NOT_NULL(v4_2, "fwstate_map_v4_object_config_new failed");
	TEST_ASSERT(
		ADDR_OF(&agent->parked_objects) == v6_0,
		"a v4 construction must reclaim only the parked v4 object"
	);

	struct cp_object *v6_1 =
		fwstate_map_v6_object_config_new(agent, "m1", &err);
	TEST_ASSERT_NOT_NULL(v6_1, "fwstate_map_v6_object_config_new failed");
	TEST_ASSERT_NULL(
		ADDR_OF(&agent->parked_objects),
		"a v6 construction must reclaim the parked v6 object"
	);
	TEST_ASSERT_EQUAL(
		(long)block_allocator_free_size(&agent->block_allocator),
		(long)checkpoint,
		"replacing each type's parked predecessor must return to the "
		"same steady state"
	);

	agent_detach(agent);
	return TEST_SUCCESS;
}

// A duplicate zero-transition on an already-parked object must not link
// it in twice.
//
// A single-node list can't tell: the head is already the node itself, so
// a duplicate push is a no-op regardless. This drives it on a two-node
// list instead: park an older object, then a newer one on top of it,
// then re-drive the callback on the older, tail entry. Without the
// guard that relinks the tail in front of the head, closing the two
// nodes into a cycle a real drain call never finishes walking.
static int
test_park_is_idempotent(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm,
		0,
		"parked-obj-park-idempotent",
		PARKED_TEST_MEMORY_LIMIT,
		&err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	struct cp_object *v4_old =
		fwstate_map_v4_object_config_new(agent, "idem-old", &err);
	TEST_ASSERT_NOT_NULL(v4_old, "fwstate_map_v4_object_config_new failed");
	struct cp_object *v4_new =
		fwstate_map_v4_object_config_new(agent, "idem-new", &err);
	TEST_ASSERT_NOT_NULL(v4_new, "fwstate_map_v4_object_config_new failed");

	size_t checkpoint = block_allocator_free_size(&agent->block_allocator);

	// Park the older object first, then the newer one on top of it: head
	// v4_new, tail v4_old self-referential.
	cp_object_registry_item_free_cb(&v4_old->config_item, NULL);
	cp_object_registry_item_free_cb(&v4_new->config_item, NULL);
	TEST_ASSERT(
		ADDR_OF(&agent->parked_objects) == v4_new,
		"the second park must move the head to v4_new"
	);
	TEST_ASSERT(
		ADDR_OF(&v4_new->parked_next) == v4_old,
		"v4_new must link onto v4_old"
	);
	TEST_ASSERT(
		ADDR_OF(&v4_old->parked_next) == v4_old,
		"v4_old must remain the self-referential tail"
	);

	// Drive the zero-transition callback again on the tail, the same way
	// a lost update between two racing releases once could.
	cp_object_registry_item_free_cb(&v4_old->config_item, NULL);

	// Walk from the head with a bound past the real list length: a cyclic
	// corruption fails this assertion instead of hanging the traversal.
	struct cp_object *seen[8];
	size_t seen_count = 0;
	struct cp_object *node = ADDR_OF(&agent->parked_objects);
	while (node != NULL && seen_count < 8) {
		seen[seen_count++] = node;
		struct cp_object *next = ADDR_OF(&node->parked_next);
		node = (next == node) ? NULL : next;
	}
	TEST_ASSERT_EQUAL(
		(long)seen_count,
		2L,
		"a duplicate zero-transition must not change the list length"
	);
	TEST_ASSERT(
		seen[0] == v4_new && seen[1] == v4_old,
		"a duplicate zero-transition must not reorder the list"
	);
	TEST_ASSERT(
		ADDR_OF(&v4_old->parked_next) == v4_old,
		"a duplicate push must leave v4_old's own link untouched"
	);
	TEST_ASSERT_EQUAL(
		(long)block_allocator_free_size(&agent->block_allocator),
		(long)checkpoint,
		"parking and re-parking an already-parked object must not move "
		"memory"
	);

	// A later v4 construction reclaims every parked entry of its own
	// type, v4_old and v4_new alike.
	struct cp_object *v4_next =
		fwstate_map_v4_object_config_new(agent, "idem-next", &err);
	TEST_ASSERT_NOT_NULL(
		v4_next, "fwstate_map_v4_object_config_new failed"
	);
	TEST_ASSERT_NULL(
		ADDR_OF(&agent->parked_objects),
		"construction must reclaim every parked object of its own type"
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

	int res = test_drain_parked_filters_by_type(shm);
	if (res == TEST_SUCCESS) {
		res = test_park_is_idempotent(shm);
	}

	dataplane_ut_free(ut);

	return (res == TEST_SUCCESS) ? 0 : 1;
}
