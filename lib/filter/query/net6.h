#pragma once

#include "lib/dataplane/packet/packet.h"
#include "lib/filter/classifiers/net6.h"

#include "declare.h"

#include <rte_ip.h>
#include <rte_mbuf.h>

#include <stdint.h>
#include <string.h>

static inline void
FILTER_ATTR_QUERY_FUNC(net6_dst)(
	void *data, struct packet **packets, uint32_t *result, uint32_t count
) {
	if (!count) {
		return;
	}

	struct net6_classifier *c = (struct net6_classifier *)data;

	uint8_t hi_keys[count][8];
	uint8_t lo_keys[count][8];
	uint32_t hi_values[count];
	uint32_t lo_values[count];

	for (uint32_t idx = 0; idx < count; ++idx) {
		struct rte_mbuf *mbuf = packet_to_mbuf(packets[idx]);
		struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv6_hdr *,
			packets[idx]->network_header.offset
		);

		memcpy(hi_keys[idx], ipv6_hdr->dst_addr, 8);
		memcpy(lo_keys[idx], ipv6_hdr->dst_addr + 8, 8);
	}

	lpm8_lookup_batch(&c->hi, hi_keys[0], hi_values, count);
	lpm8_lookup_batch(&c->lo, lo_keys[0], lo_values, count);

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
	if (!count) {
		return;
	}

	struct net6_classifier *c = (struct net6_classifier *)data;

	uint8_t hi_keys[count][8];
	uint8_t lo_keys[count][8];
	uint32_t hi_values[count];
	uint32_t lo_values[count];

	for (uint32_t idx = 0; idx < count; ++idx) {
		struct rte_mbuf *mbuf = packet_to_mbuf(packets[idx]);
		struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv6_hdr *,
			packets[idx]->network_header.offset
		);

		memcpy(hi_keys[idx], ipv6_hdr->src_addr, 8);
		memcpy(lo_keys[idx], ipv6_hdr->src_addr + 8, 8);
	}

	lpm8_lookup_batch(&c->hi, hi_keys[0], hi_values, count);
	lpm8_lookup_batch(&c->lo, lo_keys[0], lo_values, count);

	for (uint32_t idx = 0; idx < count; ++idx) {
		result[idx] = value_table_get(
			&c->comb, hi_values[idx], lo_values[idx]
		);
	}
}
