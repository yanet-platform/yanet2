#pragma once

#include "classifiers/net6_fast.h"
#include "common/big_array.h"
#include "common/btree/u64.h"
#include "common/memory_address.h"
#include "lib/dataplane/packet/packet.h"

#include <netinet/in.h>
#include <rte_ip.h>
#include <rte_mbuf.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include <stdint.h>
#include <stdio.h>
#include <string.h>

#include "declare.h"

typedef uint8_t *(get_addr_func)(struct packet *packet);

static inline uint8_t *
get_dst_addr(struct packet *packet) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);
	return ipv6_hdr->dst_addr;
}

static inline uint8_t *
get_src_addr(struct packet *packet) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);
	return ipv6_hdr->src_addr;
}

static inline void
query_batch(
	struct net6_fast_classifier *classifier,
	get_addr_func getter,
	struct packet **packets,
	uint32_t *result,
	uint32_t count,
	uint64_t *values_high,
	uint64_t *values_low,
	uint32_t *result_low
) {
	uint64_t *high_to = ADDR_OF(&classifier->high.to);
	uint64_t *low_to = ADDR_OF(&classifier->low.to);
	for (size_t i = 0; i < count; ++i) {
		uint8_t *addr = getter(packets[i]);
		uint64_t values[2];
		memcpy(values, addr, 16);
		values_high[i] = rte_be_to_cpu_64(values[0]) + 1;
		values_low[i] = rte_be_to_cpu_64(values[1]) + 1;
	}
	btree_u64_lower_bounds(
		&classifier->high.btree, values_high, count, result
	);
	btree_u64_lower_bounds(
		&classifier->low.btree, values_low, count, result_low
	);
	for (size_t i = 0; i < count; ++i) {
		if (unlikely(
			    result[i] == 0 || result_low[i] == 0 ||
			    high_to[result[i] - 1] < values_high[i] - 1 ||
			    low_to[result_low[i] - 1] < values_low[i] - 1
		    )) {
			result[i] = classifier->mismatch_classifier;
		} else {
			memcpy(&result[i],
			       big_array_get(
				       &classifier->comb,
				       sizeof(int
				       ) * ((result[i] - 1) *
						    classifier->low.btree.n +
					    result_low[i] - 1)
			       ),
			       sizeof(int));
		}
	}
}

static inline void
query(void *data,
      get_addr_func getter,
      struct packet **packets,
      uint32_t *result,
      uint32_t count) {
	struct net6_fast_classifier *classifier =
		(struct net6_fast_classifier *)data;
	uint64_t values_high[btree_u64_max_batch_size];
	uint64_t values_low[btree_u64_max_batch_size];
	uint32_t result_low[btree_u64_max_batch_size];
	while (count >= btree_u64_max_batch_size) {
		query_batch(
			classifier,
			getter,
			packets,
			result,
			btree_u64_max_batch_size,
			values_high,
			values_low,
			result_low
		);
		count -= btree_u64_max_batch_size;
		result += btree_u64_max_batch_size;
		packets += btree_u64_max_batch_size;
	}
	count %= btree_u64_max_batch_size;
	query_batch(
		classifier,
		getter,
		packets,
		result,
		count,
		values_high,
		values_low,
		result_low
	);
}

static inline void
FILTER_ATTR_QUERY_FUNC(net6_fast_src)(
	void *data, struct packet **packets, uint32_t *result, uint32_t count
) {
	query(data, get_src_addr, packets, result, count);
}

static inline void
FILTER_ATTR_QUERY_FUNC(net6_fast_dst)(
	void *data, struct packet **packets, uint32_t *result, uint32_t count
) {
	query(data, get_dst_addr, packets, result, count);
}