#include "session_table.h"

#include "../dataplane/session.h"
#include "../dataplane/session_table.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/dataplane/config/zone.h"
#include "lib/logging/log.h"

#include "common/ttlmap.h"
#include <stdatomic.h>
#include <string.h>

////////////////////////////////////////////////////////////////////////////////

static struct balancer_session_table *
session_table_alloc(struct memory_context *mctx) {
	const size_t align = alignof(struct balancer_session_table);
	uint8_t *memory = memory_balloc(
		mctx, sizeof(struct balancer_session_table) + align
	);
	if (memory == NULL) {
		return NULL;
	}
	uint32_t shift = (align - ((uintptr_t)memory) % align) % align;
	memory += shift;
	assert((uintptr_t)memory % align == 0);
	struct balancer_session_table *session_table =
		(struct balancer_session_table *)memory;
	session_table->memory_shift = shift;
	return session_table;
}

void
session_table_dealloc(struct balancer_session_table *);

// Allows to create and initialize session table
struct balancer_session_table *
balancer_session_table_create(struct agent *agent, size_t size) {
	struct balancer_session_table *session_table =
		session_table_alloc(&agent->memory_context);
	if (session_table == NULL) {
		return NULL;
	}

	SET_OFFSET_OF(&session_table->mctx, &agent->memory_context);
	session_table->current_gen = 0;
	session_table->workers_cnt = ADDR_OF(&agent->dp_config)->worker_count;

	int res = TTLMAP_INIT(
		&session_table->generations[0].map,
		&agent->memory_context,
		struct session_id,
		struct session_state,
		size
	);
	if (res != 0) {
		session_table_dealloc(session_table);
		return NULL;
	}

	ttlmap_init_empty(&session_table->generations[1].map);

	for (size_t i = 0; i < session_table->workers_cnt; ++i) {
		struct worker_info *info =
			&session_table->generations[0].worker_info[i];
		memset(info, 0, sizeof(*info));
	}

	return session_table;
}

////////////////////////////////////////////////////////////////////////////////

void
session_table_dealloc(struct balancer_session_table *session_table) {
	const size_t align = alignof(struct balancer_session_table);
	uintptr_t memory =
		(uintptr_t)session_table - session_table->memory_shift;
	memory_bfree(
		ADDR_OF(&session_table->mctx),
		(void *)memory,
		sizeof(struct balancer_session_table) + align
	);
}

// Allows to free session table memory
void
balancer_session_table_free(struct balancer_session_table *session_table) {
	struct session_table_gen *cur =
		session_table_current_gen(session_table);
	if (ttlmap_capacity(&cur->map) > 0) {
		TTLMAP_FREE(&cur->map);
	}

	struct session_table_gen *prev =
		session_table_previous_gen(session_table);
	if (ttlmap_capacity(&cur->map) > 0) {
		TTLMAP_FREE(&prev->map);
	}

	session_table_dealloc(session_table);
}

////////////////////////////////////////////////////////////////////////////////

// Allows to extend session table if it is filled enough
int
balancer_session_table_extend(
	struct balancer_session_table *session_table, bool force
) {
	struct session_table_gen *sessions_cur =
		session_table_current_gen(session_table);
	size_t active_sessions = 0;
	uint32_t density_factor = 0;
	for (size_t i = 0; i < session_table->workers_cnt; ++i) {
		struct worker_info *worker_info = &sessions_cur->worker_info[i];
		if (atomic_load_explicit(
			    &worker_info->use_prev_gen, __ATOMIC_SEQ_CST
		    ) == 1) {
			return 0;
		}
		active_sessions += atomic_load_explicit(
			&worker_info->active_sessions, __ATOMIC_SEQ_CST
		);
		density_factor =
			RTE_MAX(density_factor,
				atomic_load_explicit(
					&worker_info->density_factor,
					__ATOMIC_SEQ_CST
				));
	}

	size_t current_table_cap = ttlmap_capacity(&sessions_cur->map);

	LOG(TRACE,
	    "density_factor=%u, active_sessions=%zu, "
	    "session_table_capacity=%zu (filled by "
	    "%.2lf%%)",
	    density_factor,
	    active_sessions,
	    current_table_cap,
	    100.0 * active_sessions / current_table_cap);

	if (density_factor >= 7 || force) {
		LOG(INFO, "extending sessions table...");
		balancer_session_table_free_unused(session_table);
		struct session_table_gen *sessions_next =
			session_table_previous_gen(session_table);
		size_t next_gen_cap = current_table_cap * 2;
		int ret = TTLMAP_INIT(
			&sessions_next->map,
			ADDR_OF(&session_table->mctx),
			struct session_id,
			struct session_state,
			next_gen_cap
		);
		if (ret != 0) {
			LOG(INFO, "failed to initialize new sessions table");
			// failed to extend session table
			// probably, memory not enough
			return -1;
		}
		for (size_t i = 0; i < session_table->workers_cnt; ++i) {
			struct worker_info *worker_info =
				&sessions_next->worker_info[i];
			struct worker_info *prev_worker_info =
				&sessions_cur->worker_info[i];
			memset(worker_info, 0, sizeof(*worker_info));

			worker_info->max_deadline_prev_gen =
				prev_worker_info->max_deadline_current_gen;
			worker_info->use_prev_gen = 1;
		}
		atomic_fetch_add_explicit(
			&session_table->current_gen, 1, __ATOMIC_SEQ_CST
		);
		// successfully extended sessions table
		LOG(INFO, "successfully extended sessions table");
		return 1;
	} else {
		// no need to extend sessions table
		return 0;
	}
}

////////////////////////////////////////////////////////////////////////////////

// Try free unused memory occupied by session table
int
balancer_session_table_free_unused(struct balancer_session_table *session_table
) {
	struct session_table_gen *sessions_cur =
		session_table_current_gen(session_table);
	for (size_t i = 0; i < session_table->workers_cnt; ++i) {
		if (atomic_load_explicit(
			    &sessions_cur->worker_info[i].use_prev_gen,
			    __ATOMIC_SEQ_CST
		    ) == 1) {
			LOG(DEBUG,
			    "failed to free previous table gen as worker %zu "
			    "uses it",
			    i);
			return 0;
		}
	}
	struct session_table_gen *sessions_prev =
		session_table_previous_gen(session_table);
	if (ttlmap_capacity(&sessions_prev->map) > 0) {
		LOG(DEBUG, "trying to free previous table gen...");
		TTLMAP_FREE(&sessions_prev->map);
		// successfully free memory
		LOG(DEBUG, "successfully free previous table gen");
		return 1;
	}
	LOG(DEBUG, "previous table gen is not initialized, nothing to do");
	return 0;
}