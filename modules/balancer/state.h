#pragma once

#include "common/detail/ttlmap/bucket.h"
#include "common/ttlmap.h"
#include "subprojects/dpdk/lib/eal/include/rte_common.h"
#include <assert.h>
#include <dirent.h>
#include <rte_build_config.h>
#include <rte_common.h>

#include "defines.h"
#include "session.h"

////////////////////////////////////////////////////////////////////////////////

#define BALANCER_MAX_WORKERS_NUM 64

////////////////////////////////////////////////////////////////////////////////

#define BALANCER_SESSION_FOUND TTLMAP_FOUND
#define BALANCER_SESSION_CREATED (TTLMAP_INSERTED | TTLMAP_REPLACED)
#define BALANCER_GET_SESSION_FAILED TTLMAP_FAILED

////////////////////////////////////////////////////////////////////////////////

struct worker_local {
	uint8_t use_prev_gen;	// atomic
	uint8_t __padding[63];	// NOLINT
	size_t active_sessions; // space occupied by worker
	uint32_t max_deadline_current_gen;
	uint32_t max_deadline_prev_gen;
};

struct balancer_sessions_storage_gen {
	__rte_cache_aligned struct ttlmap session_table;
	size_t table_capacity;
	__rte_cache_aligned struct worker_local
		worker_local[BALANCER_MAX_WORKERS_NUM];
};

////////////////////////////////////////////////////////////////////////////////

struct balancer_state {
	struct balancer_sessions_storage_gen generations[2];
	size_t current_gen;
	size_t workers_cnt;
	struct memory_context *mctx;
};

int
balancer_state_init(
	struct balancer_state *state,
	size_t workers_cnt,
	size_t capacity,
	struct memory_context *mctx
);

void
balancer_state_free(struct balancer_state *state);

static inline struct balancer_sessions_storage_gen *
balancer_get_cur_storage_gen(struct balancer_state *state) {
	return &state->generations[state->current_gen & 1];
}

static inline struct balancer_sessions_storage_gen *
balancer_get_prev_storage_gen(struct balancer_state *state) {
	return &state->generations[(state->current_gen & 1) ^ 1];
}

static inline int
balancer_get_session(
	struct balancer_state *state,
	size_t worker_idx,
	uint32_t now,
	uint32_t timeout,
	struct balancer_session_id *session_id,
	struct balancer_session_state **session_state,
	balancer_session_lock_t **lock
) {
	struct balancer_sessions_storage_gen *sessions_cur =
		balancer_get_cur_storage_gen(state);
	int ret = TTLMAP_GET(
		&sessions_cur->session_table,
		session_id,
		session_state,
		lock,
		now,
		timeout
	);
	struct worker_local *worker_local =
		&sessions_cur->worker_local[worker_idx];
	if (ret == TTLMAP_FOUND) {
		worker_local->max_deadline_current_gen =
			RTE_MAX(worker_local->max_deadline_current_gen,
				now + timeout);
		return ret;
	} else if (ret == TTLMAP_INSERTED || ret == TTLMAP_REPLACED) {
		worker_local->active_sessions += (ret == TTLMAP_INSERTED);
		if (worker_local->use_prev_gen == 1) {
			if (worker_local->max_deadline_prev_gen +
				    STATE_TIMEOUT_DEFAULT <
			    now) {
				worker_local->use_prev_gen = 1;
				return BALANCER_SESSION_CREATED;
			}
			struct balancer_sessions_storage_gen *sessions_prev =
				balancer_get_prev_storage_gen(state);
			ret = TTLMAP_LOOKUP(
				&sessions_prev->session_table,
				session_id,
				*session_state,
				now
			);
			if (ret == TTLMAP_FOUND) {
				return BALANCER_SESSION_FOUND;
			} else {
				return BALANCER_SESSION_CREATED;
			}
		} else {
			return BALANCER_SESSION_CREATED;
		}
	} else { // ret == TTLMAP_FAILED
		return BALANCER_GET_SESSION_FAILED;
	}
}

static inline void
balancer_invalidate_session(struct balancer_session_state *state) {
	TTLMAP_REMOVE(struct balancer_session_id, state);
}

static inline void
balancer_unlock_session(balancer_session_lock_t *lock) {
	ttlmap_release_lock(lock);
}

////////////////////////////////////////////////////////////////////////////////

static inline int
balancer_try_free_unused(struct balancer_state *state);

////////////////////////////////////////////////////////////////////////////////

int
balancer_extend_state_on_demand(struct balancer_state *state);