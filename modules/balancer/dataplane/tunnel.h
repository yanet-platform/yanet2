#pragma once

#include "dataplane/packet/packet.h"
#include "lib/dataplane/packet/encap.h"
#include "mss.h"
#include "real.h"
#include "rte_gre.h"
#include "rte_ip.h"
#include "vs.h"

#include "../api/vs.h"
#include <assert.h>

////////////////////////////////////////////////////////////////////////////////

static inline int
tunnel_packet(vs_flags_t vs_flags, struct real *real, struct packet *packet) {
	// fix packet MSS if flag is specified and vs is IPv6
	if ((vs_flags & BALANCER_VS_FIX_MSS_FLAG) &&
	    (vs_flags & BALANCER_VS_IPV6_FLAG)) {
		fix_mss_ipv6(packet);
	}

	// encapsulate packet

	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv6_hdr *ipv6_header_inner = NULL;
	struct rte_ipv4_hdr *ipv4_header_inner = NULL;
	if (vs_flags & BALANCER_VS_IPV6_FLAG) {
		ipv6_header_inner = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv6_hdr *,
			packet->network_header.offset
		);
	} else {
		ipv4_header_inner = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr *,
			packet->network_header.offset
		);
	}

	if (real->flags & BALANCER_REAL_IPV6_FLAG) { // IPv6
		// rs->src_addr is already masked.

		uint8_t src[NET6_LEN];
		memcpy(src, real->src_addr, NET6_LEN);
		uint8_t len = (ipv4_header_inner != NULL ? NET4_LEN : NET6_LEN);
		uint8_t *src_user =
			(ipv4_header_inner != NULL
				 ? (uint8_t *)&ipv4_header_inner->src_addr
				 : ipv6_header_inner->src_addr);
		for (uint8_t i = 0; i < len; i++) {
			src[i] |= src_user[i] & (~real->src_mask[i]);
		}

		packet_ip6_encap(packet, real->dst_addr, src);
	} else { // IPv4
		// rs->src_addr is already masked.
		uint8_t src[4];
		uint8_t *src_user =
			(ipv4_header_inner != NULL)
				? (uint8_t *)&ipv4_header_inner->src_addr
				: ipv6_header_inner->src_addr;
		for (size_t i = 0; i < 4; ++i) {
			src[i] = (src_user[i] & ~real->src_mask[i]) |
				 real->src_addr[i];
		}

		packet_ip4_encap(packet, real->dst_addr, (uint8_t *)(&src));
	}

	// use GRE for encap
	if (vs_flags & BALANCER_VS_GRE_FLAG) {
		const uint16_t gre_hdr_size = sizeof(struct rte_gre_hdr);

		if (rte_pktmbuf_prepend(mbuf, gre_hdr_size) == NULL) {
			// not enough headroom to insert GRE
			return -1;
		}

		const uint16_t len_before_gre =
			packet->network_header.offset +
			((real->flags & BALANCER_REAL_IPV6_FLAG)
				 ? sizeof(struct rte_ipv6_hdr)
				 : sizeof(struct rte_ipv4_hdr));

		// move L2 + outer L3 back to head to open a gap right after
		// outer L3
		memmove(rte_pktmbuf_mtod(mbuf, char *),
			rte_pktmbuf_mtod_offset(mbuf, char *, gre_hdr_size),
			len_before_gre);

		if (real->flags & BALANCER_REAL_IPV6_FLAG) {
			struct rte_ipv6_hdr *ipv6_header =
				rte_pktmbuf_mtod_offset(
					mbuf,
					struct rte_ipv6_hdr *,
					packet->network_header.offset
				);
			ipv6_header->proto = IPPROTO_GRE;
			ipv6_header->payload_len = rte_cpu_to_be_16(
				rte_be_to_cpu_16(ipv6_header->payload_len) +
				gre_hdr_size
			);
		} else {
			struct rte_ipv4_hdr *ipv4_header =
				rte_pktmbuf_mtod_offset(
					mbuf,
					struct rte_ipv4_hdr *,
					packet->network_header.offset
				);
			ipv4_header->next_proto_id = IPPROTO_GRE;
			ipv4_header->total_length = rte_cpu_to_be_16(
				rte_be_to_cpu_16(ipv4_header->total_length) +
				gre_hdr_size
			);

			ipv4_header->hdr_checksum = 0;
			ipv4_header->hdr_checksum = rte_ipv4_cksum(ipv4_header);
		}

		// place GRE header in the created gap (right after outer L3)
		struct rte_gre_hdr *gre_header = rte_pktmbuf_mtod_offset(
			mbuf, struct rte_gre_hdr *, len_before_gre
		);
		memset(gre_header, 0, sizeof(struct rte_gre_hdr));
		gre_header->ver = 0; // default version
		gre_header->proto = rte_cpu_to_be_16(
			ipv4_header_inner != NULL ? RTE_ETHER_TYPE_IPV4
						  : RTE_ETHER_TYPE_IPV6
		);

		// advance transport offset past GRE header (inner transport
		// shifts forward)
		packet->transport_header.offset += gre_hdr_size;
	}

	return 0;
}