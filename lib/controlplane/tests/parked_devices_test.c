/*
 * Regression test for the device-parking mechanism: a construction call
 * must reclaim only parked entries of its own type, and parking an
 * already-parked device must not extend the list into a cycle.
 */

#include "api/agent.h"

#include "common/memory.h"
#include "common/memory_block.h"
#include "common/test_assert.h"

#include "controlplane/agent/agent.h"
#include "controlplane/config/cp_device.h"
#include "controlplane/config/zone.h"

#include "devices/plain/api/controlplane.h"
#include "devices/vlan/api/controlplane.h"

#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/errors/errors.h"

#include "logging/log.h"

#include <stdio.h>

#define PARKED_TEST_MEMORY_LIMIT (2u * 1024u * 1024u)

// Reproduces a cross-type parking scenario: releasing one device type's
// handle while another type shares the same agent.
//
// In production, one control plane can own plain and vlan devices through
// a single agent. A released plain handle parks once its last reference
// drops, and an unrelated vlan update must leave it alone. A release no
// longer reclaims by itself, so whichever device each type releases last
// here stays parked until a later construction of that same type reclaims
// it.
static int
test_drain_parked_filters_by_type(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm,
		0,
		"parked-dev-drain-filter",
		PARKED_TEST_MEMORY_LIMIT,
		&err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	struct cp_device_plain_config *plain_cfg0 =
		cp_device_plain_config_new("d0", 0, 0, &err);
	TEST_ASSERT_NOT_NULL(plain_cfg0, "cp_device_plain_config_new failed");
	struct cp_device *plain0 = cp_device_plain_new(agent, plain_cfg0, &err);
	cp_device_plain_config_free(plain_cfg0);
	TEST_ASSERT_NOT_NULL(plain0, "cp_device_plain_new failed");

	// Simulate a live generation holding plain0: a manual registry
	// reference, exactly like an install would take.
	struct cp_device_registry reg;
	TEST_ASSERT_SUCCESS(
		cp_device_registry_init(&agent->memory_context, &reg, &err),
		"cp_device_registry_init failed"
	);
	TEST_ASSERT_SUCCESS(
		cp_device_registry_upsert(&reg, "plain", "d0", plain0, &err),
		"cp_device_registry_upsert failed"
	);

	// Release the creator's own reference, exactly as the plain control
	// plane does on free.
	//
	// The generation reference above still holds plain0 alive, so
	// nothing parks yet.
	cp_device_plain_free(plain0);
	TEST_ASSERT_NULL(
		ADDR_OF(&agent->parked_devices),
		"nothing must be parked while a generation still holds plain0"
	);

	// Retire the generation: its last reference drops and plain0 parks.
	cp_device_registry_fini(&reg);
	TEST_ASSERT(
		ADDR_OF(&agent->parked_devices) == plain0,
		"plain0 must be parked once its last reference drops"
	);

	// An unrelated vlan construction must not touch a parked plain
	// device.
	struct cp_device_vlan_config *vlan_cfg0 =
		cp_device_vlan_config_new("v0", 0, 0, 100, &err);
	TEST_ASSERT_NOT_NULL(vlan_cfg0, "cp_device_vlan_config_new failed");
	struct cp_device *vlan0 = cp_device_vlan_new(agent, vlan_cfg0, &err);
	cp_device_vlan_config_free(vlan_cfg0);
	TEST_ASSERT_NOT_NULL(vlan0, "cp_device_vlan_new failed");
	TEST_ASSERT(
		ADDR_OF(&agent->parked_devices) == plain0,
		"vlan's own construction must leave a parked plain device "
		"untouched"
	);

	// A plain construction reaches its own type's parked list and
	// destroys plain0.
	struct cp_device_plain_config *plain_cfg1 =
		cp_device_plain_config_new("d1", 0, 0, &err);
	TEST_ASSERT_NOT_NULL(plain_cfg1, "cp_device_plain_config_new failed");
	struct cp_device *plain1 = cp_device_plain_new(agent, plain_cfg1, &err);
	cp_device_plain_config_free(plain_cfg1);
	TEST_ASSERT_NOT_NULL(plain1, "cp_device_plain_new failed");
	TEST_ASSERT_NULL(
		ADDR_OF(&agent->parked_devices),
		"plain's own construction must reclaim the parked plain device"
	);

	// Steady state: exactly one live plain and one live vlan device.
	// Reconstructing each type below must return to this same byte count.
	size_t checkpoint = block_allocator_free_size(&agent->block_allocator);

	// Freeing the only live instance of each type parks it instead of
	// destroying it: nothing constructs that type again yet to reclaim it.
	cp_device_vlan_free(vlan0);
	cp_device_plain_free(plain1);
	TEST_ASSERT(
		ADDR_OF(&agent->parked_devices) == plain1,
		"the last release of each type must park it, not destroy it"
	);
	TEST_ASSERT(
		ADDR_OF(&plain1->parked_next) == vlan0,
		"parking must chain onto the existing list head"
	);
	TEST_ASSERT(
		ADDR_OF(&vlan0->parked_next) == vlan0,
		"the list tail stays self-referential"
	);
	TEST_ASSERT_EQUAL(
		(long)block_allocator_free_size(&agent->block_allocator),
		(long)checkpoint,
		"parking must not itself move memory"
	);

	// A later construction of each type reclaims what its own release
	// left parked, exactly as plain1 reclaimed plain0 above.
	struct cp_device_plain_config *plain_cfg2 =
		cp_device_plain_config_new("d2", 0, 0, &err);
	TEST_ASSERT_NOT_NULL(plain_cfg2, "cp_device_plain_config_new failed");
	struct cp_device *plain2 = cp_device_plain_new(agent, plain_cfg2, &err);
	cp_device_plain_config_free(plain_cfg2);
	TEST_ASSERT_NOT_NULL(plain2, "cp_device_plain_new failed");
	TEST_ASSERT(
		ADDR_OF(&agent->parked_devices) == vlan0,
		"a plain construction must reclaim only the parked plain device"
	);

	struct cp_device_vlan_config *vlan_cfg1 =
		cp_device_vlan_config_new("v1", 0, 0, 100, &err);
	TEST_ASSERT_NOT_NULL(vlan_cfg1, "cp_device_vlan_config_new failed");
	struct cp_device *vlan1 = cp_device_vlan_new(agent, vlan_cfg1, &err);
	cp_device_vlan_config_free(vlan_cfg1);
	TEST_ASSERT_NOT_NULL(vlan1, "cp_device_vlan_new failed");
	TEST_ASSERT_NULL(
		ADDR_OF(&agent->parked_devices),
		"a vlan construction must reclaim the parked vlan device"
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

// A duplicate zero-transition on an already-parked device must not link
// it in twice.
//
// A single-node list can't tell: the head is already the node itself, so
// a duplicate push is a no-op regardless. This drives it on a two-node
// list instead: park an older device, then a newer one on top of it,
// then re-drive the callback on the older, tail entry. Without the
// guard that relinks the tail in front of the head, closing the two
// nodes into a cycle a real drain call never finishes walking.
static int
test_park_is_idempotent(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm,
		0,
		"parked-dev-park-idempotent",
		PARKED_TEST_MEMORY_LIMIT,
		&err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	struct cp_device_plain_config *plain_cfg_old =
		cp_device_plain_config_new("idem-old", 0, 0, &err);
	TEST_ASSERT_NOT_NULL(
		plain_cfg_old, "cp_device_plain_config_new failed"
	);
	struct cp_device *plain_old =
		cp_device_plain_new(agent, plain_cfg_old, &err);
	cp_device_plain_config_free(plain_cfg_old);
	TEST_ASSERT_NOT_NULL(plain_old, "cp_device_plain_new failed");
	struct cp_device_plain_config *plain_cfg_new =
		cp_device_plain_config_new("idem-new", 0, 0, &err);
	TEST_ASSERT_NOT_NULL(
		plain_cfg_new, "cp_device_plain_config_new failed"
	);
	struct cp_device *plain_new =
		cp_device_plain_new(agent, plain_cfg_new, &err);
	cp_device_plain_config_free(plain_cfg_new);
	TEST_ASSERT_NOT_NULL(plain_new, "cp_device_plain_new failed");

	size_t checkpoint = block_allocator_free_size(&agent->block_allocator);

	// Park the older device first, then the newer one on top of it: head
	// plain_new, tail plain_old self-referential.
	cp_device_registry_item_free_cb(&plain_old->config_item, NULL);
	cp_device_registry_item_free_cb(&plain_new->config_item, NULL);
	TEST_ASSERT(
		ADDR_OF(&agent->parked_devices) == plain_new,
		"the second park must move the head to plain_new"
	);
	TEST_ASSERT(
		ADDR_OF(&plain_new->parked_next) == plain_old,
		"plain_new must link onto plain_old"
	);
	TEST_ASSERT(
		ADDR_OF(&plain_old->parked_next) == plain_old,
		"plain_old must remain the self-referential tail"
	);

	// Drive the zero-transition callback again on the tail, the same way
	// a lost update between two racing releases once could.
	cp_device_registry_item_free_cb(&plain_old->config_item, NULL);

	// Walk from the head with a bound past the real list length: a cyclic
	// corruption fails this assertion instead of hanging the traversal.
	struct cp_device *seen[8];
	size_t seen_count = 0;
	struct cp_device *node = ADDR_OF(&agent->parked_devices);
	while (node != NULL && seen_count < 8) {
		seen[seen_count++] = node;
		struct cp_device *next = ADDR_OF(&node->parked_next);
		node = (next == node) ? NULL : next;
	}
	TEST_ASSERT_EQUAL(
		(long)seen_count,
		2L,
		"a duplicate zero-transition must not change the list length"
	);
	TEST_ASSERT(
		seen[0] == plain_new && seen[1] == plain_old,
		"a duplicate zero-transition must not reorder the list"
	);
	TEST_ASSERT(
		ADDR_OF(&plain_old->parked_next) == plain_old,
		"a duplicate push must leave plain_old's own link untouched"
	);
	TEST_ASSERT_EQUAL(
		(long)block_allocator_free_size(&agent->block_allocator),
		(long)checkpoint,
		"parking and re-parking an already-parked device must not move "
		"memory"
	);

	// A later plain construction reclaims every parked entry of its own
	// type, plain_old and plain_new alike.
	struct cp_device_plain_config *plain_cfg_next =
		cp_device_plain_config_new("idem-next", 0, 0, &err);
	TEST_ASSERT_NOT_NULL(
		plain_cfg_next, "cp_device_plain_config_new failed"
	);
	struct cp_device *plain_next =
		cp_device_plain_new(agent, plain_cfg_next, &err);
	cp_device_plain_config_free(plain_cfg_next);
	TEST_ASSERT_NOT_NULL(plain_next, "cp_device_plain_new failed");
	TEST_ASSERT_NULL(
		ADDR_OF(&agent->parked_devices),
		"construction must reclaim every parked device of its own type"
	);

	agent_detach(agent);
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("debug");

	const char *port_names[] = {"01:00.0"};
	const char *devs_to_load[] = {"plain", "vlan"};

	struct dataplane_ut_config cfg = {
		.cp_memory = 1u << 24,
		.dp_memory = 1u << 20,
		.worker_count = 1,
		.devices = port_names,
		.device_count = 1,
		.modules = NULL,
		.module_count = 0,
		.devices_to_load = devs_to_load,
		.devices_to_load_count = 2,
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
