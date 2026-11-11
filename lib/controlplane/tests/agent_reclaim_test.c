/*
 * Tests for superseded-agent reclamation on the destroy paths.
 *
 * Both scenarios strand a superseded agent the same way: its last
 * registry reference sits in a generation that a reader pin keeps alive
 * past its retirement, and the pin's final release runs on a thread
 * holding the configuration lock — an install — so the release skips
 * its own sweep and leaves the reclamation to the holder's exit path.
 * The holder here never runs one, so the agent stays allocated at zero
 * references past the unlock.
 *
 * The deferred-destroy scenario frees the stranded agent's own device
 * handle: the destroy gate survives its own sweep on the strength of
 * its destruction hold, and the reference drop that follows the typed
 * destroy reclaims the agent at once. The registry shape the
 * force-destroy of a superseding head leaves behind — the slot back on
 * its predecessor — is asserted on the same stage.
 *
 * The gate-sweep scenario frees an unrelated never-published handle of
 * the surviving agent instead: nothing but the gate itself runs between
 * the stranding and the free, so only the sweep the gate performs under
 * the locks can reclaim the stranded agent.
 */

#include "api/agent.h"

#include "common/memory_address.h"
#include "common/test_assert.h"
#include "devices/plain/api/controlplane.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/errors/errors.h"
#include "lib/logging/log.h"

#include <stdio.h>
#include <string.h>

#include "common/memory_block.h"

#define AGENT_RECLAIM_CP_MEMORY (256u * 1024u * 1024u)
#define AGENT_RECLAIM_DP_MEMORY (16u * 1024u * 1024u)
#define AGENT_RECLAIM_AGENT_MEMORY (32u * 1024u * 1024u)
#define AGENT_RECLAIM_WORKER_COUNT 2

struct agent_reclaim_state {
	struct agent *agent;
	struct dp_config *dp_config;
	struct cp_config *cp_config;
};

// Publish one plain device under the given name for the given agent and
// return its handle, the way a device update does. Returns NULL after
// logging on any failure.
static struct cp_device *
publish_plain_device(
	const struct agent_reclaim_state *state,
	const char *name,
	yanet_error **err
) {
	struct cp_device_plain_config *cfg =
		cp_device_plain_config_new(name, 0, 0, err);
	if (cfg == NULL) {
		LOG(ERROR,
		    "device config new failed: %s",
		    *err ? yanet_error_message(*err) : "?");
		return NULL;
	}

	struct cp_device *dev = cp_device_plain_new(state->agent, cfg, err);
	cp_device_plain_config_free(cfg);
	if (dev == NULL) {
		LOG(ERROR,
		    "device new failed: %s",
		    *err ? yanet_error_message(*err) : "?");
		return NULL;
	}

	struct cp_device *devs[] = {dev};
	if (cp_config_update_devices(
		    state->dp_config, state->cp_config, 1, devs, err
	    )) {
		LOG(ERROR,
		    "failed to publish device '%s': %s",
		    name,
		    *err ? yanet_error_message(*err) : "?");
		return NULL;
	}
	return dev;
}

static size_t
cp_pool_free_size(struct cp_config *cp_config) {
	return block_allocator_free_size(&cp_config->block_allocator);
}

// Return the registry head of the given name, or NULL when no slot
// names it.
static struct agent *
registry_head(const struct agent_reclaim_state *state, const char *name) {
	struct cp_agent_registry *registry =
		ADDR_OF(&state->cp_config->agent_registry);
	if (registry == NULL) {
		return NULL;
	}
	for (uint64_t idx = 0; idx < registry->count; ++idx) {
		struct agent *candidate = ADDR_OF(&registry->agents[idx]);
		if (!strncmp(candidate->name, name, AGENT_NAME_LEN)) {
			return candidate;
		}
	}
	return NULL;
}

// Strand the first agent of the pair and leave the surviving agent in
// *state: publish the first agent's device, pin that generation,
// supersede the agent with a second attachment that replaces the
// device, then release the pin while holding the configuration lock,
// the way an install-path release does.
//
// The release tears the pinned generation down and drops the first
// agent's last registry reference, but a release running on a thread
// that holds the configuration lock skips its sweep and leaves the
// reclamation to the holder's exit path — which this caller never
// runs, so the agent stays stranded at zero references past the
// unlock. Returns the surviving agent, or NULL after logging; the
// stranded agent's device handle comes back through *stranded_dev.
static struct agent *
strand_first_agent(
	struct yanet_shm *shm,
	struct agent_reclaim_state *state,
	struct cp_device **stranded_dev
) {
	yanet_error *err = NULL;
	struct agent *agent_a = agent_attach(
		shm, 0, "reclaim", AGENT_RECLAIM_AGENT_MEMORY, &err
	);
	if (agent_a == NULL) {
		LOG(ERROR,
		    "agent_attach failed: %s",
		    err ? yanet_error_message(err) : "?");
		return NULL;
	}

	state->agent = agent_a;
	state->dp_config = agent_dp_config(agent_a);
	state->cp_config = ADDR_OF(&agent_a->cp_config);

	*stranded_dev = publish_plain_device(state, "reclaim-dev", &err);
	struct cp_config_gen *pinned = cp_config_gen_acquire(state->cp_config);
	if (*stranded_dev == NULL) {
		return NULL;
	}

	struct agent *agent_b = agent_attach(
		shm, 0, "reclaim", AGENT_RECLAIM_AGENT_MEMORY, &err
	);
	if (agent_b == NULL) {
		LOG(ERROR,
		    "superseding attach failed: %s",
		    err ? yanet_error_message(err) : "?");
		return NULL;
	}
	state->agent = agent_b;
	if (publish_plain_device(state, "reclaim-dev", &err) == NULL) {
		return NULL;
	}

	// The configuration lock held here puts the pin's final release on
	// the install path, whose sweep this caller never runs, stranding
	// the first agent at zero references.
	cp_config_lock(state->cp_config);
	cp_config_gen_release(state->cp_config, pinned);

	if (ADDR_OF(&agent_b->prev) == NULL) {
		cp_config_unlock(state->cp_config);
		LOG(ERROR,
		    "the skipped release must leave the superseded agent "
		    "stranded");
		return NULL;
	}
	cp_config_unlock(state->cp_config);

	return agent_b;
}

