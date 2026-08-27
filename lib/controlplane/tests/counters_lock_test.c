/*
 * Regression tests for GH#1914: the tagged counter read must not hold the
 * config lock across its matching, allocation and per-counter value-copy
 * work, and the generation pin protecting that unlocked window must not
 * let a racing config swap tear down a generation a read still touches.
 *
 * Installs enough pipeline counter storages that the unlocked work
 * dominates the call, then checks lock-hold ordering, pin correctness
 * under concurrent real config swaps, and that a pin's release never
 * leaks a retired generation.
 */

#include "api/agent.h"
#include "api/counter.h"

#include "common/test_assert.h"
#include "devices/plain/api/controlplane.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_pipeline.h"
#include "lib/controlplane/config/zone.h"
#include "lib/controlplane/tests/counter_surface.h"
#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/errors/errors.h"
#include "lib/logging/log.h"

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
// Number of separate reads the lock-hold test performs, so a poller that
// starts watching one cycle too late still gets another chance instead of
// failing the test outright.
#define LOCK_TEST_READ_CYCLES 3

// Poll the config try-lock at this interval while the read runs on the
// other thread, capped by an overall deadline generous enough to absorb
// scheduler jitter in a busy CI VM.
#define LOCK_TEST_POLL_INTERVAL_US 100
#define LOCK_TEST_POLL_DEADLINE_NS (15ull * 1000 * 1000 * 1000)

struct reader_args {
	struct dp_config *dp_config;
	struct counter_worker_set_list *sets;
	// Cycle number of the read currently in flight.
	//
	// Bumped right before each read call. A companion flag stays set for
	// the duration of that call. The main thread uses these to notice a
	// fresh cycle starting and to poll the lock while that cycle is in
	// progress.
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
		struct counter_worker_set_list *sets =
			yanet_get_counters_by_tags_per_worker(
				args->dp_config, tags, 2, NULL, NULL
			);
		atomic_store_explicit(
			&args->in_progress, false, memory_order_release
		);
		if (sets == NULL) {
			atomic_store_explicit(
				&args->failed, true, memory_order_release
			);
			break;
		}
		if (i + 1 == LOCK_TEST_READ_CYCLES) {
			args->sets = sets;
		} else {
			yanet_counter_worker_set_list_free(sets);
		}
	}
	atomic_store_explicit(&args->done, true, memory_order_release);
	return NULL;
}

static uint64_t
now_ns(void) {
	struct timespec ts;
	clock_gettime(CLOCK_MONOTONIC, &ts);
	return (uint64_t)ts.tv_sec * 1000000000ull + (uint64_t)ts.tv_nsec;
}

// Verifies that a large tagged counter read releases the config lock long
// before it returns, instead of holding it across matching, allocation
// and the whole per-counter value-copy pass.
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
		install_counter_surface(
			agent,
			dp_config,
			cp_config,
			"dev0",
			"lock-pipe",
			LOCK_TEST_PIPELINE_COUNT
		),
		"failed to install the lock-test counter surface"
	);

	struct reader_args args = {
		.dp_config = dp_config,
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

		// Poll the try-lock while this read cycle stays in progress.
		// A success observed before the flag drops demonstrates the
		// lock was not held for the whole call. A hold reintroduced
		// across the whole call instead makes every attempt fail
		// until the flag drops, which this loop then reports as not
		// witnessed for the cycle.
		while (now_ns() < deadline &&
		       atomic_load_explicit(
			       &args.in_progress, memory_order_acquire
		       )) {
			if (cp_config_try_lock(cp_config)) {
				bool still_in_progress = atomic_load_explicit(
					&args.in_progress, memory_order_acquire
				);
				cp_config_unlock(cp_config);
				if (still_in_progress) {
					witnessed = true;
				}
				break;
			}
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
		"never witnessed a lock success while a read stayed in "
		"progress across %d read cycles",
		LOCK_TEST_READ_CYCLES
	);

	TEST_ASSERT_NOT_NULL(
		args.sets, "yanet_get_counters_by_tags_per_worker failed"
	);
	struct counter_worker_set *final_set =
		yanet_get_counter_worker_set(args.sets, 0);
	TEST_ASSERT_NOT_NULL(final_set, "worker 0 set is missing");
	TEST_ASSERT_EQUAL(
		final_set->counters->count,
		(uint64_t)LOCK_TEST_PIPELINE_COUNT *
			LOCK_TEST_COUNTERS_PER_PIPELINE,
		"unexpected matched counter count"
	);
	yanet_counter_worker_set_list_free(args.sets);

	agent_detach(agent);
	return TEST_SUCCESS;
}

#define RACE_TEST_PIPELINE_COUNT 80
#define RACE_TEST_READ_CYCLES 40
#define RACE_TEST_WRITE_SAFETY_DEADLINE_NS (30ull * 1000 * 1000 * 1000)

