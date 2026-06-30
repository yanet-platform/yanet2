#pragma once

#include <stddef.h>

#include "common/memory_address.h"
#include "common/rcu.h"
#include "common/ttlmap/ttlmap.h"

#include "types/session.h"

struct balancer_session_table {
	struct ttlmap map;
};

/* Mark a session slot as empty. Must be called while the lock is held. */
static inline void
st_chain_remove_session(struct balancer_session_state *session_state) {
	TTLMAP_REMOVE(struct balancer_session_id, session_state);
}

/* Release the session lock. */
static inline void
st_chain_unlock_session(ttlmap_lock_t *lock) {
	ttlmap_release_lock(lock);
}

/*
 * All access to session table chain must happen within a critical
 * section (st_begin_cs / st_end_cs). The critical section pins the
 * current generation, ensuring the controlplane does not
 * free a map while workers are still reading it.
 */
struct balancer_session_table_chain {
	_Atomic uint64_t gen;
	struct balancer_session_table *tables[2];
	rcu_t rcu;
};

/*
 * Enter a session table chain critical section.
 * Returns the current generation, which must be passed to
 * all subsequent st_* calls within this critical section.
 */
static inline uint64_t
st_chain_begin_cs(
	struct balancer_session_table_chain *st_chain, uint32_t worker
) {
	return RCU_READ_BEGIN(&st_chain->rcu, worker, &st_chain->gen);
}

/*
 * Leave a session table chain critical section.
 * After this call, the controlplane may update the chain.
 */
static inline void
st_chain_end_cs(
	struct balancer_session_table_chain *st_chain, uint32_t worker
) {
	RCU_READ_END(&st_chain->rcu, worker);
}

/*
 * Even generations are steady state; during odd generations (transitions),
 * both maps must be consulted as sessions may not yet have migrated to the
 * front table. The front-table index therefore advances only every second
 * generation.
 */
static inline uint32_t
st_chain_current_session_table_index(uint64_t gen) {
	return ((gen + 1) & 0b11) >> 1;
}

static inline void
st_chain_read_front_back(
	struct balancer_session_table_chain *st_chain,
	uint64_t gen,
	struct balancer_session_table **front,
	struct balancer_session_table **back
) {
	uint32_t cur = st_chain_current_session_table_index(gen);
	*front = ADDR_OF(&st_chain->tables[cur]);
	*back = ADDR_OF(&st_chain->tables[cur ^ 1]);
}

/*
 * Whether the previous map should be consulted.
 * True during transition generations (odd), when sessions may
 * still reside in the session table pending migration.
 */
static inline int
st_chain_prev_table_used(uint64_t table_gen) {
	return table_gen & 1;
}

#define SESSION_TABLE_OVERFLOW 0
#define SESSION_FOUND 1
#define SESSION_CREATED 2

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
 * On SESSION_FOUND or SESSION_CREATED, the returned session_state is
 * locked via *lock. The caller must release the lock after modifying
 * the state.
 */
static inline int
st_chain_get_or_create_session(
	struct balancer_session_table_chain *st_chain,
	uint64_t gen,
	uint32_t now,
	uint32_t timeout,
	struct balancer_session_id *session_id,
	struct balancer_session_state **session_state,
	ttlmap_lock_t **lock
) {
	struct balancer_session_table *front;
	struct balancer_session_table *back;
	st_chain_read_front_back(st_chain, gen, &front, &back);

	int res = TTLMAP_GET(
		&front->map, session_id, session_state, lock, now, timeout
	);
	int status = TTLMAP_STATUS(res);

	int result_status;
	if (status == TTLMAP_FOUND) {
		result_status = SESSION_FOUND;
	} else if (status == TTLMAP_INSERTED || status == TTLMAP_REPLACED) {
		if (!st_chain_prev_table_used(gen)) {
			result_status = SESSION_CREATED;
		} else {
			/*
			 * New slot allocated in front map. During transition
			 * (odd gen), check the back map for a session that
			 * has not been migrated yet.
			 */
			int lookup_res = TTLMAP_LOOKUP(
				&back->map, session_id, *session_state, now
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

static inline void
st_chain_prefetch_session(
	struct balancer_session_table_chain *st_chain,
	uint64_t gen,
	struct balancer_session_id *session_id
) {
	struct balancer_session_table *front;
	struct balancer_session_table *back;

	st_chain_read_front_back(st_chain, gen, &front, &back);

	TTLMAP_PREFETCH(
		&front->map, session_id, struct balancer_session_state, 1, 0
	);
}