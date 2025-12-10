#pragma once

#include "rte_ether.h"
#include "rte_ip.h"

#include "common/network.h"

#include <stdint.h>
#include <string.h>

#include "lib/dataplane/packet/packet.h"

////////////////////////////////////////////////////////////////////////////////

// Tunnel packet from this balancer (src address) to another (dst address)

static inline void
fix_ether_header(struct rte_mbuf *mbuf, uint16_t ether_type) {
	struct rte_ether_hdr *ether_header =
		rte_pktmbuf_mtod(mbuf, struct rte_ether_hdr *);

	// setup ether type
	if (ether_header->ether_type == rte_cpu_to_be_16(RTE_ETHER_TYPE_VLAN)) {
		struct rte_vlan_hdr *vlan_header = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_vlan_hdr *,
			sizeof(struct rte_ether_hdr)
		);
		vlan_header->eth_proto = rte_cpu_to_be_16(ether_type);
	} else {
		ether_header->ether_type = rte_cpu_to_be_16(ether_type);
	}
}

////////////////////////////////////////////////////////////////////////////////

static inline void
tunnel_v4(struct packet *packet, uint8_t *src, uint8_t *dst) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	// insert ipv4 header
	rte_pktmbuf_prepend(mbuf, sizeof(struct rte_ipv4_hdr));
	memmove(rte_pktmbuf_mtod(mbuf, char *),
		rte_pktmbuf_mtod_offset(
			mbuf, char *, sizeof(struct rte_ipv4_hdr)
		),
		packet->network_header.offset);

	struct rte_ipv4_hdr *outer_ip_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);

	memcpy(&outer_ip_hdr->src_addr, src, NET4_LEN);
	memcpy(&outer_ip_hdr->dst_addr, dst, NET4_LEN);

	outer_ip_hdr->version_ihl = 0x45;
	outer_ip_hdr->type_of_service = 0x00;
	outer_ip_hdr->packet_id = rte_cpu_to_be_16(0x01);
	outer_ip_hdr->fragment_offset = 0;
	outer_ip_hdr->time_to_live = 64;

	outer_ip_hdr->total_length = rte_cpu_to_be_16(
		(uint16_t)(mbuf->pkt_len - packet->network_header.offset)
	);

	if (packet->network_header.type ==
	    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		outer_ip_hdr->next_proto_id = IPPROTO_IPIP;
	} else {
		outer_ip_hdr->next_proto_id = IPPROTO_IPV6;
	}

	outer_ip_hdr->hdr_checksum = 0;
	outer_ip_hdr->hdr_checksum = rte_ipv4_cksum(outer_ip_hdr); ///< @todo

	// might need to change next protocol type in ethernet/vlan header in
	// cloned packet

	fix_ether_header(mbuf, RTE_ETHER_TYPE_IPV4);

	// Update mbuf metadata for the new outer IP header
	mbuf->l3_len = sizeof(struct rte_ipv4_hdr);
}

////////////////////////////////////////////////////////////////////////////////

static inline void
tunnel_v6(struct packet *packet, uint8_t *src, uint8_t *dst) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	// insert ipv6 header

	rte_pktmbuf_prepend(mbuf, sizeof(struct rte_ipv6_hdr));
	memmove(rte_pktmbuf_mtod(mbuf, char *),
		rte_pktmbuf_mtod_offset(
			mbuf, char *, sizeof(struct rte_ipv6_hdr)
		),
		packet->network_header.offset);

	struct rte_ipv6_hdr *outer_ip_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);

	memcpy(&outer_ip_hdr->src_addr, src, NET6_LEN);
	memcpy(&outer_ip_hdr->dst_addr, dst, NET6_LEN);

	// todo: randomize src address

	outer_ip_hdr->vtc_flow = rte_cpu_to_be_32((0x6 << 28));
	outer_ip_hdr->payload_len =
		rte_cpu_to_be_16((uint16_t)(mbuf->pkt_len -
					    packet->network_header.offset -
					    sizeof(struct rte_ipv6_hdr)));
	outer_ip_hdr->hop_limits = 64;

	if (packet->network_header.type ==
	    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		outer_ip_hdr->proto = IPPROTO_IPIP;
	} else {
		outer_ip_hdr->proto = IPPROTO_IPV6;
	}

	fix_ether_header(mbuf, RTE_ETHER_TYPE_IPV6);

	// Update mbuf metadata for the new outer IP header
	mbuf->l3_len = sizeof(struct rte_ipv6_hdr);
}