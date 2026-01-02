#pragma once

#include "declare.h"
#include "filter/classifiers/proto.h"
#include "lib/dataplane/packet/packet.h"

#include <netinet/in.h>
#include <rte_mbuf.h>
#include <rte_tcp.h>

#include <stdint.h>

static inline void
FILTER_ATTR_QUERY_FUNC(proto)(
	void *data, struct packet **packets, uint32_t *result, uint32_t count
) {
	struct proto_classifier *c = (struct proto_classifier *)data;

	for (uint32_t idx = 0; idx < count; ++idx) {
		if (packets[idx]->transport_header.type == IPPROTO_UDP) {
			result[idx] = c->max_tcp_class + 1;
		} else if (packets[idx]->transport_header.type ==
			   IPPROTO_ICMP) {
			result[idx] = c->max_tcp_class + 2;
		} else { // TCP
			struct rte_tcp_hdr *tcp_header =
				rte_pktmbuf_mtod_offset(
					packet_to_mbuf(packets[idx]),
					struct rte_tcp_hdr *,
					packets[idx]->transport_header.offset
				);
			result[idx] = value_table_get(
				&c->tcp_flags, 0, tcp_header->tcp_flags
			);
		}
	}
}
