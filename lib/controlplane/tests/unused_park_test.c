/*
 * Regression test for the "park on unused list" corruption: parking a
 * device or module on its agent's unused list used SET_OFFSET_OF with a raw
 * relative-pointer value read straight from the list head field instead of
 * resolving it to a virtual address first.
 *
 * The first park onto an empty list happened to work because the raw value
 * was zero, but every following park stored garbage into the parked
 * entry's prev field. Walking that link later (cp_device_agent_drain_unused)
 * dereferences the garbage and crashes or invokes an arbitrary free_fn.
 */

#include "api/agent.h"

#include "common/memory_block.h"
#include "common/test_assert.h"

#include "controlplane/agent/agent.h"
#include "controlplane/config/cp_device.h"
#include "controlplane/config/cp_module.h"

#include "devices/plain/api/controlplane.h"

#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/errors/errors.h"

#include "logging/log.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define UNUSED_PARK_TEST_MEMORY_LIMIT (4u * 1024u * 1024u)

static uint64_t free_call_count;

static void
counting_device_free(struct cp_device *device) {
	free_call_count += 1;
	cp_device_fini(device);
	cp_device_free(device);
}

// Module twin of counting_device_free: finalizes the base module and returns
// the struct to the creating agent's arena. Test modules here are raw
// cp_module allocations, so sizeof(struct cp_module) is the right size.
static void
counting_module_free(struct cp_module *module) {
	struct agent *agent = ADDR_OF(&module->agent);
	free_call_count += 1;
	cp_module_fini(module);
	memory_bfree(&agent->memory_context, module, sizeof(struct cp_module));
}

