#pragma once

#include "common/detail/ttlmap/bucket.h"
#include "common/ttlmap.h"
#include "subprojects/dpdk/lib/eal/include/rte_common.h"
#include <assert.h>
#include <dirent.h>
#include <rte_build_config.h>
#include <rte_common.h>
#include <stdatomic.h>

#include "session.h"

////////////////////////////////////////////////////////////////////////////////

#define BALANCER_MAX_WORKERS_NUM 64

////////////////////////////////////////////////////////////////////////////////

#define BALANCER_SESSION_FOUND TTLMAP_FOUND
#define BALANCER_SESSION_CREATED (TTLMAP_INSERTED | TTLMAP_REPLACED)
#define BALANCER_GET_SESSION_FAILED TTLMAP_FAILED

////////////////////////////////////////////////////////////////////////////////

struct worker_info {
	_Atomic uint32_t use_prev_gen; // atomic
	uint8_t __padding[63];	       // NOLINT
	_Atomic uint32_t max_deadline_current_gen;
	_Atomic uint32_t max_deadline_prev_gen;
	_Atomic uint32_t active_sessions; // sessions created by worker
	_Atomic uint32_t density_factor;
} __rte_cache_aligned;

void
worker_info_init(struct worker_info *info);

#define WORKER_SET_ATOMIC(worker_info_ptr, field, value)                       \
	__c11_atomic_store(&(worker_info_ptr)->field, value, __ATOMIC_SEQ_CST)

#define WORKER_GET_ATOMIC(worker_info_ptr, field)                              \
	__c11_atomic_load(&(worker_info_ptr)->field, __ATOMIC_SEQ_CST)

#define WORKER_INC_ATOMIC(worker_info_ptr, field)                              \
	__c11_atomic_fetch_add(&(worker_info_ptr)->field, 1, __ATOMIC_SEQ_CST)

////////////////////////////////////////////////////////////////////////////////

struct balancer_sessions_storage_gen {
	struct ttlmap session_table;
	struct worker_info worker_info[BALANCER_MAX_WORKERS_NUM];
};

////////////////////////////////////////////////////////////////////////////////

struct balancer_state {
	struct balancer_sessions_storage_gen generations[2];
	_Atomic uint32_t current_gen; // workers read, cp modify
	uint32_t workers_cnt;
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

////////////////////////////////////////////////////////////////////////////////

static inline struct balancer_sessions_storage_gen *
balancer_get_cur_storage_gen(struct balancer_state *state) {
	uint32_t current_gen =
		__c11_atomic_load(&state->current_gen, __ATOMIC_SEQ_CST);
	return &state->generations[current_gen & 1];
}

static inline struct balancer_sessions_storage_gen *
balancer_get_prev_storage_gen(struct balancer_state *state) {
	uint32_t current_gen =
		__c11_atomic_load(&state->current_gen, __ATOMIC_SEQ_CST);
	return &state->generations[(current_gen & 1) ^ 1];
}

////////////////////////////////////////////////////////////////////////////////

static inline int
balancer_get_session(
	struct balancer_state *state,
	uint32_t worker_idx,
	uint32_t now,
	uint32_t timeout,
	struct balancer_session_id *session_id,
	struct balancer_session_state **session_state,
	balancer_session_lock_t **lock
) {
	struct balancer_sessions_storage_gen *sessions_cur =
		balancer_get_cur_storage_gen(state);

	int res = TTLMAP_GET(
		&sessions_cur->session_table,
		session_id,
		session_state,
		lock,
		now,
		timeout
	);
	int status = TTLMAP_STATUS(res);
	uint32_t meta = TTLMAP_META(res);

	struct worker_info *worker_info =
		&sessions_cur->worker_info[worker_idx];
	uint32_t new_density_factor =
		RTE_MAX(meta, worker_info->density_factor);
	WORKER_SET_ATOMIC(worker_info, density_factor, new_density_factor);

	if (status == TTLMAP_FOUND) {
		uint32_t new_max_deadline =
			RTE_MAX(worker_info->max_deadline_current_gen,
				now + timeout);
		WORKER_SET_ATOMIC(
			worker_info, max_deadline_current_gen, new_max_deadline
		);
		return BALANCER_SESSION_FOUND;
	} else if (status == TTLMAP_INSERTED || status == TTLMAP_REPLACED) {
		if (status == TTLMAP_INSERTED) {
			WORKER_INC_ATOMIC(worker_info, active_sessions);
		}
		if (WORKER_GET_ATOMIC(worker_info, use_prev_gen) == 1) {
			if (WORKER_GET_ATOMIC(
				    worker_info, max_deadline_prev_gen
			    ) < now) {
				WORKER_SET_ATOMIC(worker_info, use_prev_gen, 0);
				return BALANCER_SESSION_CREATED;
			}
			struct balancer_sessions_storage_gen *sessions_prev =
				balancer_get_prev_storage_gen(state);
			status = TTLMAP_LOOKUP(
				&sessions_prev->session_table,
				session_id,
				*session_state,
				now
			);
			if (status == TTLMAP_FOUND) {
				return BALANCER_SESSION_FOUND;
			} else {
				return BALANCER_SESSION_CREATED;
			}
		} else {
			return BALANCER_SESSION_CREATED;
		}
	} else { // status == TTLMAP_FAILED
		return BALANCER_GET_SESSION_FAILED;
	}
}

static inline void
balancer_session_invalidate(struct balancer_session_state *state) {
	TTLMAP_REMOVE(struct balancer_session_id, state);
}

static inline void
balancer_session_unlock(balancer_session_lock_t *lock) {
	ttlmap_release_lock(lock);
}

////////////////////////////////////////////////////////////////////////////////

static inline size_t
balancer_session_table_capacity(struct balancer_sessions_storage_gen *storage) {
	return ttlmap_capacity(&storage->session_table);
}

////////////////////////////////////////////////////////////////////////////////

int
balancer_extend_state_on_demand(struct balancer_state *state);

int
balancer_try_free_unused(struct balancer_state *state);

////////////////////////////////////////////////////////////////////////////////