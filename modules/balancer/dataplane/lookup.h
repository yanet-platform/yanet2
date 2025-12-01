#pragma once

#include "common/memory_address.h"
#include "common/network.h"
#include "ctx.h"
#include "dataplane/packet/packet.h"
#include "filter/filter.h"
#include "module.h"
#include "vs.h"

#include <threads.h>

////////////////////////////////////////////////////////////////////////////////

#define VS_V4_TABLE_TAG __VS_V4_TABLE_TAG

FILTER_DECLARE(
	VS_V4_TABLE_TAG,
	&attribute_net4_dst,
	&attribute_port_dst,
	&attribute_proto
);

static inline uint32_t
vs_v4_table_lookup(
	struct balancer_module_config *config, struct packet *packet
) {
	uint32_t *actions;
	uint32_t actions_count;
	FILTER_QUERY(
		&config->vs_v4_table,
		VS_V4_TABLE_TAG,
		packet,
		&actions,
		&actions_count
	);
	if (actions_count == 0) {
		return -1;
	}
	/// @todo: actions_count > 1 ?
	uint32_t service_id = actions[0];
	return service_id;
}

////////////////////////////////////////////////////////////////////////////////

#define VS_V6_TABLE_TAG __VS_V6_TABLE_TAG

FILTER_DECLARE(
	VS_V6_TABLE_TAG,
	&attribute_net6_dst,
	&attribute_port_dst,
	&attribute_proto
);

static inline uint32_t
vs_v6_table_lookup(
	struct balancer_module_config *config, struct packet *packet
) {
	uint32_t *actions;
	uint32_t actions_count;
	FILTER_QUERY(
		&config->vs_v6_table,
		VS_V6_TABLE_TAG,
		packet,
		&actions,
		&actions_count
	);
	if (actions_count == 0) {
		return -1;
	}
	/// @todo: actions_count > 1 ?
	uint32_t service_id = actions[0];
	return service_id;
}

////////////////////////////////////////////////////////////////////////////////

static inline struct virtual_service *
vs_v4_lookup(
	struct packet_ctx *ctx,
	struct balancer_module_config *config,
	struct packet *packet
) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);

	// get id of the virtual service
	uint32_t service_id = vs_v4_table_lookup(config, packet);
	if (service_id == (uint32_t)-1) {
		return NULL;
	}

	if (config->vs_count <= service_id) {
		// if the service_id is out of range of available
		// services
		return NULL;
	}

	struct virtual_service *vs = ADDR_OF(&config->vs) + service_id;
	if (!(vs->flags & VS_PRESENT_IN_CONFIG_FLAG)) {
		return NULL;
	}
	packet_ctx_select_vs(ctx, vs);

	// check if packet source is allowed for the service
	/// @todo: use lpm4_lookup
	if (lpm_lookup(
		    &vs->src_filter, NET4_LEN, (uint8_t *)&ipv4_hdr->src_addr
	    ) == LPM_VALUE_INVALID) {
		packet_ctx_packet_src_not_allowed(ctx);
		return NULL;
	}

	return vs;
}

static inline struct virtual_service *
vs_v6_lookup(
	struct packet_ctx *ctx,
	struct balancer_module_config *config,
	struct packet *packet
) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);

	uint32_t service_id = vs_v6_table_lookup(config, packet);
	if (service_id == (uint32_t)-1) {
		return NULL;
	}

	if (config->vs_count <= service_id) {
		// If the service_id is out of range of available
		// services
		return NULL;
	}

	struct virtual_service *vs = ADDR_OF(&config->vs) + service_id;
	if (!(vs->flags & VS_PRESENT_IN_CONFIG_FLAG)) {
		return NULL;
	}
	packet_ctx_select_vs(ctx, vs);

	// check if packet source is allowed for the service
	/// @todo: use lpm16_lookup
	if (lpm_lookup(
		    &vs->src_filter, NET6_LEN, (uint8_t *)&ipv6_hdr->src_addr
	    ) == LPM_VALUE_INVALID) {
		packet_ctx_packet_src_not_allowed(ctx);
		return NULL;
	}
	return vs;
}

////////////////////////////////////////////////////////////////////////////////

static inline struct virtual_service *
vs_lookup(
	struct packet_ctx *ctx,
	struct balancer_module_config *config,
	struct packet *packet
) {
	if (packet->network_header.type ==
	    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		return vs_v4_lookup(ctx, config, packet);
	} else if (packet->network_header.type ==
		   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
		return vs_v6_lookup(ctx, config, packet);
	} else { // unsupported
		return NULL;
	}
}