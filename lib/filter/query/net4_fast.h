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
FILTER_ATTR_QUERY_FUNC(net4_fast_dst)(
	void *data, struct packet **packets, uint32_t *result, uint32_t count
) {
	struct segments_u32_classifier *classifier =
		(struct segments_u32_classifier *)data;
	uint32_t addrs[segments_u32_classifier_max_batch_size];
	while (count > 0) {
		size_t cur_count = count;
		if (cur_count > segments_u32_classifier_max_batch_size) {
			cur_count = segments_u32_classifier_max_batch_size;
		}
		for (size_t i = 0; i < cur_count; ++i) {
			struct rte_mbuf *mbuf = packet_to_mbuf(packets[i]);
			struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
				mbuf,
				struct rte_ipv4_hdr *,
				packets[i]->network_header.offset
			);
			addrs[i] = rte_be_to_cpu_32(ipv4_hdr->dst_addr);
		}
		cur_count = segments_u32_classify(
			classifier, cur_count, addrs, result
		);
		count -= cur_count;
		result += cur_count;
		packets += cur_count;
	}
}

static inline void
FILTER_ATTR_QUERY_FUNC(net4_fast_src)(
	void *data, struct packet **packets, uint32_t *result, uint32_t count
) {
	struct segments_u32_classifier *classifier =
		(struct segments_u32_classifier *)data;
	uint32_t addrs[segments_u32_classifier_max_batch_size];
	while (count > 0) {
		size_t cur_count = count;
		if (cur_count > segments_u32_classifier_max_batch_size) {
			cur_count = segments_u32_classifier_max_batch_size;
		}
		for (size_t i = 0; i < cur_count; ++i) {
			struct rte_mbuf *mbuf = packet_to_mbuf(packets[i]);
			struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
				mbuf,
				struct rte_ipv4_hdr *,
				packets[i]->network_header.offset
			);
			addrs[i] = rte_be_to_cpu_32(ipv4_hdr->src_addr);
		}
		cur_count = segments_u32_classify(
			classifier, cur_count, addrs, result
		);
		count -= cur_count;
		result += cur_count;
		packets += cur_count;
	}
}