// A stranded agent's own handle free must reclaim it: the destroy gate
// survives its own sweep on the strength of its destruction hold, and
// the reference drop that follows the typed destroy reclaims the agent
// at once instead of waiting for an unrelated update or listing. The
// force-destroy of a superseding head must besides give its slot back
// to the surviving predecessor.
static int
run_deferred_destroy_reclaims_test(struct yanet_shm *shm) {
	struct agent_reclaim_state state;
	struct cp_device *stranded_dev = NULL;
	struct agent *agent_b = strand_first_agent(shm, &state, &stranded_dev);
	TEST_ASSERT_NOT_NULL(agent_b, "failed to strand the superseded agent");

	yanet_error *err = NULL;
	TEST_ASSERT_SUCCESS(
		cp_device_plain_free(stranded_dev, &err),
		"freeing the stranded agent's device failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	TEST_ASSERT_NULL(
		ADDR_OF(&agent_b->prev),
		"the final reference drop of a typed destroy did not reclaim "
		"the stranded agent"
	);

	struct agent *agent_c = agent_attach(
		shm, 0, "reclaim", AGENT_RECLAIM_AGENT_MEMORY, &err
	);
	TEST_ASSERT_NOT_NULL(agent_c, "the superseding attach failed");
	TEST_ASSERT(
		ADDR_OF(&agent_c->prev) == agent_b,
		"the superseding attach must keep the referenced predecessor"
	);

	TEST_ASSERT_SUCCESS(
		agent_destroy(agent_c),
		"force-destroying the superseding head failed"
	);
	TEST_ASSERT(
		registry_head(&state, "reclaim") == agent_b,
		"the force-destroyed head's slot must return to its predecessor"
	);

	// The registry allocation follows the live slot count: growing
	// for a new name and force-destroying it returns the pool to the
	// same free size.
	size_t free_before = cp_pool_free_size(state.cp_config);
	struct agent *agent_d =
		agent_attach(shm, 0, "extra", AGENT_RECLAIM_AGENT_MEMORY, &err);
	TEST_ASSERT_NOT_NULL(agent_d, "the extra attach failed");
	TEST_ASSERT_SUCCESS(
		agent_destroy(agent_d),
		"force-destroying the extra agent failed"
	);
	TEST_ASSERT_EQUAL(
		cp_pool_free_size(state.cp_config),
		free_before,
		"the registry rebuild did not return the pool memory"
	);

	agent_detach(agent_b);
	return TEST_SUCCESS;
}

// A destroy gate holding the locks must sweep: the stranded agent is
// reclaimed by freeing the surviving agent's unrelated, never-published
// handle, with no update, attach or listing in between.
static int
run_destroy_gate_sweeps_test(struct yanet_shm *shm) {
	struct agent_reclaim_state state;
	struct cp_device *stranded_dev = NULL;
	struct agent *agent_b = strand_first_agent(shm, &state, &stranded_dev);
	TEST_ASSERT_NOT_NULL(agent_b, "failed to strand the superseded agent");

	yanet_error *err = NULL;
	struct cp_device_plain_config *cfg =
		cp_device_plain_config_new("gate-dev", 0, 0, &err);
	TEST_ASSERT_NOT_NULL(
		cfg,
		"unpublished device config new failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	struct cp_device *dev_b = cp_device_plain_new(agent_b, cfg, &err);
	cp_device_plain_config_free(cfg);
	TEST_ASSERT_NOT_NULL(
		dev_b,
		"unpublished device new failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	TEST_ASSERT_SUCCESS(
		cp_device_plain_free(dev_b, &err),
		"freeing the never-published device failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	TEST_ASSERT_NULL(
		ADDR_OF(&agent_b->prev),
		"the destroy gate did not sweep the stranded agent while it "
		"held the locks"
	);

	agent_detach(agent_b);
	return TEST_SUCCESS;
}

// Runs a test function against a fresh harness instance so one
// scenario's generation history never strains another's slot budget.
static int
run_with_harness(
	const struct dataplane_ut_config *cfg,
	int (*test_fn)(struct yanet_shm *)
) {
	struct dataplane_ut *ut = dataplane_ut_new(cfg);
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
	int res = test_fn(shm);
	dataplane_ut_free(ut);
	return (res == TEST_SUCCESS) ? 0 : 1;
}

int
main(void) {
	log_enable_name("info");

	struct dataplane_ut_config cfg = {
		.cp_memory = AGENT_RECLAIM_CP_MEMORY,
		.dp_memory = AGENT_RECLAIM_DP_MEMORY,
		.worker_count = AGENT_RECLAIM_WORKER_COUNT,
		.devices = (const char *[]){"01:00.0"},
		.device_count = 1,
		.modules = NULL,
		.module_count = 0,
		.devices_to_load = (const char *[]){"plain"},
		.devices_to_load_count = 1,
	};

	int rc = run_with_harness(&cfg, run_deferred_destroy_reclaims_test);
	if (rc != 0) {
		return rc;
	}
	return run_with_harness(&cfg, run_destroy_gate_sweeps_test);
}
