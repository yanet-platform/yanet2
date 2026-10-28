/*
 * The agent listing runs under the agent lock alone, concurrent with
 * agent churn.
 *
 * Writers supersede the head of their name over and over — every attach
 * chains a successor and sweeps the drained predecessors — and
 * periodically publish a device so some predecessors stay referenced
 * until a later retirement releases them, driving sweeps from the
 * generation install and release paths too. A destroyer repeatedly
 * attaches an entity-free agent and destroys it, so the registry array
 * grows and is rebuilt under the agent lock while every other thread
 * walks or splices it. Readers hammer the agent listing, which walks
 * the registry and every prev chain without the configuration lock: a
 * listing that slipped onto a swept agent shows up as a crash or as a
 * torn snapshot under the race detectors.
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

#define LIST_RACE_CP_MEMORY (64u * 1024u * 1024u)
#define LIST_RACE_DP_MEMORY (4u * 1024u * 1024u)
#define LIST_RACE_AGENT_MEMORY (4u * 1024u * 1024u)
#define LIST_RACE_WRITERS 2
#define LIST_RACE_READERS 2
#define LIST_RACE_ATTACHES_PER_WRITER 200
#define LIST_RACE_DESTROY_ROUNDS 200

struct list_race_state {
	struct yanet_shm *shm;
	struct dp_config *dp_config;
	struct cp_config *cp_config;
	const char *name; // this writer's exclusive agent name
	atomic_bool stop;
	atomic_bool writer_failed;
	atomic_bool reader_failed;
};

static int
race_write_once(struct list_race_state *state, unsigned iteration) {
	yanet_error *err = NULL;

	// Every attach supersedes the previous head of the name; the drained
	// predecessor is reclaimed by the attach's own sweep, racing the
	// listing's walk of the same chain.
	struct agent *agent = agent_attach(
		state->shm, 0, state->name, LIST_RACE_AGENT_MEMORY, &err
	);
	if (agent == NULL) {
		fprintf(stderr,
			"agent_attach failed: %s\n",
			err ? yanet_error_message(err) : "?");
		yanet_error_free(err);
		return -1;
	}

	// Periodically keep a device live under the fresh head, so some
	// predecessors survive until a later publish retires the generation
	// holding their device and the release-path sweep reclaims them.
	if (iteration % 4 == 0) {
		char dev_name[CP_DEVICE_NAME_LEN];
		snprintf(
			dev_name, sizeof(dev_name), "race-dev-%s", state->name
		);

		struct cp_device_plain_config *cfg =
			cp_device_plain_config_new(dev_name, 0, 0, &err);
		if (cfg == NULL) {
			fprintf(stderr, "device config new failed\n");
			yanet_error_free(err);
			return -1;
		}
		struct cp_device *dev = cp_device_plain_new(agent, cfg, &err);
		cp_device_plain_config_free(cfg);
		if (dev == NULL) {
			fprintf(stderr, "device new failed\n");
			yanet_error_free(err);
			return -1;
		}

		struct cp_device *devs[] = {dev};
		if (cp_config_update_devices(
			    state->dp_config, state->cp_config, 1, devs, &err
		    )) {
			fprintf(stderr,
				"update_devices failed: %s\n",
				err ? yanet_error_message(err) : "?");
			yanet_error_free(err);
			return -1;
		}
	}

	return 0;
}

static void *
race_writer_thread(void *arg) {
	struct list_race_state *state = (struct list_race_state *)arg;
	for (unsigned i = 0; i < LIST_RACE_ATTACHES_PER_WRITER; ++i) {
		if (race_write_once(state, i) != 0) {
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

static void *
race_destroyer_thread(void *arg) {
	struct list_race_state *state = (struct list_race_state *)arg;
	for (unsigned round = 0; round < LIST_RACE_DESTROY_ROUNDS; ++round) {
		yanet_error *err = NULL;
		struct agent *agent = agent_attach(
			state->shm, 0, state->name, LIST_RACE_AGENT_MEMORY, &err
		);
		if (agent == NULL) {
			fprintf(stderr,
				"destroyer attach failed: %s\n",
				err ? yanet_error_message(err) : "?");
			yanet_error_free(err);
			atomic_store_explicit(
				&state->writer_failed,
				true,
				memory_order_release
			);
			return NULL;
		}
		if (agent_destroy(agent)) {
			fprintf(stderr, "destroyer destroy failed\n");
			atomic_store_explicit(
				&state->writer_failed,
				true,
				memory_order_release
			);
			return NULL;
		}
	}
	return NULL;
}

static void *
race_reader_thread(void *arg) {
	struct list_race_state *state = (struct list_race_state *)arg;
	while (!atomic_load_explicit(&state->stop, memory_order_acquire)) {
		struct cp_agent_list_info *list =
			yanet_get_cp_agent_list_info(state->dp_config);
		if (list == NULL || list->count == 0) {
			atomic_store_explicit(
				&state->reader_failed,
				true,
				memory_order_release
			);
			break;
		}
		cp_agent_list_info_free(list);
	}
	return NULL;
}

static int
run_agent_list_race_test(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	// One bootstrap agent per name fixes the dp/cp handles for the
	// threads; the writers supersede their name's head from there on.
	struct agent *agent_a = agent_attach(
		shm, 0, "list-race-a", LIST_RACE_AGENT_MEMORY, &err
	);
	TEST_ASSERT_NOT_NULL(agent_a, "agent_attach failed for bootstrap A");

	struct dp_config *dp_config = agent_dp_config(agent_a);
	struct cp_config *cp_config = ADDR_OF(&agent_a->cp_config);

	struct agent *agent_b = agent_attach(
		shm, 0, "list-race-b", LIST_RACE_AGENT_MEMORY, &err
	);
	TEST_ASSERT_NOT_NULL(agent_b, "agent_attach failed for bootstrap B");

	struct list_race_state states[LIST_RACE_WRITERS] = {
		{
			.shm = shm,
			.dp_config = dp_config,
			.cp_config = cp_config,
			.name = "list-race-a",
		},
		{
			.shm = shm,
			.dp_config = dp_config,
			.cp_config = cp_config,
			.name = "list-race-b",
		},
	};
	struct list_race_state destroyer_state = {
		.shm = shm,
		.dp_config = dp_config,
		.cp_config = cp_config,
		.name = "list-race-d",
	};

	pthread_t writers[LIST_RACE_WRITERS];
	for (size_t idx = 0; idx < LIST_RACE_WRITERS; ++idx) {
		atomic_init(&states[idx].stop, false);
		atomic_init(&states[idx].writer_failed, false);
		atomic_init(&states[idx].reader_failed, false);
	}
	atomic_init(&destroyer_state.stop, false);
	atomic_init(&destroyer_state.writer_failed, false);
	atomic_init(&destroyer_state.reader_failed, false);

	// Every thread that starts is stopped and joined before any
	// assertion can leave: a failed create must not strand running
	// threads in a fixture the caller is about to tear down.
	pthread_t readers[LIST_RACE_READERS];
	size_t readers_started = 0;
	int create_rc = 0;
	for (; readers_started < LIST_RACE_READERS; ++readers_started) {
		create_rc = pthread_create(
			&readers[readers_started],
			NULL,
			race_reader_thread,
			&states[0]
		);
		if (create_rc != 0) {
			break;
		}
	}

	size_t writers_started = 0;
	if (create_rc == 0) {
		for (; writers_started < LIST_RACE_WRITERS; ++writers_started) {
			create_rc = pthread_create(
				&writers[writers_started],
				NULL,
				race_writer_thread,
				&states[writers_started]
			);
			if (create_rc != 0) {
				break;
			}
		}
	}

	pthread_t destroyer;
	bool destroyer_started = false;
	if (create_rc == 0) {
		create_rc = pthread_create(
			&destroyer,
			NULL,
			race_destroyer_thread,
			&destroyer_state
		);
		destroyer_started = (create_rc == 0);
	}

	for (size_t idx = 0; idx < writers_started; ++idx) {
		TEST_ASSERT_EQUAL(
			pthread_join(writers[idx], NULL),
			0,
			"pthread_join failed for writer"
		);
	}
	if (destroyer_started) {
		TEST_ASSERT_EQUAL(
			pthread_join(destroyer, NULL),
			0,
			"pthread_join failed for destroyer"
		);
	}

	atomic_store_explicit(&states[0].stop, true, memory_order_release);
	for (size_t idx = 0; idx < readers_started; ++idx) {
		TEST_ASSERT_EQUAL(
			pthread_join(readers[idx], NULL),
			0,
			"pthread_join failed for reader"
		);
	}

	TEST_ASSERT_EQUAL(
		create_rc, 0, "pthread_create failed for a race thread"
	);

	TEST_ASSERT(
		!atomic_load_explicit(
			&states[0].writer_failed, memory_order_acquire
		) &&
			!atomic_load_explicit(
				&states[1].writer_failed, memory_order_acquire
			) &&
			!atomic_load_explicit(
				&destroyer_state.writer_failed,
				memory_order_acquire
			),
		"a writer failed during the race"
	);
	TEST_ASSERT(
		!atomic_load_explicit(
			&states[0].reader_failed, memory_order_acquire
		),
		"a reader failed during the race"
	);

	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("debug");

	const char *port_names[] = {"01:00.0"};
	const char *devs_to_load[] = {"plain"};

	struct dataplane_ut_config cfg = {
		.cp_memory = LIST_RACE_CP_MEMORY,
		.dp_memory = LIST_RACE_DP_MEMORY,
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

	int res = run_agent_list_race_test(shm);

	dataplane_ut_free(ut);

	return (res == TEST_SUCCESS) ? 0 : 1;
}
