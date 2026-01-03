#pragma once

#include "../vs.h"
#include "common/memory_address.h"
#include "context.h"
#include "lib/dataplane/module/module.h"
#include "real.h"
#include "state/real.h"
#include "state/vs.h"
#include <string.h>

////////////////////////////////////////////////////////////////////////////////

static inline void
packet_ctx_set_vs(struct packet_ctx *ctx, struct vs *vs) {
	struct vs_state *vs_state = ADDR_OF(&vs->state);
	ctx->vs.info = &vs_state->info.shard[ctx->worker->idx];
	ctx->vs.ph_stats =
		vs_counter(vs, ctx->worker->idx, ctx->counter.storage);
	ctx->vs.state_stats = &ctx->vs.info->stats;
	ctx->vs.view = vs;
}

static inline void
packet_ctx_set_real(struct packet_ctx *ctx, struct real *real) {
	struct real_state *real_state = ADDR_OF(&real->state);
	ctx->real.info = &real_state->info.shard[ctx->worker->idx];
	ctx->real.ph_stats =
		real_counter(real, ctx->worker->idx, ctx->counter.storage);
	ctx->real.state_stats = &ctx->real.info->stats;
	ctx->real.view = real;
}

static inline void
packet_ctx_unset_real(struct packet_ctx *ctx) {
	memset(&ctx->real, 0, sizeof(ctx->real));
}

////////////////////////////////////////////////////////////////////////////////

static inline void
packet_ctx_send_packet(struct packet_ctx *ctx) {
	packet_front_output(ctx->packet_front, ctx->packet);
}

static inline void
packet_ctx_drop_packet(struct packet_ctx *ctx) {
	packet_front_drop(ctx->packet_front, ctx->packet);
}