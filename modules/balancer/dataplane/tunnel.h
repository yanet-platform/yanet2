#pragma once

#include "handler/vs.h"

#include "dataplane/packet/packet.h"
#include "lib/dataplane/packet/encap.h"
#include "mss.h"
#include "rte_gre.h"
#include "rte_ip.h"

#include <assert.h>

////////////////////////////////////////////////////////////////////////////////

static inline void
tunnel_packet(struct vs *vs, struct real *real, struct packet *packet) {
	int vs_ip_proto = vs->identifier.ip_proto;
	uint8_t vs_flags = vs->flags;

	// fix packet MSS if flag is specified and vs is IPv6
	if ((vs_flags & VS_FIX_MSS_FLAG) && (vs_ip_proto == IPPROTO_IPV6)) {
		fix_mss_ipv6(packet);
	}

	// encapsulate packet

	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv6_hdr *ipv6_header_inner = NULL;
	struct rte_ipv4_hdr *ipv4_header_inner = NULL;
	if (vs_ip_proto == IPPROTO_IPV6) {
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

	const int real_ipv6 = real->identifier.ip_proto == IPPROTO_IPV6 ? 1 : 0;

	if (real_ipv6) { // IPv6
		// rs->src_addr is already masked.

		const struct net6 *n6 = &real->src.v6;

		uint8_t src[NET6_LEN];
		memcpy(src, n6->addr, NET6_LEN);
		uint8_t len = (ipv4_header_inner != NULL ? NET4_LEN : NET6_LEN);
		uint8_t *src_user =
			(ipv4_header_inner != NULL
				 ? (uint8_t *)&ipv4_header_inner->src_addr
				 : ipv6_header_inner->src_addr);
		for (uint8_t i = 0; i < len; i++) {
			src[i] |= src_user[i] & (~n6->mask[i]);
		}

		packet_ip6_encap(packet, real->identifier.addr.v6.bytes, src);
	} else { // IPv4
		// rs->src_addr is already masked.
		const struct net4 *n4 = &real->src.v4;
		uint8_t src[4];
		uint8_t *src_user =
			(ipv4_header_inner != NULL)
				? (uint8_t *)&ipv4_header_inner->src_addr
				: ipv6_header_inner->src_addr;
		for (size_t i = 0; i < 4; ++i) {
			src[i] = (src_user[i] & ~n4->mask[i]) | n4->addr[i];
		}

		packet_ip4_encap(
			packet,
			real->identifier.addr.v4.bytes,
			(uint8_t *)(&src)
		);
	}

	// use GRE for encap
	if (vs_flags & VS_GRE_FLAG) {
		const uint16_t gre_hdr_size = sizeof(struct rte_gre_hdr);

		if (rte_pktmbuf_prepend(mbuf, gre_hdr_size) == NULL) {
			// not enough headroom to insert GRE
			assert(false);
		}

		const uint16_t len_before_gre =
			packet->network_header.offset +
			(real_ipv6 ? sizeof(struct rte_ipv6_hdr)
				   : sizeof(struct rte_ipv4_hdr));

		// move L2 + outer L3 back to head to open a gap right after
		// outer L3
		memmove(rte_pktmbuf_mtod(mbuf, char *),
			rte_pktmbuf_mtod_offset(mbuf, char *, gre_hdr_size),
			len_before_gre);

		if (real_ipv6) {
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
}
