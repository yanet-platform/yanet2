#pragma once

#include "state.h"

////////////////////////////////////////////////////////////////////////////////

void workers_prepare_globals();

////////////////////////////////////////////////////////////////////////////////

struct worker_config {
    struct balancer_session_id *sessions;
    size_t session_count;
    uint32_t timeout_min;
    uint32_t timeout_max;
    uint32_t worker_idx;
    uint32_t iterations;
    struct balancer_state *balancer;
    struct run_worker_result *result;
};

struct run_worker_result {
    uint32_t elapsed_ms;
    uint32_t failed;
};

void
run_worker(struct worker_config *config);