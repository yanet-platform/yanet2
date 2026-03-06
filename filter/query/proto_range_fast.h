#pragma once

#include "../classifiers/proto_range.h"
#include "declare.h"
#include "lib/dataplane/packet/packet.h"
#include "segments.h"

#include <stdint.h>

#include <netinet/in.h>
#include <rte_icmp.h>
#include <rte_mbuf.h>
#include <rte_tcp.h>

static inline void
FILTER_ATTR_QUERY_FUNC(proto_range_fast)(
	void *data, struct packet **packets, uint32_t *result, uint32_t count
) {
	struct proto_range_fast_classifier *c =
		(struct proto_range_fast_classifier *)data;

	uint16_t protos[segment_u16_classifier_max_batch_size];
	while (count > 0) {
		size_t cur_count = count;
		if (cur_count > segment_u16_classifier_max_batch_size) {
			cur_count = segment_u16_classifier_max_batch_size;
		}

		for (size_t idx = 0; idx < cur_count; ++idx) {
			struct packet *packet = packets[idx];

			uint16_t proto = packet->transport_header.type * 256;
			if (packet->transport_header.type == IPPROTO_TCP) {
				struct rte_tcp_hdr *tcp_header =
					rte_pktmbuf_mtod_offset(
						packet_to_mbuf(packet),
						struct rte_tcp_hdr *,
						packet->transport_header.offset
					);
				proto += tcp_header->tcp_flags;
			}
			if (packet->transport_header.type == IPPROTO_ICMP) {
				struct rte_icmp_hdr *icmp_header =
					rte_pktmbuf_mtod_offset(
						packet_to_mbuf(packet),
						struct rte_icmp_hdr *,
						packet->transport_header.offset
					);
				proto += icmp_header->icmp_type;
			}
			if (packet->transport_header.type == IPPROTO_ICMPV6) {
				struct rte_icmp_hdr *icmp_header =
					rte_pktmbuf_mtod_offset(
						packet_to_mbuf(packet),
						struct rte_icmp_hdr *,
						packet->transport_header.offset
					);
				proto += icmp_header->icmp_type;
			}

			protos[idx] = proto;
		}

		cur_count = segment_u16_classify(
			&c->classifier, cur_count, protos, result
		);
		count -= cur_count;
		result += cur_count;
		packets += cur_count;
	}
}