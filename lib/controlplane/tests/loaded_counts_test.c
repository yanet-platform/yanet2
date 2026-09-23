/*
 * Agent reclamation follows references held by live generations.
 *
 * A superseded owner remains linked while a generation references its
 * arena-backed items. It is reclaimed when ordinary retirement or a delayed
 * reader release drops the final reference. Reinserting the same item leaves
 * ownership counts balanced.
 */

#include "api/agent.h"

#include "common/test_assert.h"

#include "devices/plain/api/controlplane.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/zone.h"

#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/errors/errors.h"

#include "lib/logging/log.h"

#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define LOADED_COUNTS_TEST_MEMORY_LIMIT (4u * 1024u * 1024u)

// Install a plain device owned by agent, mirroring the plain control
// plane: construct, then update so the live generation holds the only
// reference. The owner's free attempt afterwards is answered with EAGAIN,
// exactly as it is in production, and the handle would be retried once
// the generations drain.
static int
install_device(
	struct agent *agent,
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	const char *name
) {
	yanet_error *err = NULL;
	struct cp_device_plain_config *cfg =
		cp_device_plain_config_new(name, 0, 0, &err);
	TEST_ASSERT_NOT_NULL(
		cfg,
		"device config new failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	struct cp_device *dev = cp_device_plain_new(agent, cfg, &err);
	cp_device_plain_config_free(cfg);
	TEST_ASSERT_NOT_NULL(
		dev,
		"device new failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	struct cp_device *devs[] = {dev};
	int rc = cp_config_update_devices(dp_config, cp_config, 1, devs, &err);
	TEST_ASSERT_SUCCESS(
		rc,
		"update_devices failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	// The live generation now references the device, so the owner's free
	// attempt must fail with EAGAIN. If the update displaced this agent's
	// own older device, that older one is already dangling at zero
	// references, awaiting its owner's next free attempt.
	yanet_error *free_err = NULL;
	TEST_ASSERT(
		cp_device_plain_free(dev, &free_err) == -1 && errno == EAGAIN,
		"freeing a generation-referenced device must fail with EAGAIN"
	);
	yanet_error_free(free_err);
	return TEST_SUCCESS;
}

// Verifies that ordinary generation retirement reclaims its superseded owner.
static int
run_loaded_counts_test(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	// Agent A installs a device. The upsert refs A's device (0->1), so A's
	// loaded_device_count rises to one. Before the fix the count was never
	// written, so every superseded agent was considered reclaimable.
	struct agent *agent_a = agent_attach(
		shm, 0, "loaded-counts", LOADED_COUNTS_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agent_a, "agent_attach failed for agent A");

	struct dp_config *dp_config = agent_dp_config(agent_a);
	struct cp_config *cp_config = ADDR_OF(&agent_a->cp_config);

	TEST_ASSERT_SUCCESS(
		install_device(agent_a, dp_config, cp_config, "dev0"),
		"failed to install dev0 under agent A"
	);
	TEST_ASSERT_EQUAL(
		agent_a->loaded_module_count,
		0,
		"agent A module count should be zero"
	);
	TEST_ASSERT_EQUAL(
		agent_a->loaded_device_count,
		1,
		"agent A device count should reflect its live device"
	);

	// Attaching a second agent under the same name supersedes agent A.
	struct agent *agent_b = agent_attach(
		shm, 0, "loaded-counts", LOADED_COUNTS_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agent_b, "agent_attach failed for agent B");
	TEST_ASSERT(
		ADDR_OF(&agent_b->prev) == agent_a,
		"agent A is not linked as agent B's prev"
	);

	// While agent A still owns a live device, reclamation must be withheld.
	// Before the fix the guard saw zero counts and freed agent A, leaving
	// dev0's memory dangling.
	agent_free_unused_agents(agent_b);
	TEST_ASSERT(
		ADDR_OF(&agent_b->prev) == agent_a,
		"agent A was reclaimed while its device is still live"
	);

	// Retiring the only referencing generation makes the superseded owner
	// reclaimable before the update returns.
	TEST_ASSERT_SUCCESS(
		install_device(agent_b, dp_config, cp_config, "dev0"),
		"failed to replace dev0 under agent B"
	);
	TEST_ASSERT_EQUAL(
		agent_b->loaded_device_count,
		1,
		"agent B device count should reflect its live device"
	);
	TEST_ASSERT_NULL(
		ADDR_OF(&agent_b->prev),
		"generation retirement did not reclaim agent A"
	);

	return TEST_SUCCESS;
}

// Verifies that a final delayed release reclaims the superseded owner it made
// unreferenced.
static int
run_delayed_generation_release_test(struct yanet_shm *shm) {
	yanet_error *err = NULL;
	struct agent *agent_a = agent_attach(
		shm, 0, "delayed-release", LOADED_COUNTS_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agent_a, "agent_attach failed for agent A");

	struct dp_config *dp_config = agent_dp_config(agent_a);
	struct cp_config *cp_config = ADDR_OF(&agent_a->cp_config);
	TEST_ASSERT_SUCCESS(
		install_device(agent_a, dp_config, cp_config, "delayed-dev"),
		"failed to install delayed-dev under agent A"
	);

	cp_config_lock(cp_config);
	struct cp_config_gen *pinned_gen = cp_config_gen_acquire(cp_config);
	cp_config_unlock(cp_config);

	struct agent *agent_b = agent_attach(
		shm, 0, "delayed-release", LOADED_COUNTS_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agent_b, "agent_attach failed for agent B");
	TEST_ASSERT(
		ADDR_OF(&agent_b->prev) == agent_a,
		"agent A is not linked as agent B's prev"
	);
	TEST_ASSERT_SUCCESS(
		install_device(agent_b, dp_config, cp_config, "delayed-dev"),
		"failed to replace delayed-dev under agent B"
	);
	cp_config_lock(cp_config);
	bool generation_retired =
		ADDR_OF(&cp_config->cp_config_gen) != pinned_gen;
	uint64_t remaining_pins = pinned_gen->refcnt;
	cp_config_unlock(cp_config);

	TEST_ASSERT(generation_retired, "pinned generation was not retired");
	TEST_ASSERT_EQUAL(
		remaining_pins, 1, "reader should hold the final generation pin"
	);
	TEST_ASSERT_EQUAL(
		agent_a->loaded_device_count,
		1,
		"retired generation should keep agent A's device referenced"
	);
	agent_free_unused_agents(agent_b);
	TEST_ASSERT(
		ADDR_OF(&agent_b->prev) == agent_a,
		"agent A was reclaimed while the retired generation was pinned"
	);

	cp_config_lock(cp_config);
	cp_config_gen_release(cp_config, pinned_gen);
	cp_config_unlock(cp_config);

	TEST_ASSERT_NULL(
		ADDR_OF(&agent_b->prev),
		"final generation release did not reclaim agent A"
	);

	return TEST_SUCCESS;
}

// Regression test for re-inserting the same device instance: the loaded
// count follows generation references, so upserting an already-referenced
// instance a second time must not bump the count again.
// Before the fix the upsert incremented unconditionally, which over-counted
// and permanently blocked reclamation of the owning agent.
static int
run_reinsert_count_test(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm, 0, "reinsert-count", LOADED_COUNTS_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed for reinsert test");

	struct dp_config *dp_config = agent_dp_config(agent);
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);

	struct cp_device_plain_config *cfg =
		cp_device_plain_config_new("dev0", 0, 0, &err);
	TEST_ASSERT_NOT_NULL(
		cfg,
		"device config new failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	struct cp_device *dev = cp_device_plain_new(agent, cfg, &err);
	cp_device_plain_config_free(cfg);
	TEST_ASSERT_NOT_NULL(dev, "device new failed");

	// First install: the device gains a generation reference (count 0->1).
	struct cp_device *devs[] = {dev};
	TEST_ASSERT_SUCCESS(
		cp_config_update_devices(dp_config, cp_config, 1, devs, &err),
		"first update_devices failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	TEST_ASSERT_EQUAL(
		agent->loaded_device_count,
		1,
		"device count should be one after the first install"
	);

	// Re-insert the SAME pointer: the upsert's reference gain and its
	// displacement of the instance itself must cancel out, so the count
	// must stay one. Before the fix this bumped it to two.
	TEST_ASSERT_SUCCESS(
		cp_config_update_devices(dp_config, cp_config, 1, devs, &err),
		"second update_devices (same pointer) failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	TEST_ASSERT_EQUAL(
		agent->loaded_device_count,
		1,
		"re-inserting the same device must not double-count"
	);

	// The live generation keeps holding the device, so the owner's free
	// attempt is answered with EAGAIN, as the control plane would see it.
	yanet_error *free_err = NULL;
	TEST_ASSERT(
		cp_device_plain_free(dev, &free_err) == -1 && errno == EAGAIN,
		"freeing a generation-referenced device must fail with EAGAIN"
	);
	yanet_error_free(free_err);

	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("debug");

	const char *port_names[] = {"01:00.0"};
	const char *devs_to_load[] = {"plain"};

	struct dataplane_ut_config cfg = {
		.cp_memory = 1u << 25,
		.dp_memory = 1u << 20,
		.worker_count = 1,
		.devices = port_names,
		.device_count = 1,
		.modules = NULL,
		.module_count = 0,
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

	int res = run_loaded_counts_test(shm);
	if (res == TEST_SUCCESS) {
		res = run_delayed_generation_release_test(shm);
	}
	if (res == TEST_SUCCESS) {
		res = run_reinsert_count_test(shm);
	}

	dataplane_ut_free(ut);

	return (res == TEST_SUCCESS) ? 0 : 1;
}
