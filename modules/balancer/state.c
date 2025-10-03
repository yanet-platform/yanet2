#include "common/memory.h"
#include "common/memory_address.h"
#include "common/ttlmap.h"

#include "state.h"
#include <assert.h>

////////////////////////////////////////////////////////////////////////////////

int
balancer_state_init(
	struct balancer_state *state,
	size_t workers_cnt,
	size_t capacity,
	struct memory_context *mctx
) {
	SET_OFFSET_OF(&state->mctx, mctx);
	state->current_gen = 0;
	state->workers_cnt = workers_cnt;
	state->generations[0].session_table_capacity = capacity;
	int ret = TTLMAP_INIT(
		&state->generations[0].session_table,
		mctx,
		struct balancer_session_id,
		struct balancer_session_state,
		capacity
	);
	for (size_t i = 0; i < workers_cnt; ++i) {
		struct worker_info *worker_local =
			&state->generations[0].worker_info[i];
		worker_local->max_deadline_current_gen = 0;
		worker_local->max_deadline_prev_gen = 0;
		worker_local->use_prev_gen = 0;
		worker_local->active_sessions = 0;
	}
	return ret;
}

////////////////////////////////////////////////////////////////////////////////

void
balancer_state_free(struct balancer_state *state) {
	struct balancer_sessions_storage_gen *cur_storage =
		balancer_get_cur_storage_gen(state);
	if (cur_storage->session_table_capacity > 0) {
		TTLMAP_FREE(&cur_storage->session_table);
	}

	struct balancer_sessions_storage_gen *prev_storage =
		balancer_get_prev_storage_gen(state);
	if (prev_storage->session_table_capacity > 0) {
		TTLMAP_FREE(&prev_storage->session_table);
	}
}

////////////////////////////////////////////////////////////////////////////////

int
balancer_extend_state_on_demand(struct balancer_state *state) {
	struct balancer_sessions_storage_gen *sessions_cur =
		balancer_get_cur_storage_gen(state);
	size_t active_sessions = 0;
	for (size_t i = 0; i < state->workers_cnt; ++i) {
		struct worker_info *worker_info = &sessions_cur->worker_info[i];
		if (worker_info->use_prev_gen == 1) {
			return 0;
		}
		// active_sessions += worker_info->active_sessions
		active_sessions +=
			WORKER_GET_ATOMIC(worker_info, active_sessions);
	}
	if (active_sessions * 10 > sessions_cur->session_table_capacity * 8) {
		int free_result = balancer_try_free_unused(state);
		assert(free_result == 1);
		struct balancer_sessions_storage_gen *sessions_next =
			balancer_get_prev_storage_gen(state);
		int ret = TTLMAP_INIT(
			&sessions_next->session_table,
			ADDR_OF(&state->mctx),
			struct balancer_session_id,
			struct balancer_session_state,
			sessions_cur->session_table_capacity * 2
		);
		if (ret != 0) {
			return -1;
		}
		for (size_t i = 0; i < state->workers_cnt; ++i) {
			struct worker_info *worker_local =
				&sessions_next->worker_info[i];
			worker_local->active_sessions = 0;
			worker_local->max_deadline_current_gen = 0;
			worker_local->max_deadline_prev_gen =
				sessions_cur->worker_info[i]
					.max_deadline_current_gen;
			worker_local->use_prev_gen = 1;
		}
		__c11_atomic_fetch_add(
			&state->current_gen, 1, __ATOMIC_SEQ_CST
		);
		return 1;
	} else {
		return 0;
	}
}

////////////////////////////////////////////////////////////////////////////////

static inline int
balancer_try_free_unused(struct balancer_state *state) {
	struct balancer_sessions_storage_gen *sessions_cur =
		balancer_get_cur_storage_gen(state);
	for (size_t i = 0; i < state->workers_cnt; ++i) {
		if (WORKER_GET_ATOMIC(
			    &sessions_cur->worker_info[i], use_prev_gen
		    ) == 1) {
			return 0;
		}
	}
	struct balancer_sessions_storage_gen *sessions_prev =
		balancer_get_prev_storage_gen(state);
	if (sessions_prev->session_table_capacity > 0) {
		TTLMAP_FREE(&sessions_prev->session_table);
	}
	return 1;
}