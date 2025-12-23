#pragma once

#include "filter/classifiers/net6.h"
#include "lib/dataplane/packet/packet.h"

#include "declare.h"

#include <rte_ip.h>
#include <rte_mbuf.h>

#include <stdint.h>

static inline uint32_t
FILTER_ATTR_QUERY_FUNC(net6_dst)(struct packet *packet, void *data) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);

	struct net6_classifier *c = (struct net6_classifier *)data;
	uint32_t hi = lpm8_lookup(&c->hi, (const uint8_t *)ipv6_hdr->dst_addr);
	uint32_t lo =
		lpm8_lookup(&c->lo, (const uint8_t *)ipv6_hdr->dst_addr + 8);

	return value_table_get(&c->comb, hi, lo);
}

static inline uint32_t
FILTER_ATTR_QUERY_FUNC(net6_src)(struct packet *packet, void *data) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);

	struct net6_classifier *c = (struct net6_classifier *)data;
	uint32_t hi = lpm8_lookup(&c->hi, (const uint8_t *)ipv6_hdr->src_addr);
	uint32_t lo =
		lpm8_lookup(&c->lo, (const uint8_t *)ipv6_hdr->src_addr + 8);

	return value_table_get(&c->comb, hi, lo);
}