#include <assert.h>
#include <lib/logging/log.h>

#include <pthread.h>
#include <stdlib.h>

#include "run.h"

////////////////////////////////////////////////////////////////////////////////

#define ARENA_SIZE ((size_t)1 << 30)

////////////////////////////////////////////////////////////////////////////////

struct run_params {
	size_t workers_cnt;
	size_t session_table_capacity;
	size_t sessions_per_worker;
	size_t worker_iterations;
	size_t session_timeout_min;
	size_t session_timeout_max;
	const char *description;
};

////////////////////////////////////////////////////////////////////////////////

int
main() {
	log_enable_name("debug");

	void *arena = malloc(ARENA_SIZE);
	if (arena == NULL) {
		LOG(ERROR, "Failed to allocate memory arena");
		return 1;
	}
	LOG(INFO, "Allocated memory arena");

	struct run_params tests[] = {
		{1, 1000, 100, 10000, 50, 1000, "small test single worker"},
		{2, 1000, 100, 10000, 50, 1000, "small test two workers"},
		{4, 1000, 100, 10000, 50, 1000, "small test four workers"},
		{4, 1e5, 1e3, 1e5, 50, 1000, "medium test four workers 1"},
		{4,
		 1e5,
		 1e3,
		 1e5,
		 1e4,
		 1e5,
		 "medium test four workers big timeouts"},
		{4,
		 1e5,
		 1e5,
		 1e5,
		 50,
		 1000,
		 "big test four workers many sessions small timeout"},
		{4,
		 1e6,
		 1e5,
		 1e5,
		 1e4,
		 1e5,
		 "big test four workers many sessions big timeout"}
	};

	size_t retries = 5;
	size_t tests_failed = 0;
	size_t test_count = sizeof(tests) / sizeof(struct run_params);
	for (size_t test = 0; test < test_count; ++test) {
		struct run_params *test_params = &tests[test];
		size_t retries_failed = 0;
		for (size_t retry = 0; retry < retries; ++retry) {
			LOG(INFO,
			    "Running Test #%zu (retry #%zu): '%s'...",
			    test + 1,
			    retry,
			    test_params->description);
			int run_result =
				run(arena,
				    ARENA_SIZE,
				    test_params->workers_cnt,
				    test_params->session_table_capacity,
				    test_params->sessions_per_worker,
				    test_params->worker_iterations,
				    test_params->session_timeout_min,
				    test_params->session_timeout_max);
			if (run_result == 1) {
				++retries_failed;
			}
		}
		if (retries_failed > 0) {
			++tests_failed;
			LOG(ERROR,
			    "Test #%zu failed (%zu times of %zu retries)",
			    test + 1,
			    retries_failed,
			    retries);
		} else {
			LOG(INFO, "Test #%zu passed", test + 1);
		}
	}

	free(arena);

	if (tests_failed == 0) {
		LOG(INFO, "All tests successfully passed");
		return 0;
	} else {
		LOG(ERROR, "%zu/%zu tests failed", tests_failed, test_count);
		return 1;
	}
}