#pragma once

#include "api/stats.h"
#include "context.h"

#include "rte_mbuf_core.h"

////////////////////////////////////////////////////////////////////////////////
// Common module stats
////////////////////////////////////////////////////////////////////////////////

static inline void
packet_ctx_update_common_stats_on_outgoing_packet(struct packet_ctx *ctx) {
	uint64_t pkt_len = ctx->packet->mbuf->pkt_len;

	ctx->stats.common->outgoing_packets += 1;
	ctx->stats.common->outgoing_bytes += pkt_len;
}

static inline void
packet_ctx_update_common_stats_on_incoming_packet(struct packet_ctx *ctx) {
	uint64_t pkt_len = ctx->packet->mbuf->pkt_len;

	ctx->stats.common->incoming_packets += 1;
	ctx->stats.common->incoming_bytes += pkt_len;
}

////////////////////////////////////////////////////////////////////////////////
// Virtual service
////////////////////////////////////////////////////////////////////////////////

static inline void
packet_ctx_update_vs_stats_on_outgoing_packet(struct packet_ctx *ctx) {
	uint64_t pkt_len = ctx->packet->mbuf->pkt_len;

	ctx->vs.stats->outgoing_packets += 1;
	ctx->vs.stats->outgoing_bytes += pkt_len;
}

static inline void
packet_ctx_update_vs_stats_on_incoming_packet(struct packet_ctx *ctx) {
	uint64_t pkt_len = ctx->packet->mbuf->pkt_len;

	ctx->vs.stats->incoming_packets += 1;
	ctx->vs.stats->incoming_bytes += pkt_len;
}

////////////////////////////////////////////////////////////////////////////////
// Real
////////////////////////////////////////////////////////////////////////////////

static inline void
packet_ctx_update_real_stats_on_packet(struct packet_ctx *ctx) {
	uint64_t pkt_len = ctx->packet->mbuf->pkt_len;

	ctx->real.stats->packets += 1;
	ctx->real.stats->bytes += pkt_len;
}