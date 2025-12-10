#include "session_table.h"
#include "common/memory.h"
#include "common/ttlmap/ttlmap.h"
#include "modules/balancer/api/state.h"
#include <assert.h>
#include <stdalign.h>
#include <string.h>

#include "common/exp_array.h"

////////////////////////////////////////////////////////////////////////////////

static inline int
session_table_worker_use_prev_gen(
	struct session_table_gen *cur, size_t worker
) {
	struct worker_info *info = &cur->worker_info[worker];
	return worker_info_use_prev_gen(info);
}

static inline int
session_table_workers_use_prev_gen(struct session_table *table) {
	struct session_table_gen *sessions_cur =
		session_table_current_gen(table);
	for (size_t i = 0; i < table->workers; ++i) {
		if (session_table_worker_use_prev_gen(sessions_cur, i)) {
			return 1;
		}
	}
	return 0;
}

////////////////////////////////////////////////////////////////////////////////

int
session_table_init(
	struct session_table *table,
	struct memory_context *mctx,
	size_t size,
	size_t workers
) {
	const size_t align = alignof(struct session_table);
	uint8_t *memory =
		memory_balloc(mctx, sizeof(struct session_table) + align);
	if (memory == NULL) {
		return -1;
	}
	table->current_gen = 0;
	table->workers = workers;
	SET_OFFSET_OF(&table->mctx, mctx);

	int res = TTLMAP_INIT(
		&table->generations[0].map,
		mctx,
		struct balancer_session_id,
		struct balancer_session_state,
		size
	);
	if (res != 0) {
		return -1;
	}

	ttlmap_init_empty(&table->generations[1].map);

	for (size_t i = 0; i < table->workers; ++i) {
		struct worker_info *info =
			&table->generations[0].worker_info[i];
		memset(info, 0, sizeof(*info));
	}

	return 0;
}

void
session_table_free(struct session_table *table) {
	struct session_table_gen *cur = session_table_current_gen(table);
	if (ttlmap_capacity(&cur->map) > 0) {
		TTLMAP_FREE(&cur->map);
	}

	struct session_table_gen *prev = session_table_previous_gen(table);
	if (ttlmap_capacity(&cur->map) > 0) {
		TTLMAP_FREE(&prev->map);
	}
}

////////////////////////////////////////////////////////////////////////////////

// returns 0 if we previous gen still in use.
// 1 if it we freed some memory.
// 2 if it it already was free.
static int
try_free_prev_gen(struct session_table *table) {
	if (session_table_workers_use_prev_gen(table)) {
		// some workers use prev gen, can not free
		return 0;
	}
	struct session_table_gen *sessions_prev =
		session_table_previous_gen(table);
	if (ttlmap_capacity(&sessions_prev->map) > 0) {
		TTLMAP_FREE(&sessions_prev->map);
		return 1;
	} else {
		// already was free, we did nothing
		return 2;
	}
}

int
session_table_free_unused(struct session_table *table) {
	int result = try_free_prev_gen(table);
	if (result == 2) {
		// already was free,
		// we did nothing, so return zero
		result = 0;
	}
	return result;
}

////////////////////////////////////////////////////////////////////////////////

size_t
session_table_capacity(struct session_table *table) {
	struct session_table_gen *cur = session_table_current_gen(table);
	return ttlmap_capacity(&cur->map);
}

////////////////////////////////////////////////////////////////////////////////

struct iter_context {
	struct memory_context *mctx;
	struct balancer_sessions_info *info;
	bool only_count;
	bool failed;
	uint32_t now;
};

static int
iter_callback(
	struct balancer_session_id *id,
	struct balancer_session_state *state,
	struct iter_context *ctx
) {
	// skip outdated sessions
	if (state->last_packet_timestamp + state->timeout <= ctx->now) {
		return 0;
	}

	struct balancer_session_info current_session_info = {
		.vs_id = id->vs_id,
		.real_id = state->real_id,
		.create_timestamp = state->create_timestamp,
		.last_packet_timestamp = state->last_packet_timestamp,
		.client_port = id->client_port,
		.timeout = state->timeout,
	};
	memcpy(current_session_info.client_ip, id->client_ip, 16);

	// extend ctx->info->sessions array
	void *memory = ctx->info->sessions;
	uint64_t *count = &ctx->info->count;
	int res = mem_array_expand_exp(
		ctx->mctx, &memory, sizeof(struct balancer_session_info), count
	);
	if (res != 0) {
		// break iteration
		ctx->failed = true;
		return 1;
	}
	ctx->info->sessions = memory;
	ctx->info->sessions[*count - 1] = current_session_info;
	return 0;
}

static int
sessions_table_gen_sessions_info(
	struct session_table_gen *gen,
	struct balancer_sessions_info *info,
	struct memory_context *mctx,
	uint32_t now,
	bool only_count
) {
	struct iter_context ctx = {
		.info = info,
		.only_count = only_count,
		.failed = false,
		.mctx = mctx,
		.now = now,
	};
	TTLMAP_ITER(
		&gen->map,
		struct balancer_session_id,
		struct balancer_session_state,
		now,
		iter_callback,
		&ctx
	);
	return ctx.failed ? -1 : 0;
}

int
session_table_fill_sessions_info(
	struct session_table *table,
	struct balancer_sessions_info *info,
	struct memory_context *mctx,
	uint32_t now,
	bool only_count
) {
	memset(info, 0, sizeof(*info));

	// iterate over current gen
	struct session_table_gen *cur = session_table_current_gen(table);
	int res = sessions_table_gen_sessions_info(
		cur, info, mctx, now, only_count
	);
	if (res != 0) {
		return -1;
	}

	// iterate over previous gen
	struct session_table_gen *prev = session_table_previous_gen(table);
	return sessions_table_gen_sessions_info(
		prev, info, mctx, now, only_count
	);
}

void
session_table_free_sessions_info(
	struct balancer_sessions_info *info, struct memory_context *mctx
) {
	if (info->sessions != NULL) {
		mem_array_free_exp(
			mctx,
			info->sessions,
			sizeof(struct balancer_session_info),
			info->count
		);
	}
}

////////////////////////////////////////////////////////////////////////////////

int
session_table_resize(struct session_table *table, size_t new_size) {
	int can_free = try_free_prev_gen(table);
	if (can_free == 0) {
		// Failed to resize, because prev gen is still
		// used by some workers.
		return 0;
	}

	struct session_table_gen *sessions_cur =
		session_table_current_gen(table);
	struct session_table_gen *sessions_next =
		session_table_previous_gen(table);
	int ret = TTLMAP_INIT(
		&sessions_next->map,
		ADDR_OF(&table->mctx),
		struct balancer_session_id,
		struct balancer_session_state,
		new_size
	);
	if (ret != 0) {
		// failed to extend session table
		// memory not enough (probably)
		return -1;
	}
	for (size_t i = 0; i < table->workers; ++i) {
		struct worker_info *next_worker_info =
			&sessions_next->worker_info[i];
		struct worker_info *cur_worker_info =
			&sessions_cur->worker_info[i];
		memset(next_worker_info, 0, sizeof(*next_worker_info));

		next_worker_info->max_deadline_prev_gen =
			cur_worker_info->max_deadline_current_gen;

		next_worker_info->max_deadline_current_gen = 0;

		next_worker_info->last_timestamp =
			cur_worker_info->last_timestamp;
	}

	atomic_fetch_add_explicit(&table->current_gen, 1, __ATOMIC_SEQ_CST);

	return 1;
}
