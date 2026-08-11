/*
 * Regression tests for GH#1914: the tagged counter read must not hold the
 * config lock across its per-counter value-copy loop, and the reference
 * pin protecting that unlocked window must not read a storage's registry
 * after a racing config swap has already freed it.
 *
 * This installs enough pipeline counter storages that the value-copy phase
 * clearly dominates the call, then exercises both the lock-hold ordering
 * and, concurrently with real config swaps, the pin and release
 * correctness.
 */

#include "api/agent.h"
#include "api/counter.h"

#include "common/test_assert.h"
#include "controlplane/agent/agent.h"
#include "devices/plain/api/controlplane.h"
#include "lib/controlplane/config/cp_pipeline.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/errors/errors.h"
#include "logging/log.h"

#include <pthread.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <time.h>
#include <unistd.h>

#define LOCK_TEST_CP_MEMORY (512u * 1024u * 1024u)
#define LOCK_TEST_DP_MEMORY (16u * 1024u * 1024u)
#define LOCK_TEST_AGENT_MEMORY (16u * 1024u * 1024u)
#define LOCK_TEST_WORKER_COUNT 4
#define LOCK_TEST_PIPELINE_COUNT 1000
// Each length-0 pipeline registers 6 counters: input, output, drop,
// input histogram, pending_input, pending_output.
#define LOCK_TEST_COUNTERS_PER_PIPELINE 6
// Number of separate reads the lock-hold test performs, so a single cycle
// that the polling loop starts watching too late does not vacuously pass.
#define LOCK_TEST_READ_CYCLES 3

// Poll the config try-lock at this interval while the read runs on the
// other thread, capped by an overall deadline generous enough to absorb
// scheduler jitter in a busy CI VM.
#define LOCK_TEST_POLL_INTERVAL_US 100
#define LOCK_TEST_POLL_DEADLINE_NS (15ull * 1000 * 1000 * 1000)

struct reader_args {
	struct dp_config *dp_config;
	struct counter_handle_list *list;
	// Cycle number of the read currently in flight, bumped right before
	// each read call. A companion flag stays set for the duration of
	// that call. The main thread uses these to notice a fresh cycle
	// starting and to confirm a later successful lock attempt happened
	// before the read returned.
	atomic_uint cycle;
	atomic_bool in_progress;
	atomic_bool done;
	atomic_bool failed;
};

static void *
reader_thread(void *arg) {
	struct reader_args *args = (struct reader_args *)arg;
	struct counter_tag tags[] = {
		{.key = "device", .value = "dev0"},
		{.key = "kind", .value = "pipeline"},
	};
	for (unsigned i = 0; i < LOCK_TEST_READ_CYCLES; ++i) {
		atomic_fetch_add_explicit(
			&args->cycle, 1, memory_order_relaxed
		);
		atomic_store_explicit(
			&args->in_progress, true, memory_order_release
		);
		struct counter_handle_list *list = yanet_get_counters_by_tags(
			args->dp_config, tags, 2, NULL, -1, NULL
		);
		atomic_store_explicit(
			&args->in_progress, false, memory_order_release
		);
		if (list == NULL) {
			atomic_store_explicit(
				&args->failed, true, memory_order_release
			);
			break;
		}
		if (i + 1 == LOCK_TEST_READ_CYCLES) {
			args->list = list;
		} else {
			yanet_counter_handle_list_free(list);
		}
	}
	atomic_store_explicit(&args->done, true, memory_order_release);
	return NULL;
}

