#pragma once

#include "common/network.h"
#include "dataplane/packet/packet.h"
#include "rte_byteorder.h"
#include "rte_ether.h"
#include <netinet/in.h>
#include <stdint.h>

#include <rte_ip.h>
#include <rte_tcp.h>
#include <rte_udp.h>

////////////////////////////////////////////////////////////////////////////////

struct packet_metadata {
	uint8_t network_proto;
	uint8_t transport_proto;

	uint8_t src_addr[16];
	uint8_t dst_addr[16];
	uint16_t src_port;
	uint16_t dst_port;

	uint8_t tcp_flags;

	uint64_t hash;
	size_t len;
};

////////////////////////////////////////////////////////////////////////////////

static inline int
fill_packet_metadata(struct packet *packet, struct packet_metadata *metadata) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	if (packet->network_header.type ==
	    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		metadata->network_proto = IPPROTO_IP;
		struct rte_ipv4_hdr *ipv4_header = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr *,
			packet->network_header.offset
		);

		memcpy(metadata->dst_addr,
		       (uint8_t *)&ipv4_header->dst_addr,
		       NET4_LEN);
		memcpy(metadata->src_addr,
		       (uint8_t *)&ipv4_header->src_addr,
		       NET4_LEN);
	} else if (packet->network_header.type ==
		   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
		metadata->network_proto = IPPROTO_IPV6;
		struct rte_ipv6_hdr *ipv6_header = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv6_hdr *,
			packet->network_header.offset
		);

		memcpy(metadata->dst_addr, ipv6_header->dst_addr, NET6_LEN);
		memcpy(metadata->src_addr, ipv6_header->src_addr, NET6_LEN);
	} else { // unsupported
		return -1;
	}

	if (packet->transport_header.type == IPPROTO_TCP) {
		metadata->transport_proto = IPPROTO_TCP;
		struct rte_tcp_hdr *tcp_header = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_tcp_hdr *,
			packet->transport_header.offset
		);

		metadata->dst_port = tcp_header->dst_port;
		metadata->src_port = tcp_header->src_port;
		metadata->tcp_flags = tcp_header->tcp_flags;
	} else if (packet->transport_header.type == IPPROTO_UDP) {
		metadata->transport_proto = IPPROTO_UDP;
		struct rte_udp_hdr *udp_header = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_udp_hdr *,
			packet->transport_header.offset
		);

		metadata->dst_port = udp_header->dst_port;
		metadata->src_port = udp_header->src_port;
		metadata->tcp_flags = 0;
	} else { // unsupported
		return -1;
	}

	metadata->hash = packet->hash;

	return 0;
}