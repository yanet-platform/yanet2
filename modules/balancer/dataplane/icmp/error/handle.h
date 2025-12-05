#pragma once

#include "broadcast.h"

#include <netinet/icmp6.h>
#include <netinet/in.h>
#include <netinet/ip_icmp.h>

#include "../../module.h"

#include "../../tunnel.h"
#include "../../vs.h"

#include "validate.h"

////////////////////////////////////////////////////////////////////////////////

void
handle_icmp_error_packet(struct packet_ctx *ctx) { // todo: packet -> packet_ctx
	// If session with goal real is present on the balancer,
	// forward packet to this real.
	//
	// Else, if packet is not clone, clone it and broadcast to other
	// balancers.

	// First, validate and parse packet.
	enum validate_packet_result validate_result =
		validate_and_parse_packet(ctx);

	switch (validate_result) {

	// If packet is invalid, drop it.
	case validate_packet_error:
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
		if (tunnel_packet( // added tunneling for packet
			    ctx->vs.ptr->flags,
			    ctx->real.ptr,
			    ctx->packet
		    ) == 0) {

			// successfully tunnel packet
			packet_ctx_send_packet(ctx);
		} else {
			// todo: handle tunnel errors
			assert(false);

			// drop packet
			packet_ctx_drop_packet(ctx);
		}
		break;
	}
}