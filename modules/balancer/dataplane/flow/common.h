#pragma once

#include "../vs.h"
#include "context.h"
#include "lib/dataplane/module/module.h"

////////////////////////////////////////////////////////////////////////////////

static inline void
packet_ctx_set_vs(struct packet_ctx *ctx, struct virtual_service *vs) {
	ctx->vs.ptr = vs;
	ctx->vs.counter =
		vs_counter(vs, ctx->worker->idx, ctx->counter.storage);
	ctx->vs.state = ADDR_OF(&vs->state) + ctx->worker->idx;
}

static inline void
packet_ctx_set_real(struct packet_ctx *ctx, struct real *real) {
	ctx->real.ptr = real;
	ctx->real.counter =
		real_counter(real, ctx->worker->idx, ctx->counter.storage);
	ctx->real.state = ADDR_OF(&real->state) + ctx->worker->idx;
}

static inline void
packet_ctx_unset_real(struct packet_ctx *ctx) {
	ctx->real.ptr = NULL;
	ctx->real.counter = NULL;
	ctx->real.state = NULL;
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