/*
 * Tests for the configuration-lock instrumentation: every acquisition is
 * attributed to the API site that made it, and the wait (time spent
 * acquiring) and hold (time spent locked) durations land in that site's
 * counters with sane totals and maxima.
 *
 * The hold/wait bounds are generous one-sided checks: durations may only
 * be underestimated by scheduling jitter, never overestimated beyond the
 * sleeps and lock holds the test itself creates.
 */

#include "api/agent.h"
#include "api/info.h"

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
#include <string.h>
#include <time.h>
#include <unistd.h>

#define STATS_TEST_CP_MEMORY (64u * 1024u * 1024u)
#define STATS_TEST_DP_MEMORY (16u * 1024u * 1024u)
#define STATS_TEST_AGENT_MEMORY (16u * 1024u * 1024u)
#define STATS_TEST_WORKER_COUNT 2

// Duration of the deliberate holds and waits the accounting checks
// measure, with the lower bounds they must survive in the counters.
#define STATS_TEST_HOLD_US (120 * 1000)
#define STATS_TEST_HOLD_MIN_NS (80ull * 1000 * 1000)
#define STATS_TEST_WAIT_MIN_NS (80ull * 1000 * 1000)
// An uncontended acquisition must not report a wait anywhere near the
// deliberate hold lengths.
#define STATS_TEST_UNCONTENDED_WAIT_MAX_NS (10ull * 1000 * 1000)
// Poll interval and deadline for noticing the waiter thread has reached
// the config lock, sized like the counters_lock test's poll loop.
#define STATS_TEST_POLL_INTERVAL_US 100
#define STATS_TEST_ARRIVAL_DEADLINE_NS (15ull * 1000 * 1000 * 1000)

static uint64_t
now_ns(void) {
	struct timespec ts;
	clock_gettime(CLOCK_MONOTONIC, &ts);
	return (uint64_t)ts.tv_sec * 1000000000ull + (uint64_t)ts.tv_nsec;
}

// Snapshot of one site's counters, copied out of the stats info list.
struct site_snapshot {
	bool found;
	uint64_t acquisitions;
	uint64_t wait_ns;
	uint64_t wait_max_ns;
	uint64_t hold_ns;
	uint64_t hold_max_ns;
};

// Snapshot one site's counters into out; the assert macros demand an
// int-returning function.
static int
read_site(
	struct dp_config *dp_config,
	enum cp_config_lock_site site,
	struct site_snapshot *out
) {
	struct cp_config_lock_stats_info *stats =
		yanet_get_cp_config_lock_stats(dp_config);
	TEST_ASSERT_NOT_NULL(stats, "failed to read lock stats");
	TEST_ASSERT_EQUAL(
		stats->site_count,
		(uint64_t)CP_CONFIG_LOCK_SITE_COUNT,
		"unexpected site count"
	);

	memset(out, 0, sizeof(*out));
	for (uint64_t idx = 0; idx < stats->site_count; ++idx) {
		const struct cp_config_lock_site_info *info =
			&stats->sites[idx];
		TEST_ASSERT(
			info->name[0] != '\0', "site %lu has an empty name", idx
		);
		if (idx == (uint64_t)site) {
			out->found = true;
			out->acquisitions = info->acquisitions;
			out->wait_ns = info->wait_ns;
			out->wait_max_ns = info->wait_max_ns;
			out->hold_ns = info->hold_ns;
			out->hold_max_ns = info->hold_max_ns;
		}
	}

	cp_config_lock_stats_info_free(stats);
	return TEST_SUCCESS;
}

// Deliberate hold under a tagged site, accounted as hold time.
static int
test_hold_accounting(struct dp_config *dp_config, struct cp_config *cp_config) {
	struct site_snapshot before;
	TEST_ASSERT_SUCCESS(
		read_site(
			dp_config, CP_CONFIG_LOCK_SITE_UPDATE_MODULES, &before
		),
		"failed to read the site before the hold"
	);

	cp_config_lock_site(cp_config, CP_CONFIG_LOCK_SITE_UPDATE_MODULES);
	usleep(STATS_TEST_HOLD_US);
	cp_config_unlock(cp_config);

	struct site_snapshot after;
	TEST_ASSERT_SUCCESS(
		read_site(
			dp_config, CP_CONFIG_LOCK_SITE_UPDATE_MODULES, &after
		),
		"failed to read the site after the hold"
	);

	TEST_ASSERT_EQUAL(
		after.acquisitions,
		before.acquisitions + 1,
		"the deliberate hold must be accounted as one acquisition"
	);
	TEST_ASSERT(
		after.hold_ns - before.hold_ns >= STATS_TEST_HOLD_MIN_NS,
		"hold duration (%lu ns) under-reported the deliberate %d us "
		"hold",
		after.hold_ns - before.hold_ns,
		STATS_TEST_HOLD_US
	);
	TEST_ASSERT(
		after.wait_ns - before.wait_ns <=
			STATS_TEST_UNCONTENDED_WAIT_MAX_NS,
		"uncontended acquisition reported %lu ns of wait",
		after.wait_ns - before.wait_ns
	);
	TEST_ASSERT(
		after.hold_max_ns >= STATS_TEST_HOLD_MIN_NS,
		"hold maximum (%lu ns) missed the deliberate hold",
		after.hold_max_ns
	);
	TEST_ASSERT(
		after.wait_max_ns <= STATS_TEST_UNCONTENDED_WAIT_MAX_NS ||
			before.wait_max_ns == after.wait_max_ns,
		"wait maximum grew to %lu ns without contention",
		after.wait_max_ns
	);

	return TEST_SUCCESS;
}

