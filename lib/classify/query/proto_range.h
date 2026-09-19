#pragma once

#include "../classifiers/proto_range.h"
#include "declare.h"
#include "lib/dataplane/packet/packet.h"

#include <stdint.h>

#include <rte_icmp.h>
#include <rte_tcp.h>

struct classify_query_attr_proto_range_handlers {
	struct classify_query_attr_handlers attr_handlers;
};

static inline uint16_t
filter_packet_get_proto_range(const struct packet *packet) {
	uint16_t proto = packet->transport_header.type * 256;
	if (packet->transport_header.type == IPPROTO_TCP) {
		// The parser validates the transport header against the
		// whole packet length, so a chained packet can carry it
		// past the head segment; the flag and type bytes are read
		// only when the header fits the head segment.
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		if (rte_pktmbuf_data_len(mbuf) >=
		    packet->transport_header.offset +
			    sizeof(struct rte_tcp_hdr)) {
			struct rte_tcp_hdr *tcp_header =
				rte_pktmbuf_mtod_offset(
					mbuf,
					struct rte_tcp_hdr *,
					packet->transport_header.offset
				);
			proto += tcp_header->tcp_flags;
		}
	}
	if (packet->transport_header.type == IPPROTO_ICMP ||
	    packet->transport_header.type == IPPROTO_ICMPV6) {
		// Only the leading type byte of the message is classified;
		// a bare echo header carries it without the rest of the
		// full struct.
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		if (rte_pktmbuf_data_len(mbuf) >
		    packet->transport_header.offset) {
			struct rte_icmp_hdr *icmp_header =
				rte_pktmbuf_mtod_offset(
					mbuf,
					struct rte_icmp_hdr *,
					packet->transport_header.offset
				);
			proto += icmp_header->icmp_type;
		}
	}
	return proto;
}

static inline void
classify_query_attr_proto_range_lookup(
	const struct classify_query_attr *attr,
	const struct classify_query_attr_handlers *attr_handlers,
	const struct packet **packets,
	uint32_t *results,
	uint32_t packet_count
) {
	(void)attr_handlers;

	const struct classify_query_attr_proto_range *proto_range_attr =
		container_of(
			attr, struct classify_query_attr_proto_range, attr
		);

	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const uint16_t proto_range =
			filter_packet_get_proto_range(packets[idx]);
		results[idx] = vline_get(
			(struct vline *)&proto_range_attr->line, proto_range
		);
	}
}

static const struct classify_query_attr_handlers filter_query_proto_range = {
	.lookup = classify_query_attr_proto_range_lookup,
};

static const struct classify_query_attr_proto_range_handlers
	classify_query_attr_proto_range = {
		.attr_handlers = filter_query_proto_range,
};