static int
install_pipelines(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	const char *name_prefix,
	size_t count
) {
	yanet_error *err = NULL;
	struct cp_pipeline_config **cfgs = calloc(count, sizeof(*cfgs));
	TEST_ASSERT_NOT_NULL(cfgs, "failed to allocate pipeline config array");

	for (size_t idx = 0; idx < count; ++idx) {
		cfgs[idx] = calloc(1, sizeof(struct cp_pipeline_config));
		TEST_ASSERT_NOT_NULL(
			cfgs[idx], "failed to allocate pipeline config %zu", idx
		);
		snprintf(
			cfgs[idx]->name,
			CP_PIPELINE_NAME_LEN,
			"%s-%zu",
			name_prefix,
			idx
		);
		cfgs[idx]->length = 0;
	}

	int rc = cp_config_update_pipelines(
		dp_config, cp_config, count, cfgs, &err
	);

	for (size_t idx = 0; idx < count; ++idx) {
		free(cfgs[idx]);
	}
	free(cfgs);

	TEST_ASSERT_SUCCESS(
		rc,
		"update_pipelines failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	return TEST_SUCCESS;
}

// Install a device with every pipeline named using the given prefix and
// index, wired as an equal-weight input, so a counter registry lookup for
// that device under the pipeline kind tag matches one storage per pipeline,
// per worker. Reinstalling the same pipeline set always allocates a fresh
// pipeline object and counter registry per name, so it genuinely retires
// the previous generation's matched storages.
static int
install_device(
	struct agent *agent,
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	const char *device_name,
	const char *pipeline_prefix,
	size_t pipeline_count
) {
	yanet_error *err = NULL;
	struct cp_device_plain_config *cfg = cp_device_plain_config_new(
		device_name, pipeline_count, 0, &err
	);
	TEST_ASSERT_NOT_NULL(
		cfg,
		"device config new failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	char name[CP_PIPELINE_NAME_LEN];
	for (size_t idx = 0; idx < pipeline_count; ++idx) {
		snprintf(name, sizeof(name), "%s-%zu", pipeline_prefix, idx);
		int rc = cp_device_plain_config_set_input_pipeline(
			cfg, idx, name, 1
		);
		TEST_ASSERT_EQUAL(
			rc, 0, "set_input_pipeline failed at index %zu", idx
		);
	}

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
	return TEST_SUCCESS;
}

static uint64_t
now_ns(void) {
	struct timespec ts;
	clock_gettime(CLOCK_MONOTONIC, &ts);
	return (uint64_t)ts.tv_sec * 1000000000ull + (uint64_t)ts.tv_nsec;
}

// Verifies that a large tagged counter read releases the config lock
// before it returns, instead of holding it across the whole per-counter
// value-copy loop.
static int
test_lock_released_during_value_copy(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent =
		agent_attach(shm, 0, "cnt-1914", LOCK_TEST_AGENT_MEMORY, &err);
	TEST_ASSERT_NOT_NULL(
		agent,
		"agent_attach failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	struct dp_config *dp_config = agent_dp_config(agent);
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);

	TEST_ASSERT_SUCCESS(
		install_pipelines(
			dp_config,
			cp_config,
			"lock-pipe",
			LOCK_TEST_PIPELINE_COUNT
		),
		"failed to install pipelines"
	);
	TEST_ASSERT_SUCCESS(
		install_device(
			agent,
			dp_config,
			cp_config,
			"dev0",
			"lock-pipe",
			LOCK_TEST_PIPELINE_COUNT
		),
		"failed to install dev0"
	);

	struct reader_args args = {
		.dp_config = dp_config,
		.list = NULL,
		.cycle = 0,
		.in_progress = false,
		.done = false,
		.failed = false,
	};

	pthread_t reader;
	int rc = pthread_create(&reader, NULL, reader_thread, &args);
	TEST_ASSERT_EQUAL(rc, 0, "pthread_create failed");

	bool witnessed = false;
	unsigned seen_cycle = 0;
	uint64_t deadline = now_ns() + LOCK_TEST_POLL_DEADLINE_NS;

	while (!witnessed && now_ns() < deadline) {
		unsigned cycle = 0;
		bool started = false;
		while (now_ns() < deadline) {
			cycle = atomic_load_explicit(
				&args.cycle, memory_order_acquire
			);
			if (cycle != seen_cycle &&
			    atomic_load_explicit(
				    &args.in_progress, memory_order_acquire
			    )) {
				started = true;
				break;
			}
			if (atomic_load_explicit(
				    &args.done, memory_order_acquire
			    )) {
				break;
			}
			usleep(LOCK_TEST_POLL_INTERVAL_US);
		}
		if (!started) {
			break;
		}
		seen_cycle = cycle;

		// Require a contended lock attempt before trusting a later
		// success as evidence the read released the lock mid-call.
		// Otherwise a success sampled before the reader even takes
		// the lock would pass vacuously.
		bool saw_contention = false;
		while (now_ns() < deadline) {
			if (cp_config_try_lock(cp_config)) {
				bool still_in_progress = atomic_load_explicit(
					&args.in_progress, memory_order_acquire
				);
				cp_config_unlock(cp_config);
				if (saw_contention && still_in_progress) {
					witnessed = true;
				}
				break;
			}
			saw_contention = true;
			usleep(LOCK_TEST_POLL_INTERVAL_US);
		}
	}

	pthread_join(reader, NULL);

	TEST_ASSERT(
		!atomic_load_explicit(&args.failed, memory_order_acquire),
		"reader thread reported a failure"
	);
	TEST_ASSERT(
		witnessed,
		"never witnessed cp_config_lock contention followed by a "
		"mid-call release across %d read cycles",
		LOCK_TEST_READ_CYCLES
	);

	TEST_ASSERT_NOT_NULL(args.list, "yanet_get_counters_by_tags failed");
	TEST_ASSERT_EQUAL(
		args.list->count,
		(uint64_t)LOCK_TEST_PIPELINE_COUNT *
			LOCK_TEST_COUNTERS_PER_PIPELINE,
		"unexpected matched counter count"
	);
	yanet_counter_handle_list_free(args.list);

	// Reference-count balance check: reinstalling the same pipeline set
	// always allocates a fresh pipeline object and counter registry per
	// name, genuinely retiring the previous generation's matched
	// storages. A pin left unbalanced by the reads above would
	// double-free or leak here.
	TEST_ASSERT_SUCCESS(
		install_pipelines(
			dp_config,
			cp_config,
			"lock-pipe",
			LOCK_TEST_PIPELINE_COUNT
		),
		"failed to reinstall pipelines"
	);

	struct counter_tag tags[] = {
		{.key = "device", .value = "dev0"},
		{.key = "kind", .value = "pipeline"},
	};
	struct counter_handle_list *recheck =
		yanet_get_counters_by_tags(dp_config, tags, 2, NULL, -1, NULL);
	TEST_ASSERT_NOT_NULL(recheck, "post-update re-read failed");
	TEST_ASSERT_EQUAL(
		recheck->count,
		(uint64_t)LOCK_TEST_PIPELINE_COUNT *
			LOCK_TEST_COUNTERS_PER_PIPELINE,
		"match count changed after reinstalling the same pipeline set"
	);
	yanet_counter_handle_list_free(recheck);

	agent_detach(agent);
	return TEST_SUCCESS;
}

#define RACE_TEST_PIPELINE_COUNT 80
#define RACE_TEST_READ_CYCLES 40
#define RACE_TEST_WRITE_SAFETY_DEADLINE_NS (30ull * 1000 * 1000 * 1000)

// Shared state for the racing-swap regression test: a writer thread keeps
// genuinely retiring "race-pipe-*" storages (a fresh pipeline object per
// reinstall) while a reader thread concurrently reads them, exercising the
// pin release against a storage whose registry may already be freed.
struct race_state {
	struct dp_config *dp_config;
	struct cp_config *cp_config;
	atomic_bool stop;
	atomic_bool reader_failed;
	atomic_bool writer_failed;
	uint64_t reader_iterations;
	uint64_t writer_iterations;
};

static void *
race_reader_thread(void *arg) {
	struct race_state *state = (struct race_state *)arg;
	struct counter_tag tags[] = {
		{.key = "device", .value = "race-dev0"},
		{.key = "kind", .value = "pipeline"},
	};
	// Every generation carries the full "race-pipe-*" set, so the match
	// count is deterministic even while the writer is swapping storages.
	uint64_t expected_matches = (uint64_t)RACE_TEST_PIPELINE_COUNT *
				    LOCK_TEST_COUNTERS_PER_PIPELINE;
	for (uint64_t i = 0; i < RACE_TEST_READ_CYCLES; ++i) {
		struct counter_handle_list *list = yanet_get_counters_by_tags(
			state->dp_config, tags, 2, NULL, -1, NULL
		);
		if (list == NULL || list->count != expected_matches) {
			if (list != NULL) {
				yanet_counter_handle_list_free(list);
			}
			atomic_store_explicit(
				&state->reader_failed,
				true,
				memory_order_release
			);
			break;
		}
		yanet_counter_handle_list_free(list);
		state->reader_iterations = i + 1;
	}
	atomic_store_explicit(&state->stop, true, memory_order_release);
	return NULL;
}

static void *
race_writer_thread(void *arg) {
	struct race_state *state = (struct race_state *)arg;
	uint64_t deadline = now_ns() + RACE_TEST_WRITE_SAFETY_DEADLINE_NS;
	while (!atomic_load_explicit(&state->stop, memory_order_acquire) &&
	       now_ns() < deadline) {
		if (install_pipelines(
			    state->dp_config,
			    state->cp_config,
			    "race-pipe",
			    RACE_TEST_PIPELINE_COUNT
		    ) != TEST_SUCCESS) {
			atomic_store_explicit(
				&state->writer_failed,
				true,
				memory_order_release
			);
			break;
		}
		state->writer_iterations += 1;
	}
	return NULL;
}

// Verifies that concurrent reads survive a racing config swap that
// genuinely retires the matched storages: each reinstall below allocates a
// fresh pipeline object and counter registry, so an unpin that still read
// the retired registry would use-after-free under the address sanitizer.
static int
test_racing_swap_no_uaf(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent =
		agent_attach(shm, 0, "cnt-race", LOCK_TEST_AGENT_MEMORY, &err);
	TEST_ASSERT_NOT_NULL(
		agent,
		"agent_attach failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	struct dp_config *dp_config = agent_dp_config(agent);
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);

	TEST_ASSERT_SUCCESS(
		install_pipelines(
			dp_config,
			cp_config,
			"race-pipe",
			RACE_TEST_PIPELINE_COUNT
		),
		"failed to install race pipelines"
	);
	TEST_ASSERT_SUCCESS(
		install_device(
			agent,
			dp_config,
			cp_config,
			"race-dev0",
			"race-pipe",
			RACE_TEST_PIPELINE_COUNT
		),
		"failed to install race-dev0"
	);

	struct race_state state = {
		.dp_config = dp_config,
		.cp_config = cp_config,
		.stop = false,
		.reader_failed = false,
		.writer_failed = false,
		.reader_iterations = 0,
		.writer_iterations = 0,
	};

	pthread_t writer;
	int rc = pthread_create(&writer, NULL, race_writer_thread, &state);
	TEST_ASSERT_EQUAL(rc, 0, "pthread_create(writer) failed");

	pthread_t reader;
	rc = pthread_create(&reader, NULL, race_reader_thread, &state);
	if (rc != 0) {
		// Stop and reap the writer before failing out, so it does not
		// keep writing into this stack frame and installing pipelines
		// into an arena the caller is about to tear down.
		atomic_store_explicit(&state.stop, true, memory_order_release);
		pthread_join(writer, NULL);
		TEST_ASSERT_EQUAL(rc, 0, "pthread_create(reader) failed");
	}

	pthread_join(reader, NULL);
	pthread_join(writer, NULL);

	TEST_ASSERT(
		!atomic_load_explicit(
			&state.reader_failed, memory_order_acquire
		),
		"reader thread reported a failure"
	);
	TEST_ASSERT(
		!atomic_load_explicit(
			&state.writer_failed, memory_order_acquire
		),
		"writer thread reported a failure"
	);
	TEST_ASSERT(
		state.writer_iterations > 0,
		"writer never completed a single racing pipeline reinstall"
	);

	agent_detach(agent);
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("info");

	const char *port_names[] = {"01:00.0"};
	const char *devs_to_load[] = {"plain"};

	struct dataplane_ut_config cfg = {
		.cp_memory = LOCK_TEST_CP_MEMORY,
		.dp_memory = LOCK_TEST_DP_MEMORY,
		.worker_count = LOCK_TEST_WORKER_COUNT,
		.devices = port_names,
		.device_count = 1,
		.modules = NULL,
		.module_count = 0,
		.devices_to_load = devs_to_load,
		.devices_to_load_count = 1,
	};

	// Each test gets its own harness instance, so the racing test's
	// generation swaps only ever rebuild its own small pipeline set
	// instead of also re-walking the lock-hold test's 1000 pipelines.
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
	int res = test_lock_released_during_value_copy(shm);
	dataplane_ut_free(ut);
	if (res != TEST_SUCCESS) {
		return 1;
	}

	ut = dataplane_ut_new(&cfg);
	if (ut == NULL) {
		fprintf(stderr, "dataplane_ut_new failed\n");
		return 1;
	}
	shm = dataplane_ut_shm(ut);
	if (shm == NULL) {
		fprintf(stderr, "dataplane_ut_shm returned NULL\n");
		dataplane_ut_free(ut);
		return 1;
	}
	res = test_racing_swap_no_uaf(shm);
	dataplane_ut_free(ut);

	return (res == TEST_SUCCESS) ? 0 : 1;
}
