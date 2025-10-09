#pragma once

#include "config.h"
#include "dataplane/packet/encap.h"
#include "ring.h"
#include "rs_def.h"
#include "rte_tcp.h"
#include "session.h"
#include "vs_def.h"
#include <assert.h>
#include <filter/filter.h>
#include <netinet/in.h>
#include <stdint.h>

#include "mss.h"

////////////////////////////////////////////////////////////////////////////////

static inline bool
balancer_reschedule_real(struct balancer_packet_metadata *metadata) {
	// True for UDP and TCP SYN packets
	return (metadata->transport_proto == IPPROTO_UDP) ||
	       (metadata->transport_proto == IPPROTO_TCP &&
		((metadata->tcp_flags & (RTE_TCP_SYN_FLAG | RTE_TCP_RST_FLAG)
		 ) == RTE_TCP_SYN_FLAG));
}

////////////////////////////////////////////////////////////////////////////////

static inline struct balancer_rs *
balancer_select_rs(
	struct balancer_module_config *config,
	uint32_t worker_idx,
	struct balancer_vs *vs,
	struct balancer_packet_metadata *metadata
) {
	struct balancer_rs *reals = ADDR_OF(&config->reals);

	if (vs->flags & YANET_BALANCER_OPS_FLAG) {
		uint32_t real_id = ring_get(&vs->real_ring, metadata->hash);
		if (real_id == RING_VALUE_INVALID) {
			return NULL;
		}
		real_id += vs->real_start;
		return &reals[real_id];
	}

	uint32_t now = clock_get_time(&config->clock);
	uint32_t timeout = balancer_session_timeout(
		&config->state_config.timeouts, metadata
	);

	struct balancer_session_id session_id;
	fill_session_id(&session_id, metadata, vs->flags & VS_PURE_L3);

	struct balancer_session_state *session_state;
	balancer_session_lock_t *session_lock;
	int get_session_result = balancer_get_or_create_session(
		&config->state,
		worker_idx,
		now,
		timeout,
		&session_id,
		&session_state,
		&session_lock
	);
	if (get_session_result == BALANCER_SESSION_TABLE_OVERFLOW) {
		return NULL;
	}

	if (get_session_result == BALANCER_SESSION_FOUND) {
		struct balancer_rs *rs = &reals[session_state->real_id];
		if (rs->weight > 0) {
			session_state->timeout = timeout;
			session_state->last_packet_timestamp = now;
			balancer_session_unlock(session_lock);
			return rs;
		}
	}
	if (!balancer_reschedule_real(metadata)) {
		balancer_session_invalidate(session_state);
		balancer_session_unlock(session_lock);
		return NULL;
	}

	// Select new real

	uint32_t real_id = ring_get(&vs->real_ring, metadata->hash);
	if (real_id == RING_VALUE_INVALID) {
		balancer_session_unlock(session_lock);
		return NULL;
	}
	real_id += vs->real_start;
	session_state->create_timestamp = now;
	session_state->last_packet_timestamp = now;
	session_state->real_id = real_id;
	session_state->timeout = timeout;
	balancer_session_unlock(session_lock);
	return &reals[real_id];
}

////////////////////////////////////////////////////////////////////////////////

static inline int
balancer_tunnel_packet(
	balancer_vs_flags_t vs_flags,
	struct balancer_rs *rs,
	struct packet *packet
) {
	if (vs_flags & VS_FIX_MSS) {
        balancer_fix_mss(packet);
	}

	if (rs->flags & YANET_BALANCER_FLAG_DST_IPV6) { // IPv6
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);

		struct rte_ipv6_hdr *ipv6_header = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv6_hdr *,
			packet->network_header.offset
		);

		uint8_t src[16];
		for (uint8_t i = 0; i < 16; i++) {
			// rs->src_addr is already masked.
			src[i] = (ipv6_header->src_addr[i] & (~rs->src_mask[i])
				 ) |
				 rs->src_addr[i];
		}

		if (vs_flags & VS_GRE_FORWARDING) {
			/// @todo: support GRE
		}

		return packet_ip6_encap(packet, rs->dst_addr, src);
	} else {
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);

		struct rte_ipv4_hdr *ipv4_header = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr *,
			packet->network_header.offset
		);
		uint32_t src_mask = *(uint32_t *)(&rs->src_mask[0]);
		uint32_t src_addr = *(uint32_t *)(&rs->src_addr[0]);
		// rs->src_addr is already masked.
		uint32_t src = (ipv4_header->src_addr & ~src_mask) | src_addr;

		if (vs_flags & VS_GRE_FORWARDING) {
			/// @todo: support GRE
		}

		return packet_ip4_encap(
			packet, rs->dst_addr, (uint8_t *)(&src)
		);
	}
}