#pragma once

#include "common/lpm.h"

#include "declare.h"
#include "filter/classifiers/net4.h"

#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/packet.h"

#include <rte_ip.h>
#include <rte_mbuf.h>

#include <stdint.h>

typedef const uint8_t *(*packet_get_net4_func)(const struct packet *packet);

struct filter_query_attr_net4_handlers {
	struct filter_query_attr_handlers attr_handlers;
	packet_get_net4_func get_net4;
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
			struct filter_query_attr_net4_handlers,
			attr_handlers
		);

	const struct filter_query_attr_net4 *attr_net4 =
		container_of(attr, struct filter_query_attr_net4, attr);

	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const uint8_t *addr = net4_handlers->get_net4(packets[idx]);
		results[idx] = lpm4_lookup(&attr_net4->lpm, addr);
	}
}

static inline const uint8_t *
filter_packet_get_net4_src(const struct packet *packet) {

	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);

	return (const uint8_t *)&ipv4_hdr->src_addr;
}

static inline const uint8_t *
filter_packet_get_net4_dst(const struct packet *packet) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);

	return (const uint8_t *)&ipv4_hdr->dst_addr;
}

static const struct filter_query_attr_handlers filter_query_net4 = {
	.lookup = filter_query_attr_net4_lookup,
};

static const struct filter_query_attr_net4_handlers filter_query_attr_net4_src =
	{
		.attr_handlers = filter_query_net4,
		.get_net4 = filter_packet_get_net4_src,
};

static const struct filter_query_attr_net4_handlers filter_query_attr_net4_dst =
	{
		.attr_handlers = filter_query_net4,
		.get_net4 = filter_packet_get_net4_dst,
};
