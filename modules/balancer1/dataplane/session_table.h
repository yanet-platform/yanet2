#pragma once

#include "common/ttlmap.h"

#include "session.h"
#include <stdatomic.h>

////////////////////////////////////////////////////////////////////////////////

#define SESSION_FOUND TTLMAP_FOUND
#define SESSION_CREATED (TTLMAP_INSERTED | TTLMAP_REPLACED)
#define SESSION_TABLE_OVERFLOW TTLMAP_FAILED

////////////////////////////////////////////////////////////////////////////////

struct worker_info {
	_Atomic uint8_t use_prev_gen; // atomic
	uint8_t pad[63];
	_Atomic uint32_t max_deadline_current_gen;
	_Atomic uint32_t max_deadline_prev_gen;
	_Atomic uint32_t active_sessions; // sessions created by worker
	_Atomic uint32_t density_factor;
} __rte_cache_aligned;

#define MAX_WORKERS_NUM 64

struct session_table_gen {
	struct ttlmap map;
	struct worker_info worker_info[MAX_WORKERS_NUM];
};

////////////////////////////////////////////////////////////////////////////////

struct balancer_session_table {
	struct session_table_gen generations[2];
	_Atomic uint32_t current_gen; // workers read, cp modify
	uint32_t workers_cnt;

	// relative pointer to the memory context of the
	// agent who created session table
	struct memory_context *mctx;

	// shift of &balancer_session_table in memory
	// which allows to deallocate table properly.
	uint32_t memory_shift;
};

////////////////////////////////////////////////////////////////////////////////

static inline struct session_table_gen *
session_table_current_gen(struct balancer_session_table *state) {
	uint32_t current_gen =
		atomic_load_explicit(&state->current_gen, __ATOMIC_SEQ_CST);
	return &state->generations[current_gen & 1];
}

static inline struct session_table_gen *
session_table_previous_gen(struct balancer_session_table *state) {
	uint32_t current_gen =
		atomic_load_explicit(&state->current_gen, __ATOMIC_SEQ_CST);
	return &state->generations[(current_gen & 1) ^ 1];
}

static inline int
get_or_create_session(
	struct balancer_session_table *session_table,
	uint32_t worker_idx,
	uint32_t now,
	uint32_t timeout,
	struct session_id *session_id,
	struct session_state **session_state,
	session_lock_t **lock
) {
	struct session_table_gen *cur =
		session_table_current_gen(session_table);

	int res = TTLMAP_GET(
		&cur->map, session_id, session_state, lock, now, timeout
	);
	int status = TTLMAP_STATUS(res);
	uint32_t meta = TTLMAP_META(res);

	struct worker_info *worker_info = &cur->worker_info[worker_idx];
	uint32_t new_density_factor =
		RTE_MAX(meta, worker_info->density_factor);
	atomic_store_explicit(
		&worker_info->density_factor,
		new_density_factor,
		__ATOMIC_SEQ_CST
	);

	if (status == TTLMAP_FOUND) {
		uint32_t new_max_deadline =
			RTE_MAX(atomic_load_explicit(
					&worker_info->max_deadline_current_gen,
					__ATOMIC_SEQ_CST
				),
				now + timeout);
		atomic_store_explicit(
			&worker_info->max_deadline_current_gen,
			new_max_deadline,
			__ATOMIC_SEQ_CST
		);
		return SESSION_FOUND;
	} else if (status == TTLMAP_INSERTED || status == TTLMAP_REPLACED) {
		if (status == TTLMAP_INSERTED) {
			atomic_fetch_add_explicit(
				&worker_info->active_sessions,
				1,
				__ATOMIC_SEQ_CST
			);
		}
		if (atomic_load_explicit(
			    &worker_info->use_prev_gen, __ATOMIC_SEQ_CST
		    ) == 1) {
			if (atomic_load_explicit(
				    &worker_info->max_deadline_prev_gen,
				    __ATOMIC_SEQ_CST
			    ) < now) {
				atomic_store_explicit(
					&worker_info->use_prev_gen,
					0,
					__ATOMIC_SEQ_CST
				);
				return SESSION_CREATED;
			}
			struct session_table_gen *prev =
				session_table_previous_gen(session_table);
			status = TTLMAP_LOOKUP(
				&prev->map, session_id, *session_state, now
			);
			if (status == TTLMAP_FOUND) {
				return SESSION_FOUND;
			} else {
				return SESSION_CREATED;
			}
		} else {
			return SESSION_CREATED;
		}
	} else { // status == TTLMAP_FAILED
		return SESSION_TABLE_OVERFLOW;
	}
}

static inline void
session_invalidate(struct session_state *session_state) {
	TTLMAP_REMOVE(struct session_id, session_state);
}

static inline void
session_unlock(session_lock_t *lock) {
	ttlmap_release_lock(lock);
}

#undef MAX_WORKERS_NUM