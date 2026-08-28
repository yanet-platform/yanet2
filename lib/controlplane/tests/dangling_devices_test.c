/*
 * Tests for the dangling-reference free protocol on devices.
 *
 * A device's reference count is the number of registries — live
 * configuration generations — that registered it; construction takes no
 * reference of its own. Only a dangling device may be destroyed, and only
 * by its owner: the free entry point of the device's own subclass. While
 * a generation still holds the device, the free attempt is answered with
 * EAGAIN and the owner must retry later.
 */

#include "api/agent.h"

#include "common/memory.h"
#include "common/memory_block.h"
#include "common/test_assert.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_device.h"
#include "lib/controlplane/config/zone.h"

#include "devices/plain/api/controlplane.h"
#include "devices/vlan/api/controlplane.h"

#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/errors/errors.h"

#include "lib/logging/log.h"

#include <errno.h>
#include <stdio.h>

#define DANGLING_TEST_MEMORY_LIMIT (2u * 1024u * 1024u)

// A device referenced by a registry must refuse the owner's free with
// EAGAIN and keep its memory; once the registry retires and the device
// turns dangling, the same free succeeds.
static int
test_referenced_free_eagain_then_destroy(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm,
		0,
		"dangling-dev-referenced",
		DANGLING_TEST_MEMORY_LIMIT,
		&err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	size_t baseline = block_allocator_free_size(&agent->block_allocator);

	struct cp_device_plain_config *plain_cfg0 =
		cp_device_plain_config_new("d0", 0, 0, &err);
	TEST_ASSERT_NOT_NULL(plain_cfg0, "cp_device_plain_config_new failed");
	struct cp_device *plain0 = cp_device_plain_new(agent, plain_cfg0, &err);
	cp_device_plain_config_free(plain_cfg0);
	TEST_ASSERT_NOT_NULL(plain0, "cp_device_plain_new failed");

	// Simulate a live generation holding plain0: a registry reference,
	// exactly like an install would take.
	struct cp_device_registry reg;
	TEST_ASSERT_SUCCESS(
		cp_device_registry_init(
			&agent->memory_context, NULL, &reg, &err
		),
		"cp_device_registry_init failed"
	);
	TEST_ASSERT_SUCCESS(
		cp_device_registry_upsert(&reg, "plain", "d0", plain0, &err),
		"cp_device_registry_upsert failed"
	);

	yanet_error *free_err = NULL;
	TEST_ASSERT(
		cp_device_plain_free(plain0, &free_err) == -1 &&
			errno == EAGAIN,
		"freeing a generation-referenced device must fail with EAGAIN"
	);
	yanet_error_free(free_err);
	TEST_ASSERT(
		block_allocator_free_size(&agent->block_allocator) < baseline,
		"a refused free must leave the device's memory in place"
	);

	// Retire the generation: plain0 turns dangling and the same free
	// succeeds.
	cp_device_registry_fini(&reg);
	free_err = NULL;
	TEST_ASSERT_SUCCESS(
		cp_device_plain_free(plain0, &free_err),
		"freeing a dangling device after its last generation retired "
		"must destroy it"
	);
	TEST_ASSERT_EQUAL(
		(long)block_allocator_free_size(&agent->block_allocator),
		(long)baseline,
		"destroying the device must return the arena to its baseline"
	);

	agent_detach(agent);
	return TEST_SUCCESS;
}

// Two device subclasses share one agent — plain and vlan here, as one
// control plane can own both in production. After the shared generation
// retires, each subclass's owner destroys its own device without any
// knowledge of the other subclass.
static int
test_typed_destroy_is_per_owner(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm,
		0,
		"dangling-dev-per-owner",
		DANGLING_TEST_MEMORY_LIMIT,
		&err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	struct cp_device_plain_config *plain_cfg0 =
		cp_device_plain_config_new("d0", 0, 0, &err);
	TEST_ASSERT_NOT_NULL(plain_cfg0, "cp_device_plain_config_new failed");
	struct cp_device *plain0 = cp_device_plain_new(agent, plain_cfg0, &err);
	cp_device_plain_config_free(plain_cfg0);
	TEST_ASSERT_NOT_NULL(plain0, "cp_device_plain_new failed");

	struct cp_device_vlan_config *vlan_cfg0 =
		cp_device_vlan_config_new("v0", 0, 0, 100, &err);
	TEST_ASSERT_NOT_NULL(vlan_cfg0, "cp_device_vlan_config_new failed");
	struct cp_device *vlan0 = cp_device_vlan_new(agent, vlan_cfg0, &err);
	cp_device_vlan_config_free(vlan_cfg0);
	TEST_ASSERT_NOT_NULL(vlan0, "cp_device_vlan_new failed");

	struct cp_device_registry reg;
	TEST_ASSERT_SUCCESS(
		cp_device_registry_init(
			&agent->memory_context, NULL, &reg, &err
		),
		"cp_device_registry_init failed"
	);
	TEST_ASSERT_SUCCESS(
		cp_device_registry_upsert(&reg, "plain", "d0", plain0, &err),
		"cp_device_registry_upsert(plain) failed"
	);
	TEST_ASSERT_SUCCESS(
		cp_device_registry_upsert(&reg, "vlan", "v0", vlan0, &err),
		"cp_device_registry_upsert(vlan) failed"
	);

	// Both frees are refused while the generation holds the devices.
	yanet_error *free_err = NULL;
	TEST_ASSERT(
		cp_device_plain_free(plain0, &free_err) == -1 &&
			errno == EAGAIN,
		"freeing a generation-referenced plain device must fail with "
		"EAGAIN"
	);
	yanet_error_free(free_err);
	free_err = NULL;
	TEST_ASSERT(
		cp_device_vlan_free(vlan0, &free_err) == -1 && errno == EAGAIN,
		"freeing a generation-referenced vlan device must fail with "
		"EAGAIN"
	);
	yanet_error_free(free_err);

	// Retire the generation; the vlan owner destroys its device only.
	cp_device_registry_fini(&reg);
	size_t after_vlan = block_allocator_free_size(&agent->block_allocator);
	free_err = NULL;
	TEST_ASSERT_SUCCESS(
		cp_device_vlan_free(vlan0, &free_err),
		"vlan's owner must destroy its dangling device"
	);
	TEST_ASSERT(
		block_allocator_free_size(&agent->block_allocator) > after_vlan,
		"destroying the vlan device must return its memory"
	);

	// plain0 is still intact: its owner has not asked to destroy it, and
	// nothing the vlan owner did may touch it.
	free_err = NULL;
	TEST_ASSERT_SUCCESS(
		cp_device_plain_free(plain0, &free_err),
		"plain's owner must still be able to destroy its device"
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

	int res = test_referenced_free_eagain_then_destroy(shm);
	if (res == TEST_SUCCESS) {
		res = test_typed_destroy_is_per_owner(shm);
	}

	dataplane_ut_free(ut);

	return (res == TEST_SUCCESS) ? 0 : 1;
}
