#pragma once

#include "flow/helpers.h"
#include "flow/stats.h"
#include "select.h"

#include "../lookup.h"
#include "../tunnel.h"

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

	struct real *real = select_real(ctx, vs, &meta);
	if (real == NULL) { // failed to select real
		// update stats
		L4_STATS_INC(select_real_failed, ctx);
		packet_ctx_drop_packet(ctx);
		return;
	}

	// 4. Add tunnel to the selected real for the packet

	tunnel_packet(vs, real, ctx->packet);

	// 5. Pass packet to the next module

	packet_ctx_send_packet(ctx);

	// update stats
	L4_STATS_INC(outgoing_packets, ctx);
	packet_ctx_update_common_stats_on_outgoing_packet(ctx);
}