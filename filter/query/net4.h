#pragma once

#include "common/lpm.h"
#include "declare.h"
#include "lib/dataplane/packet/packet.h"

#include <rte_ip.h>
#include <rte_mbuf.h>

#include <stdint.h>

static inline uint32_t
FILTER_ATTR_QUERY_FUNC(net4_src)(struct packet *packet, void *data) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);
	struct lpm *lpm = (struct lpm *)data;
	return lpm4_lookup(lpm, (uint8_t *)&ipv4_hdr->src_addr);
}

static inline uint32_t
FILTER_ATTR_QUERY_FUNC(net4_dst)(struct packet *packet, void *data) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);

	struct lpm *lpm = (struct lpm *)data;
	return lpm4_lookup(lpm, (uint8_t *)&ipv4_hdr->dst_addr);
}
