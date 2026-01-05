#pragma once

#include "context.h"
#include "handler.h"

#include "handler/handler.h"

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
	ctx->stats.storage = ADDR_OF(&ectx->counter_storage);
	ctx->worker = worker;
	ctx->worker_idx = worker->idx;
	ctx->stats.common = common_handler_counter(
		handler, worker->idx, ctx->stats.storage
	);
	ctx->stats.icmp_v4 = icmp_v4_handler_counter(
		handler, worker->idx, ctx->stats.storage
	);
	ctx->stats.icmp_v6 = icmp_v4_handler_counter(
		handler, worker->idx, ctx->stats.storage
	);
	ctx->stats.l4 =
		l4_handler_counter(handler, worker->idx, ctx->stats.storage);
	ctx->packet_front = packet_front;
	ctx->balancer_state = ADDR_OF(&handler->state);
}

static inline void
packet_ctx_set_packet(struct packet_ctx *ctx, struct packet *packet) {
	ctx->packet = packet;
}