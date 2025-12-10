#pragma once

#include "common/lpm.h"
#include "common/network.h"

#include <netinet/in.h>
#include <rte_ip.h>

#include "flow/context.h"

#include "flow/helpers.h"
#include "lib/dataplane/packet/decap.h"

////////////////////////////////////////////////////////////////////////////////

static inline int
decap_ipv4(struct packet *packet, struct balancer_module_config *config) {
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

// Try to decapsulate packet if its destination address is from the allowed
// list. If decap failed, just pass packet further. Returns -1 only if packet
// network proto is invalid.
static inline int
try_decap(struct packet_ctx *ctx) {
	ctx->decap = false;

	struct packet *packet = ctx->packet;
	struct balancer_module_config *config = ctx->config;

	uint16_t network_protocol = packet->network_header.type;

	// check if decap is allowed.
	// decap is allowed if destination address
	// of the packet is in the decap list of the balancer.
	int decap_is_allowed;
	if (network_protocol == rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		decap_is_allowed = decap_ipv4(packet, config);
	} else if (network_protocol == rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
		decap_is_allowed = decap_ipv6(packet, config);
	} else {
		COMMON_STATS_INC(unexpected_network_proto, ctx);
		return -1;
	}

	// check if decap is allowed
	if (decap_is_allowed) {
		// if decap is allowed, make decap
		// and check result
		int decap_result = packet_decap(packet);
		if (decap_result != 0) {
			// decap failed, but it is ok
			COMMON_STATS_INC(decap_failed, ctx);
		} else {
			// successfully made decap
			COMMON_STATS_INC(decap_successful, ctx);
			ctx->decap = true;
		}
	}

	return 0;
}