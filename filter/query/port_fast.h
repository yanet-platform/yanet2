#pragma once

#include "classifiers/segments.h"
#include "lib/dataplane/packet/packet.h"

#include <netinet/in.h>
#include <rte_ip.h>
#include <rte_mbuf.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include <stdint.h>

#include "declare.h"

#include "segments.h"

static inline void
FILTER_ATTR_QUERY_FUNC(port_fast_dst)(
	void *data, struct packet **packets, uint32_t *result, uint32_t count
) {
	struct segment_u16_classifier *classifier =
		(struct segment_u16_classifier *)data;
	uint16_t ports[segment_u16_classifier_max_batch_size];
	while (count > 0) {
		size_t cur_count = count;
		if (cur_count > segment_u16_classifier_max_batch_size) {
			cur_count = segment_u16_classifier_max_batch_size;
		}
		for (size_t packet_idx = 0; packet_idx < cur_count;
		     ++packet_idx) {
			struct packet *packet = packets[packet_idx];
			struct rte_mbuf *mbuf = packet_to_mbuf(packet);
			if (packet->transport_header.type == IPPROTO_TCP) {
				struct rte_tcp_hdr *tcp_hdr =
					rte_pktmbuf_mtod_offset(
						mbuf,
						struct rte_tcp_hdr *,
						packet->transport_header.offset
					);
				ports[packet_idx] =
					rte_be_to_cpu_16(tcp_hdr->dst_port);
			} else if (packet->transport_header.type ==
				   IPPROTO_UDP) {
				struct rte_udp_hdr *udp_hdr =
					rte_pktmbuf_mtod_offset(
						mbuf,
						struct rte_udp_hdr *,
						packet->transport_header.offset
					);
				ports[packet_idx] =
					rte_be_to_cpu_16(udp_hdr->dst_port);
			}
		}
		cur_count = segment_u16_classify(
			classifier, cur_count, ports, result
		);
		count -= cur_count;
		result += cur_count;
		packets += cur_count;
	}
}

static inline void
FILTER_ATTR_QUERY_FUNC(port_fast_src)(
	void *data, struct packet **packets, uint32_t *result, uint32_t count
) {
	struct segment_u16_classifier *classifier =
		(struct segment_u16_classifier *)data;
	uint16_t ports[segment_u16_classifier_max_batch_size];
	while (count > 0) {
		size_t cur_count = count;
		if (cur_count > segment_u16_classifier_max_batch_size) {
			cur_count = segment_u16_classifier_max_batch_size;
		}
		for (size_t packet_idx = 0; packet_idx < cur_count;
		     ++packet_idx) {
			struct packet *packet = packets[packet_idx];
			struct rte_mbuf *mbuf = packet_to_mbuf(packet);
			if (packet->transport_header.type == IPPROTO_TCP) {
				struct rte_tcp_hdr *tcp_hdr =
					rte_pktmbuf_mtod_offset(
						mbuf,
						struct rte_tcp_hdr *,
						packet->transport_header.offset
					);
				ports[packet_idx] =
					rte_be_to_cpu_16(tcp_hdr->src_port);
			} else if (packet->transport_header.type ==
				   IPPROTO_UDP) {
				struct rte_udp_hdr *udp_hdr =
					rte_pktmbuf_mtod_offset(
						mbuf,
						struct rte_udp_hdr *,
						packet->transport_header.offset
					);
				ports[packet_idx] =
					rte_be_to_cpu_16(udp_hdr->src_port);
			}
		}
		cur_count = segment_u16_classify(
			classifier, cur_count, ports, result
		);
		count -= cur_count;
		result += cur_count;
		packets += cur_count;
	}
}