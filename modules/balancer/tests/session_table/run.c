#include "api/session_table.h"

#include "controlplane.h"
#include "dataplane/session_table.h"
#include "run.h"
#include "tests/utils/mock.h"
#include "worker.h"

#include "helpers.h"
#include <assert.h>
#include <lib/logging/log.h>

#include <pthread.h>
#include <stdlib.h>

////////////////////////////////////////////////////////////////////////////////

static void *
run_cp(void *watcher) {
	run_watcher((struct watcher *)watcher);
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

	void *sessions_memory = arena - workers_cnt * (1 << 20);
	arena_size -= workers_cnt * (1 << 20);

	struct mock *mock = mock_init(arena, arena_size);
	if (mock == NULL) {
		LOG(ERROR, "failed to init mock");
		return 1;
	}
	struct agent *agent = mock_create_agent(mock, arena_size - (1 << 20));
	if (agent == NULL) {
		LOG(ERROR, "failed to create mock agent");
		return 1;
	}

	// Init balancer state
	struct balancer_session_table *session_table =
		balancer_session_table_create(agent, capacity);
	if (session_table == NULL) {
		LOG(ERROR, "failed to initialize balancer session table");
		return 1;
	}
	LOG(INFO, "initialized balancer state");

	// init watcher
	struct watcher watcher;
	watcher.session_table = session_table;
	atomic_store(&watcher.stop, 0);

	// Run controlplance
	pthread_t cp;
	int res = pthread_create(&cp, NULL, run_cp, &watcher);
	if (res != 0) {
		LOG(ERROR, "failed to create watcher thread, errno=%d\n", errno
		);
		return 1;
	}

	LOG(INFO, "launched watcher");

	// Run workers
	LOG(INFO, "trying to initialize and run workers...");
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
		cfg->sessions = gen_sessions(sessions, sessions_memory, i);
		if (cfg->sessions == NULL) {
			LOG(ERROR,
			    "Failed to allocate memory for worker sessions");
			return 1;
		}
		sessions_memory =
			cfg->sessions + sizeof(struct session_id) * sessions;
		cfg->session_count = sessions;
		cfg->iterations = iterations;
		cfg->worker_idx = i;
		cfg->session_table = session_table;
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

	// Stop watcher
	LOG(INFO, "waiting for controlplane...");
	atomic_store(&watcher.stop, 1);
	res = pthread_join(cp, NULL);
	if (res != 0) {
		LOG(ERROR, "watcher failed");
		return 1;
	}

	balancer_session_table_free(session_table);

	LOG(INFO, "OK");

	double insert_failure_perc = 100.0 * (double)insert_failures /
				     (double)(iterations * workers_cnt);
	LOG(INFO, "insert failures: %.4lf%%", insert_failure_perc);

	double elapsed_s = elapsed_ns / 1e9;
	LOG(INFO,
	    "elapsed: %.2lfs (%.2lf MRPS)",
	    elapsed_s,
	    (double)(iterations * workers_cnt) / 1e6 / elapsed_s);

	return 0;
}