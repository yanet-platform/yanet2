#pragma once

#include "context.h"

////////////////////////////////////////////////////////////////////////////////

static inline void
packet_ctx_setup(
	struct packet_ctx *ctx,
	uint32_t now,
	struct dp_worker *worker,
	struct module_ectx *ectx,
	struct balancer_module_config *config,
	struct packet_front *packet_front
) {
	memset(ctx, 0, sizeof(struct packet_ctx));
	ctx->packet = NULL;
	ctx->config = config;
	ctx->now = now;
	ctx->counter.storage = ADDR_OF(&ectx->counter_storage);
	ctx->worker = worker;
	ctx->counter.common =
		get_module_counter(config, worker->idx, ctx->counter.storage);
	ctx->counter.icmp_v4 = get_icmp_v4_module_counter(
		config, worker->idx, ctx->counter.storage
	);
	ctx->counter.icmp_v6 = get_icmp_v4_module_counter(
		config, worker->idx, ctx->counter.storage
	);
	ctx->counter.l4 = get_l4_module_counter(
		config, worker->idx, ctx->counter.storage
	);
	ctx->packet_front = packet_front;
	ctx->state.ptr = ADDR_OF(&config->state);
	ctx->state.stats = &ctx->state.ptr->stats[worker->idx];
}

static inline void
packet_ctx_set_packet(struct packet_ctx *ctx, struct packet *packet) {
	ctx->packet = packet;
}