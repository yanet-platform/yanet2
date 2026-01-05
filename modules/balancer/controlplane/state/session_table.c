#include "session_table.h"
#include "common/memory.h"
#include "common/rcu.h"
#include "common/ttlmap/ttlmap.h"

#include "lib/controlplane/diag/diag.h"
#include "lib/logging/log.h"

#include <arpa/inet.h>
#include <assert.h>
#include <netinet/in.h>
#include <stdalign.h>
#include <stdatomic.h>
#include <stdio.h>
#include <string.h>
#include <time.h>

#include "common/ttlmap/ttlmap.h"

#include "common/ttlmap/ttlmap.h"

#include "api/session.h"

#include "session.h"

#include "state.h"

////////////////////////////////////////////////////////////////////////////////

int
session_table_init(
	struct session_table *table, struct memory_context *mctx, size_t size
) {
	memory_context_init_from(&table->mctx, mctx, "session_table");

	int res = TTLMAP_INIT(
		&table->maps[0],
		&table->mctx,
		struct session_id,
		struct session_state,
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
	struct balancer_state *state;
	struct named_session_info *info;
	size_t count;
	size_t size;
	bool only_count;
	uint32_t now;
};

static int
fill_sessions_callback(
	struct session_id *id,
	struct session_state *state,
	struct fill_sessions_context *ctx
) {
	// skip outdated sessions
	if (state->last_packet_timestamp + state->timeout <= ctx->now) {
		return 0;
	}

	if (ctx->only_count) {
		++ctx->count;
		return 0;
	}

	struct balancer_state *balancer_state = ctx->state;

	struct real_state *real = (struct real_state *)service_registry_lookup(
		&balancer_state->real_registry, state->real_id
	);

	struct session_identifier identifier = {
		.client_ip = id->client_ip,
		.client_port = ntohs(id->client_port),
		.real = real->identifier
	};

	struct session_info info = {
		.create_timestamp = state->create_timestamp,
		.last_packet_timestamp = state->last_packet_timestamp,
		.timeout = state->timeout
	};

	struct named_session_info session = {
		.identifier = identifier, .info = info
	};

	if (ctx->count == ctx->size) {
		size_t next_size = ctx->size == 0 ? 4096 : ctx->size * 2;
		struct named_session_info *infos =
			realloc(ctx->info,
				next_size * sizeof(struct named_session_info));
		ctx->size = next_size;
		ctx->info = infos;
	}

	ctx->info[ctx->count++] = session;

	return 0;
}

size_t
session_table_fill_sessions_info(
	struct session_table *table,
	struct named_session_info **info,
	uint32_t now
) {
	memset(info, 0, sizeof(*info));
	struct named_session_info *infos = NULL;
	struct fill_sessions_context ctx = {
		.info = infos,
		.count = 0,
		.size = 0,
		.now = now,
	};

	struct ttlmap *map =
		session_table_map(table, session_table_current_gen(table));
	TTLMAP_ITER(
		map,
		struct session_id,
		struct session_state,
		now,
		fill_sessions_callback,
		&ctx
	);

	*info = ctx.info;
	return ctx.count;
}

////////////////////////////////////////////////////////////////////////////////

struct move_sessions_context {
	struct ttlmap *next_map;
	uint32_t now;
};

static int
move_sessions_callback(
	struct session_id *id,
	struct session_state *state,
	struct move_sessions_context *ctx
) {
	if (state->last_packet_timestamp + state->timeout <= ctx->now) {
		return 0;
	}

	session_lock_t *lock;
	struct session_state *found;
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
		memcpy(found, state, sizeof(struct session_state));
		ttlmap_release_lock(lock);
	} else if (status == TTLMAP_FOUND) {
		ttlmap_release_lock(lock);
	} else { // status == TTLMAP_FAILED
		// critical: misses some session, session table grows too fast
		LOG(WARN,
		    "missed session on table resize: "
		    "vs = [%d]",
		    id->vs_id);
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
	struct memory_context *mctx = &table->mctx;

	int init_result = TTLMAP_INIT(
		next_map,
		mctx,
		struct session_id,
		struct session_state,
		new_size
	);
	if (init_result != 0) {
		NEW_ERROR("failed to init new table");
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
		struct session_id,
		struct session_state,
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

int
session_table_iter(
	struct session_table *table,
	uint32_t now,
	session_table_iter_callback cb,
	void *userdata
) {
	return TTLMAP_ITER(
		session_table_map(table, session_table_current_gen(table)),
		struct session_id,
		struct session_state,
		now,
		cb,
		userdata
	);
}