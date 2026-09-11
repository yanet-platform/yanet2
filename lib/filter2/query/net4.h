#pragma once

#include "common/lpm.h"

#include "declare.h"
#include "lib/filter2/classifiers/net4.h"

#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/packet.h"

#include <rte_ip.h>
#include <rte_mbuf.h>

#include <stdint.h>

#include "common/network.h"
#include <string.h>

typedef void (*packet_get_net4_batch_func)(
	const struct packet **packets, uint8_t *addrs, uint32_t packet_count
);

struct filter_query_attr_net4_handlers {
	const struct filter_query_attr_handlers attr_handlers;
	const packet_get_net4_batch_func get_net4;
};

static inline void
filter_query_attr_net4_lookup(
	const struct filter_query_attr *attr,
	const struct filter_query_attr_handlers *attr_handlers,
	const struct packet **packets,
	uint32_t *results,
	uint32_t packet_count
) {
	const struct filter_query_attr_net4_handlers *net4_handlers =
		container_of(
			attr_handlers,
			const struct filter_query_attr_net4_handlers,
			attr_handlers
		);

	const struct filter_query_attr_net4 *attr_net4 =
		container_of(attr, const struct filter_query_attr_net4, attr);

	// The addresses are gathered in one batched call, so the per packet
	// getter dispatch amortizes over the batch and the walk reads
	// contiguous keys.
	uint8_t addrs[packet_count][NET4_LEN];
	net4_handlers->get_net4(packets, addrs[0], packet_count);

	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		results[idx] = vline_get(
			&attr_net4->line,
			lpm4_lookup(&attr_net4->lpm, addrs[idx])
		);
	}
}

static inline void
filter_packet_get_net4_src_batch(
	const struct packet **packets, uint8_t *addrs, uint32_t packet_count
) {
	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const struct packet *packet = packets[idx];
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr *,
			packet->network_header.offset
		);
		memcpy(addrs + idx * NET4_LEN, &ipv4_hdr->src_addr, NET4_LEN);
	}
}

static inline void
filter_packet_get_net4_dst_batch(
	const struct packet **packets, uint8_t *addrs, uint32_t packet_count
) {
	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const struct packet *packet = packets[idx];
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr *,
			packet->network_header.offset
		);
		memcpy(addrs + idx * NET4_LEN, &ipv4_hdr->dst_addr, NET4_LEN);
	}
}

static const struct filter_query_attr_handlers filter_query_net4 = {
	.lookup = filter_query_attr_net4_lookup,
};

static const struct filter_query_attr_net4_handlers filter_query_attr_net4_src =
	{
		.attr_handlers = filter_query_net4,
		.get_net4 = filter_packet_get_net4_src_batch,
};

static const struct filter_query_attr_net4_handlers filter_query_attr_net4_dst =
	{
		.attr_handlers = filter_query_net4,
		.get_net4 = filter_packet_get_net4_dst_batch,
};
