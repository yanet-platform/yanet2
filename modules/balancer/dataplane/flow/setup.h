#pragma once

#include "context.h"
#include "handler.h"

#include "handler/handler.h"
#include "state/state.h"

////////////////////////////////////////////////////////////////////////////////

static inline void
packet_ctx_setup(
	struct packet_ctx *ctx,
	uint32_t now,
	struct dp_worker *worker,
	struct module_ectx *ectx,
	struct packet_handler *handler,
	struct packet_front *packet_front
) {
	memset(ctx, 0, sizeof(struct packet_ctx));
	ctx->packet = NULL;
	ctx->handler = handler;
	ctx->now = now;
	ctx->counter.storage = ADDR_OF(&ectx->counter_storage);
	ctx->worker = worker;
	ctx->counter.common = common_handler_counter(
		handler, worker->idx, ctx->counter.storage
	);
	ctx->counter.icmp_v4 = icmp_v4_handler_counter(
		handler, worker->idx, ctx->counter.storage
	);
	ctx->counter.icmp_v6 = icmp_v4_handler_counter(
		handler, worker->idx, ctx->counter.storage
	);
	ctx->counter.l4 =
		l4_handler_counter(handler, worker->idx, ctx->counter.storage);
	ctx->packet_front = packet_front;
	ctx->state.ptr = ADDR_OF(&handler->state);
	ctx->state.stats = &ctx->state.ptr->stats[worker->idx];
}

static inline void
packet_ctx_set_packet(struct packet_ctx *ctx, struct packet *packet) {
	ctx->packet = packet;
}