#include "common/memory.h"
#include "common/memory_address.h"
#include "common/ttlmap.h"
#include "rte_common.h"

#include "state.h"
#include <assert.h>

////////////////////////////////////////////////////////////////////////////////

void
worker_info_init(struct worker_info *info) {
	info->active_sessions = 0;
	info->density_factor = 0;
	info->max_deadline_current_gen = 0;
	info->max_deadline_prev_gen = 0;
	info->use_prev_gen = 0;
}

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
	int res = TTLMAP_INIT(
		&state->generations[0].session_table,
		mctx,
		struct balancer_session_id,
		struct balancer_session_state,
		capacity
	);
	for (size_t i = 0; i < workers_cnt; ++i) {
		struct worker_info *worker_info =
			&state->generations[0].worker_info[i];
		worker_info_init(worker_info);
	}
	return res;
}

////////////////////////////////////////////////////////////////////////////////

void
balancer_state_free(struct balancer_state *state) {
	struct balancer_sessions_storage_gen *cur_storage =
		balancer_get_cur_storage_gen(state);
	if (balancer_session_table_capacity(cur_storage) > 0) {
		TTLMAP_FREE(&cur_storage->session_table);
	}

	struct balancer_sessions_storage_gen *prev_storage =
		balancer_get_prev_storage_gen(state);
	if (balancer_session_table_capacity(prev_storage) > 0) {
		TTLMAP_FREE(&prev_storage->session_table);
	}
}

////////////////////////////////////////////////////////////////////////////////

int
balancer_extend_state_on_demand(struct balancer_state *state) {
	struct balancer_sessions_storage_gen *sessions_cur =
		balancer_get_cur_storage_gen(state);
	size_t active_sessions = 0;
	uint32_t density_factor = 0;
	for (size_t i = 0; i < state->workers_cnt; ++i) {
		struct worker_info *worker_info = &sessions_cur->worker_info[i];
		if (WORKER_GET_ATOMIC(worker_info, use_prev_gen) == 1) {
			return 0;
		}
		active_sessions +=
			WORKER_GET_ATOMIC(worker_info, active_sessions);
		density_factor =
			RTE_MAX(density_factor,
				WORKER_GET_ATOMIC(worker_info, density_factor));
	}

	size_t current_table_cap =
		balancer_session_table_capacity(sessions_cur);

	LOG(INFO,
	    "[balancer state] density_factor=%u, active_sessions=%zu, "
	    "session_table_capacity=%zu (filled by "
	    "%.2lf%%)",
	    density_factor,
	    active_sessions,
	    current_table_cap,
	    100.0 * active_sessions / current_table_cap);

	if (density_factor >= 7) {
		balancer_try_free_unused(state);
		struct balancer_sessions_storage_gen *sessions_next =
			balancer_get_prev_storage_gen(state);
		size_t next_gen_cap = current_table_cap * 2;
		int ret = TTLMAP_INIT(
			&sessions_next->session_table,
			ADDR_OF(&state->mctx),
			struct balancer_session_id,
			struct balancer_session_state,
			next_gen_cap
		);
		if (ret != 0) {
			return -1;
		}
		for (size_t i = 0; i < state->workers_cnt; ++i) {
			struct worker_info *worker_info =
				&sessions_next->worker_info[i];
			struct worker_info *prev_worker_info =
				&sessions_cur->worker_info[i];

			worker_info_init(worker_info);

			worker_info->max_deadline_prev_gen =
				prev_worker_info->max_deadline_current_gen;
			worker_info->use_prev_gen = 1;
		}
		__c11_atomic_fetch_add(
			&state->current_gen, 1, __ATOMIC_SEQ_CST
		);
		/// @todo: add memory barrier here
		return 1;
	} else {
		return 0;
	}
}

////////////////////////////////////////////////////////////////////////////////

int
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
	if (balancer_session_table_capacity(sessions_prev) > 0) {
		TTLMAP_FREE(&sessions_prev->session_table);
		return 1;
	}
	return 0;
}