struct holder_args {
	struct cp_config *cp_config;
	// Set while this thread verifiably holds the lock, so the main
	// thread observing the flag knows its own acquisition must block
	// behind the deliberate hold: contention is confirmed, not merely
	// imminent. A flag set before the lock attempt could not prove
	// that — an oversubscribed runner might never schedule the attempt
	// inside the hold window.
	atomic_bool holding;
	// Set by the main thread right before it enters the tagged
	// acquisition: the deliberate hold starts only then. Nothing the
	// main thread does before its lock call — including the mid-wait
	// snapshot below — can therefore eat into, or delay its entry
	// into, the measured window.
	atomic_bool start_hold;
};

// Takes the lock untagged (landing in OTHER, keeping the measured site
// clean), announces the hold, and holds for the deliberate window once
// the main thread signals it is entering its acquisition.
static void *
holder_thread(void *arg) {
	struct holder_args *args = (struct holder_args *)arg;
	cp_config_lock(args->cp_config);
	atomic_store_explicit(&args->holding, true, memory_order_release);
	while (!atomic_load_explicit(&args->start_hold, memory_order_acquire)) {
		usleep(STATS_TEST_POLL_INTERVAL_US);
	}
	usleep(STATS_TEST_HOLD_US);
	cp_config_unlock(args->cp_config);
	return NULL;
}

// Contended acquisition, accounted as wait time on the main thread's site
// behind a holder's deliberate hold.
static int
test_wait_accounting(struct dp_config *dp_config, struct cp_config *cp_config) {
	struct site_snapshot before;
	TEST_ASSERT_SUCCESS(
		read_site(
			dp_config, CP_CONFIG_LOCK_SITE_DELETE_PIPELINE, &before
		),
		"failed to read the site before the wait"
	);

	struct holder_args args = {.cp_config = cp_config};
	atomic_init(&args.holding, false);
	atomic_init(&args.start_hold, false);

	pthread_t holder;
	int rc = pthread_create(&holder, NULL, holder_thread, &args);
	TEST_ASSERT_EQUAL(rc, 0, "pthread_create failed");

	// Wait until the holder verifiably holds the lock, capped by a
	// deadline generous enough for a busy CI VM.
	uint64_t deadline = now_ns() + STATS_TEST_ARRIVAL_DEADLINE_NS;
	while (!atomic_load_explicit(&args.holding, memory_order_acquire) &&
	       now_ns() < deadline) {
		usleep(STATS_TEST_POLL_INTERVAL_US);
	}
	TEST_ASSERT(
		atomic_load_explicit(&args.holding, memory_order_acquire),
		"holder never acquired the config lock"
	);

	// Snapshot the measured site while the acquisition is still pending;
	// the hold has not started yet, so this scan cannot shorten it.
	struct site_snapshot mid;
	TEST_ASSERT_SUCCESS(
		read_site(dp_config, CP_CONFIG_LOCK_SITE_DELETE_PIPELINE, &mid),
		"failed to read the site before the contended acquisition"
	);
	TEST_ASSERT_EQUAL(
		mid.acquisitions,
		before.acquisitions,
		"the contended site must stay untouched until the tagged "
		"acquisition completes"
	);

	// Start the hold and enter the acquisition immediately after: the
	// holder sees the signal within one poll interval, so the call
	// lands well inside the 120 ms window and must block to its end.
	atomic_store_explicit(&args.start_hold, true, memory_order_release);
	cp_config_lock_site(cp_config, CP_CONFIG_LOCK_SITE_DELETE_PIPELINE);
	cp_config_unlock(cp_config);

	pthread_join(holder, NULL);

	struct site_snapshot after;
	TEST_ASSERT_SUCCESS(
		read_site(
			dp_config, CP_CONFIG_LOCK_SITE_DELETE_PIPELINE, &after
		),
		"failed to read the site after the wait"
	);

	TEST_ASSERT_EQUAL(
		after.acquisitions,
		before.acquisitions + 1,
		"the contended acquisition must be accounted"
	);
	TEST_ASSERT(
		after.wait_ns - before.wait_ns >= STATS_TEST_WAIT_MIN_NS,
		"wait duration (%lu ns) under-reported the contended %d us "
		"hold",
		after.wait_ns - before.wait_ns,
		STATS_TEST_HOLD_US
	);
	TEST_ASSERT(
		after.wait_max_ns >= STATS_TEST_WAIT_MIN_NS,
		"wait maximum (%lu ns) missed the contended acquisition",
		after.wait_max_ns
	);

	return TEST_SUCCESS;
}

