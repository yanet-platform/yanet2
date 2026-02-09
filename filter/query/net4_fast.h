#pragma once

#include "classifiers/net4_fast.h"
#include "common/btree/u32.h"
#include "common/memory_address.h"
#include "lib/dataplane/packet/packet.h"

#include <netinet/in.h>
#include <rte_ip.h>
#include <rte_mbuf.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include <stdint.h>
#include <stdio.h>

#include "declare.h"

static inline void
FILTER_ATTR_QUERY_FUNC(net4_fast_dst)(
	void *data, struct packet **packets, uint32_t *result, uint32_t count
) {
	struct net4_fast_classifier *classifier =
		(struct net4_fast_classifier *)data;
	uint32_t *to = ADDR_OF(&classifier->to);
	uint32_t values[btree_u32_max_batch_size];
	while (count >= btree_u32_max_batch_size) {
		for (size_t i = 0; i < btree_u32_max_batch_size; ++i) {
			struct rte_mbuf *mbuf = packet_to_mbuf(packets[i]);
			struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
				mbuf,
				struct rte_ipv4_hdr *,
				packets[i]->network_header.offset
			);
			values[i] = rte_be_to_cpu_32(ipv4_hdr->dst_addr) + 1;
		}
		btree_u32_lower_bounds(
			&classifier->btree,
			values,
			btree_u32_max_batch_size,
			result
		);
		for (size_t i = 0; i < btree_u32_max_batch_size; ++i) {
			if (unlikely(
				    result[i] == 0 ||
				    to[result[i] - 1] < values[i] - 1
			    )) {
				result[i] = classifier->btree.n;
			} else {
				--result[i];
			}
		}
		result += btree_u32_max_batch_size;
		packets += btree_u32_max_batch_size;
		count -= btree_u32_max_batch_size;
	}
	count %= btree_u32_max_batch_size;
	for (size_t i = 0; i < count; ++i) {
		struct rte_mbuf *mbuf = packet_to_mbuf(packets[i]);
		struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr *,
			packets[i]->network_header.offset
		);
		values[i] = rte_be_to_cpu_32(ipv4_hdr->dst_addr) + 1;
	}
	btree_u32_lower_bounds(&classifier->btree, values, count, result);
	for (size_t i = 0; i < count; ++i) {
		if (unlikely(
			    result[i] == 0 || to[result[i] - 1] < values[i] - 1
		    )) {
			result[i] = classifier->btree.n;
		} else {
			--result[i];
		}
	}
}

static inline void
FILTER_ATTR_QUERY_FUNC(net4_fast_src)(
	void *data, struct packet **packets, uint32_t *result, uint32_t count
) {
	struct net4_fast_classifier *classifier =
		(struct net4_fast_classifier *)data;
	uint32_t *to = ADDR_OF(&classifier->to);
	uint32_t values[btree_u32_max_batch_size];
	while (count >= btree_u32_max_batch_size) {
		for (size_t i = 0; i < btree_u32_max_batch_size; ++i) {
			struct rte_mbuf *mbuf = packet_to_mbuf(packets[i]);
			struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
				mbuf,
				struct rte_ipv4_hdr *,
				packets[i]->network_header.offset
			);
			values[i] = rte_be_to_cpu_32(ipv4_hdr->src_addr) + 1;
		}
		btree_u32_lower_bounds(
			&classifier->btree,
			values,
			btree_u32_max_batch_size,
			result
		);
		for (size_t i = 0; i < btree_u32_max_batch_size; ++i) {
			if (unlikely(
				    result[i] == 0 ||
				    to[result[i] - 1] < values[i] - 1
			    )) {
				result[i] = classifier->btree.n;
			} else {
				--result[i];
			}
		}
		result += btree_u32_max_batch_size;
		packets += btree_u32_max_batch_size;
		count -= btree_u32_max_batch_size;
	}
	count %= btree_u32_max_batch_size;
	for (size_t i = 0; i < count; ++i) {
		struct rte_mbuf *mbuf = packet_to_mbuf(packets[i]);
		struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr *,
			packets[i]->network_header.offset
		);
		values[i] = rte_be_to_cpu_32(ipv4_hdr->src_addr) + 1;
	}
	btree_u32_lower_bounds(&classifier->btree, values, count, result);
	for (size_t i = 0; i < count; ++i) {
		if (unlikely(
			    result[i] == 0 || to[result[i] - 1] < values[i] - 1
		    )) {
			result[i] = classifier->btree.n;
		} else {
			--result[i];
		}
	}
}
