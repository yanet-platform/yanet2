#pragma once

#include "common/value.h"
#include "lib/dataplane/packet/packet.h"

#include <netinet/in.h>
#include <rte_ip.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include <stdint.h>

#include "declare.h"

static inline uint16_t
packet_src_port(const struct packet *packet) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	if (packet->transport_header.type == IPPROTO_TCP) {
		struct rte_tcp_hdr *tcp_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_tcp_hdr *,
			packet->transport_header.offset
		);
		return rte_be_to_cpu_16(tcp_hdr->src_port);
	} else if (packet->transport_header.type == IPPROTO_UDP) {
		struct rte_udp_hdr *udp_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_udp_hdr *,
			packet->transport_header.offset
		);
		return rte_be_to_cpu_16(udp_hdr->src_port);
	} else {
		// non tcp/udp
		return 0;
	}
}

static inline uint16_t
packet_dst_port(const struct packet *packet) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	if (packet->transport_header.type == IPPROTO_TCP) {
		struct rte_tcp_hdr *tcp_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_tcp_hdr *,
			packet->transport_header.offset
		);
		return rte_be_to_cpu_16(tcp_hdr->dst_port);
	} else if (packet->transport_header.type == IPPROTO_UDP) {
		struct rte_udp_hdr *udp_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_udp_hdr *,
			packet->transport_header.offset
		);
		return rte_be_to_cpu_16(udp_hdr->dst_port);
	} else {
		// non tcp/udp
		return 0;
	}
}

////////////////////////////////////////////////////////////////////////////////

static inline void
FILTER_ATTR_QUERY_FUNC(port_src)(
	void *data, struct packet **packets, uint32_t *result, uint32_t count
) {
	struct value_table *table = (struct value_table *)data;

	for (uint32_t idx = 0; idx < count; ++idx) {
		result[idx] = value_table_get(
			table, 0, packet_src_port(packets[idx])
		);
	}
}

static inline void
FILTER_ATTR_QUERY_FUNC(port_dst)(
	void *data, struct packet **packets, uint32_t *result, uint32_t count
) {
	struct value_table *table = (struct value_table *)data;

	for (uint32_t idx = 0; idx < count; ++idx) {
		result[idx] = value_table_get(
			table, 0, packet_dst_port(packets[idx])
		);
	}
}