// Verifies that parking two devices back-to-back onto an agent's
// unused_device list, then draining it, reclaims exactly the parked devices
// without crashing and returns the agent's arena to its pre-park size.
static int
test_device_park_and_drain(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm, 0, "unused-park-dev", UNUSED_PARK_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	size_t baseline = block_allocator_free_size(&agent->block_allocator);

	struct cp_device_registry registry;
	TEST_ASSERT_SUCCESS(
		cp_device_registry_init(
			&agent->memory_context, &registry, &err
		),
		"device registry init failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	struct cp_device_config cfg0;
	TEST_ASSERT_SUCCESS(
		cp_device_config_init(&cfg0, "plain", "dev0", 0, 0, &err),
		"device config init failed for dev0: %s",
		err ? yanet_error_message(err) : "?"
	);
	struct cp_device *device0 = cp_device_new(&agent->memory_context);
	TEST_ASSERT_NOT_NULL(device0, "cp_device_new failed for dev0");
	TEST_ASSERT_SUCCESS(
		cp_device_init(device0, agent, &cfg0, &err),
		"cp_device_init failed for dev0: %s",
		err ? yanet_error_message(err) : "?"
	);
	cp_device_config_fini(&cfg0);
	TEST_ASSERT_SUCCESS(
		cp_device_registry_upsert(&registry, "dev0", device0, &err),
		"device registry upsert failed for dev0: %s",
		err ? yanet_error_message(err) : "?"
	);

	struct cp_device_config cfg1;
	TEST_ASSERT_SUCCESS(
		cp_device_config_init(&cfg1, "plain", "dev1", 0, 0, &err),
		"device config init failed for dev1: %s",
		err ? yanet_error_message(err) : "?"
	);
	struct cp_device *device1 = cp_device_new(&agent->memory_context);
	TEST_ASSERT_NOT_NULL(device1, "cp_device_new failed for dev1");
	TEST_ASSERT_SUCCESS(
		cp_device_init(device1, agent, &cfg1, &err),
		"cp_device_init failed for dev1: %s",
		err ? yanet_error_message(err) : "?"
	);
	cp_device_config_fini(&cfg1);
	TEST_ASSERT_SUCCESS(
		cp_device_registry_upsert(&registry, "dev1", device1, &err),
		"device registry upsert failed for dev1: %s",
		err ? yanet_error_message(err) : "?"
	);

	// Releasing the registry parks both devices onto agent->unused_device
	// back-to-back: this is the exact corruption trigger, since the
	// second park reads the first park's raw relative-pointer value
	// straight out of unused_device instead of resolving it first.
	cp_device_registry_fini(&registry);

	struct cp_device *head = ADDR_OF(&agent->unused_device);
	TEST_ASSERT(
		head == device1,
		"unused_device head is not the last parked device"
	);
	struct cp_device *second = ADDR_OF(&head->prev);
	TEST_ASSERT(
		second == device0,
		"second parked device's prev does not resolve to the first "
		"parked device"
	);
	TEST_ASSERT_NULL(
		ADDR_OF(&second->prev), "first parked device's prev is not NULL"
	);

	free_call_count = 0;
	cp_device_agent_drain_unused(agent, counting_device_free);
	TEST_ASSERT_EQUAL(
		free_call_count, 2, "drain did not free exactly two devices"
	);
	TEST_ASSERT_NULL(
		ADDR_OF(&agent->unused_device),
		"unused_device is not NULL after drain"
	);

	size_t after = block_allocator_free_size(&agent->block_allocator);
	TEST_ASSERT_EQUAL(
		(long)after,
		(long)baseline,
		"agent arena did not return to its pre-park size: "
		"baseline=%zu after=%zu",
		baseline,
		after
	);

	agent_detach(agent);
	return TEST_SUCCESS;
}

// Verifies that the module twin of the same park-on-unused-list path links
// parked modules correctly: the second parked module's prev resolves to the
// first, and the walk terminates at NULL.
static int
test_module_park_chain(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm, 0, "unused-park-mod", UNUSED_PARK_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	struct cp_module_registry registry;
	TEST_ASSERT_SUCCESS(
		cp_module_registry_init(
			&agent->memory_context, &registry, &err
		),
		"module registry init failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	struct cp_module *module0 = (struct cp_module *)memory_balloc(
		&agent->memory_context, sizeof(struct cp_module)
	);
	TEST_ASSERT_NOT_NULL(module0, "failed to allocate module0");
	TEST_ASSERT_SUCCESS(
		cp_module_init(module0, agent, "route", "mod0", &err),
		"cp_module_init failed for mod0: %s",
		err ? yanet_error_message(err) : "?"
	);
	TEST_ASSERT_SUCCESS(
		cp_module_registry_upsert(
			&registry, "route", "mod0", module0, &err
		),
		"module registry upsert failed for mod0: %s",
		err ? yanet_error_message(err) : "?"
	);

	struct cp_module *module1 = (struct cp_module *)memory_balloc(
		&agent->memory_context, sizeof(struct cp_module)
	);
	TEST_ASSERT_NOT_NULL(module1, "failed to allocate module1");
	TEST_ASSERT_SUCCESS(
		cp_module_init(module1, agent, "route", "mod1", &err),
		"cp_module_init failed for mod1: %s",
		err ? yanet_error_message(err) : "?"
	);
	TEST_ASSERT_SUCCESS(
		cp_module_registry_upsert(
			&registry, "route", "mod1", module1, &err
		),
		"module registry upsert failed for mod1: %s",
		err ? yanet_error_message(err) : "?"
	);

	// Releasing the registry parks both modules onto agent->unused_module
	// back-to-back, the same corruption trigger as the device path above.
	cp_module_registry_fini(&registry);

	struct cp_module *head = ADDR_OF(&agent->unused_module);
	TEST_ASSERT(
		head == module1,
		"unused_module head is not the last parked module"
	);
	struct cp_module *second = ADDR_OF(&head->prev);
	TEST_ASSERT(
		second == module0,
		"second parked module's prev does not resolve to the first "
		"parked module"
	);
	TEST_ASSERT_NULL(
		ADDR_OF(&second->prev), "first parked module's prev is not NULL"
	);

	// Drain the parked chain through the module drain helper instead of
	// leaving it for agent_detach.
	free_call_count = 0;
	cp_module_agent_drain_unused(agent, counting_module_free);
	TEST_ASSERT_EQUAL(
		free_call_count, 2, "drain did not free exactly two modules"
	);
	TEST_ASSERT_NULL(
		ADDR_OF(&agent->unused_module),
		"unused_module is not NULL after drain"
	);

	agent_detach(agent);
	return TEST_SUCCESS;
}

// Regression test for GH#1535: a device parked on a prior (creating) agent
// must still be drained when the current agent reclaims its unused lists.
// loaded_device_count keeps the prior agent alive until the parked device is
// finalized, and the drain walks the prev chain to reach it.
static int
test_drain_reaches_prior_agent(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	// agentA creates and parks a device; its loaded_device_count keeps it
	// alive when agentB attaches with the same name.
	struct agent *agentA = agent_attach(
		shm, 0, "xagent", UNUSED_PARK_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agentA, "agentA attach failed");

	struct cp_device_config cfg;
	TEST_ASSERT_SUCCESS(
		cp_device_config_init(&cfg, "plain", "xa0", 0, 0, &err),
		"device config init failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	struct cp_device *device = cp_device_new(&agentA->memory_context);
	TEST_ASSERT_NOT_NULL(device, "cp_device_new failed");
	TEST_ASSERT_SUCCESS(
		cp_device_init(device, agentA, &cfg, &err),
		"cp_device_init failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	cp_device_config_fini(&cfg);
	TEST_ASSERT_EQUAL(
		agentA->loaded_device_count,
		0,
		"cp_device_init accounted the device before it was parked"
	);

	struct cp_device_registry registry;
	TEST_ASSERT_SUCCESS(
		cp_device_registry_init(
			&agentA->memory_context, &registry, &err
		),
		"device registry init failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	TEST_ASSERT_SUCCESS(
		cp_device_registry_upsert(&registry, "xa0", device, &err),
		"device registry upsert failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	// Releasing the registry parks the device on agentA, which accounts it.
	cp_device_registry_fini(&registry);
	TEST_ASSERT(
		ADDR_OF(&agentA->unused_device) == device,
		"device is not parked on agentA"
	);
	TEST_ASSERT_EQUAL(
		agentA->loaded_device_count,
		1,
		"parking did not account the device on agentA"
	);

	// agentB attaches with the same name; agentA has a live device so it
	// must survive as agentB->prev instead of being cleaned up.
	struct agent *agentB = agent_attach(
		shm, 0, "xagent", UNUSED_PARK_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agentB, "agentB attach failed");
	TEST_ASSERT(
		ADDR_OF(&agentB->prev) == agentA,
		"agentA was cleaned up instead of staying as agentB->prev"
	);

	// Draining from agentB must reach agentA's parked device through the
	// prev chain, finalizing it (which drops agentA's count to zero).
	free_call_count = 0;
	cp_device_agent_drain_unused(agentB, counting_device_free);
	TEST_ASSERT_EQUAL(
		free_call_count, 1, "drain did not free agentA's parked device"
	);
	TEST_ASSERT_NULL(
		ADDR_OF(&agentA->unused_device),
		"agentA unused_device is not empty after drain"
	);
	TEST_ASSERT_EQUAL(
		agentA->loaded_device_count,
		0,
		"agentA loaded_device_count did not drop to zero after drain"
	);

	agent_detach(agentB);
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("debug");

	const char *port_names[] = {"01:00.0"};
	const char *modules[] = {"route"};
	const char *devs_to_load[] = {"plain"};

	struct dataplane_ut_config cfg = {
		.cp_memory = 1u << 27,
		.dp_memory = 1u << 20,
		.worker_count = 1,
		.devices = port_names,
		.device_count = 1,
		.modules = modules,
		.module_count = 1,
		.devices_to_load = devs_to_load,
		.devices_to_load_count = 1,
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

	int res = test_device_park_and_drain(shm);
	if (res == TEST_SUCCESS) {
		res = test_module_park_chain(shm);
	}
	if (res == TEST_SUCCESS) {
		res = test_drain_reaches_prior_agent(shm);
	}

	dataplane_ut_free(ut);

	return (res == TEST_SUCCESS) ? 0 : 1;
}