// Shared state for the racing-swap regression test: a writer thread keeps
// genuinely retiring "race-pipe-*" storages (a fresh pipeline object per
// reinstall) while a reader thread concurrently reads them, exercising a
// pin's release against a generation a racing update has already retired.
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
		struct counter_worker_set_list *sets =
			yanet_get_counters_by_tags_per_worker(
				state->dp_config, tags, 2, NULL, NULL
			);
		struct counter_worker_set *set0 =
			sets != NULL ? yanet_get_counter_worker_set(sets, 0)
				     : NULL;
		if (set0 == NULL || set0->counters->count != expected_matches) {
			yanet_counter_worker_set_list_free(sets);
			atomic_store_explicit(
				&state->reader_failed,
				true,
				memory_order_release
			);
			break;
		}
		yanet_counter_worker_set_list_free(sets);
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
// fresh pipeline object and counter registry, so a release that still read
// through a retired generation would use-after-free under the address
// sanitizer.
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
		install_counter_surface(
			agent,
			dp_config,
			cp_config,
			"race-dev0",
			"race-pipe",
			RACE_TEST_PIPELINE_COUNT
		),
		"failed to install the race-test counter surface"
	);

	struct race_state state = {
		.dp_config = dp_config,
		.cp_config = cp_config,
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
	TEST_ASSERT(
		state.reader_iterations > 0,
		"reader never completed a single racing counter read"
	);

	agent_detach(agent);
	return TEST_SUCCESS;
}

// A tight budget so a leaked generation exhausts it quickly.
//
// Not merely a leaked handle list: a leaked generation alone must run the
// budget out within a small, deterministic number of cycles, rather than
// only near the end of a long run. Confirmed against a build with the
// release path reverted to a no-op: each unreleased generation costs
// roughly 47 KB against the 8 MB arena, so the swap failure lands about a
// fifth of the way through the cycle budget. A correct release keeps only
// one generation live and completes every cycle.
#define LEAK_TEST_CP_MEMORY (8u * 1024u * 1024u)
#define LEAK_TEST_DP_MEMORY (2u * 1024u * 1024u)
#define LEAK_TEST_AGENT_MEMORY (512u * 1024u)
#define LEAK_TEST_WORKER_COUNT 2
#define LEAK_TEST_PIPELINE_COUNT 1
#define LEAK_TEST_CYCLES 500

// Verifies that repeated read-then-swap cycles do not leak configuration
// generations.
//
// Generations live in the shared-memory allocator, invisible to a heap
// sanitizer, so a leak is instead caught by exhausting a budget
// deliberately too small to hold more than a handful of them.
static int
test_repeated_swap_no_generation_leak(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent =
		agent_attach(shm, 0, "cnt-leak", LEAK_TEST_AGENT_MEMORY, &err);
	TEST_ASSERT_NOT_NULL(
		agent,
		"agent_attach failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	struct dp_config *dp_config = agent_dp_config(agent);
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);

	TEST_ASSERT_SUCCESS(
		install_counter_surface(
			agent,
			dp_config,
			cp_config,
			"leak-dev0",
			"leak-pipe",
			LEAK_TEST_PIPELINE_COUNT
		),
		"failed to install the leak-test counter surface"
	);

	struct counter_tag tags[] = {
		{.key = "device", .value = "leak-dev0"},
		{.key = "kind", .value = "pipeline"},
	};

	for (unsigned i = 0; i < LEAK_TEST_CYCLES; ++i) {
		struct counter_worker_set_list *sets =
			yanet_get_counters_by_tags_per_worker(
				dp_config, tags, 2, NULL, NULL
			);
		TEST_ASSERT_NOT_NULL(
			sets, "read failed unexpectedly at cycle %u", i
		);
		yanet_counter_worker_set_list_free(sets);

		TEST_ASSERT_SUCCESS(
			install_pipelines(
				dp_config,
				cp_config,
				"leak-pipe",
				LEAK_TEST_PIPELINE_COUNT
			),
			"swap failed at cycle %u",
			i
		);
	}

	agent_detach(agent);
	return TEST_SUCCESS;
}

// Runs a test function against a fresh harness instance, so one test's
// generation swaps only ever rebuild its own pipeline set instead of also
// re-walking another test's much larger one.
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

	int rc = run_with_harness(&cfg, test_lock_released_during_value_copy);
	if (rc != 0) {
		return rc;
	}

	rc = run_with_harness(&cfg, test_racing_swap_no_uaf);
	if (rc != 0) {
		return rc;
	}

	struct dataplane_ut_config leak_cfg = cfg;
	leak_cfg.cp_memory = LEAK_TEST_CP_MEMORY;
	leak_cfg.dp_memory = LEAK_TEST_DP_MEMORY;
	leak_cfg.worker_count = LEAK_TEST_WORKER_COUNT;

	return run_with_harness(
		&leak_cfg, test_repeated_swap_no_generation_leak
	);
}
