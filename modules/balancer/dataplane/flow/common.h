#pragma once

#include "../vs.h"
#include "context.h"
#include "lib/dataplane/module/module.h"
#include "real.h"

#include <string.h>

////////////////////////////////////////////////////////////////////////////////

static inline void
packet_ctx_set_vs(struct packet_ctx *ctx, struct vs *vs) {
	ctx->vs.ptr = vs;
	ctx->vs.stats = vs_counter(vs, ctx->worker_idx, ctx->stats.storage);
}

static inline void
packet_ctx_set_real(struct packet_ctx *ctx, struct real *real) {
	ctx->real.ptr = real;
	ctx->real.stats =
		real_counter(real, ctx->worker_idx, ctx->stats.storage);
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