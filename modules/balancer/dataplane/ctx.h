#pragma once

#include <string.h>
#include <threads.h>

#include "common/interval_counter.h"
#include "common/memory_address.h"
#include "controlplane/config/econtext.h"
#include "lib/counters/counters.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/module/module.h"
#include "lib/dataplane/packet/packet.h"
#include "module.h"
#include "modules/balancer/state/state.h"
#include "real.h"
#include "vs.h"

#include "icmp/error/info.h"

#include "../api/counter.h"

////////////////////////////////////////////////////////////////////////////////

// Context of the balancer packet flow.
// Beeing initialized during packet processing.
struct packet_ctx {
	// packet and packet front
	struct packet *packet;
	struct packet_front *packet_front;

	// worker which process current packet
	struct dp_worker *worker;

	// module config
	struct balancer_module_config *config;

	// state of the balancer
	struct balancer_state *state;

	// current time in seconds
	uint32_t now;

	// module counters
	struct {
		struct balancer_common_module_stats *common;
		struct balancer_icmp_module_stats *icmp;
		struct balancer_l4_module_stats *l4;
		struct counter_storage *storage;
	} counter;

	// selected virtual service
	struct {
		struct balancer_vs_stats *counter;
		struct service_state *state;
		struct virtual_service *ptr;
	} vs;

	// selected real
	struct {
		struct balancer_real_stats *counter;
		struct service_state *state;
		struct real *ptr;
	} real;

	// info about icmp payload if any
	struct icmp_packet_info icmp_info;
};

////////////////////////////////////////////////////////////////////////////////

static inline struct balancer_common_module_stats *
common_module_counter(struct packet_ctx *ctx) {
	return ctx->counter.common;
}

static inline struct balancer_l4_module_stats *
l4_module_counter(struct packet_ctx *ctx) {
	return ctx->counter.l4;
}

static inline struct balancer_icmp_module_stats *
icmp_module_counter(struct packet_ctx *ctx) {
	return ctx->counter.icmp;
}

static inline struct balancer_vs_stats *
vs_config_counter(struct packet_ctx *ctx) {
	return ctx->vs.counter;
}

static inline struct balancer_vs_stats *
vs_state_counter(struct packet_ctx *ctx) {
	return &ctx->vs.state->stats.vs;
}

static inline struct balancer_real_stats *
real_config_counter(struct packet_ctx *ctx) {
	return ctx->real.counter;
}

static inline struct balancer_real_stats *
real_state_counter(struct packet_ctx *ctx) {
	return &ctx->real.state->stats.real;
}

////////////////////////////////////////////////////////////////////////////////

static inline void
real_counter_incoming_packet(
	struct balancer_real_stats *real_counter, uint64_t len
) {
	real_counter->packets += 1;
	real_counter->bytes += len;
}

static inline void
vs_counter_incoming_packet(struct balancer_vs_stats *vs_counter, uint64_t len) {
	vs_counter->incoming_packets += 1;
	vs_counter->incoming_bytes += len;
}

static inline void
vs_counter_outgoing_packet(struct balancer_vs_stats *vs_counter, uint64_t len) {
	vs_counter->outgoing_packets += 1;
	vs_counter->outgoing_bytes += len;
}

static inline void
module_config_counter_incoming_packet(
	struct balancer_common_module_stats *module_counter, uint64_t len
) {
	module_counter->incoming_packets += 1;
	module_counter->incoming_bytes += len;
}

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
	ctx->counter.icmp = get_icmp_module_counter(
		config, worker->idx, ctx->counter.storage
	);
	ctx->counter.l4 = get_l4_module_counter(
		config, worker->idx, ctx->counter.storage
	);
	ctx->packet_front = packet_front;
	ctx->state = ADDR_OF(&config->state);
}

////////////////////////////////////////////////////////////////////////////////

// Packet income

static inline void
packet_ctx_incoming_packet(struct packet_ctx *ctx, struct packet *packet) {
	ctx->packet = packet;
	module_config_counter_incoming_packet(
		common_module_counter(ctx), packet_to_mbuf(packet)->pkt_len
	);
}

////////////////////////////////////////////////////////////////////////////////

// Select vs

static inline void
packet_ctx_failed_to_select_vs(struct packet_ctx *ctx) {
	ctx->counter.l4->select_vs_failed += 1;
}

static inline void
packet_ctx_set_vs(struct packet_ctx *ctx, struct virtual_service *vs) {
	ctx->vs.ptr = vs;
	ctx->vs.counter =
		vs_counter(vs, ctx->worker->idx, ctx->counter.storage);
	ctx->vs.state = ADDR_OF(&vs->state) + ctx->worker->idx;
}

static inline void
packet_ctx_select_vs_icmp(
	struct packet_ctx *ctx, struct virtual_service *vs, bool error
) {
	packet_ctx_set_vs(ctx, vs);
	// todo: update vs icmp counters
	(void)error;
}

static inline void
packet_ctx_select_vs(struct packet_ctx *ctx, struct virtual_service *vs) {
	packet_ctx_set_vs(ctx, vs);

	vs_counter_incoming_packet(
		vs_config_counter(ctx), packet_to_mbuf(ctx->packet)->pkt_len
	);
	vs_counter_incoming_packet(
		vs_state_counter(ctx), packet_to_mbuf(ctx->packet)->pkt_len
	);
}

////////////////////////////////////////////////////////////////////////////////

