#pragma once

#include "state/session.h"
#include "state/session_table.h"

#include "common/ttlmap/ttlmap.h"

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
	struct session_id *session_id,
	struct session_state **session_state,
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
	struct session_id *session_id,
	uint32_t now
) {
	// Get ttlmap
	struct ttlmap *map =
		session_table_map(session_table, current_table_gen);

	struct session_state session_state;
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
session_remove(struct session_state *session_state) {
	TTLMAP_REMOVE(struct session_id, session_state);
}

static inline void
session_unlock(session_lock_t *lock) {
	ttlmap_release_lock(lock);
}