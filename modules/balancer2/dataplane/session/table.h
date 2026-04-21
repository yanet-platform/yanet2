#pragma once

#include "common/ttlmap/detail/lock.h"
#include "common/ttlmap/ttlmap.h"

#include "types/session.h"

/*
 * Session table operations for the dataplane.
 *
 * All session access must happen within a critical section
 * (st_begin_cs / st_end_cs). The critical section pins the
 * current generation, ensuring the controlplane does not
 * free a map while workers are still reading it.
 */

#define SESSION_FOUND TTLMAP_FOUND
#define SESSION_CREATED 2
#define SESSION_TABLE_OVERFLOW TTLMAP_FAILED

/*
 * Enter a session table critical section.
 * Returns the current generation, which must be passed to
 * all subsequent st_* calls within this critical section.
 */
static inline uint64_t
st_begin_cs(struct balancer_session_table *st, uint32_t worker) {
	return RCU_READ_BEGIN(&st->rcu, worker, &st->current_gen);
}

/*
 * Leave a session table critical section.
 * After this call, the controlplane may free the map that
 * was pinned by st_begin_cs.
 */
static inline void
st_end_cs(struct balancer_session_table *st, uint32_t worker) {
	RCU_READ_END(&st->rcu, worker);
}

static inline void
st_prefetch_session(
	struct balancer_session_table *st,
	uint64_t current_table_gen,
	struct balancer_session_id *session_id
) {
	struct ttlmap *map = balancer_st_cur_map(st, current_table_gen);
	TTLMAP_PREFETCH(map, session_id, struct balancer_session_state, 1, 0);
}

/*
 * Whether the previous map should be consulted.
 * True during transition generations (odd), when sessions may
 * still reside in the old map pending migration.
 */
static inline int
st_prev_map_used(uint32_t table_gen) {
	return table_gen & 1;
}

/*
 * Look up or create a session entry.
 *
 * First tries the current map. If the session is found, returns
 * SESSION_FOUND with session_state pointing to the existing entry.
 *
 * If the session is not found, a new slot is allocated in the
 * current map. During transition generations (odd), the previous
 * map is also checked -- if the session exists there, its state
 * is copied into the new slot and SESSION_FOUND is returned.
 * This handles sessions that have not yet been migrated.
 *
 * If neither map has the session, returns SESSION_CREATED with
 * a blank slot allocated in the current map.
 *
 * Returns SESSION_TABLE_OVERFLOW if the current map is full and
 * no slot could be allocated. In this case no lock is held.
 *
 * On SESSION_FOUND or SESSION_CREATED, the returned session_state
 * is locked via *lock. The caller must call st_unlock_session
 * after modifying the state.
 */
static inline int
st_get_or_create_session(
	struct balancer_session_table *st,
	uint64_t current_table_gen,
	uint32_t now,
	uint32_t timeout,
	struct balancer_session_id *session_id,
	struct balancer_session_state **session_state,
	ttlmap_lock_t **lock
) {
	struct ttlmap *map = balancer_st_cur_map(st, current_table_gen);

	int res =
		TTLMAP_GET(map, session_id, session_state, lock, now, timeout);
	int status = TTLMAP_STATUS(res);

	int result_status;
	if (status == TTLMAP_FOUND) {
		result_status = SESSION_FOUND;
	} else if (status == TTLMAP_INSERTED || status == TTLMAP_REPLACED) {
		if (!st_prev_map_used(current_table_gen)) {
			result_status = SESSION_CREATED;
		} else {
			/*
			 * New slot allocated in current map. During transition
			 * (odd gen), check the previous map for a session that
			 * has not been migrated yet.
			 */
			struct ttlmap *prev_map =
				balancer_st_prev_map(st, current_table_gen);
			int lookup_res = TTLMAP_LOOKUP(
				prev_map, session_id, *session_state, now
			);
			if (TTLMAP_STATUS(lookup_res) == TTLMAP_FAILED) {
				result_status = SESSION_CREATED;
			} else {
				result_status = SESSION_FOUND;
			}
		}
	} else { // status == TTLMAP_FAILED
		result_status = SESSION_TABLE_OVERFLOW;
	}

	return result_status;
}

/* Mark a session slot as empty. Must be called while the lock is held. */
static inline void
st_remove_session(struct balancer_session_state *session_state) {
	TTLMAP_REMOVE(struct balancer_session_id, session_state);
}

/* Release the lock acquired by st_get_or_create_session. */
static inline void
st_unlock_session(ttlmap_lock_t *lock) {
	ttlmap_release_lock(lock);
}