#pragma once

#include "../../ctx.h"

#include "common/network.h"
#include "dataplane/packet/packet.h"
#include "lib/dataplane/worker/worker.h"
#include "vs.h"

#include "tunnel.h"

////////////////////////////////////////////////////////////////////////////////

static inline struct packet *
clone_packet(struct dp_worker *worker, struct packet *packet) {
	return worker_clone_packet(worker, packet);
}

////////////////////////////////////////////////////////////////////////////////

static inline void
broadcast_icmp_packet(struct packet_ctx *ctx) {
	// If packet is cloned already, do nothing.
	//
	// Else, if virtual service for the packet was found,
	// we iterate over virtual service peers and broadcast packet
	// to them.

	struct virtual_service *vs = ctx->vs.ptr;
	if (vs == NULL) {
		// todo: handle case when vs is NULL
		return;
	}
	// todo: check if icmp temporary info is null,
	// In that case we dont clone as packet is already cloned by
	// sender peer.

	// Broadcast packet to v4 peers.
	uint8_t *balancer_src_v4 = ctx->config->source_ip;
	for (size_t i = 0; i < vs->peers_v4_count; ++i) {
		struct packet *clone = clone_packet(ctx->worker, ctx->packet);
		if (clone == NULL) {
			// todo: update counter
			continue;
		}

		// tunnel packet to peer
		struct net4_addr *peer = &vs->peers_v4[i];
		tunnel_v4(clone, balancer_src_v4, peer->bytes);

		// send packet
		packet_ctx_send_cloned_icmp_packet(ctx, clone);
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
		packet_ctx_send_cloned_icmp_packet(ctx, clone);
	}
}