#pragma once

#include <string.h>
#include <threads.h>

#include "common/interval_counter.h"
#include "common/memory_address.h"
#include "controlplane/config/econtext.h"
#include "counter.h"
#include "counters/counters.h"
#include "dataplane/packet/packet.h"
#include "module.h"
#include "real.h"
#include "vs.h"

////////////////////////////////////////////////////////////////////////////////

// Initialized during packet processing

struct packet_ctx {
	struct counter_storage *counter_storage;

	size_t worker;

	size_t packet_len;

	struct module_config_counter *module_config_counter;

	struct {
		vs_counter_t *config_counter;
		struct service_state *persistent_state;
	} vs;

	struct {
		real_counter_t *config_counter;
		struct service_state *persistent_state;
	} real;
};

////////////////////////////////////////////////////////////////////////////////

static inline struct module_config_counter *
module_config_counter(struct packet_ctx *ctx) {
	return ctx->module_config_counter;
}

static inline vs_counter_t *
vs_config_counter(struct packet_ctx *ctx) {
	return ctx->vs.config_counter;
}

static inline vs_counter_t *
vs_state_counter(struct packet_ctx *ctx) {
	return &ctx->vs.persistent_state->stats.vs;
}

static inline real_counter_t *
real_config_counter(struct packet_ctx *ctx) {
	return ctx->real.config_counter;
}

static inline real_counter_t *
real_state_counter(struct packet_ctx *ctx) {
	return &ctx->real.persistent_state->stats.real;
}

////////////////////////////////////////////////////////////////////////////////

static inline void
packet_ctx_setup(
	struct packet_ctx *ctx,
	size_t worker,
	struct module_ectx *ectx,
	struct balancer_module_config *config
) {
	memset(ctx, 0, sizeof(struct packet_ctx));
	ctx->counter_storage = ADDR_OF(&ectx->counter_storage);
	ctx->worker = worker;
	ctx->module_config_counter = balancer_module_config_counter(
		config, worker, ctx->counter_storage
	);
}

////////////////////////////////////////////////////////////////////////////////

// Packet income

static inline void
packet_ctx_incoming_packet(struct packet_ctx *ctx, struct packet *packet) {
	ctx->packet_len = packet_to_mbuf(packet)->pkt_len;
	module_config_counter_incoming_packet(
		module_config_counter(ctx), ctx->packet_len
	);
}

////////////////////////////////////////////////////////////////////////////////

// Select vs

static inline void
packet_ctx_failed_to_select_vs(struct packet_ctx *ctx) {
	module_config_counter(ctx)->select_vs_failed += 1;
}

static inline void
packet_ctx_select_vs(struct packet_ctx *ctx, struct virtual_service *vs) {
	ctx->vs.config_counter =
		vs_counter(vs, ctx->worker, ctx->counter_storage);
	ctx->vs.persistent_state = ADDR_OF(&vs->state) + ctx->worker;
	vs_counter_incoming_packet(vs_config_counter(ctx), ctx->packet_len);
	vs_counter_incoming_packet(vs_state_counter(ctx), ctx->packet_len);
}

////////////////////////////////////////////////////////////////////////////////

// Check if packet src is allowed

static inline void
packet_ctx_packet_src_not_allowed(struct packet_ctx *ctx) {
	vs_config_counter(ctx)->packet_src_not_allowed += 1;
	vs_state_counter(ctx)->packet_src_not_allowed += 1;
	module_config_counter(ctx)->select_vs_failed += 1;
}

////////////////////////////////////////////////////////////////////////////////

// Select real

static inline void
packet_ctx_no_reals(struct packet_ctx *ctx) {
	vs_config_counter(ctx)->no_reals += 1;
	vs_state_counter(ctx)->no_reals += 1;
	module_config_counter(ctx)->select_real_failed += 1;
}

static inline void
packet_ctx_session_table_overflow(struct packet_ctx *ctx) {
	vs_config_counter(ctx)->session_table_overflow += 1;
	vs_state_counter(ctx)->session_table_overflow += 1;
	module_config_counter(ctx)->select_real_failed += 1;
}

// Real is disabled, but we try to select new if packet can be rescheduled,
// so packet not dropped here
static inline void
packet_ctx_real_disabled(struct packet_ctx *ctx, struct real *real) {
	if (real->flags & REAL_PRESENT_IN_CONFIG_FLAG) {
		real_counter(real, ctx->worker, ctx->counter_storage)
			->disabled += 1;
		ADDR_OF(&real->state)[ctx->worker].stats.real.disabled += 1;
	}
}

static inline void
packet_ctx_packet_not_rescheduled(struct packet_ctx *ctx) {
	vs_config_counter(ctx)->packet_not_rescheduled += 1;
	vs_state_counter(ctx)->packet_not_rescheduled += 1;
	module_config_counter(ctx)->select_real_failed += 1;
}

static inline void
packet_ctx_select_real_raw(struct packet_ctx *ctx, struct real *real) {
	ctx->real.config_counter =
		real_counter(real, ctx->worker, ctx->counter_storage);
	ctx->real.persistent_state = ADDR_OF(&real->state) + ctx->worker;

	real_counter_incoming_packet(real_config_counter(ctx), ctx->packet_len);
	real_counter_incoming_packet(real_state_counter(ctx), ctx->packet_len);

	vs_counter_outgoing_packet(vs_config_counter(ctx), ctx->packet_len);
	vs_counter_outgoing_packet(vs_state_counter(ctx), ctx->packet_len);

	module_config_counter(ctx)->outgoing_packets += 1;
	module_config_counter(ctx)->outgoing_bytes += ctx->packet_len;
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
		&ctx->vs.persistent_state->active_sessions;
	interval_counter_put(vs_active_sessions, from, timeout, 1);
	interval_counter_advance_time(vs_active_sessions, now);

	struct interval_counter *real_active_sessions =
		&ctx->real.persistent_state->active_sessions;
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