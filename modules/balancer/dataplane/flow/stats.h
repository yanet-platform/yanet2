#pragma once

#include "context.h"

////////////////////////////////////////////////////////////////////////////////
// Common module stats
////////////////////////////////////////////////////////////////////////////////

static inline void
packet_ctx_update_common_stats_on_outgoing_packet(struct packet_ctx *ctx) {
	uint64_t pkt_len = ctx->packet->mbuf->pkt_len;

	ctx->counter.common->outgoing_packets += 1;
	ctx->counter.common->outgoing_bytes += pkt_len;

	ctx->state.stats->common.outgoing_packets += 1;
	ctx->state.stats->common.outgoing_bytes += pkt_len;
}

static inline void
packet_ctx_update_common_stats_on_incoming_packet(struct packet_ctx *ctx) {
	uint64_t pkt_len = ctx->packet->mbuf->pkt_len;

	ctx->counter.common->outgoing_packets += 1;
	ctx->counter.common->outgoing_bytes += pkt_len;

	ctx->state.stats->common.outgoing_packets += 1;
	ctx->state.stats->common.outgoing_bytes += pkt_len;
}

////////////////////////////////////////////////////////////////////////////////
// Virtual service
////////////////////////////////////////////////////////////////////////////////

static inline void
packet_ctx_update_vs_stats_on_outgoing_packet(struct packet_ctx *ctx) {
	uint64_t pkt_len = ctx->packet->mbuf->pkt_len;

	ctx->vs.counter->outgoing_packets += 1;
	ctx->vs.counter->outgoing_bytes += pkt_len;

	ctx->vs.state->stats.vs.outgoing_packets += 1;
	ctx->vs.state->stats.vs.outgoing_bytes += pkt_len;
}

static inline void
packet_ctx_update_vs_stats_on_incoming_packet(struct packet_ctx *ctx) {
	uint64_t pkt_len = ctx->packet->mbuf->pkt_len;

	ctx->vs.counter->incoming_packets += 1;
	ctx->vs.counter->incoming_bytes += pkt_len;

	ctx->vs.state->stats.vs.incoming_packets += 1;
	ctx->vs.state->stats.vs.incoming_bytes += pkt_len;
}

////////////////////////////////////////////////////////////////////////////////
// Real
////////////////////////////////////////////////////////////////////////////////

static inline void
packet_ctx_update_real_stats_on_packet(struct packet_ctx *ctx) {
	uint64_t pkt_len = ctx->packet->mbuf->pkt_len;

	ctx->real.counter->packets += 1;
	ctx->real.counter->bytes += pkt_len;

	ctx->real.state->stats.real.packets += 1;
	ctx->real.state->stats.real.bytes += pkt_len;
}