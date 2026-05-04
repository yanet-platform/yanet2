#pragma once

#include "common/container_of.h"

#include "filter/classifiers/net6.h"
#include "lib/dataplane/packet/packet.h"

#include "declare.h"

#include <rte_ip.h>
#include <rte_mbuf.h>

#include <stdint.h>

typedef const uint8_t *(*packet_get_net6_func)(const struct packet *packet);

struct filter_query_attr_net6_handlers {
	struct filter_query_attr_handlers attr_handlers;
	packet_get_net6_func get_net6;
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
			struct filter_query_attr_net6_handlers,
			attr_handlers
		);

	struct filter_query_attr_net6 *attr_net6 =
		container_of(attr, struct filter_query_attr_net6, attr);

	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const uint8_t *addr = net6_handlers->get_net6(packets[idx]);
		uint32_t hi = lpm8_lookup(&attr_net6->hi, addr);
		uint32_t lo = lpm8_lookup(&attr_net6->lo, addr + 8);
		results[idx] = *value_table_get_ptr(&attr_net6->comb, hi, lo);
	}
}

static inline const uint8_t *
filter_packet_get_net6_src(const struct packet *packet) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);

	return (const uint8_t *)ipv6_hdr->src_addr;
}

static inline const uint8_t *
filter_packet_get_net6_dst(const struct packet *packet) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);

	return (const uint8_t *)ipv6_hdr->dst_addr;
}

static const struct filter_query_attr_handlers filter_query_net6 = {
	.lookup = filter_query_attr_net6_lookup,
};

static const struct filter_query_attr_net6_handlers filter_query_attr_net6_src =
	{
		.attr_handlers = filter_query_net6,
		.get_net6 = filter_packet_get_net6_src,
};

static const struct filter_query_attr_net6_handlers filter_query_attr_net6_dst =
	{
		.attr_handlers = filter_query_net6,
		.get_net6 = filter_packet_get_net6_dst,
};
