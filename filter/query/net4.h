#pragma once

#include "common/lpm.h"
#include "declare.h"
#include "lib/dataplane/packet/packet.h"

#include <rte_ip.h>
#include <rte_mbuf.h>

#include <stdint.h>

static inline void
FILTER_ATTR_QUERY_FUNC(net4_src)(
	void *data, struct packet **packets, uint32_t *result, uint32_t count
) {
	struct lpm *lpm = (struct lpm *)data;

	for (uint32_t idx = 0; idx < count; ++idx) {
		struct rte_mbuf *mbuf = packet_to_mbuf(packets[idx]);
		struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr *,
			packets[idx]->network_header.offset
		);
		result[idx] = lpm4_lookup(lpm, (uint8_t *)&ipv4_hdr->src_addr);
	}
}

static inline void
FILTER_ATTR_QUERY_FUNC(net4_dst)(
	void *data, struct packet **packets, uint32_t *result, uint32_t count
) {
	struct lpm *lpm = (struct lpm *)data;

	for (uint32_t idx = 0; idx < count; ++idx) {
		struct rte_mbuf *mbuf = packet_to_mbuf(packets[idx]);
		struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr *,
			packets[idx]->network_header.offset
		);
		result[idx] = lpm4_lookup(lpm, (uint8_t *)&ipv4_hdr->dst_addr);
	}
}
