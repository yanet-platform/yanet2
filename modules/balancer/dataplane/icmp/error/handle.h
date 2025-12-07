#pragma once

#include "broadcast.h"

#include <netinet/icmp6.h>
#include <netinet/in.h>
#include <netinet/ip_icmp.h>

#include "../../flow/helpers.h"
#include "../../module.h"
#include "../../tunnel.h"
#include "../../vs.h"

#include "flow/stats.h"
#include "validate.h"

////////////////////////////////////////////////////////////////////////////////

void
handle_icmp_error_packet(struct packet_ctx *ctx) {
	// If session with goal real is present on the balancer,
	// forward packet to this real.
	//
	// Else, if packet is not clone, clone it and broadcast to other
	// balancers.

	// First, validate and parse packet.
	// On errors, update corresponding counters.
	enum validate_packet_result validate_result =
		validate_and_parse_packet(ctx);

	switch (validate_result) {

	// If packet is invalid, drop it.
	case validate_packet_error:
		// counters already updated
		packet_ctx_drop_packet(ctx);
		break;

	case validate_packet_vs_not_found:
		// virtual service not found,
		// so we can not broadcast packet nor
		// forward it.
		// counters already updated.
		packet_ctx_drop_packet(ctx);
		break;

	// If session with real not found on the balancer,
	// try to broadcast packet to other balancers.
	case validate_packet_session_not_found:
		broadcast_icmp_packet(ctx);
		break;

	// If session with real found on the balancer,
	// tunnel packet to real.
	case validate_packet_session_found:
		// send packet to real
		tunnel_packet(
			ctx->vs.ptr->flags,
			ctx->real.ptr,
			ctx->packet
		); // added tunneling for packet

		// send packet to the next module
		packet_ctx_send_packet(ctx);

		// update stats

		// update module stats

		// update icmp stats
		if (ctx->packet->transport_header.type == IPPROTO_ICMP) {
			ICMP_V4_STATS_INC(forwarded_packets, ctx);
		} else {
			ICMP_V6_STATS_INC(forwarded_packets, ctx);
		}

		// update common module stats
		packet_ctx_update_common_stats_on_outgoing_packet(ctx);

		// update vs counter
		VS_STATS_INC(error_icmp_packets, ctx);

		// update real counter
		REAL_STATS_INC(error_icmp_packets, ctx);

		break;
	}
}