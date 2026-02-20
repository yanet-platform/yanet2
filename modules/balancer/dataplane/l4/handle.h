#pragma once

#include "api/session.h"
#include "common/likely.h"
#include "flow/helpers.h"
#include "flow/stats.h"
#include "meta.h"
#include "select.h"

#include "../lookup.h"
#include "../tunnel.h"
#include "session_table.h"
#include "state/state.h"
#include <assert.h>
#include <stdio.h>

////////////////////////////////////////////////////////////////////////////////

static inline void
handle_l4_packet(struct packet_ctx *ctx) {
	// update stats
	L4_STATS_INC(incoming_packets, ctx);

	// 1. Validate packet and set metadata
	struct packet_metadata meta;
	int res = fill_packet_metadata(ctx->packet, &meta);
	if (res != 0) { // unexpected packet type
		L4_STATS_INC(invalid_packets, ctx);
		packet_ctx_drop_packet(ctx);
		return;
	}

	// 2. Lookup virtual service for which packet is
	// directed to

	struct vs *vs = vs_lookup_and_fw(ctx);
	if (vs == NULL) { // not found virtual service
		L4_STATS_INC(select_vs_failed, ctx);
		packet_ctx_drop_packet(ctx);
		return;
	}

	// update VS incoming stats
	packet_ctx_update_vs_stats_on_incoming_packet(ctx);

	// 3. Select real for which packet will be forwarded
}

enum { adv = 8 };

static inline void
handle_l4_packets(struct packet_ctx *ctxs, size_t count) {
	assert(count > 0);

	struct packet_handler *handler = ctxs[0].handler;
	struct sessions_timeouts *timeouts = &handler->sessions_timeouts;

	// start critical section for the session table
	struct session_table *table = &ctxs[0].balancer_state->session_table;
	uint64_t current_table_gen =
		session_table_begin_cs(table, ctxs[0].worker_idx);

	// select virtual service for each packet
	for (size_t i = 0; i < count; ++i) {
		struct packet_ctx *ctx = &ctxs[i];

		// skip processed packets
		if (unlikely(ctx->processed)) {
			continue;
		}

		// update stats
		L4_STATS_INC(incoming_packets, ctx);

		// 1. Validate packet and set metadata
		struct packet_metadata meta;
		int res = fill_packet_metadata(ctx->packet, &meta);
		if (unlikely(res != 0)) { // unexpected packet type
			L4_STATS_INC(invalid_packets, ctx);
			packet_ctx_drop_packet(ctx);
			continue;
		}

		// 2. Lookup virtual service for which packet is
		// directed to

		struct vs *vs = vs_lookup_and_fw(ctx);
		if (unlikely(vs == NULL)) { // not found virtual service
			L4_STATS_INC(select_vs_failed, ctx);
			packet_ctx_drop_packet(ctx);
			continue;
		}

		// update VS incoming stats
		packet_ctx_update_vs_stats_on_incoming_packet(ctx);

		// fill session id and timeout
		fill_session_id(&ctx->session, &meta, vs);
		ctx->session_timeout = session_timeout(timeouts, &meta);

		ctx->transport_proto = meta.transport_proto;
		ctx->tcp_flags = meta.tcp_flags;

		prefetch_session(table, current_table_gen, &ctx->session);
	}

	for (size_t i = 0; i < count; ++i) {
		struct packet_ctx *ctx = &ctxs[i];
		if (unlikely(ctx->processed)) {
			continue;
		}
		struct real *selected_real =
			select_real(ctx, ctx->vs.ptr, table, current_table_gen);
		if (unlikely(selected_real == NULL)) { // failed to select real
			// update stats
			L4_STATS_INC(select_real_failed, ctx);
			packet_ctx_drop_packet(ctx);
			continue;
		}
	}

	session_table_end_cs(table, ctxs[0].worker_idx);

	for (size_t i = 0; i < count; ++i) {
		struct packet_ctx *ctx = &ctxs[i];
		if (unlikely(ctx->processed)) {
			continue;
		}

		// 4. Add tunnel to the selected real for the packet

		tunnel_packet(ctx->vs.ptr, ctx->real.ptr, ctx->packet);

		// 5. Pass packet to the next module

		packet_ctx_send_packet(ctx);

		// update stats
		L4_STATS_INC(outgoing_packets, ctx);
		packet_ctx_update_common_stats_on_outgoing_packet(ctx);
	}
}
