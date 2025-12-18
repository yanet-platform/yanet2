#include "session_table.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "common/ttlmap/detail/bucket.h"
#include "common/ttlmap/ttlmap.h"
#include "modules/balancer/api/info.h"
#include "modules/balancer/api/state.h"
#include <assert.h>
#include <netinet/in.h>
#include <stdalign.h>
#include <stdio.h>
#include <string.h>
#include <time.h>

#include "common/exp_array.h"
#include "session.h"

////////////////////////////////////////////////////////////////////////////////

static int
worker_in_use(uint32_t mark) {
	return mark & 1;
}

static int
worker_gen(uint32_t mark) {
	return mark >> 1;
}

static void
wait_for_workers(struct session_table *table) {
	uint32_t current_gen = atomic_load(&table->current_gen);
	while (1) {
		bool need_wait = false;
		for (size_t w = 0; w < table->workers; ++w) {
			struct worker_info *info = &table->worker_info[w];
			uint32_t mark = worker_mark(info);
			uint32_t gen = worker_gen(mark);
			int in_use = worker_in_use(mark);
			assert(gen <= current_gen);
			if (gen < current_gen && in_use) {
				need_wait = true;
				break;
			}
		}
		if (!need_wait) {
			break;
		} else {
			// sleep for 1 ms
			struct timespec ms = {0, 1000000}; // 1 ms
			nanosleep(&ms, NULL);
		}
	}
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
		&table->maps[0],
		mctx,
		struct balancer_session_id,
		struct balancer_session_state,
		size
	);
	if (res != 0) {
		return -1;
	}

	ttlmap_init_empty(&table->maps[1]);

	for (size_t i = 0; i < table->workers; ++i) {
		struct worker_info *info = &table->worker_info[i];
		memset(info, 0, sizeof(*info));
	}

	return 0;
}

void
session_table_free(struct session_table *table) {
	for (size_t i = 0; i < 2; ++i) {
		TTLMAP_FREE(&table->maps[i]);
	}
}

////////////////////////////////////////////////////////////////////////////////

size_t
session_table_capacity(struct session_table *table) {
	struct ttlmap *ttlmap =
		session_table_map(table, session_table_current_gen(table));
	return ttlmap_capacity(ttlmap);
}

////////////////////////////////////////////////////////////////////////////////

struct fill_sessions_context {
	struct memory_context *mctx;
	struct balancer_sessions_info *info;
	bool only_count;
	bool failed;
	uint32_t now;
};

static int
fill_sessions_callback(
	struct balancer_session_id *id,
	struct balancer_session_state *state,
	struct fill_sessions_context *ctx
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
		.client_port = ntohs(id->client_port),
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

int
session_table_fill_sessions_info(
	struct session_table *table,
	struct balancer_sessions_info *info,
	struct memory_context *mctx,
	uint32_t now,
	bool only_count
) {
	memset(info, 0, sizeof(*info));
	struct fill_sessions_context ctx = {
		.info = info,
		.only_count = only_count,
		.failed = false,
		.mctx = mctx,
		.now = now,
	};

	struct ttlmap *map =
		session_table_map(table, session_table_current_gen(table));
	TTLMAP_ITER(
		map,
		struct balancer_session_id,
		struct balancer_session_state,
		now,
		fill_sessions_callback,
		&ctx
	);

	return ctx.failed ? -1 : 0;
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

struct move_sessions_context {
	struct ttlmap *next_map;
	uint32_t now;
};

static int
move_sessions_callback(
	struct balancer_session_id *id,
	struct balancer_session_state *state,
	struct move_sessions_context *ctx
) {
	if (state->last_packet_timestamp + state->timeout <= ctx->now) {
		return 0;
	}

	session_lock_t *lock;
	struct balancer_session_state *found;
	int res = TTLMAP_GET(
		ctx->next_map,
		id,
		&found,
		&lock,
		state->last_packet_timestamp,
		state->timeout
	);
	if (TTLMAP_STATUS(res) != TTLMAP_FAILED) {
		// Copy the entire state to preserve all fields including
		// timestamps
		memcpy(found, state, sizeof(struct balancer_session_state));
		ttlmap_release_lock(lock);
	}
	return 0;
}

int
session_table_resize(
	struct session_table *table, size_t new_size, uint32_t now
) {
	uint32_t current_gen = session_table_current_gen(table);

	struct ttlmap *next_map = session_table_prev_map(table, current_gen);
	struct memory_context *mctx = ADDR_OF(&table->mctx);

	int init_result = TTLMAP_INIT(
		next_map,
		mctx,
		struct balancer_session_id,
		struct balancer_session_state,
		new_size
	);
	if (init_result != 0) {
		// no memory
		return -1;
	}

	// Update current gen, so all workers use primary `next_map`
	// and fallbacks to the `current_map`
	struct ttlmap *current_map = session_table_map(table, current_gen);
	++current_gen;
	atomic_store(&table->current_gen, current_gen);

	// Wait while all workers consume update
	wait_for_workers(table);

	// Now, workers can not update `current_map`.
	// They insert only into the `next_map`.

	// After that, we should move all sessions from the current_map to the
	// next_map

	struct move_sessions_context ctx = {
		.next_map = next_map,
		.now = now,
	};
	TTLMAP_ITER(
		current_map,
		struct balancer_session_id,
		struct balancer_session_state,
		now,
		move_sessions_callback,
		&ctx
	);

	// Sessions are moved, so workers dont need to use previous map
	++current_gen;
	atomic_store(&table->current_gen, current_gen);

	// Wait until all workers consume update
	wait_for_workers(table);

	// After that, workers will not use previous map

	// So we can free current_map
	TTLMAP_FREE(current_map);

	return 0;
}
