/*
 * Tests for the generation pin protocol: readers pin the published
 * generation under the publication lock and walk it unlocked, while
 * writers keep publishing under the configuration lock alone.
 *
 * The race scenario drives continuous publishes and raw pin cycles from
 * separate threads and checks every pinned generation walks consistently
 * and every publish succeeds. The delayed-release scenario checks that a
 * generation retired under a reader's pin is torn down by that reader's
 * own final release — dropping registry references at once — and that
 * the sweep inside the release reclaims the superseded agent as soon as
 * no update holds the configuration lock.
 */

#include "api/agent.h"

#include "common/memory.h"
#include "common/memory_address.h"
#include "common/test_assert.h"
#include "devices/plain/api/controlplane.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/zone.h"
#include "lib/controlplane/tests/counter_surface.h"
#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/errors/errors.h"
#include "lib/logging/log.h"

#include <errno.h>
#include <pthread.h>
#include <stdatomic.h>
#include <stdio.h>
#include <string.h>

#define PIN_RACE_CP_MEMORY (256u * 1024u * 1024u)
#define PIN_RACE_DP_MEMORY (16u * 1024u * 1024u)
// Displaced devices and their counter registries dangle at zero
// references until teardown reclaims the arena, so the budget must hold
// every publish the race performs.
#define PIN_RACE_AGENT_MEMORY (32u * 1024u * 1024u)
#define PIN_RACE_WORKER_COUNT 2
#define PIN_RACE_PIPELINE_COUNT 4
#define PIN_RACE_WRITERS 2
#define PIN_RACE_READERS 4
#define PIN_RACE_WRITES_PER_THREAD 800

struct pin_race_state {
	struct agent *agent;
	struct dp_config *dp_config;
	struct cp_config *cp_config;
	atomic_bool stop;
	atomic_bool writer_failed;
	atomic_bool reader_failed;
};

// Publish one device under the given name, the way a module update does.
static int
race_publish_device(
	struct pin_race_state *state, const char *name, yanet_error **err
) {
	struct cp_device_plain_config *cfg = cp_device_plain_config_new(
		name, PIN_RACE_PIPELINE_COUNT, 0, err
	);
	TEST_ASSERT_NOT_NULL(
		cfg,
		"device config new failed: %s",
		*err ? yanet_error_message(*err) : "?"
	);

	char pipeline[CP_PIPELINE_NAME_LEN];
	for (size_t idx = 0; idx < PIN_RACE_PIPELINE_COUNT; ++idx) {
		snprintf(pipeline, sizeof(pipeline), "race-pipe-%zu", idx);
		int rc = cp_device_plain_config_set_input_pipeline(
			cfg, idx, pipeline, 1
		);
		TEST_ASSERT_EQUAL(
			rc, 0, "set_input_pipeline failed at index %zu", idx
		);
	}

	struct cp_device *dev = cp_device_plain_new(state->agent, cfg, err);
	cp_device_plain_config_free(cfg);
	TEST_ASSERT_NOT_NULL(
		dev,
		"device new failed: %s",
		*err ? yanet_error_message(*err) : "?"
	);

	struct cp_device *devs[] = {dev};
	return cp_config_update_devices(
		state->dp_config, state->cp_config, 1, devs, err
	);
}

