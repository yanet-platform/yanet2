#pragma once

#include "../classifiers/proto_range.h"
#include "declare.h"
#include "lib/dataplane/packet/packet.h"

#include <stdint.h>

#include <rte_icmp.h>
#include <rte_tcp.h>

struct filter_query_attr_proto_range_handlers {
	struct filter_query_attr_handlers attr_handlers;
};

static inline uint16_t
filter_packet_get_proto_range(const struct packet *packet) {
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
	return proto;
}

static inline void
filter_query_attr_proto_range_lookup(
	const struct filter_query_attr *attr,
	const struct filter_query_attr_handlers *attr_handlers,
	const struct packet **packets,
	uint32_t *results,
	uint32_t packet_count
) {
	(void)attr_handlers;

	const struct filter_query_attr_proto_range *proto_range_attr =
		container_of(attr, struct filter_query_attr_proto_range, attr);

	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const uint16_t proto_range =
			filter_packet_get_proto_range(packets[idx]);
		results[idx] = *value_table_get_ptr(
			&proto_range_attr->value_table, 0, proto_range
		);
	}
}

static const struct filter_query_attr_handlers filter_query_proto_range = {
	.lookup = filter_query_attr_proto_range_lookup,
};

static const struct filter_query_attr_proto_range_handlers
	filter_query_attr_proto_range = {
		.attr_handlers = filter_query_proto_range,
};
