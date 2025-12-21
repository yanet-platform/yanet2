#pragma once

#include "context.h"
#include "modules/balancer/api/stats.h"

////////////////////////////////////////////////////////////////////////////////
// Config stats
////////////////////////////////////////////////////////////////////////////////

static inline struct balancer_icmp_module_stats *
packet_ctx_icmp_v4_config_stats(struct packet_ctx *ctx) {
	return ctx->counter.icmp_v4;
}

static inline struct balancer_icmp_module_stats *
packet_ctx_icmp_v6_config_stats(struct packet_ctx *ctx) {
	return ctx->counter.icmp_v6;
}

static inline struct balancer_common_module_stats *
packet_ctx_common_config_stats(struct packet_ctx *ctx) {
	return ctx->counter.common;
}

static inline struct balancer_l4_module_stats *
packet_ctx_l4_config_stats(struct packet_ctx *ctx) {
	return ctx->counter.l4;
}

////////////////////////////////////////////////////////////////////////////////
// State stats
////////////////////////////////////////////////////////////////////////////////

static inline struct balancer_icmp_module_stats *
packet_ctx_icmp_v4_state_stats(struct packet_ctx *ctx) {
	return &ctx->state.stats->icmp_ipv4;
}

static inline struct balancer_icmp_module_stats *
packet_ctx_icmp_v6_state_stats(struct packet_ctx *ctx) {
	return &ctx->state.stats->icmp_ipv6;
}

static inline struct balancer_common_module_stats *
packet_ctx_common_state_stats(struct packet_ctx *ctx) {
	return &ctx->state.stats->common;
}

static inline struct balancer_l4_module_stats *
packet_ctx_l4_state_stats(struct packet_ctx *ctx) {
	return &ctx->state.stats->l4;
}

////////////////////////////////////////////////////////////////////////////////
// Module macros
////////////////////////////////////////////////////////////////////////////////

#define L4_STATS_INC(name, ctx)                                                \
	do {                                                                   \
		atomic_fetch_add_explicit(                                     \
			&packet_ctx_l4_config_stats(ctx)->name,                \
			1,                                                     \
			memory_order_relaxed                                   \
		);                                                             \
		atomic_fetch_add_explicit(                                     \
			&packet_ctx_l4_state_stats(ctx)->name,                 \
			1,                                                     \
			memory_order_relaxed                                   \
		);                                                             \
	} while (0)

#define COMMON_STATS_INC(name, ctx)                                            \
	do {                                                                   \
		atomic_fetch_add_explicit(                                     \
			&packet_ctx_common_config_stats(ctx)->name,            \
			1,                                                     \
			memory_order_relaxed                                   \
		);                                                             \
		atomic_fetch_add_explicit(                                     \
			&packet_ctx_common_state_stats(ctx)->name,             \
			1,                                                     \
			memory_order_relaxed                                   \
		);                                                             \
	} while (0)

#define COMMON_STATS_ADD(name, ctx, value)                                     \
	do {                                                                   \
		atomic_fetch_add_explicit(                                     \
			&packet_ctx_common_config_stats(ctx)->name,            \
			(value),                                               \
			memory_order_relaxed                                   \
		);                                                             \
		atomic_fetch_add_explicit(                                     \
			&packet_ctx_common_state_stats(ctx)->name,             \
			(value),                                               \
			memory_order_relaxed                                   \
		);                                                             \
	} while (0)

#define ICMP_V4_STATS_INC(name, ctx)                                           \
	do {                                                                   \
		atomic_fetch_add_explicit(                                     \
			&packet_ctx_icmp_v4_config_stats(ctx)->name,           \
			1,                                                     \
			memory_order_relaxed                                   \
		);                                                             \
		atomic_fetch_add_explicit(                                     \
			&packet_ctx_icmp_v4_state_stats(ctx)->name,            \
			1,                                                     \
			memory_order_relaxed                                   \
		);                                                             \
	} while (0)

#define ICMP_V6_STATS_INC(name, ctx)                                           \
	do {                                                                   \
		atomic_fetch_add_explicit(                                     \
			&packet_ctx_icmp_v6_config_stats(ctx)->name,           \
			1,                                                     \
			memory_order_relaxed                                   \
		);                                                             \
		atomic_fetch_add_explicit(                                     \
			&packet_ctx_icmp_v6_state_stats(ctx)->name,            \
			1,                                                     \
			memory_order_relaxed                                   \
		);                                                             \
	} while (0)

#define ICMP_STATS_INC(name, header_type, ctx)                                 \
	do {                                                                   \
		if ((header_type) == IPPROTO_ICMP) {                           \
			ICMP_V4_STATS_INC(name, ctx);                          \
		} else if ((header_type) == IPPROTO_ICMPV6) {                  \
			ICMP_V6_STATS_INC(name, ctx);                          \
		} else {                                                       \
			assert(false);                                         \
		}                                                              \
	} while (0)

////////////////////////////////////////////////////////////////////////////////
// Vs Stats and Info
////////////////////////////////////////////////////////////////////////////////

static inline struct balancer_vs_stats *
packet_ctx_vs_config_stats(struct packet_ctx *ctx) {
	return ctx->vs.counter;
}

static inline struct service_state *
packet_ctx_vs_state_info(struct packet_ctx *ctx) {
	return ctx->vs.state;
}

static inline struct balancer_vs_stats *
packet_ctx_vs_state_stats(struct packet_ctx *ctx) {
	return &ctx->vs.state->stats.vs;
}

////////////////////////////////////////////////////////////////////////////////
// Vs macros
////////////////////////////////////////////////////////////////////////////////

#define VS_STATS_INC(name, ctx)                                                \
	do {                                                                   \
		atomic_fetch_add_explicit(                                     \
			&packet_ctx_vs_config_stats(ctx)->name,                \
			1,                                                     \
			memory_order_relaxed                                   \
		);                                                             \
		atomic_fetch_add_explicit(                                     \
			&packet_ctx_vs_state_stats(ctx)->name,                 \
			1,                                                     \
			memory_order_relaxed                                   \
		);                                                             \
	} while (0)

////////////////////////////////////////////////////////////////////////////////
// Real Stats and Info
////////////////////////////////////////////////////////////////////////////////

static inline struct balancer_real_stats *
packet_ctx_real_config_stats(struct packet_ctx *ctx) {
	return ctx->real.counter;
}

static inline struct service_state *
packet_ctx_real_state_info(struct packet_ctx *ctx) {
	return ctx->real.state;
}

static inline struct balancer_real_stats *
packet_ctx_real_state_stats(struct packet_ctx *ctx) {
	return &ctx->real.state->stats.real;
}

////////////////////////////////////////////////////////////////////////////////
// Real macros
////////////////////////////////////////////////////////////////////////////////

#define REAL_STATS_INC(name, ctx)                                              \
	do {                                                                   \
		atomic_fetch_add_explicit(                                     \
			&packet_ctx_real_config_stats(ctx)->name,              \
			1,                                                     \
			memory_order_relaxed                                   \
		);                                                             \
		atomic_fetch_add_explicit(                                     \
			&packet_ctx_real_state_stats(ctx)->name,               \
			1,                                                     \
			memory_order_relaxed                                   \
		);                                                             \
	} while (0)
