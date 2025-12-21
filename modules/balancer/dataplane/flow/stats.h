#pragma once

#include "context.h"

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
		&ctx->vs.counter->outgoing_packets, 1, memory_order_relaxed
	);
	atomic_fetch_add_explicit(
		&ctx->vs.counter->outgoing_bytes, pkt_len, memory_order_relaxed
	);

	atomic_fetch_add_explicit(
		&ctx->vs.state->stats.vs.outgoing_packets,
		1,
		memory_order_relaxed
	);
	atomic_fetch_add_explicit(
		&ctx->vs.state->stats.vs.outgoing_bytes,
		pkt_len,
		memory_order_relaxed
	);
}

static inline void
packet_ctx_update_vs_stats_on_incoming_packet(struct packet_ctx *ctx) {
	uint64_t pkt_len = ctx->packet->mbuf->pkt_len;

	atomic_fetch_add_explicit(
		&ctx->vs.counter->incoming_packets, 1, memory_order_relaxed
	);
	atomic_fetch_add_explicit(
		&ctx->vs.counter->incoming_bytes, pkt_len, memory_order_relaxed
	);

	atomic_fetch_add_explicit(
		&ctx->vs.state->stats.vs.incoming_packets,
		1,
		memory_order_relaxed
	);
	atomic_fetch_add_explicit(
		&ctx->vs.state->stats.vs.incoming_bytes,
		pkt_len,
		memory_order_relaxed
	);
	atomic_store_explicit(
		&ctx->vs.state->last_packet_timestamp,
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
		&ctx->real.counter->packets, 1, memory_order_relaxed
	);
	atomic_fetch_add_explicit(
		&ctx->real.counter->bytes, pkt_len, memory_order_relaxed
	);

	atomic_fetch_add_explicit(
		&ctx->real.state->stats.real.packets, 1, memory_order_relaxed
	);
	atomic_fetch_add_explicit(
		&ctx->real.state->stats.real.bytes,
		pkt_len,
		memory_order_relaxed
	);
	atomic_store_explicit(
		&ctx->real.state->last_packet_timestamp,
		ctx->now,
		memory_order_relaxed
	);
}