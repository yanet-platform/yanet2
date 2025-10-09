#include "worker.h"
#include "helpers.h"
#include "session.h"
#include "state.h"

#include <assert.h>
#include <sys/time.h>
#include <time.h>

////////////////////////////////////////////////////////////////////////////////

static _Atomic uint32_t iterations = 0;

void
workers_prepare_globals() {
	__c11_atomic_store(&iterations, 0, __ATOMIC_SEQ_CST);
}

////////////////////////////////////////////////////////////////////////////////

void
run_worker(struct worker_config *config) {
	uint64_t worker_start_ns = get_time_ns();

	config->run_result->failed = 0;
	uint64_t rng = config->worker_idx;
	for (size_t i = 0; i < config->iterations; ++i) {
		size_t modulo = 1ll << ((63 - __builtin_clzll(i + 1)) + 1);
		if (modulo > config->session_count) {
			modulo = config->session_count;
		}
		size_t idx = rng_next(&rng) % modulo;
		struct balancer_session_id *session_id = &config->sessions[idx];
		uint32_t timeout = config->timeout_min +
				   (rng_next(&rng) % (config->timeout_max -
						      config->timeout_min + 1) +
				    config->timeout_min);
		uint32_t now = __c11_atomic_fetch_add(
			&iterations, 1, __ATOMIC_SEQ_CST
		);
		struct balancer_session_state *session_state;
		balancer_session_lock_t *session_lock;
		int res = balancer_get_or_create_session(
			config->balancer,
			config->worker_idx,
			now,
			timeout,
			session_id,
			&session_state,
			&session_lock
		);
		if (res == BALANCER_SESSION_TABLE_OVERFLOW) {
			LOG(WARN,
			    "worker #%u failed to insert on %zu iteration",
			    config->worker_idx,
			    i + 1);
			++config->run_result->failed;
			continue;
		}
		if (res == BALANCER_SESSION_CREATED) {
			session_state->create_timestamp = now;
			session_state->real_id = 100;
		}
		session_state->last_packet_timestamp = now;
		session_state->timeout = timeout;
		balancer_session_unlock(session_lock);
	}

	uint64_t worker_end_ns = get_time_ns();
	uint32_t elapsed_ms = (double)(worker_end_ns - worker_start_ns) / 1e6;
	config->run_result->elapsed_ms = elapsed_ms;
}