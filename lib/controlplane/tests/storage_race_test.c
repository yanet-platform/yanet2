/*
 * Storage writes are ordered against agent reclamation.
 *
 * The freeze scenario pins a device-owning head, supersedes it and
 * checks the frozen generation: its writes are refused with failed
 * precondition while its stored bytes survive untouched. The churn
 * scenario then runs the sweep flat out — each churner superseding its
 * own name's head, device publishes retiring older generations — while
 * a writer rewrites one key through its own never-superseded head: the
 * write path takes the same lock the sweep frees storage under, so the
 * scenario pins that ordering under load without deadlocking, tearing
 * the writer's list or failing a write.
 */

#include "api/agent.h"

#include "common/test_assert.h"

#include "devices/plain/api/controlplane.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/zone.h"

#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/errors/errors.h"

#include "lib/logging/log.h"

#include <pthread.h>
#include <stdatomic.h>
#include <stdio.h>
#include <string.h>

#define STORAGE_RACE_CP_MEMORY (64u * 1024u * 1024u)
#define STORAGE_RACE_DP_MEMORY (4u * 1024u * 1024u)
#define STORAGE_RACE_AGENT_MEMORY (4u * 1024u * 1024u)
#define STORAGE_RACE_CHURNERS 2
#define STORAGE_RACE_ROUNDS 200
#define STORAGE_RACE_WRITES_PER_ROUND 4
#define STORAGE_RACE_KEY "race-key"
#define STORAGE_RACE_PAYLOAD_MAX 1024

struct storage_race_state {
	struct yanet_shm *shm;
	struct dp_config *dp_config;
	struct cp_config *cp_config;
	const char *name;    // this thread set's agent name
	atomic_bool *failed; // shared across every racing thread set
};

// Install a plain device owned by the agent, pinning it: the live
// generation's reference keeps the agent's arenas alive even once a
// later attach supersedes it.
static int
install_device(
	struct storage_race_state *state,
	struct agent *agent,
	const char *dev_suffix,
	yanet_error **err
) {
	char dev_name[CP_DEVICE_NAME_LEN];
	snprintf(dev_name, sizeof(dev_name), "race-dev-%s", dev_suffix);

	struct cp_device_plain_config *cfg =
		cp_device_plain_config_new(dev_name, 0, 0, err);
	if (cfg == NULL) {
		return -1;
	}
	struct cp_device *dev = cp_device_plain_new(agent, cfg, err);
	cp_device_plain_config_free(cfg);
	if (dev == NULL) {
		return -1;
	}

	struct cp_device *devs[] = {dev};
	return cp_config_update_devices(
		state->dp_config, state->cp_config, 1, devs, err
	);
}

// Supersede the head of the churning name over and over; every fourth
// round also publishes a device under the fresh head, so some
// predecessors stay referenced until a later publish retires their
// generation and the release path sweeps them too.
static int
churn_once(struct storage_race_state *state, unsigned round) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		state->shm, 0, state->name, STORAGE_RACE_AGENT_MEMORY, &err
	);
	if (agent == NULL) {
		fprintf(stderr,
			"churn attach failed: %s\n",
			err ? yanet_error_message(err) : "?");
		yanet_error_free(err);
		return -1;
	}

	if (round % 4 == 0) {
		// One constant device name across the churners: every
		// install displaces the previous one, so the pinned
		// predecessors stay bounded instead of exhausting the pool.
		if (install_device(state, agent, "churn", &err)) {
			fprintf(stderr,
				"churn device install failed: %s\n",
				err ? yanet_error_message(err) : "?");
			yanet_error_free(err);
			return -1;
		}
	}

	return 0;
}

static void *
churner_thread(void *arg) {
	struct storage_race_state *state = (struct storage_race_state *)arg;
	for (unsigned round = 0; round < STORAGE_RACE_ROUNDS; ++round) {
		if (churn_once(state, round) != 0) {
			atomic_store_explicit(
				state->failed, true, memory_order_release
			);
			return NULL;
		}
	}
	return NULL;
}

