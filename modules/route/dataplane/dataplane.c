#include "config.h"

#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_mbuf.h>

#include "common/memory.h"
#include "lib/logging/log.h"

#include "dataplane/config/zone.h"

#include "dataplane/module/module.h"
#include "dataplane/packet/packet.h"

struct route_module {
	struct module module;
};

static uint32_t
route_handle_v4(struct route_module_config *config, struct packet *packet) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv4_hdr *header = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);

	return lpm_lookup(&config->lpm_v4, 4, (uint8_t *)&header->dst_addr);
}

static uint32_t
route_handle_v6(struct route_module_config *config, struct packet *packet) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv6_hdr *header = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);

	return lpm_lookup(&config->lpm_v6, 16, header->dst_addr);
}

static void
route_set_packet_destination(struct packet *packet, struct route *route) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	/*
	 * FIXME: should we check that the packet starts from an
	 * ethernet header?
	 */
	struct rte_ether_hdr *ether_hdr =
		rte_pktmbuf_mtod_offset(mbuf, struct rte_ether_hdr *, 0);

	LOG_TRACEX(
		,
		"route_set_packet_destination [pre] "
		"src_mac: " RTE_ETHER_ADDR_PRT_FMT
		", dst_mac: " RTE_ETHER_ADDR_PRT_FMT,
		RTE_ETHER_ADDR_BYTES(&ether_hdr->src_addr),
		RTE_ETHER_ADDR_BYTES(&ether_hdr->dst_addr)
	);

	LOG_TRACEX(
		,
		"route_set_packet_destination [route] "
		"src_mac: " RTE_ETHER_ADDR_PRT_FMT
		", dst_mac: " RTE_ETHER_ADDR_PRT_FMT,
		RTE_ETHER_ADDR_BYTES((struct rte_ether_addr *)&route->src_addr),
		RTE_ETHER_ADDR_BYTES((struct rte_ether_addr *)&route->dst_addr)
	);

	memcpy(ether_hdr->dst_addr.addr_bytes,
	       route->dst_addr.addr,
	       sizeof(route->dst_addr));

	memcpy(ether_hdr->src_addr.addr_bytes,
	       route->src_addr.addr,
	       sizeof(route->src_addr));

	LOG_TRACEX(
		,
		"route_set_packet_destination [post] "
		"src_mac: " RTE_ETHER_ADDR_PRT_FMT
		", dst_mac: " RTE_ETHER_ADDR_PRT_FMT,
		RTE_ETHER_ADDR_BYTES(&ether_hdr->src_addr),
		RTE_ETHER_ADDR_BYTES(&ether_hdr->dst_addr)
	);
}

static void
route_handle_packets(
	struct dp_config *dp_config,
	uint64_t worker_idx,
	struct cp_module *cp_module,
	struct counter_storage *counter_storage,
	struct packet_front *packet_front
) {
	(void)dp_config;
	(void)worker_idx;
	(void)counter_storage;

	struct route_module_config *route_config =
		container_of(cp_module, struct route_module_config, cp_module);

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		uint32_t route_list_id = 0;

		if (packet->network_header.type ==
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
			route_list_id = route_handle_v4(route_config, packet);
		} else if (packet->network_header.type ==
			   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
			route_list_id = route_handle_v6(route_config, packet);
		} else {
			route_list_id = LPM_VALUE_INVALID;
		}

		if (route_list_id == LPM_VALUE_INVALID) {
			packet_front_drop(packet_front, packet);
			continue;
		}

		struct route_list *route_list =
			ADDR_OF(&route_config->route_lists) + route_list_id;
		if (route_list->count == 0) {
			packet_front_drop(packet_front, packet);
			continue;
		}

		// TODO: Route selection should be based on hash/NUMA/etc
		uint64_t route_index = ADDR_OF(&route_config->route_indexes
		)[route_list->start + packet->hash % route_list->count];

		struct route *route =
			ADDR_OF(&route_config->routes) + route_index;
		route_set_packet_destination(packet, route);
		packet_front_output(packet_front, packet);
	}
}

struct module *
new_module_route() {
	struct route_module *module =
		(struct route_module *)malloc(sizeof(struct route_module));

	if (module == NULL) {
		return NULL;
	}

	snprintf(
		module->module.name, sizeof(module->module.name), "%s", "route"
	);
	module->module.handler = route_handle_packets;

	return &module->module;
}
