#pragma once

#include "common/memory.h"
#include "common/rcu.h"
#include "common/ttlmap/detail/ttlmap.h"
#include "common/ttlmap/ttlmap.h"

#include "session.h"
#include <assert.h>
#include <stdatomic.h>
#include <stdio.h>

#include "../api/info.h"

////////////////////////////////////////////////////////////////////////////////

#define SESSION_FOUND TTLMAP_FOUND
#define SESSION_CREATED (TTLMAP_INSERTED | TTLMAP_REPLACED)
#define SESSION_TABLE_OVERFLOW TTLMAP_FAILED

////////////////////////////////////////////////////////////////////////////////

struct session_table {
	struct ttlmap maps[2];

	rcu_t rcu;
	atomic_ulong current_gen; // workers read, cp modify (guarded by rcu)

	// relative pointer to the memory context of the
	// agent who created session table
	struct memory_context *mctx;
};

static inline int
session_table_map_idx(uint32_t gen) {
	return ((gen + 1) & 0b11) >> 1;
}

static inline struct ttlmap *
session_table_map(struct session_table *table, uint32_t gen) {
	return &table->maps[session_table_map_idx(gen)];
}

static inline struct ttlmap *
session_table_prev_map(struct session_table *table, uint32_t gen) {
	return &table->maps[session_table_map_idx(gen) ^ 1];
}

static inline uint32_t
session_table_current_gen(struct session_table *table) {
	return atomic_load_explicit(&table->current_gen, memory_order_acquire);
}

////////////////////////////////////////////////////////////////////////////////

int
session_table_init(
	struct session_table *table, struct memory_context *mctx, size_t size
);

void
session_table_free(struct session_table *table);

size_t
session_table_capacity(struct session_table *table);

////////////////////////////////////////////////////////////////////////////////

// Fill info about sessions in sessions table
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

static inline uint64_t
session_table_begin_cs(struct session_table *session_table, uint32_t worker) {
	return RCU_READ_BEGIN(
		&session_table->rcu, worker, &session_table->current_gen
	);
}

static inline void
session_table_end_cs(struct session_table *table, uint32_t worker) {
	RCU_READ_END(&table->rcu, worker);
}

static inline int
worker_use_prev_map(uint32_t table_gen) {
	return table_gen & 1;
}

////////////////////////////////////////////////////////////////////////////////

static inline int
get_or_create_session(
	struct session_table *session_table,
	uint64_t current_table_gen,
	uint32_t now,
	uint32_t timeout,
	struct balancer_session_id *session_id,
	struct balancer_session_state **session_state,
	session_lock_t **lock
) {
	// Get ttlmap
	struct ttlmap *map =
		session_table_map(session_table, current_table_gen);

	int res =
		TTLMAP_GET(map, session_id, session_state, lock, now, timeout);
	int status = TTLMAP_STATUS(res);

	int result_status;
	if (status == TTLMAP_FOUND) {
		result_status = SESSION_FOUND;
	} else if (status == TTLMAP_INSERTED || status == TTLMAP_REPLACED) {
		if (worker_use_prev_map(current_table_gen
		    )) { // if worker in this gen should use prev map
			struct ttlmap *prev_map = session_table_prev_map(
				session_table, current_table_gen
			);
			int lookup_res = TTLMAP_LOOKUP(
				prev_map, session_id, *session_state, now
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
		result_status = SESSION_TABLE_OVERFLOW;
	}

	return result_status;
}

static inline uint32_t
get_session_real(
	struct session_table *session_table,
	uint32_t current_table_gen,
	struct balancer_session_id *session_id,
	uint32_t now
) {
	// Get ttlmap
	struct ttlmap *map =
		session_table_map(session_table, current_table_gen);

	struct balancer_session_state session_state;
	int res = TTLMAP_LOOKUP(map, session_id, &session_state, now);
	int status = TTLMAP_STATUS(res);

	uint32_t real_id = -1;
	if (status == TTLMAP_FOUND) {
		real_id = session_state.real_id;
	} else {
		assert(status == TTLMAP_FAILED);
		if (worker_use_prev_map(current_table_gen
		    )) { // if worker in this gen should use prev map
			struct ttlmap *prev = session_table_prev_map(
				session_table, current_table_gen
			);
			int res = TTLMAP_LOOKUP(
				prev, session_id, &session_state, now
			);
			status = TTLMAP_STATUS(res);
			if (status == TTLMAP_FOUND) {
				real_id = session_state.real_id;
			}
		};
	}

	return real_id;
}

static inline void
session_remove(struct balancer_session_state *session_state) {
	TTLMAP_REMOVE(struct balancer_session_id, session_state);
}

static inline void
session_unlock(session_lock_t *lock) {
	ttlmap_release_lock(lock);
}

////////////////////////////////////////////////////////////////////////////////

// Try to resize session table.
// Returns:
// 	0 on resize
// 	-1 on error (memory not enough)
int
session_table_resize(
	struct session_table *table, size_t new_size, uint32_t now
);