// Check if packet src is allowed

static inline void
packet_ctx_packet_src_not_allowed(struct packet_ctx *ctx) {
	vs_config_counter(ctx)->packet_src_not_allowed += 1;
	vs_state_counter(ctx)->packet_src_not_allowed += 1;
	l4_module_counter(ctx)->select_vs_failed += 1;
}

////////////////////////////////////////////////////////////////////////////////

// Select real

static inline void
packet_ctx_no_reals(struct packet_ctx *ctx) {
	vs_config_counter(ctx)->no_reals += 1;
	vs_state_counter(ctx)->no_reals += 1;
	l4_module_counter(ctx)->select_real_failed += 1;
}

static inline void
packet_ctx_session_table_overflow(struct packet_ctx *ctx) {
	vs_config_counter(ctx)->session_table_overflow += 1;
	vs_state_counter(ctx)->session_table_overflow += 1;
	l4_module_counter(ctx)->select_real_failed += 1;
}

// Real is disabled, but we try to select new if packet can be rescheduled,
// so packet not dropped here
static inline void
packet_ctx_real_disabled(struct packet_ctx *ctx, struct real *real) {
	if (real->flags & REAL_PRESENT_IN_CONFIG_FLAG) {
		real_counter(real, ctx->worker->idx, ctx->counter.storage)
			->packets_real_disabled += 1;
		ADDR_OF(&real->state)
		[ctx->worker->idx].stats.real.packets_real_disabled += 1;
	}
}

static inline void
packet_ctx_packet_not_rescheduled(struct packet_ctx *ctx) {
	vs_config_counter(ctx)->packet_not_rescheduled += 1;
	vs_state_counter(ctx)->packet_not_rescheduled += 1;
	l4_module_counter(ctx)->select_real_failed += 1;
}

static inline void
packet_ctx_set_real(struct packet_ctx *ctx, struct real *real) {
	ctx->real.ptr = real;
	ctx->real.counter =
		real_counter(real, ctx->worker->idx, ctx->counter.storage);
	ctx->real.state = ADDR_OF(&real->state) + ctx->worker->idx;
}

static inline void
packet_ctx_select_real_raw(struct packet_ctx *ctx, struct real *real) {
	packet_ctx_set_real(ctx, real);

	uint32_t pkt_len = packet_to_mbuf(ctx->packet)->pkt_len;

	real_counter_incoming_packet(real_config_counter(ctx), pkt_len);
	real_counter_incoming_packet(real_state_counter(ctx), pkt_len);

	vs_counter_outgoing_packet(vs_config_counter(ctx), pkt_len);
	vs_counter_outgoing_packet(vs_state_counter(ctx), pkt_len);

	common_module_counter(ctx)->outgoing_packets += 1;
	common_module_counter(ctx)->outgoing_bytes += pkt_len;
}

static inline void
packet_ctx_select_real_icmp(struct packet_ctx *ctx, struct real *real) {
	packet_ctx_set_real(ctx, real);
}

// helper
static inline void
packet_ctx_select_real(
	struct packet_ctx *ctx,
	struct real *real,
	bool new_session,
	uint32_t now,
	uint32_t from,
	uint32_t timeout
) {
	// select real
	packet_ctx_select_real_raw(ctx, real);

	// store session info
	if (new_session) {
		vs_config_counter(ctx)->created_sessions += 1;
		vs_state_counter(ctx)->created_sessions += 1;

		real_config_counter(ctx)->created_sessions += 1;
		real_state_counter(ctx)->created_sessions += 1;
	}

	struct interval_counter *vs_active_sessions =
		&ctx->vs.state->active_sessions;
	interval_counter_put(vs_active_sessions, from, timeout, 1);
	interval_counter_advance_time(vs_active_sessions, now);

	struct interval_counter *real_active_sessions =
		&ctx->real.state->active_sessions;
	interval_counter_put(real_active_sessions, from, timeout, 1);
	interval_counter_advance_time(real_active_sessions, now);
}

static inline void
packet_ctx_new_session(
	struct packet_ctx *ctx,
	struct real *real,
	uint32_t now,
	uint32_t timeout
) {
	packet_ctx_select_real(ctx, real, true, now, now, timeout);
}

static inline void
packet_ctx_extend_session(
	struct packet_ctx *ctx,
	struct real *real,
	uint32_t now,
	uint32_t from,
	uint32_t timeout
) {
	packet_ctx_select_real(ctx, real, false, now, from, timeout);
}

static inline void
packet_ctx_select_real_ops(struct packet_ctx *ctx, struct real *real) {
	// select real
	packet_ctx_select_real_raw(ctx, real);

	vs_config_counter(ctx)->ops_packets += 1;
	vs_state_counter(ctx)->ops_packets += 1;

	real_config_counter(ctx)->ops_packets += 1;
	real_state_counter(ctx)->ops_packets += 1;
}

////////////////////////////////////////////////////////////////////////////////

static inline void
packet_ctx_drop_packet(struct packet_ctx *ctx) {
	packet_front_drop(ctx->packet_front, ctx->packet);
}

static inline void
packet_ctx_send_packet(struct packet_ctx *ctx) {
	packet_front_output(ctx->packet_front, ctx->packet);
}

static inline void
packet_ctx_send_cloned_icmp_packet(
	struct packet_ctx *ctx, struct packet *clone
) {
	packet_front_output(ctx->packet_front, clone);
}