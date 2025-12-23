#pragma once

#include "declare.h"
#include "filter/classifiers/proto.h"
#include "lib/dataplane/packet/packet.h"

#include <netinet/in.h>
#include <rte_mbuf.h>
#include <rte_tcp.h>

#include <stdint.h>

static inline uint32_t
FILTER_ATTR_QUERY_FUNC(proto)(struct packet *packet, void *data) {
	struct proto_classifier *c = (struct proto_classifier *)data;

	if (packet->transport_header.type == IPPROTO_UDP) {
		return c->max_tcp_class + 1;
	} else if (packet->transport_header.type == IPPROTO_ICMP) {
		return c->max_tcp_class + 2;
	} else { // TCP
		struct rte_tcp_hdr *tcp_header = rte_pktmbuf_mtod_offset(
			packet_to_mbuf(packet),
			struct rte_tcp_hdr *,
			packet->transport_header.offset
		);
		return value_table_get(&c->tcp_flags, 0, tcp_header->tcp_flags);
	}
}