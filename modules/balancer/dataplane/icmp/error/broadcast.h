#pragma once

#include "../../flow/context.h"

#include "common/network.h"
#include "dataplane/module/module.h"
#include "dataplane/packet/packet.h"
#include "flow/common.h"
#include "flow/helpers.h"
#include "lib/dataplane/worker/worker.h"
#include "vs.h"

#include "tunnel.h"
#include <assert.h>
#include <linux/magic.h>
#include <netinet/in.h>

////////////////////////////////////////////////////////////////////////////////

static inline struct packet *
clone_packet(struct dp_worker *worker, struct packet *packet) {
	return worker_clone_packet(worker, packet);
}

////////////////////////////////////////////////////////////////////////////////

static inline void
send_cloned_packet(struct packet_ctx *ctx, struct packet *packet) {
	// update common module counters
	uint64_t pkt_len = ctx->packet->mbuf->pkt_len;
	COMMON_STATS_ADD(outgoing_bytes, ctx, pkt_len);
	COMMON_STATS_INC(outgoing_packets, ctx);

	// update icmp module counters
	ICMP_STATS_INC(packet_clones_sent, packet->transport_header.type, ctx);

	// we send cloned packets to other balancer,
	// so we dont update vs or real counters here.

	// send packet to the next module
	packet_front_output(ctx->packet_front, packet);
}

////////////////////////////////////////////////////////////////////////////////

static inline void
update_counters_on_packet_clone_failed(struct packet_ctx *ctx) {
	struct packet *packet = ctx->packet;
	uint16_t type = packet->transport_header.type;
	ICMP_STATS_INC(packet_clone_failures, type, ctx);
}

////////////////////////////////////////////////////////////////////////////////

// ICMP error message header structure
// For error messages, the format is:
// [type:1][code:1][checksum:2][unused:4][original packet...] We use the first 2
// bytes of the unused field to store our broadcast marker
struct icmp_error_hdr {
	uint8_t type;
	uint8_t code;
	rte_be16_t checksum;
	rte_be16_t unused_marker; // We use this for ICMP_BROADCAST_IDENT
	rte_be16_t unused_rest;
} __rte_packed;

static inline struct icmp_error_hdr *
icmp_error_hdr(struct packet *packet) {
	struct icmp_error_hdr *icmp = rte_pktmbuf_mtod_offset(
		packet->mbuf,
		struct icmp_error_hdr *,
		packet->transport_header.offset
	);
	return icmp;
}

////////////////////////////////////////////////////////////////////////////////

#define ICMP_BROADCAST_IDENT 0xBDC

////////////////////////////////////////////////////////////////////////////////

static inline void
set_cloned_mark(struct packet *packet) {
	icmp_error_hdr(packet)->unused_marker =
		rte_cpu_to_be_16(ICMP_BROADCAST_IDENT);
}

////////////////////////////////////////////////////////////////////////////////

static inline void
broadcast_icmp_packet(struct packet_ctx *ctx) {
	// If packet is cloned already, do nothing.
	//
	// Else, if virtual service for the packet was found,
	// we iterate over virtual service peers and broadcast packet
	// to them.

	// Check if packet is a cloned already
	if (ctx->decap && icmp_error_hdr(ctx->packet)->unused_marker ==
				  rte_cpu_to_be_16(ICMP_BROADCAST_IDENT)) {
		// Update module counters
		uint16_t header_type = ctx->packet->transport_header.type;
		ICMP_STATS_INC(packet_clones_received, header_type, ctx);
		packet_ctx_drop_packet(ctx);
		return;
	}

	struct virtual_service *vs = ctx->vs.ptr;
	assert(vs != NULL);

	// here virtual service can not be null

	// Update counters

	// Update virtual service counters
	VS_STATS_INC(broadcasted_icmp_packets, ctx);

	// Update module counters
	if (ctx->packet->transport_header.type == IPPROTO_ICMP) {
		ICMP_V4_STATS_INC(broadcasted_packets, ctx);
	} else if (ctx->packet->transport_header.type == IPPROTO_ICMPV6) {
		ICMP_V6_STATS_INC(broadcasted_packets, ctx);
	} else {
		// impossible
		assert(false);
	}

	// Broadcast packet to v4 peers.
	uint8_t *balancer_src_v4 = ctx->config->source_ip;
	for (size_t i = 0; i < vs->peers_v4_count; ++i) {
		struct packet *clone = clone_packet(ctx->worker, ctx->packet);
		if (clone == NULL) {
			update_counters_on_packet_clone_failed(ctx);
			continue;
		}

		// set mark that the packet is cloned
		set_cloned_mark(clone);

		// tunnel packet to peer
		struct net4_addr *peer = &vs->peers_v4[i];
		tunnel_v4(clone, balancer_src_v4, peer->bytes);

		// send packet
		send_cloned_packet(ctx, clone);
	}

	// Broadcast packet to v6 peers.
	uint8_t *balancer_src_v6 = ctx->config->source_ip_v6;
	for (size_t i = 0; i < vs->peers_v6_count; ++i) {
		struct packet *clone = clone_packet(ctx->worker, ctx->packet);
		if (clone == NULL) {
			update_counters_on_packet_clone_failed(ctx);
			continue;
		}

		// set mark that the packet is cloned
		set_cloned_mark(clone);

		// tunnel packet to peer
		struct net6_addr *peer = &vs->peers_v6[i];
		tunnel_v6(clone, balancer_src_v6, peer->bytes);

		// send packet
		send_cloned_packet(ctx, clone);
	}

	// Drop the initial packet
	packet_ctx_drop_packet(ctx);
}

////////////////////////////////////////////////////////////////////////////////

#undef ICMP_BROADCAST_IDENT