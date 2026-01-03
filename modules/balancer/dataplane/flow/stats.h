#pragma once

#include "api/stats.h"
#include "context.h"
#include "rte_mbuf_core.h"
#include <stdatomic.h>

////////////////////////////////////////////////////////////////////////////////
// Common module stats
////////////////////////////////////////////////////////////////////////////////

static inline void
packet_ctx_update_common_stats_on_outgoing_packet(struct packet_ctx *ctx) {
	uint64_t pkt_len = ctx->packet->mbuf->pkt_len;

	atomic_fetch_add_explicit(
		&ctx->counter.common->outgoing_packets, 1, memory_order_relaxed
	);
	atomic_fetch_add_explicit(
		&ctx->counter.common->outgoing_bytes,
		pkt_len,
		memory_order_relaxed
	);

	atomic_fetch_add_explicit(
		&ctx->state.stats->common.outgoing_packets,
		1,
		memory_order_relaxed
	);
	atomic_fetch_add_explicit(
		&ctx->state.stats->common.outgoing_bytes,
		pkt_len,
		memory_order_relaxed
	);
}

static inline void
packet_ctx_update_common_stats_on_incoming_packet(struct packet_ctx *ctx) {
	uint64_t pkt_len = ctx->packet->mbuf->pkt_len;

	atomic_fetch_add_explicit(
		&ctx->counter.common->incoming_packets, 1, memory_order_relaxed
	);
	atomic_fetch_add_explicit(
		&ctx->counter.common->incoming_bytes,
		pkt_len,
		memory_order_relaxed
	);

	atomic_fetch_add_explicit(
		&ctx->state.stats->common.incoming_packets,
		1,
		memory_order_relaxed
	);
	atomic_fetch_add_explicit(
		&ctx->state.stats->common.incoming_bytes,
		pkt_len,
		memory_order_relaxed
	);
}

////////////////////////////////////////////////////////////////////////////////
// Virtual service
////////////////////////////////////////////////////////////////////////////////

static inline void
packet_ctx_update_vs_stats_on_outgoing_packet(struct packet_ctx *ctx) {
	uint64_t pkt_len = ctx->packet->mbuf->pkt_len;

	atomic_fetch_add_explicit(
		&ctx->vs.ph_stats->outgoing_packets, 1, memory_order_relaxed
	);
	atomic_fetch_add_explicit(
		&ctx->vs.ph_stats->outgoing_bytes, pkt_len, memory_order_relaxed
	);

	atomic_fetch_add_explicit(
		&ctx->vs.state_stats->outgoing_packets, 1, memory_order_relaxed
	);
	atomic_fetch_add_explicit(
		&ctx->vs.state_stats->outgoing_bytes,
		pkt_len,
		memory_order_relaxed
	);
}

static inline void
packet_ctx_update_vs_stats_on_incoming_packet(struct packet_ctx *ctx) {
	uint64_t pkt_len = ctx->packet->mbuf->pkt_len;

	atomic_fetch_add_explicit(
		&ctx->vs.ph_stats->incoming_packets, 1, memory_order_relaxed
	);
	atomic_fetch_add_explicit(
		&ctx->vs.ph_stats->incoming_bytes, pkt_len, memory_order_relaxed
	);

	atomic_fetch_add_explicit(
		&ctx->vs.state_stats->incoming_packets, 1, memory_order_relaxed
	);
	atomic_fetch_add_explicit(
		&ctx->vs.state_stats->incoming_bytes,
		pkt_len,
		memory_order_relaxed
	);
	atomic_store_explicit(
		&ctx->vs.info->last_packet_timestamp,
		ctx->now,
		memory_order_relaxed
	);
}

////////////////////////////////////////////////////////////////////////////////
// Real
////////////////////////////////////////////////////////////////////////////////

static inline void
packet_ctx_update_real_stats_on_packet(struct packet_ctx *ctx) {
	uint64_t pkt_len = ctx->packet->mbuf->pkt_len;

	atomic_fetch_add_explicit(
		&ctx->real.ph_stats->packets, 1, memory_order_relaxed
	);
	atomic_fetch_add_explicit(
		&ctx->real.ph_stats->bytes, pkt_len, memory_order_relaxed
	);

	atomic_fetch_add_explicit(
		&ctx->real.state_stats->packets, 1, memory_order_relaxed
	);
	atomic_fetch_add_explicit(
		&ctx->real.state_stats->bytes, pkt_len, memory_order_relaxed
	);
	atomic_store_explicit(
		&ctx->real.info->last_packet_timestamp,
		ctx->now,
		memory_order_relaxed
	);
}