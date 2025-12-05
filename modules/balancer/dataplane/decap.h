#pragma once

#include "common/lpm.h"
#include "common/network.h"
#include "ctx.h"
#include "rte_ip.h"
#include <netinet/in.h>

////////////////////////////////////////////////////////////////////////////////

static inline int
decap_ip(struct packet *packet, struct balancer_module_config *config) {
	struct rte_ipv4_hdr *ipv4 = rte_pktmbuf_mtod_offset(
		packet->mbuf,
		struct rte_ipv4_hdr *,
		packet->network_header.offset
	);
	if (lpm_lookup(
		    &config->decap_filter_v4,
		    NET4_LEN,
		    (const uint8_t *)&ipv4->dst_addr
	    ) != LPM_VALUE_INVALID) {
		return 1;
	} else {
		return 0;
	}
}

static inline int
decap_ipv6(struct packet *packet, struct balancer_module_config *config) {
	struct rte_ipv6_hdr *ipv6 = rte_pktmbuf_mtod_offset(
		packet->mbuf,
		struct rte_ipv6_hdr *,
		packet->network_header.offset
	);
	if (lpm_lookup(&config->decap_filter_v6, NET6_LEN, ipv6->dst_addr) !=
	    LPM_VALUE_INVALID) {
		return 1;
	} else {
		return 0;
	}
}

////////////////////////////////////////////////////////////////////////////////

static inline void
try_decap(struct packet_ctx *ctx) {
	struct packet *packet = ctx->packet;
	struct balancer_module_config *config = ctx->config;
	uint16_t network_protocol = packet->network_header.type;
	int decap_result;
	if (network_protocol == IPPROTO_IP) {
		decap_result = decap_ip(packet, config);
	} else if (network_protocol == IPPROTO_IPV6) {
		decap_result = decap_ipv6(packet, config);
	} else {
		// todo: handle error
		decap_result = -1;
	}
	(void)decap_result;
	// todo: counters
}