// The writer owns its head exclusively — nothing supersedes it — so
// every write must land while the sweep churns other names under the
// same lock the write path takes.
static void *
storage_writer_thread(void *arg) {
	struct storage_race_state *state = (struct storage_race_state *)arg;
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		state->shm, 0, state->name, STORAGE_RACE_AGENT_MEMORY, &err
	);
	if (agent == NULL) {
		fprintf(stderr,
			"writer attach failed: %s\n",
			err ? yanet_error_message(err) : "?");
		yanet_error_free(err);
		atomic_store_explicit(
			state->failed, true, memory_order_release
		);
		return NULL;
	}

	for (unsigned round = 0; round < STORAGE_RACE_ROUNDS; ++round) {
		for (unsigned write_idx = 0;
		     write_idx < STORAGE_RACE_WRITES_PER_ROUND;
		     ++write_idx) {
			char payload[STORAGE_RACE_PAYLOAD_MAX];
			memset(payload,
			       (int)(round + write_idx),
			       sizeof(payload));
			size_t size = 16 + ((round * 7 + write_idx * 13) %
					    (STORAGE_RACE_PAYLOAD_MAX - 16));

			yanet_error *write_err = NULL;
			if (agent_storage_put(
				    agent,
				    STORAGE_RACE_KEY,
				    payload,
				    size,
				    &write_err
			    )) {
				fprintf(stderr,
					"storage write failed: %s\n",
					yanet_error_message(write_err));
				yanet_error_free(write_err);
				atomic_store_explicit(
					state->failed,
					true,
					memory_order_release
				);
				return NULL;
			}
		}
	}
	return NULL;
}