// Public API entry points account on their own sites: an attach, a
// memory-limit read and one tagged counter read each land where their
// names say, and an untouched site stays at zero.
static int
test_api_attribution(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm, 0, "lock-stats", STATS_TEST_AGENT_MEMORY, &err
	);
	TEST_ASSERT_NOT_NULL(
		agent,
		"agent_attach failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	struct dp_config *dp_config = agent_dp_config(agent);
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);

	struct site_snapshot attach_before;
	TEST_ASSERT_SUCCESS(
		read_site(
			dp_config,
			CP_CONFIG_LOCK_SITE_AGENT_ATTACH,
			&attach_before
		),
		"failed to read the attach site"
	);
	TEST_ASSERT(
		attach_before.acquisitions >= 1,
		"agent_attach did not account on its site"
	);

	(void)agent_memory_limit(agent);
	struct site_snapshot limit_after;
	TEST_ASSERT_SUCCESS(
		read_site(
			dp_config,
			CP_CONFIG_LOCK_SITE_AGENT_MEMORY_LIMIT,
			&limit_after
		),
		"failed to read the memory-limit site"
	);
	TEST_ASSERT(
		limit_after.acquisitions >= 1,
		"agent_memory_limit did not account on its site"
	);

	TEST_ASSERT_SUCCESS(
		install_counter_surface(
			agent,
			dp_config,
			cp_config,
			"stats-dev0",
			"stats-pipe",
			1
		),
		"failed to install the counter surface"
	);

	struct site_snapshot counters_before;
	TEST_ASSERT_SUCCESS(
		read_site(
			dp_config,
			CP_CONFIG_LOCK_SITE_GET_COUNTERS,
			&counters_before
		),
		"failed to read the counters site"
	);
	struct counter_tag tags[] = {
		{.key = "device", .value = "stats-dev0"},
		{.key = "kind", .value = "pipeline"},
	};
	struct counter_worker_set_list *sets =
		yanet_get_counters_by_tags_per_worker(
			dp_config, tags, 2, NULL, NULL
		);
	TEST_ASSERT_NOT_NULL(sets, "counter read failed");
	yanet_counter_worker_set_list_free(sets);

	struct site_snapshot counters_after;
	TEST_ASSERT_SUCCESS(
		read_site(
			dp_config,
			CP_CONFIG_LOCK_SITE_GET_COUNTERS,
			&counters_after
		),
		"failed to read the counters site"
	);
	// One read accounts two acquisitions: the generation pin and its
	// release.
	TEST_ASSERT(
		counters_after.acquisitions >= counters_before.acquisitions + 2,
		"a counter read must account its pin and unpin"
	);

	struct site_snapshot untouched;
	TEST_ASSERT_SUCCESS(
		read_site(
			dp_config, CP_CONFIG_LOCK_SITE_DELETE_DEVICE, &untouched
		),
		"failed to read the untouched site"
	);
	TEST_ASSERT_EQUAL(
		untouched.acquisitions,
		0,
		"an untouched site must stay at zero acquisitions"
	);

	agent_detach(agent);
	return TEST_SUCCESS;
}

// Runs a test function against a fresh harness instance so the stats
// table starts from a clean zero for every check.
static int
run_with_harness(
	const struct dataplane_ut_config *cfg,
	int (*test_fn)(struct dp_config *, struct cp_config *)
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
	struct dp_config *dp_config = yanet_shm_dp_config(shm, 0);
	struct cp_config *cp_config = ADDR_OF(&dp_config->cp_config);
	int res = test_fn(dp_config, cp_config);
	dataplane_ut_free(ut);
	return (res == TEST_SUCCESS) ? 0 : 1;
}

int
main(void) {
	log_enable_name("info");

	const char *port_names[] = {"01:00.0"};
	const char *devs_to_load[] = {"plain"};

	struct dataplane_ut_config cfg = {
		.cp_memory = STATS_TEST_CP_MEMORY,
		.dp_memory = STATS_TEST_DP_MEMORY,
		.worker_count = STATS_TEST_WORKER_COUNT,
		.devices = port_names,
		.device_count = 1,
		.modules = NULL,
		.module_count = 0,
		.devices_to_load = devs_to_load,
		.devices_to_load_count = 1,
	};

	int rc = run_with_harness(&cfg, test_hold_accounting);
	if (rc != 0) {
		return rc;
	}

	rc = run_with_harness(&cfg, test_wait_accounting);
	if (rc != 0) {
		return rc;
	}

	// The attribution test attaches its own agent.
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
	rc = test_api_attribution(shm);
	dataplane_ut_free(ut);
	if (rc != TEST_SUCCESS) {
		return 1;
	}

	return 0;
}
