#include "session_table.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "common/rcu.h"
#include "common/ttlmap/ttlmap.h"
#include "logging/log.h"
#include "modules/balancer/api/info.h"
#include "modules/balancer/api/state.h"
#include <assert.h>
#include <netinet/in.h>
#include <stdalign.h>
#include <stdatomic.h>
#include <stdio.h>
#include <string.h>
#include <time.h>

#include "common/exp_array.h"
#include "session.h"

////////////////////////////////////////////////////////////////////////////////

int
session_table_init(
	struct session_table *table, struct memory_context *mctx, size_t size
) {
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

	// Init generation count
	// (guarded with rcu)
	rcu_init(&table->rcu);
	table->current_gen = 0;

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
	int status = TTLMAP_STATUS(res);
	if (status == TTLMAP_INSERTED || status == TTLMAP_REPLACED) {
		memcpy(found, state, sizeof(struct balancer_session_state));
		ttlmap_release_lock(lock);
	} else if (status == TTLMAP_FOUND) {
		ttlmap_release_lock(lock);
	} else { // status == TTLMAP_FAILED
		// critical: misses some session, session table grows too fast
		char client_ip_buf[100];
		sprintf(client_ip_buf,
			"%x%x%x%x%x%x%x%x%x%x%x%x%x%x%x%x",
			id->client_ip[0],
			id->client_ip[1],
			id->client_ip[2],
			id->client_ip[3],
			id->client_ip[4],
			id->client_ip[5],
			id->client_ip[6],
			id->client_ip[7],
			id->client_ip[8],
			id->client_ip[9],
			id->client_ip[10],
			id->client_ip[11],
			id->client_ip[12],
			id->client_ip[13],
			id->client_ip[14],
			id->client_ip[15]);
		LOG(WARN,
		    "missed session on table resize [vs_id=%d, client=%s:%d]",
		    id->vs_id,
		    client_ip_buf,
		    id->client_port);
	}
	return 0;
}

static inline void
set_gen(struct session_table *table, uint32_t gen) {
	rcu_update(&table->rcu, &table->current_gen, gen);
}

static inline uint64_t
get_gen(struct session_table *table) {
	return rcu_load(&table->rcu, &table->current_gen);
}

int
session_table_resize(
	struct session_table *table, size_t new_size, uint32_t now
) {
	uint32_t current_gen = get_gen(table);

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
	set_gen(table, current_gen);

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
	set_gen(table, current_gen);

	// After that, workers will not use previous map

	// So we can free current_map
	TTLMAP_FREE(current_map);

	return 0;
}
