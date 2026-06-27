#pragma once

#include "lib/dataplane/packet/packet.h"
#include "lib/filter/classifiers/net6.h"

#include "declare.h"

#include <rte_ip.h>
#include <rte_mbuf.h>

#include <stdint.h>

static inline void
FILTER_ATTR_QUERY_FUNC(net6_dst)(
	void *data, struct packet **packets, uint32_t *result, uint32_t count
) {
	if (!count)
		return;

	struct net6_classifier *c = (struct net6_classifier *)data;

	uint32_t hi_values[count];
	uint32_t lo_values[count];

	for (uint32_t idx = 0; idx < count; ++idx) {
		struct rte_mbuf *mbuf = packet_to_mbuf(packets[idx]);
		struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv6_hdr *,
			packets[idx]->network_header.offset
		);

		const uint8_t *addr = (const uint8_t *)ipv6_hdr->dst_addr;
		hi_values[idx] = lpm8_lookup(&c->hi, addr);
		lo_values[idx] = lpm8_lookup(&c->lo, addr + 8);
	}

	for (uint32_t idx = 0; idx < count; ++idx) {
		result[idx] = value_table_get(
			&c->comb, hi_values[idx], lo_values[idx]
		);
	}
}

static inline void
FILTER_ATTR_QUERY_FUNC(net6_src)(
	void *data, struct packet **packets, uint32_t *result, uint32_t count
) {
	if (!count)
		return;

	struct net6_classifier *c = (struct net6_classifier *)data;

	uint32_t hi_values[count];
	uint32_t lo_values[count];

	for (uint32_t idx = 0; idx < count; ++idx) {
		struct rte_mbuf *mbuf = packet_to_mbuf(packets[idx]);
		struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv6_hdr *,
			packets[idx]->network_header.offset
		);

		const uint8_t *addr = (const uint8_t *)ipv6_hdr->src_addr;
		hi_values[idx] = lpm8_lookup(&c->hi, addr);
		lo_values[idx] = lpm8_lookup(&c->lo, addr + 8);
	}

	for (uint32_t idx = 0; idx < count; ++idx) {
		result[idx] = value_table_get(
			&c->comb, hi_values[idx], lo_values[idx]
		);
	}
}
