#include "common/memory.h"
#include "common/memory_block.h"

#include "controlplane.h"
#include "helpers.h"
#include "run.h"
#include "state.h"
#include "worker.h"

#include <assert.h>
#include <lib/logging/log.h>

#include <pthread.h>

#include "../utils/balancer.h"

////////////////////////////////////////////////////////////////////////////////

static void *
run_cp(void *cfg) {
	run_controlplane((struct cp_config *)cfg);
	return NULL;
}

////////////////////////////////////////////////////////////////////////////////

static void *
run_dp_worker(void *cfg) {
	run_worker((struct worker_config *)cfg);
	return NULL;
}

////////////////////////////////////////////////////////////////////////////////

int
run(void *arena,
    size_t arena_size,
    uint32_t workers_cnt,
    uint32_t capacity,
    uint32_t sessions,
    uint32_t iterations,
    uint32_t timeout_min,
    uint32_t timeout_max) {
	LOG(INFO,
	    "Starting Balancer State Test with the following "
	    "params:\n\t\t\t\t\t\t\t- Number "
	    "of workers: %u\n\t\t\t\t\t\t\t- Initial session table capacity: "
	    "%u\n\t\t\t\t\t\t\t- Number of "
	    "sessions worker use: %u\n\t\t\t\t\t\t\t- Worker iterations: "
	    "%u\n\t\t\t\t\t\t\t- Min session "
	    "timeout: %u\n\t\t\t\t\t\t\t- Max session timeout: %u",
	    workers_cnt,
	    capacity,
	    sessions,
	    iterations,
	    timeout_min,
	    timeout_max);

	struct block_allocator alloc;
	block_allocator_init(&alloc);
	block_allocator_put_arena(&alloc, arena, arena_size);
	struct memory_context mctx;

	int res = memory_context_init(&mctx, "test", &alloc);
	if (res != 0) {
		LOG(ERROR, "Failed to initialize memory context");
		return 1;
	}
	LOG(INFO, "Initialized memory context");

	// Init balancer state
	struct balancer_state *balancer =
		make_balancer_state(&mctx, workers_cnt, capacity);
	if (balancer == NULL) {
		LOG(ERROR, "Failed to initialize balancer state");
		return 1;
	}
	LOG(INFO, "Initialized balancer state");

	// Init controlplane
	struct cp_config cp_config;
	cp_config.balancer = balancer;
	atomic_store(&cp_config.stop, 0);

	// Run controlplance
	pthread_t cp;
	res = pthread_create(&cp, NULL, run_cp, &cp_config);
	if (res != 0) {
		LOG(ERROR,
		    "Failed to create controlplane thread, errno=%d\n",
		    errno);
		return 1;
	}

	LOG(INFO, "Launched controlplane");

	// Run workers
	LOG(INFO, "Trying to initialize and run workers...");
	struct {
		pthread_t thread;
		struct worker_config cfg;
		struct worker_run_result run_result;
	} workers[64];

	uint64_t start_ns = get_time_ns();

	workers_prepare_globals();

	for (uint32_t i = 0; i < workers_cnt; ++i) {
		struct worker_config *cfg = &workers[i].cfg;
		cfg->run_result = &workers[i].run_result;
		cfg->sessions = gen_sessions(sessions, &mctx, i);
		if (cfg->sessions == NULL) {
			LOG(ERROR,
			    "Failed to allocate memory for worker sessions");
			return 1;
		}
		cfg->session_count = sessions;
		cfg->iterations = iterations;
		cfg->worker_idx = i;
		cfg->balancer = balancer;
		cfg->iterations = iterations;
		cfg->timeout_min = timeout_min;
		cfg->timeout_max = timeout_max;
		res = pthread_create(
			&workers[i].thread, NULL, run_dp_worker, cfg
		);
		if (res != 0) {
			LOG(ERROR,
			    "Failed to create worker thread, errno=%d\n",
			    errno);
			return 1;
		}
		LOG(INFO, "Launched %zu-th worker", (size_t)i + 1);
	}

	size_t insert_failures = 0;

	// Wait for workers
	LOG(INFO, "Waiting for workers...");
	for (uint32_t i = 0; i < workers_cnt; ++i) {
		res = pthread_join(workers[i].thread, NULL);
		if (res != 0) {
			LOG(ERROR, "Worker %zu failed", (size_t)i + 1);
			return 1;
		}
		struct worker_run_result *result = &workers[i].run_result;
		LOG(INFO,
		    "Worker %zu done in %ums (%.2lf MRPS)",
		    (size_t)i + 1,
		    result->elapsed_ms,
		    (iterations / 1e6) / ((double)result->elapsed_ms / 1000.0));
		if (result->failed > 0) {
			LOG(WARN,
			    "Worker %zu failed to insert %u times (%.6lf%%)",
			    (size_t)i + 1,
			    result->failed,
			    100.0 * result->failed / iterations);
		} else {
			LOG(INFO,
			    "Worker %zu successfully inserted all of the "
			    "entries",
			    (size_t)i + 1);
		}
		insert_failures += result->failed;
	}

	uint64_t elapsed_ns = get_time_ns() - start_ns;

	LOG(INFO, "All workers done");
	if (insert_failures > 0) {
		LOG(WARN,
		    "Insert failures: %zu (%.6lf%%)",
		    insert_failures,
		    100.0 * insert_failures / (workers_cnt * iterations));
	} else {
		LOG(INFO, "Insert failures: %zu", insert_failures);
	}

	// Stop controlplane
	LOG(INFO, "Waiting for controlplane...");
	atomic_store(&cp_config.stop, 1);
	res = pthread_join(cp, NULL);
	if (res != 0) {
		LOG(ERROR, "Controlplane failed");
		return 1;
	}

	balancer_state_free(balancer);

	LOG(INFO, "OK");

	double elapsed_s = elapsed_ns / 1e9;
	LOG(INFO,
	    "Elapsed: %.2lfs (%.2lf MRPS)",
	    elapsed_s,
	    (double)(iterations * workers_cnt) / 1e6 / elapsed_s);

	double insert_failure_perc = 100.0 * (double)insert_failures /
				     (double)(iterations * workers_cnt);
	if (insert_failure_perc > 0.01) {
		LOG(ERROR,
		    "Too big insert failures per (%.4lf%%)",
		    insert_failure_perc);
		return 1;
	}

	return 0;
}