/*
 * Tests for superseded-agent reclamation on the destroy paths.
 *
 * Both scenarios strand a superseded agent the same way: its last registry
 * reference sits in a generation that a reader pin keeps alive past its
 * retirement, and the pin's final release runs while the test thread holds
 * the configuration lock, so the release's opportunistic sweep defers to a
 * holder and the agent stays allocated at zero references.
 *
 * The deferred-destroy scenario then frees the stranded agent's own device
 * handle: the destroy gate survives its own sweep on the strength of its
 * destruction hold, and the reference drop that follows the typed destroy
 * reclaims the agent at once.
 *
 * The gate-sweep scenario frees an unrelated never-published handle of the
 * surviving agent instead: nothing but the gate itself runs between the
 * stranding and the free, so only the sweep the gate performs under the
 * configuration lock can reclaim the stranded agent.
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

#include <pthread.h>
#include <stdio.h>

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

struct deferred_release_args {
	struct cp_config *cp_config;
	struct cp_config_gen *gen;
};

static void *
deferred_release_thread(void *arg) {
	struct deferred_release_args *release = arg;
	cp_config_gen_release(release->cp_config, release->gen);
	return NULL;
}

// Strand the first agent of the pair and leave the surviving agent in
// *state: publish the first agent's device, pin that generation, supersede
// the agent with a second attachment that replaces the device, then release
// the pin from a helper thread while the calling thread holds the
// configuration lock.
//
// The release tears the pinned generation down and drops the first agent's
// last registry reference, but its opportunistic sweep defers to the lock
// holder and returns without reclaiming — the exact state a release racing
// a destroy gate leaves behind. Returns the surviving agent, or NULL after
// logging; the stranded agent's device handle comes back through
// *stranded_dev.
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

	// The lock the calling thread holds here makes the helper's final
	// release defer its sweep to this holder, stranding the first agent
	// at zero references.
	cp_config_lock(state->cp_config);
	struct deferred_release_args release = {
		.cp_config = state->cp_config,
		.gen = pinned,
	};
	pthread_t thread;
	if (pthread_create(&thread, NULL, deferred_release_thread, &release)) {
		cp_config_unlock(state->cp_config);
		LOG(ERROR, "pthread_create for the deferred release failed");
		return NULL;
	}
	pthread_join(thread, NULL);

	if (ADDR_OF(&agent_b->prev) == NULL) {
		cp_config_unlock(state->cp_config);
		LOG(ERROR,
		    "the deferred release must leave the superseded agent "
		    "stranded");
		return NULL;
	}
	cp_config_unlock(state->cp_config);

	return agent_b;
}

// A stranded agent's own handle free must reclaim it: the destroy gate
// survives its own sweep on the strength of its destruction hold, and the
// reference drop that follows the typed destroy reclaims the agent at once
// instead of waiting for an unrelated update or listing.
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

	agent_detach(agent_b);
	return TEST_SUCCESS;
}

// A destroy gate holding the configuration lock must sweep: the stranded
// agent is reclaimed by freeing the surviving agent's unrelated,
// never-published handle, with no update, attach or listing in between.
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
		"held the configuration lock"
	);

	agent_detach(agent_b);
	return TEST_SUCCESS;
}

// Runs a test function against a fresh harness instance so one scenario's
// generation history never strains another's slot budget.
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
