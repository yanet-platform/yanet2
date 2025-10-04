#include "common/memory.h"
#include "common/memory_block.h"

#include "controlplane.h"
#include "helpers.h"
#include "state.h"
#include "worker.h"

#include <lib/logging/log.h>

#include <pthread.h>

////////////////////////////////////////////////////////////////////////////////

#define ARENA_SIZE (1 << 30)

////////////////////////////////////////////////////////////////////////////////

void *
run_cp(void *cfg) {
	run_controlplane((struct cp_config *)cfg);
	return NULL;
}

////////////////////////////////////////////////////////////////////////////////

void *
run_dp_worker(void *cfg) {
	run_worker((struct worker_config *)cfg);
	return NULL;
}

////////////////////////////////////////////////////////////////////////////////

int
main(int argc, char **argv) {
	log_enable_name("debug");

	if (argc < 7) {
		printf("Usage %s: <WORKERS> <CAPACITY> <SESSIONS PER WORKER> "
		       "<WORKER ITERATIONS> <MIN SESSION TIMEOUT> <MAX SESSION "
		       "TIMEOUT>\n",
		       argv[0]);
		return 1;
	}
	uint32_t workers_cnt = atoi(argv[1]);
	uint32_t capacity = atoi(argv[2]);
	uint32_t sessions = atoi(argv[3]);
	uint32_t iterations = atoi(argv[4]);
	uint32_t timeout_min = atoi(argv[5]);
	uint32_t timeout_max = atoi(argv[6]);

	LOG(INFO,
	    "Starting Balancer State test with the following "
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

	// Allocate memory
	LOG(INFO,
	    "Trying to allocate memory arena of %zu bytes...",
	    (size_t)ARENA_SIZE);
	void *arena = malloc(ARENA_SIZE);
	if (arena == NULL) {
		LOG(ERROR, "Failed to allocate memory arena");
		return 1;
	}
	LOG(INFO, "Allocated memory arena");

	struct block_allocator alloc;
	block_allocator_init(&alloc);
	block_allocator_put_arena(&alloc, arena, ARENA_SIZE);
	struct memory_context mctx;

	LOG(INFO, "Trying to initialize memory context...");
	int res = memory_context_init(&mctx, "test", &alloc);
	if (res != 0) {
		LOG(ERROR, "Failed to initialize memory context");
		return 1;
	}
	LOG(INFO, "Initialized memory context");

	// Init balancer state
	LOG(INFO, "Trying to initialize balancer state...");
	struct balancer_state balancer;
	res = balancer_state_init(&balancer, workers_cnt, capacity, &mctx);
	if (res != 0) {
		LOG(ERROR, "Failed to initialize balancer state");
		return 1;
	}
	LOG(INFO, "Initialized balancer state");

	// Init controlplane
	LOG(INFO, "Trying to initialize and run controlplane...");
	struct cp_config cp_config;
	cp_config.balancer = &balancer;
	__c11_atomic_store(&cp_config.stop, 0, __ATOMIC_SEQ_CST);

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

	for (uint32_t i = 0; i < workers_cnt; ++i) {
		LOG(INFO,
		    "Trying to initialize and run %zu-th worker...",
		    (size_t)i + 1);
		struct worker_config *cfg = &workers[i].cfg;
		cfg->run_result = &workers[i].run_result;
		cfg->sessions = gen_sessions(sessions, &mctx, i);
		if (cfg->sessions == NULL) {
			LOG(ERROR,
			    "Failed to allocate memory for worker sessions");
			return 1;
		}
		LOG(DEBUG, "Gen session done");
		cfg->session_count = sessions;
		cfg->iterations = iterations;
		cfg->worker_idx = i;
		cfg->balancer = &balancer;
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

	// Wait for workers
	LOG(INFO, "Waiting for workers...");
	for (uint32_t i = 0; i < workers_cnt; ++i) {
		LOG(INFO, "Waiting for worker %zu", (size_t)i + 1);
		res = pthread_join(workers[i].thread, NULL);
		if (res != 0) {
			LOG(ERROR, "Worker %zu failed", (size_t)i + 1);
			return 1;
		}
		struct worker_run_result *result = &workers[i].run_result;
		LOG(INFO,
		    "Worker %zu done in %ums",
		    (size_t)i + 1,
		    result->elapsed_ms);
		if (result->failed > 0) {
			LOG(ERROR,
			    "Worker %zu failed to insert %u times",
			    (size_t)i + 1,
			    result->failed);
		}
	}

	LOG(INFO, "All workers done");

	// Stop controlplane
	LOG(INFO, "Waiting for controlplane...");
	__c11_atomic_store(&cp_config.stop, 1, __ATOMIC_SEQ_CST);
	res = pthread_join(cp, NULL);
	if (res != 0) {
		LOG(ERROR, "Controlplane failed");
		return 1;
	}

	balancer_state_free(&balancer);

	free(arena);

	LOG(INFO, "Controlplane done");

	LOG(INFO, "OK");

	return 0;
}