// Verifies the frozen-generation refusal: a write through a superseded
// but still-referenced head is refused with failed precondition while
// the same write went through before the supersession, and the stored
// value survives it.
static int
run_storage_freeze_test(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	atomic_bool freeze_failed;
	atomic_init(&freeze_failed, false);

	struct storage_race_state state = {
		.shm = shm,
		.name = "storage-freeze",
		.failed = &freeze_failed,
	};

	struct agent *agent_a = agent_attach(
		shm, 0, "storage-freeze", STORAGE_RACE_AGENT_MEMORY, &err
	);
	TEST_ASSERT_NOT_NULL(
		agent_a,
		"attach failed for the frozen-generation test: %s",
		err ? yanet_error_message(err) : "?"
	);
	state.dp_config = agent_dp_config(agent_a);
	state.cp_config = ADDR_OF(&agent_a->cp_config);

	TEST_ASSERT_SUCCESS(
		install_device(&state, agent_a, "freeze-a", &err),
		"pinning the frozen generation failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	char payload[] = "frozen";
	TEST_ASSERT_SUCCESS(
		agent_storage_put(
			agent_a,
			STORAGE_RACE_KEY,
			payload,
			sizeof(payload),
			&err
		),
		"storage write failed for the current generation"
	);

	struct agent *agent_b = agent_attach(
		shm, 0, "storage-freeze", STORAGE_RACE_AGENT_MEMORY, &err
	);
	TEST_ASSERT_NOT_NULL(agent_b, "superseding attach failed");

	yanet_error *refuse_err = NULL;
	char replacement[] = "melting";
	TEST_ASSERT(
		agent_storage_put(
			agent_a,
			STORAGE_RACE_KEY,
			replacement,
			sizeof(replacement),
			&refuse_err
		) == -1 &&
			yanet_error_kind(refuse_err) ==
				YANET_ERROR_FAILED_PRECONDITION,
		"a write through the superseded generation must be refused "
		"with failed precondition"
	);
	yanet_error_free(refuse_err);

	const void *stored = agent_storage_read(agent_a, STORAGE_RACE_KEY);
	TEST_ASSERT_NOT_NULL(
		stored, "the frozen generation's storage must stay readable"
	);
	TEST_ASSERT_EQUAL(
		memcmp(stored, payload, sizeof(payload)),
		0,
		"the frozen generation's storage must keep its bytes"
	);

	return TEST_SUCCESS;
}

static int
run_storage_race_test(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	atomic_bool race_failed;
	atomic_init(&race_failed, false);

	struct storage_race_state writer_state = {
		.shm = shm,
		.name = "storage-race",
		.failed = &race_failed,
	};
	struct storage_race_state churn_states[STORAGE_RACE_CHURNERS];
	for (size_t idx = 0; idx < STORAGE_RACE_CHURNERS; ++idx) {
		static const char *const churn_names[STORAGE_RACE_CHURNERS] = {
			"storage-churn-0",
			"storage-churn-1",
		};
		churn_states[idx] = (struct storage_race_state){
			.shm = shm,
			.name = churn_names[idx],
			.failed = &race_failed,
		};
	}

	// One bootstrap attach fixes the dp/cp handles for the threads and
	// gives the churn a head to supersede; the handle itself is never
	// used again once the threads start.
	struct agent *bootstrap = agent_attach(
		shm, 0, "storage-race", STORAGE_RACE_AGENT_MEMORY, &err
	);
	TEST_ASSERT_NOT_NULL(
		bootstrap,
		"bootstrap attach failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	writer_state.dp_config = agent_dp_config(bootstrap);
	writer_state.cp_config = ADDR_OF(&bootstrap->cp_config);
	for (size_t idx = 0; idx < STORAGE_RACE_CHURNERS; ++idx) {
		churn_states[idx].dp_config = writer_state.dp_config;
		churn_states[idx].cp_config = writer_state.cp_config;
	}

	pthread_t churners[STORAGE_RACE_CHURNERS];
	pthread_t writer;
	TEST_ASSERT_EQUAL(
		pthread_create(
			&writer, NULL, storage_writer_thread, &writer_state
		),
		0,
		"pthread_create failed for the storage writer"
	);
	for (size_t idx = 0; idx < STORAGE_RACE_CHURNERS; ++idx) {
		TEST_ASSERT_EQUAL(
			pthread_create(
				&churners[idx],
				NULL,
				churner_thread,
				&churn_states[idx]
			),
			0,
			"pthread_create failed for a churner"
		);
	}

	TEST_ASSERT_EQUAL(
		pthread_join(writer, NULL),
		0,
		"pthread_join failed for the writer"
	);
	for (size_t idx = 0; idx < STORAGE_RACE_CHURNERS; ++idx) {
		TEST_ASSERT_EQUAL(
			pthread_join(churners[idx], NULL),
			0,
			"pthread_join failed for a churner"
		);
	}

	TEST_ASSERT(
		!atomic_load_explicit(&race_failed, memory_order_acquire),
		"a thread failed during the storage race"
	);

	// A write through a fresh head of the settled name lands whole.
	struct agent *settled = agent_attach(
		shm, 0, "storage-race", STORAGE_RACE_AGENT_MEMORY, &err
	);
	TEST_ASSERT_NOT_NULL(settled, "post-race attach failed");

	char final_payload[] = "settled";
	TEST_ASSERT_SUCCESS(
		agent_storage_put(
			settled,
			STORAGE_RACE_KEY,
			final_payload,
			sizeof(final_payload),
			&err
		),
		"storage write failed after the race"
	);
	const void *stored = agent_storage_read(settled, STORAGE_RACE_KEY);
	TEST_ASSERT_NOT_NULL(stored, "the settled head must hold the key");
	TEST_ASSERT_EQUAL(
		memcmp(stored, final_payload, sizeof(final_payload)),
		0,
		"the settled head must hold the exact written bytes"
	);

	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("debug");

	const char *port_names[] = {"01:00.0"};
	const char *devs_to_load[] = {"plain"};

	struct dataplane_ut_config cfg = {
		.cp_memory = STORAGE_RACE_CP_MEMORY,
		.dp_memory = STORAGE_RACE_DP_MEMORY,
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

	int res = run_storage_freeze_test(shm);
	if (res == TEST_SUCCESS) {
		res = run_storage_race_test(shm);
	}

	dataplane_ut_free(ut);

	return (res == TEST_SUCCESS) ? 0 : 1;
}
