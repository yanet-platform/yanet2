#pragma once

#include "common/memory_address.h"
#include "common/network.h"
#include "filter/filter.h"
#include "module.h"
#include "ring.h"

////////////////////////////////////////////////////////////////////////////////

typedef uint8_t vs_flags_t;

////////////////////////////////////////////////////////////////////////////////

struct virtual_service {
	vs_flags_t flags;

	uint8_t address[16];

	uint16_t port;
	uint8_t proto;

	uint64_t real_start;
	uint64_t real_count;

	struct lpm src_filter;

	struct ring real_ring;

	uint64_t round_robin_counter;
};

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
vs_v4_lookup(struct balancer_module_config *config, struct packet *packet) {
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

	// check if packet source is allowed for the service
	/// @todo: use lpm4_lookup
	if (lpm_lookup(
		    &vs->src_filter, NET4_LEN, (uint8_t *)&ipv4_hdr->src_addr
	    ) == LPM_VALUE_INVALID) {
		return NULL;
	}

	return vs;
}

static inline struct virtual_service *
vs_v6_lookup(struct balancer_module_config *config, struct packet *packet) {
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

	// check if packet source is allowed for the service
	/// @todo: use lpm16_lookup
	if (lpm_lookup(
		    &vs->src_filter, NET6_LEN, (uint8_t *)&ipv6_hdr->src_addr
	    ) == LPM_VALUE_INVALID) {
		return NULL;
	}
	return vs;
}

////////////////////////////////////////////////////////////////////////////////

static inline struct virtual_service *
vs_lookup(struct balancer_module_config *config, struct packet *packet) {
	if (packet->network_header.type ==
	    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		return vs_v4_lookup(config, packet);
	} else if (packet->network_header.type ==
		   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
		return vs_v6_lookup(config, packet);
	} else { // unsupported
		return NULL;
	}
}