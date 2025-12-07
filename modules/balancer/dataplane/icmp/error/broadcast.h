#pragma once

#include "../../flow/context.h"

#include "common/network.h"
#include "dataplane/module/module.h"
#include "dataplane/packet/packet.h"
#include "flow/helpers.h"
#include "lib/dataplane/worker/worker.h"
#include "vs.h"

#include "tunnel.h"
#include <assert.h>
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
	if (packet->transport_header.type == IPPROTO_ICMP) {
		ICMP_V4_STATS_INC(packet_clones, ctx);
	} else if (packet->transport_header.type == IPPROTO_ICMPV6) {
		ICMP_V6_STATS_INC(packet_clones, ctx);
	} else {
		// impossible
		assert(false);
	}

	// we send cloned packets to other balancer,
	// so we dont update vs or real counters here.

	// send packet to the next module
	packet_front_output(ctx->packet_front, packet);
}

////////////////////////////////////////////////////////////////////////////////

static inline void
update_counters_on_packet_clone_failed(struct packet_ctx *ctx) {
	struct packet *packet = ctx->packet;
	if (packet->transport_header.type == IPPROTO_ICMP) {
		ICMP_V4_STATS_INC(packet_clone_failures, ctx);
	} else if (packet->transport_header.type == IPPROTO_ICMPV6) {
		ICMP_V6_STATS_INC(packet_clone_failures, ctx);
	}
}

////////////////////////////////////////////////////////////////////////////////

static inline void
broadcast_icmp_packet(struct packet_ctx *ctx) {
	// If packet is cloned already, do nothing.
	//
	// Else, if virtual service for the packet was found,
	// we iterate over virtual service peers and broadcast packet
	// to them.

	// todo: check if icmp info is null,
	// In that case we dont clone as packet is already cloned by
	// sender peer.
	// if (cloned) { <- todo
	//	return;
	// }

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
			// todo: update counter
			continue;
		}

		// tunnel packet to peer
		struct net6_addr *peer = &vs->peers_v6[i];
		tunnel_v6(clone, balancer_src_v6, peer->bytes);

		// send packet
		send_cloned_packet(ctx, clone);
	}
}