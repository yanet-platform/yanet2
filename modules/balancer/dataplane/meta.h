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
#include <string.h>

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
};

////////////////////////////////////////////////////////////////////////////////

static inline void
fill_packet_metadata_ipv4(
	struct rte_ipv4_hdr *ip_hdr, struct packet_metadata *metadata
) {
	memcpy(metadata->dst_addr, (uint8_t *)&ip_hdr->dst_addr, NET4_LEN);
	memcpy(metadata->src_addr, (uint8_t *)&ip_hdr->src_addr, NET4_LEN);
}

static inline void
fill_packet_metadata_ipv6(
	struct rte_ipv6_hdr *ip_hdr, struct packet_metadata *metadata
) {
	metadata->network_proto = IPPROTO_IPV6;
	memcpy(metadata->dst_addr, ip_hdr->dst_addr, NET6_LEN);
	memcpy(metadata->src_addr, ip_hdr->src_addr, NET6_LEN);
}

////////////////////////////////////////////////////////////////////////////////

static inline void
fill_packet_metadata_tcp(
	struct rte_tcp_hdr *tcp_header, struct packet_metadata *metadata
) {
	metadata->transport_proto = IPPROTO_TCP;
	metadata->dst_port = tcp_header->dst_port;
	metadata->src_port = tcp_header->src_port;
	metadata->tcp_flags = tcp_header->tcp_flags;
}

static inline void
fill_packet_metadata_udp(
	struct rte_udp_hdr *udp_header, struct packet_metadata *metadata
) {
	metadata->transport_proto = IPPROTO_UDP;
	metadata->dst_port = udp_header->dst_port;
	metadata->src_port = udp_header->src_port;
	metadata->tcp_flags = 0;
}

////////////////////////////////////////////////////////////////////////////////

static inline int
fill_packet_metadata(struct packet *packet, struct packet_metadata *metadata) {
	memset(metadata, 0, sizeof(struct packet_metadata));

	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	if (packet->network_header.type ==
	    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		struct rte_ipv4_hdr *ipv4_header = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr *,
			packet->network_header.offset
		);
		fill_packet_metadata_ipv4(ipv4_header, metadata);
	} else if (packet->network_header.type ==
		   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
		struct rte_ipv6_hdr *ipv6_header = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv6_hdr *,
			packet->network_header.offset
		);
		fill_packet_metadata_ipv6(ipv6_header, metadata);
	} else { // unsupported
		return -1;
	}

	if (packet->transport_header.type == IPPROTO_TCP) {
		struct rte_tcp_hdr *tcp_header = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_tcp_hdr *,
			packet->transport_header.offset
		);
		fill_packet_metadata_tcp(tcp_header, metadata);
	} else if (packet->transport_header.type == IPPROTO_UDP) {
		struct rte_udp_hdr *udp_header = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_udp_hdr *,
			packet->transport_header.offset
		);
		fill_packet_metadata_udp(udp_header, metadata);
	} else { // unsupported
		return -1;
	}

	metadata->hash = packet->hash;

	return 0;
}