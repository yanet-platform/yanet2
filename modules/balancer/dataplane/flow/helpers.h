#pragma once

#include "api/stats.h"
#include "context.h"

////////////////////////////////////////////////////////////////////////////////
// Config stats
////////////////////////////////////////////////////////////////////////////////

static inline struct balancer_icmp_stats *
packet_ctx_icmp_v4_config_stats(struct packet_ctx *ctx) {
	return ctx->stats.icmp_v4;
}

static inline struct balancer_icmp_stats *
packet_ctx_icmp_v6_config_stats(struct packet_ctx *ctx) {
	return ctx->stats.icmp_v6;
}

static inline struct balancer_common_stats *
packet_ctx_common_config_stats(struct packet_ctx *ctx) {
	return ctx->stats.common;
}

static inline struct balancer_l4_stats *
packet_ctx_l4_config_stats(struct packet_ctx *ctx) {
	return ctx->stats.l4;
}

////////////////////////////////////////////////////////////////////////////////
// Module macros
////////////////////////////////////////////////////////////////////////////////

#define L4_STATS_INC(name, ctx)                                                \
	do {                                                                   \
		packet_ctx_l4_config_stats(ctx)->name += 1;                    \
	} while (0)

#define COMMON_STATS_INC(name, ctx)                                            \
	do {                                                                   \
		packet_ctx_common_config_stats(ctx)->name += 1;                \
	} while (0)

#define COMMON_STATS_ADD(name, ctx, value)                                     \
	do {                                                                   \
		packet_ctx_common_config_stats(ctx)->name += value;            \
	} while (0)

#define ICMP_V4_STATS_INC(name, ctx)                                           \
	do {                                                                   \
		packet_ctx_icmp_v4_config_stats(ctx)->name += 1;               \
	} while (0)

#define ICMP_V6_STATS_INC(name, ctx)                                           \
	do {                                                                   \
		packet_ctx_icmp_v6_config_stats(ctx)->name += 1;               \
	} while (0)

#define ICMP_STATS_INC(name, header_type, ctx)                                 \
	do {                                                                   \
		if ((header_type) == IPPROTO_ICMP) {                           \
			ICMP_V4_STATS_INC(name, ctx);                          \
		} else {                                                       \
			ICMP_V6_STATS_INC(name, ctx);                          \
		}                                                              \
	} while (0)

////////////////////////////////////////////////////////////////////////////////
// Vs Stats and Info
////////////////////////////////////////////////////////////////////////////////

static inline struct vs_stats *
packet_ctx_vs_stats(struct packet_ctx *ctx) {
	return ctx->vs.stats;
}

////////////////////////////////////////////////////////////////////////////////
// Vs macros
////////////////////////////////////////////////////////////////////////////////

#define VS_STATS_INC(name, ctx)                                                \
	do {                                                                   \
		packet_ctx_vs_stats(ctx)->name += 1;                           \
	} while (0)

////////////////////////////////////////////////////////////////////////////////
// Real Stats and Info
////////////////////////////////////////////////////////////////////////////////

static inline struct real_stats *
packet_ctx_real_stats(struct packet_ctx *ctx) {
	return ctx->real.stats;
}

////////////////////////////////////////////////////////////////////////////////
// Real macros
////////////////////////////////////////////////////////////////////////////////

#define REAL_STATS_INC(name, ctx)                                              \
	do {                                                                   \
		packet_ctx_real_stats(ctx)->name += 1;                         \
	} while (0)
