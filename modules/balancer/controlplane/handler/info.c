#include "info.h"

#include "api/balancer.h"
#include "api/vs.h"
#include "common/memory_address.h"
#include "handler/handler.h"
#include "real.h"
#include "state/session.h"
#include "state/state.h"
#include "vs.h"

#include <assert.h>
#include <netinet/in.h>
#include <stdlib.h>

////////////////////////////////////////////////////////////////////////////////

struct fill_sessions_info_ctx {
	struct named_session_info *sessions;
	struct packet_handler *handler;
	struct balancer_state *state;
	size_t size;
	size_t capacity;
};

static int
fill_sessions_callback(
	struct session_id *id, struct session_state *state, void *userdata
) {
	struct fill_sessions_info_ctx *ctx = userdata;
	if (ctx->size == ctx->capacity) {
		ctx->capacity =
			ctx->capacity == 0 ? (1 << 16) : ctx->capacity * 2;
		struct named_session_info *new_info =
			realloc(ctx->sessions,
				ctx->capacity *
					sizeof(struct named_session_info));
		if (new_info == NULL) {
			return -1;
		}
		ctx->sessions = new_info;
	}

	// real not present in current packet handler config
	if (ADDR_OF(&ctx->handler->reals_index)[state->real_id] ==
	    INDEX_INVALID) {
		return 0;
	}

	struct named_session_info *session_info = &ctx->sessions[ctx->size++];

	// fill identifier
	session_info->identifier.real =
		balancer_state_get_real_by_idx(ctx->state, state->real_id)
			->identifier;
	session_info->identifier.client_ip = id->client_ip;
	session_info->identifier.client_port = ntohs(id->client_port);

	// fill info
	session_info->info = (struct session_info
	){.create_timestamp = state->create_timestamp,
	  .last_packet_timestamp = state->last_packet_timestamp,
	  .timeout = state->timeout};

	return 0;
}

size_t
packet_handler_sessions_info(
	struct packet_handler *handler,
	struct named_session_info **sessions,
	uint32_t now
) {
	struct balancer_state *state = ADDR_OF(&handler->state);
	struct fill_sessions_info_ctx ctx = {
		.state = state,
		.sessions = NULL,
		.size = 0,
		.capacity = 0,
		.handler = handler,
	};
	int res = session_table_iter(
		&state->session_table, now, fill_sessions_callback, &ctx
	);
	assert(res == 0);
	*sessions = ctx.sessions;
	return ctx.size;
}

////////////////////////////////////////////////////////////////////////////////

static void
init_real_info(struct named_real_info *info, struct real *real) {
	info->real = real->identifier;
	info->active_sessions = 0;
	info->last_packet_timestamp = 0;
}

static void
init_real_infos(
	struct named_real_info *real_infos, struct packet_handler *handler
) {
	struct real *reals = ADDR_OF(&handler->reals);
	for (size_t i = 0; i < handler->reals_count; i++) {
		init_real_info(&real_infos[i], &reals[i]);
	}
}

static void
init_vs_info(
	struct named_vs_info *info,
	struct vs *vs,
	struct named_real_info *real_infos
) {
	info->identifier = vs->identifier;
	info->reals_count = vs->reals_count;
	info->reals = real_infos;
	info->active_sessions = 0;
	info->last_packet_timestamp = 0;
}

static void
init_vs_infos(
	struct named_vs_info *vs_infos,
	struct named_real_info *real_infos,
	struct packet_handler *handler
) {
	struct vs *vss = ADDR_OF(&handler->vs);
	size_t reals_counter = 0;
	for (size_t i = 0; i < handler->vs_count; i++) {
		struct vs *vs = &vss[i];
		struct named_vs_info *info = &vs_infos[i];
		init_vs_info(info, vs, real_infos + reals_counter);
		reals_counter += vs->reals_count;
	}
}

struct fill_balancer_info_ctx {
	struct balancer_info *info;
	struct named_real_info *reals;
	struct packet_handler *handler;
	struct balancer_state *state;
	uint32_t now;
};

static void
check_max(uint32_t *value, uint32_t c) {
	if (*value < c) {
		*value = c;
	}
}

int
fill_balancer_info_callback(
	struct session_id *id, struct session_state *state, void *userdata
) {
	struct fill_balancer_info_ctx *ctx = userdata;
	uint32_t real_idx = ADDR_OF(&ctx->handler->reals_index)[state->real_id];
	if (real_idx ==
	    INDEX_INVALID) { // real not present in packet handler config
		return 0;
	}

	const int is_session_active =
		state->last_packet_timestamp + state->timeout > ctx->now;

	uint32_t vs_idx = ADDR_OF(&ctx->handler->vs_index)[id->vs_id];
	assert(vs_idx != INDEX_INVALID);

	ctx->info->active_sessions += is_session_active;
	check_max(
		&ctx->info->last_packet_timestamp, state->last_packet_timestamp
	);

	struct named_real_info *real_info = &ctx->reals[real_idx];
	real_info->active_sessions += is_session_active;
	check_max(
		&real_info->last_packet_timestamp, state->last_packet_timestamp
	);

	struct named_vs_info *vs_info = &ctx->info->vs[vs_idx];
	vs_info->active_sessions += is_session_active;
	check_max(
		&vs_info->last_packet_timestamp, state->last_packet_timestamp
	);

	return 0;
}

void
packet_handler_balancer_info(
	struct packet_handler *handler, struct balancer_info *info, uint32_t now
) {
	struct balancer_state *state = ADDR_OF(&handler->state);

	struct named_real_info *reals =
		malloc(sizeof(struct named_real_info) * handler->reals_count);
	init_real_infos(reals, handler);

	struct named_vs_info *vs =
		malloc(sizeof(struct named_vs_info) * handler->vs_count);
	init_vs_infos(vs, reals, handler);

	// Initialize info structure
	info->vs_count = handler->vs_count;
	info->vs = vs;
	info->active_sessions = 0;
	info->last_packet_timestamp = 0;

	struct fill_balancer_info_ctx ctx = {
		.handler = handler,
		.state = state,
		.info = info,
		.reals = reals,
		.now = now
	};

	int res = session_table_iter(
		&state->session_table, 0, fill_balancer_info_callback, &ctx
	);
	assert(res == 0);
}