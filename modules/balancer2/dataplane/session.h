#pragma once

#include <stddef.h>

#include "common/rcu.h"
#include "common/ttlmap/detail/ttlmap.h"

struct balancer_session_table {
	struct ttlmap map;
};

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