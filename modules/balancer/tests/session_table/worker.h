#pragma once

#include "dataplane/session_table.h"
#include <stddef.h>

////////////////////////////////////////////////////////////////////////////////

void
workers_prepare_globals();

////////////////////////////////////////////////////////////////////////////////

struct worker_run_result {
	uint32_t elapsed_ms;
	uint32_t failed;
};

struct worker_config {
	struct session_id *sessions;
	size_t session_count;
	uint32_t timeout_min;
	uint32_t timeout_max;
	uint32_t worker_idx;
	uint32_t iterations;
	struct balancer_session_table *session_table;
	struct worker_run_result *run_result;
};

void
run_worker(struct worker_config *config);