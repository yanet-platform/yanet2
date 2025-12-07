#pragma once

#include "echo/handle.h"
#include "error/handle.h"

#include "lib/dataplane/packet/packet.h"

////////////////////////////////////////////////////////////////////////////////

static inline void
handle_icmp_packet(struct packet_ctx *ctx) {
	// Separately handle echo request and error packets.
	// On echo, just answer from the balancer and dont forward packet to
	// real. On error, we need to determine of packet is for the real, which
	// serves by balancer. If so, forward packet to the real. Else,
	// broadcast packet to other balancers, which serves this virtual
	// services.

	struct rte_mbuf *mbuf = packet_to_mbuf(ctx->packet);
	struct rte_icmp_hdr *icmp = rte_pktmbuf_mtod_offset(
		mbuf,
		struct rte_icmp_hdr *,
		ctx->packet->transport_header.offset
	);

	// this functions send or drop packets under the hood
	// (and update corresponding counters)
	switch (icmp->icmp_type) {
	case ICMP_ECHO:
		handle_icmp_echo_ipv4(ctx);
		break;
	case ICMP6_ECHO_REQUEST:
		handle_icmp_echo_ipv6(ctx);
		break;
	default:
		handle_icmp_error_packet(ctx);
	}
}