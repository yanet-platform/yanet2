#pragma once

#include "common/memory.h"
#include "common/ttlmap/ttlmap.h"

#include "session.h"
#include <assert.h>
#include <stdatomic.h>
#include <stdio.h>

#include "worker.h"

////////////////////////////////////////////////////////////////////////////////

#define SESSION_FOUND TTLMAP_FOUND
#define SESSION_CREATED (TTLMAP_INSERTED | TTLMAP_REPLACED)
#define SESSION_TABLE_OVERFLOW TTLMAP_FAILED

////////////////////////////////////////////////////////////////////////////////

struct worker_info {
	// updated on the start of handle modules
	_Atomic uint32_t last_timestamp;

	uint8_t pad[63];
	_Atomic uint32_t max_deadline_current_gen;
	_Atomic uint32_t max_deadline_prev_gen;
} __rte_cache_aligned;

static inline int
worker_info_use_prev_gen(struct worker_info *info) {
	return info->last_timestamp < info->max_deadline_prev_gen;
}

////////////////////////////////////////////////////////////////////////////////

struct session_table_gen {
	struct ttlmap map;
	struct worker_info worker_info[MAX_WORKERS_NUM];
};

////////////////////////////////////////////////////////////////////////////////

struct session_table {
	struct session_table_gen generations[2];
	_Atomic uint32_t current_gen; // workers read, cp modify
	size_t workers;

	// relative pointer to the memory context of the
	// agent who created session table
	struct memory_context *mctx;
};

////////////////////////////////////////////////////////////////////////////////

int
session_table_init(
	struct session_table *table,
	struct memory_context *mctx,
	size_t size,
	size_t workers
);

void
session_table_free(struct session_table *table);

size_t
session_table_capacity(struct session_table *table);

////////////////////////////////////////////////////////////////////////////////

int
session_table_fill_sessions_info(
	struct session_table *table,
	struct balancer_sessions_info *info,
	struct memory_context *mctx,
	uint32_t now,
	bool only_count
);

void
session_table_free_sessions_info(
	struct balancer_sessions_info *info, struct memory_context *mctx
);

////////////////////////////////////////////////////////////////////////////////

static inline struct session_table_gen *
session_table_current_gen(struct session_table *state) {
	uint32_t current_gen =
		atomic_load_explicit(&state->current_gen, __ATOMIC_SEQ_CST);
	return &state->generations[current_gen & 1];
}

static inline struct session_table_gen *
session_table_previous_gen(struct session_table *state) {
	uint32_t current_gen =
		atomic_load_explicit(&state->current_gen, __ATOMIC_SEQ_CST);
	return &state->generations[(current_gen & 1) ^ 1];
}

////////////////////////////////////////////////////////////////////////////////

static inline void
session_table_update_worker_time(
	struct session_table *table, size_t worker, uint32_t now
) {
	struct session_table_gen *sessions_cur =
		session_table_current_gen(table);
	struct worker_info *worker_info = &sessions_cur->worker_info[worker];
	worker_info->last_timestamp = now;
}

////////////////////////////////////////////////////////////////////////////////

static inline int
get_or_create_session(
	struct session_table *session_table,
	uint32_t worker_idx,
	uint32_t now,
	uint32_t timeout,
	struct balancer_session_id *session_id,
	struct balancer_session_state **session_state,
	session_lock_t **lock
) {
	struct session_table_gen *cur =
		session_table_current_gen(session_table);

	int res = TTLMAP_GET(
		&cur->map, session_id, session_state, lock, now, timeout
	);
	int status = TTLMAP_STATUS(res);

	struct worker_info *worker_info = &cur->worker_info[worker_idx];

	int result_status;
	if (status == TTLMAP_FOUND) {
		result_status = SESSION_FOUND;
	} else if (status == TTLMAP_INSERTED || status == TTLMAP_REPLACED) {
		if (worker_info_use_prev_gen(worker_info)) {
			struct session_table_gen *prev =
				session_table_previous_gen(session_table);
			int lookup_res = TTLMAP_LOOKUP(
				&prev->map, session_id, *session_state, now
			);
			if (TTLMAP_STATUS(lookup_res) == TTLMAP_FOUND) {
				result_status = SESSION_FOUND;
			} else {
				result_status = SESSION_CREATED;
			}
		} else {
			result_status = SESSION_CREATED;
		}
	} else { // status == TTLMAP_FAILED
		return SESSION_TABLE_OVERFLOW;
	}

	// update max deadline
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

	return result_status;
}

static inline uint32_t
get_session_real(
	struct session_table *session_table,
	struct balancer_session_id *session_id,
	uint32_t now,
	uint32_t worker_idx
) {
	struct session_table_gen *cur =
		session_table_current_gen(session_table);

	struct balancer_session_state session_state;
	int res = TTLMAP_LOOKUP(&cur->map, session_id, &session_state, now);
	int status = TTLMAP_STATUS(res);

	if (status == TTLMAP_FOUND) {
		return session_state.real_id;
	} else {
		assert(status == TTLMAP_FAILED);
		struct worker_info *worker_info = &cur->worker_info[worker_idx];
		if (worker_info_use_prev_gen(worker_info)) {
			struct session_table_gen *prev =
				session_table_previous_gen(session_table);
			int res = TTLMAP_LOOKUP(
				&prev->map, session_id, &session_state, now
			);
			status = TTLMAP_STATUS(res);
			if (status == TTLMAP_FOUND) {
				return session_state.real_id;
			}
		}
		return (uint32_t)-1;
	}
}

static inline void
session_remove(struct balancer_session_state *session_state) {
	TTLMAP_REMOVE(struct balancer_session_id, session_state);
}

static inline void
session_unlock(session_lock_t *lock) {
	ttlmap_release_lock(lock);
}

// Try to free unused in session table.
// Returns:
// 	1 on free
//	0 on no free
// 	-1 on error
int
session_table_free_unused(struct session_table *table);

// Try to resize session table.
// Returns:
// 	1 on resize
//	0 on no resize
// 	-1 on error (memory not enough)
int
session_table_resize(struct session_table *table, size_t new_size);