#pragma once

#include "classifiers/net6_fast.h"
#include "common/value.h"
#include "lib/dataplane/packet/packet.h"

#include <assert.h>
#include <netinet/in.h>
#include <rte_ip.h>
#include <rte_mbuf.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include <stdint.h>
#include <stdio.h>
#include <string.h>

#include "declare.h"
#include "segments.h"

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
	uint32_t *result_high,
	uint32_t *result_low
) {
	for (size_t i = 0; i < count; ++i) {
		uint8_t *addr = getter(packets[i]);
		uint64_t values[2];
		memcpy(values, addr, 16);
		values_high[i] = rte_be_to_cpu_64(values[0]);
		values_low[i] = rte_be_to_cpu_64(values[1]);
	}

	size_t count_high = segments_u64_classify(
		&classifier->high, count, values_high, result_high
	);
	size_t count_low = segments_u64_classify(
		&classifier->low, count, values_low, result_low
	);

	assert(count_high == count_low);

	for (size_t i = 0; i < count; ++i) {
		size_t idx_high = result_high[i];
		size_t idx_low = result_low[i];
		result[i] =
			value_table_get(&classifier->comb, idx_high, idx_low);
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
	uint64_t values_high[segments_u64_classifier_max_batch_size];
	uint64_t values_low[segments_u64_classifier_max_batch_size];
	uint32_t result_high[segments_u64_classifier_max_batch_size];
	uint32_t result_low[segments_u64_classifier_max_batch_size];
	while (count >= segments_u64_classifier_max_batch_size) {
		query_batch(
			classifier,
			getter,
			packets,
			result,
			segments_u64_classifier_max_batch_size,
			values_high,
			values_low,
			result_high,
			result_low
		);
		count -= segments_u64_classifier_max_batch_size;
		result += segments_u64_classifier_max_batch_size;
		packets += segments_u64_classifier_max_batch_size;
	}
	if (count > 0) {
		query_batch(
			classifier,
			getter,
			packets,
			result,
			count,
			values_high,
			values_low,
			result_high,
			result_low
		);
	}
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