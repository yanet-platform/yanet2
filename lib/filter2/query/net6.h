#pragma once

#include "common/container_of.h"

#include "lib/dataplane/packet/packet.h"
#include "lib/filter2/classifiers/net6.h"

#include "declare.h"

#include <rte_ip.h>
#include <rte_mbuf.h>

#include <stdint.h>

#include "common/network.h"
#include <string.h>

typedef void (*packet_get_net6_batch_func)(
	const struct packet **packets, uint8_t *addrs, uint32_t packet_count
);

struct filter_query_attr_net6_handlers {
	const struct filter_query_attr_handlers attr_handlers;
	const packet_get_net6_batch_func get_net6;
};

static inline void
filter_query_attr_net6_lookup(
	const struct filter_query_attr *attr,
	const struct filter_query_attr_handlers *attr_handlers,
	const struct packet **packets,
	uint32_t *results,
	uint32_t packet_count
) {
	const struct filter_query_attr_net6_handlers *net6_handlers =
		container_of(
			attr_handlers,
			const struct filter_query_attr_net6_handlers,
			attr_handlers
		);

	const struct filter_query_attr_net6 *attr_net6 =
		container_of(attr, const struct filter_query_attr_net6, attr);

	uint32_t *row_scalar = ADDR_OF(&attr_net6->row_scalar);
	uint32_t *row_index = ADDR_OF(&attr_net6->row_index);

	// The addresses are gathered in one batched call, so the per packet
	// getter dispatch amortizes over the batch and the walks read
	// contiguous keys.
	uint8_t addrs[packet_count][NET6_LEN];
	net6_handlers->get_net6(packets, addrs[0], packet_count);

	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const uint8_t *addr = addrs[idx];
		uint32_t hi = lpm8_lookup(&attr_net6->hi, addr);
		uint32_t scalar = row_scalar[hi];
		if (scalar != FILTER_NET6_ROW_2D) {
			results[idx] = scalar;
		} else {
			uint32_t lo = lpm8_lookup(&attr_net6->lo, addr + 8);
			results[idx] = value_table_get(
				&attr_net6->comb, row_index[hi], lo
			);
		}
	}
}

static inline void
filter_packet_get_net6_src_batch(
	const struct packet **packets, uint8_t *addrs, uint32_t packet_count
) {
	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const struct packet *packet = packets[idx];
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv6_hdr *,
			packet->network_header.offset
		);
		memcpy(addrs + idx * NET6_LEN, ipv6_hdr->src_addr, NET6_LEN);
	}
}

static inline void
filter_packet_get_net6_dst_batch(
	const struct packet **packets, uint8_t *addrs, uint32_t packet_count
) {
	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const struct packet *packet = packets[idx];
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv6_hdr *,
			packet->network_header.offset
		);
		memcpy(addrs + idx * NET6_LEN, ipv6_hdr->dst_addr, NET6_LEN);
	}
}

static const struct filter_query_attr_handlers filter_query_net6 = {
	.lookup = filter_query_attr_net6_lookup,
};

static const struct filter_query_attr_net6_handlers filter_query_attr_net6_src =
	{
		.attr_handlers = filter_query_net6,
		.get_net6 = filter_packet_get_net6_src_batch,
};

static const struct filter_query_attr_net6_handlers filter_query_attr_net6_dst =
	{
		.attr_handlers = filter_query_net6,
		.get_net6 = filter_packet_get_net6_dst_batch,
};
