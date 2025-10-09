#pragma once

#include "config.h"
#include "vs_def.h"

#include <filter/attribute.h>
#include <filter/filter.h>

#include <assert.h>

////////////////////////////////////////////////////////////////////////////////

#define VS_ID_INVALID (uint32_t)-1

////////////////////////////////////////////////////////////////////////////////

////////////////////////////////////////////////////////////////////////////////
// Utils for IPv4 Virtual Service lookups
////////////////////////////////////////////////////////////////////////////////

#define __V4_LOOKUP_TAG __BALANCER_V4_LOOKUP_TAG

FILTER_DECLARE(
	__V4_LOOKUP_TAG,
	&attribute_net4_dst,
	&attribute_port_dst,
	&attribute_proto
);

////////////////////////////////////////////////////////////////////////////////

static inline uint32_t
v4_vs_lookup_get(
	struct balancer_module_config *balancer_config, struct packet *packet
) {
	uint32_t *actions;
	uint32_t actions_count;
	FILTER_QUERY(
		&balancer_config->v4_service_lookup,
		__V4_LOOKUP_TAG,
		packet,
		&actions,
		&actions_count
	);
	if (actions_count == 0) {
		return VS_ID_INVALID;
	}
	/// @todo: actions_count > 1 ?
	uint32_t service_id = actions[0];
	return service_id;
}

static inline int
v4_vs_lookup_init(
	struct balancer_module_config *balancer_config,
	struct memory_context *mctx,
	struct filter_rule *rules,
	size_t rule_count
) {
	return FILTER_INIT(
		&balancer_config->v4_service_lookup,
		__V4_LOOKUP_TAG,
		rules,
		rule_count,
		mctx
	);
}

static inline void
v4_vs_lookup_free(struct balancer_module_config *balancer_config) {
	FILTER_FREE(&balancer_config->v4_service_lookup, __V4_LOOKUP_TAG);
}

////////////////////////////////////////////////////////////////////////////////

////////////////////////////////////////////////////////////////////////////////
// Utils for IPv6 Virtual Service lookups
////////////////////////////////////////////////////////////////////////////////

#define __V6_LOOKUP_TAG __BALANCER_V6_LOOKUP_TAG

FILTER_DECLARE(
	__V6_LOOKUP_TAG,
	&attribute_net6_dst,
	&attribute_port_dst,
	&attribute_proto
);

////////////////////////////////////////////////////////////////////////////////

static inline uint32_t
v6_vs_lookup_get(
	struct balancer_module_config *balancer_config, struct packet *packet
) {
	uint32_t *actions;
	uint32_t actions_count;
	FILTER_QUERY(
		&balancer_config->v6_service_lookup,
		__V6_LOOKUP_TAG,
		packet,
		&actions,
		&actions_count
	);
	if (actions_count == 0) {
		return VS_ID_INVALID;
	}
	/// @todo: actions_count > 1 ?
	uint32_t service_id = actions[0];
	return service_id;
}

static inline int
v6_vs_lookup_init(
	struct balancer_module_config *balancer_config,
	struct memory_context *mctx,
	struct filter_rule *rules,
	size_t rule_count
) {
	return FILTER_INIT(
		&balancer_config->v6_service_lookup,
		__V6_LOOKUP_TAG,
		rules,
		rule_count,
		mctx
	);
}

static inline void
v6_vs_lookup_free(struct balancer_module_config *balancer_config) {
	FILTER_FREE(&balancer_config->v6_service_lookup, __V6_LOOKUP_TAG);
}

////////////////////////////////////////////////////////////////////////////////

static inline struct balancer_vs *
balancer_vs_v4_lookup(
	struct balancer_module_config *balancer_config, struct packet *packet
) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);

	uint32_t service_id = v4_vs_lookup_get(balancer_config, packet);
	if (service_id == VS_ID_INVALID) {
		return NULL;
	}

	if (balancer_config->service_count <= service_id) {
		// If the service_id is out of range of available
		// services
		return NULL;
	}

	struct balancer_vs **vs_ptr =
		ADDR_OF(&balancer_config->services) + service_id;
	struct balancer_vs *vs = ADDR_OF(vs_ptr);

	if (lpm_lookup(&vs->src_filter, 4, (uint8_t *)&ipv4_hdr->src_addr) ==
	    LPM_VALUE_INVALID) {
		return NULL;
	}

	return vs;
}

static inline struct balancer_vs *
balancer_vs_v6_lookup(
	struct balancer_module_config *balancer_config, struct packet *packet
) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);

	uint32_t service_id = v6_vs_lookup_get(balancer_config, packet);
	if (service_id == VS_ID_INVALID) {
		return NULL;
	}

	if (balancer_config->service_count <= service_id) {
		// If the service_id is out of range of available
		// services
		return NULL;
	}

	struct balancer_vs **vs_ptr =
		ADDR_OF(&balancer_config->services) + service_id;
	struct balancer_vs *vs = ADDR_OF(vs_ptr);

	if (lpm_lookup(&vs->src_filter, 16, (uint8_t *)&ipv6_hdr->src_addr) ==
	    LPM_VALUE_INVALID) {
		return NULL;
	}

	/*
	 * FIXME: lpm value is 4 byte long where service_id is 8 bytes but
	 * it is less possible to have more thant UINT32_MAX services.
	 */
	return vs;
}

static inline struct balancer_vs *
balancer_lookup_vs(
	struct balancer_module_config *balancer_config, struct packet *packet
) {
	if (packet->network_header.type ==
	    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		return balancer_vs_v4_lookup(balancer_config, packet);
	} else if (packet->network_header.type ==
		   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
		return balancer_vs_v6_lookup(balancer_config, packet);
	} else {
		return NULL;
	}
}