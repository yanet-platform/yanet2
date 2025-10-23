#pragma once

#include "mss.h"
#include "lib/dataplane/packet/encap.h"
#include "vs.h"
#include "real.h"

#include "../api/vs.h"

////////////////////////////////////////////////////////////////////////////////

static inline int
tunnel_packet(
	vs_flags_t vs_flags,
	struct real *real,
	struct packet *packet
) {
	if ((vs_flags & BALANCER_VS_FIX_MSS_FLAG) &&
	    (vs_flags & BALANCER_VS_IPV6_FLAG)) {
		fix_mss_ipv6(packet);
	}

	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv4_hdr *ipv4_header = NULL;
	struct rte_ipv6_hdr *ipv6_header = NULL;
	if (vs_flags & BALANCER_VS_IPV6_FLAG) {
		ipv6_header = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv6_hdr *,
			packet->network_header.offset
		);
	} else {
		ipv4_header = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr *,
			packet->network_header.offset
		);
	}

	if (real->flags & BALANCER_REAL_IPV6_FLAG) { // IPv6
		// rs->src_addr is already masked.

		uint8_t src[NET6_LEN];
		memcpy(src, real->src_addr, NET6_LEN);
		uint8_t len = (ipv4_header != NULL ? NET4_LEN : NET6_LEN);
		uint8_t *src_user =
			(ipv4_header != NULL ? (uint8_t *)&ipv4_header->src_addr
					     : ipv6_header->src_addr);
		for (uint8_t i = 0; i < len; i++) {
			src[i] |= src_user[i] & (~real->src_mask[i]);
		}

		if (vs_flags & BALANCER_VS_GRE_FLAG) {
			/// @todo: support GRE
		}

		return packet_ip6_encap(packet, real->dst_addr, src);
	} else { // IPv4
		// rs->src_addr is already masked.

		uint32_t src_mask = *(uint32_t *)(real->src_mask);
		uint32_t src_addr = *(uint32_t *)(real->src_addr);
		uint32_t src_user =
			(ipv4_header != NULL)
				? ipv4_header->src_addr
				: *(uint32_t *)ipv6_header->src_addr;
		uint32_t src = (src_user & ~src_mask) | src_addr;

		if (vs_flags & BALANCER_VS_GRE_FLAG) {
			/// @todo: support GRE
		}

		return packet_ip4_encap(
			packet, real->dst_addr, (uint8_t *)(&src)
		);
	}
}