// Publish one device under a rotating name, the way a module update does.
//
// Displaced devices dangle at zero references until teardown reclaims the
// arena, exactly as the single-writer fixture helpers accept.
static int
race_write_once(struct pin_race_state *state, unsigned iteration) {
	char name[CP_DEVICE_NAME_LEN];
	snprintf(name, sizeof(name), "race-dev-%u", iteration % 8);

	yanet_error *err = NULL;
	TEST_ASSERT_SUCCESS(
		race_publish_device(state, name, &err),
		"update_devices failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	return TEST_SUCCESS;
}

static void *
race_writer_thread(void *arg) {
	struct pin_race_state *state = (struct pin_race_state *)arg;
	for (unsigned i = 0; i < PIN_RACE_WRITES_PER_THREAD; ++i) {
		if (race_write_once(state, i) != TEST_SUCCESS) {
			atomic_store_explicit(
				&state->writer_failed,
				true,
				memory_order_release
			);
			break;
		}
	}
	return NULL;
}

// Pin the current generation and walk its device registry, requiring the
// freshly installed device to be present with a live name.
//
// Every generation ever published carries one of the race devices, so a
// pin that slipped onto a freed or half-built generation shows up as a
// crash or as the walk finding nothing.
static void *
race_reader_thread(void *arg) {
	struct pin_race_state *state = (struct pin_race_state *)arg;
	while (!atomic_load_explicit(&state->stop, memory_order_acquire)) {
		struct cp_config_gen *gen =
			cp_config_gen_acquire(state->cp_config);

		struct cp_device_registry *registry = &gen->device_registry;
		bool found = false;
		for (uint64_t idx = 0;
		     idx < registry->registry.capacity && !found;
		     ++idx) {
			struct cp_device *device =
				cp_config_gen_get_device(gen, idx);
			if (device == NULL) {
				continue;
			}
			if (strncmp(device->name, "race-dev-", 9) == 0) {
				found = true;
			}
		}

		cp_config_gen_release(state->cp_config, gen);
		if (!found) {
			atomic_store_explicit(
				&state->reader_failed,
				true,
				memory_order_release
			);
			break;
		}
	}
	return NULL;
}

// Verifies that continuous publishes stay correct while unlocked readers
// pin and walk generations with no lock at all.
static int
run_acquire_vs_publish_race_test(struct yanet_shm *shm) {
	yanet_error *err = NULL;
	struct agent *agent =
		agent_attach(shm, 0, "pin-race", PIN_RACE_AGENT_MEMORY, &err);
	TEST_ASSERT_NOT_NULL(
		agent,
		"agent_attach failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	struct pin_race_state state = {
		.agent = agent,
		.dp_config = agent_dp_config(agent),
		.cp_config = ADDR_OF(&agent->cp_config),
	};

	TEST_ASSERT_SUCCESS(
		install_counter_surface(
			state.agent,
			state.dp_config,
			state.cp_config,
			"race-dev-0",
			"race-pipe",
			PIN_RACE_PIPELINE_COUNT
		),
		"failed to install the first race surface"
	);

	// Track how far creation got: a failed create must stop and reap the
	// already-running threads before the assertion abandons the harness.
	size_t writers_created = 0;
	size_t readers_created = 0;
	pthread_t writers[PIN_RACE_WRITERS];
	for (size_t i = 0; i < PIN_RACE_WRITERS; ++i) {
		int rc = pthread_create(
			&writers[i], NULL, race_writer_thread, &state
		);
		if (rc != 0) {
			atomic_store_explicit(
				&state.stop, true, memory_order_release
			);
			for (size_t j = 0; j < writers_created; ++j) {
				pthread_join(writers[j], NULL);
			}
			TEST_ASSERT_EQUAL(
				rc, 0, "pthread_create(writer %zu) failed", i
			);
		}
		++writers_created;
	}
	pthread_t readers[PIN_RACE_READERS];
	for (size_t i = 0; i < PIN_RACE_READERS; ++i) {
		int rc = pthread_create(
			&readers[i], NULL, race_reader_thread, &state
		);
		if (rc != 0) {
			atomic_store_explicit(
				&state.stop, true, memory_order_release
			);
			for (size_t j = 0; j < writers_created; ++j) {
				pthread_join(writers[j], NULL);
			}
			for (size_t j = 0; j < readers_created; ++j) {
				pthread_join(readers[j], NULL);
			}
			TEST_ASSERT_EQUAL(
				rc, 0, "pthread_create(reader %zu) failed", i
			);
		}
		++readers_created;
	}

	for (size_t i = 0; i < PIN_RACE_WRITERS; ++i) {
		pthread_join(writers[i], NULL);
	}
	atomic_store_explicit(&state.stop, true, memory_order_release);
	for (size_t i = 0; i < PIN_RACE_READERS; ++i) {
		pthread_join(readers[i], NULL);
	}

	TEST_ASSERT(
		!atomic_load_explicit(
			&state.writer_failed, memory_order_acquire
		),
		"a racing publish failed while readers held only brief pins"
	);
	TEST_ASSERT(
		!atomic_load_explicit(
			&state.reader_failed, memory_order_acquire
		),
		"a reader walked a pinned generation without the race device"
	);

	agent_detach(agent);
	return TEST_SUCCESS;
}

// Verifies that a generation retired under a reader's pin is freed by
// that reader's final release — dropping its registry references at
// once — and that the sweep inside the release reclaims the superseded
// agent while the configuration lock stays free.
static int
run_delayed_release_free_test(struct yanet_shm *shm) {
	yanet_error *err = NULL;
	struct agent *agent_a = agent_attach(
		shm, 0, "pin-delayed", PIN_RACE_AGENT_MEMORY, &err
	);
	TEST_ASSERT_NOT_NULL(
		agent_a,
		"agent_attach failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	struct pin_race_state state = {
		.agent = agent_a,
		.dp_config = agent_dp_config(agent_a),
		.cp_config = ADDR_OF(&agent_a->cp_config),
	};
	TEST_ASSERT_SUCCESS(
		install_counter_surface(
			agent_a,
			state.dp_config,
			state.cp_config,
			"delayed-dev",
			"race-pipe",
			PIN_RACE_PIPELINE_COUNT
		),
		"failed to install the delayed-release surface"
	);

	struct cp_config_gen *pinned = cp_config_gen_acquire(state.cp_config);

	struct agent *agent_b = agent_attach(
		shm, 0, "pin-delayed", PIN_RACE_AGENT_MEMORY, &err
	);
	TEST_ASSERT_NOT_NULL(
		agent_b,
		"superseding attach failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	state.agent = agent_b;
	TEST_ASSERT_SUCCESS(
		race_publish_device(&state, "delayed-dev", &err),
		"failed to replace the device under agent B: %s",
		err ? yanet_error_message(err) : "?"
	);

	TEST_ASSERT(
		cp_config_current_gen(state.cp_config) != pinned,
		"the pinned generation was not retired"
	);
	TEST_ASSERT_EQUAL(
		cp_config_gen_pin_count(state.cp_config, pinned),
		1,
		"the retired generation must retain exactly the reader pin"
	);
	TEST_ASSERT_EQUAL(
		agent_a->loaded_device_count,
		1,
		"the retired generation must still reference agent A's device"
	);

	// The last release frees the generation — dropping its device
	// reference — and the opportunistic sweep inside reclaims the
	// superseded agent wholesale; its memory must not be read
	// afterwards, so the surviving agent's chain tells the outcome.
	cp_config_gen_release(state.cp_config, pinned);
	TEST_ASSERT_NULL(
		ADDR_OF(&agent_b->prev),
		"the final release did not drop the retired generation's "
		"references and reclaim agent A"
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
		.cp_memory = PIN_RACE_CP_MEMORY,
		.dp_memory = PIN_RACE_DP_MEMORY,
		.worker_count = PIN_RACE_WORKER_COUNT,
		.devices = (const char *[]){"01:00.0"},
		.device_count = 1,
		.modules = NULL,
		.module_count = 0,
		.devices_to_load = (const char *[]){"plain"},
		.devices_to_load_count = 1,
	};

	int rc = run_with_harness(&cfg, run_acquire_vs_publish_race_test);
	if (rc != 0) {
		return rc;
	}
	return run_with_harness(&cfg, run_delayed_release_free_test);
}
