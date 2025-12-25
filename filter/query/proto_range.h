#pragma once

#include "../classifiers/proto_range.h"
#include "declare.h"
#include "lib/dataplane/packet/packet.h"

#include <stdint.h>

#include <rte_icmp.h>
#include <rte_tcp.h>

static inline uint32_t
FILTER_ATTR_QUERY_FUNC(proto_range)(struct packet *packet, void *data) {
	struct proto_range_classifier *c =
		(struct proto_range_classifier *)data;
	uint16_t proto = packet->transport_header.type * 256;
	if (packet->transport_header.type == IPPROTO_TCP) {
		struct rte_tcp_hdr *tcp_header = rte_pktmbuf_mtod_offset(
			packet_to_mbuf(packet),
			struct rte_tcp_hdr *,
			packet->transport_header.offset
		);
		proto += tcp_header->tcp_flags;
	}
	if (packet->transport_header.type == IPPROTO_ICMP) {
		struct rte_icmp_hdr *icmp_header = rte_pktmbuf_mtod_offset(
			packet_to_mbuf(packet),
			struct rte_icmp_hdr *,
			packet->transport_header.offset
		);
		proto += icmp_header->icmp_type;
	}
	if (packet->transport_header.type == IPPROTO_ICMPV6) {
		struct rte_icmp_hdr *icmp_header = rte_pktmbuf_mtod_offset(
			packet_to_mbuf(packet),
			struct rte_icmp_hdr *,
			packet->transport_header.offset
		);
		proto += icmp_header->icmp_type;
	}

	return value_table_get(&c->table, 0, proto);
}
