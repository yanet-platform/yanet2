#include "config.h"

#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_mbuf.h>

#include "common/memory.h"
#include "lib/logging/log.h"

#include "dataplane/config/zone.h"

#include "dataplane/module/module.h"
#include "dataplane/packet/packet.h"
#include "dataplane/pipeline/pipeline.h"

struct route_module {
	struct module module;
};

static uint32_t
route_handle_v4(struct route_module_config *config, struct packet *packet) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv4_hdr *header = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);
	if (header->time_to_live <= 1) {
		return LPM_VALUE_INVALID;
	}
	header->time_to_live -= 1;

	/*
	 * Recalculate IPv4 header checksum using RFC 1624 incremental update.
	 * When TTL is decremented by 1, we add 0xFEFF to the complemented
	 * checksum. This is equivalent to subtracting 0x0100 from the original
	 * checksum (TTL field change).
	 */
	unsigned int sum =
		((~rte_be_to_cpu_16(header->hdr_checksum)) & 0xFFFF) + 0xFEFF;
	header->hdr_checksum = ~rte_cpu_to_be_16(sum + (sum >> 16));

	return lpm_lookup(&config->lpm_v4, 4, (uint8_t *)&header->dst_addr);
}

static uint32_t
route_handle_v6(struct route_module_config *config, struct packet *packet) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv6_hdr *header = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);
	if (header->hop_limits <= 1) {
		return LPM_VALUE_INVALID;
	}
	header->hop_limits -= 1;

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
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
) {
	(void)dp_worker;

	struct route_module_config *route_config = container_of(
		ADDR_OF(&module_ectx->cp_module),
		struct route_module_config,
		cp_module
	);

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

		// TODO: Route selection should be based on hash/NUMA/dp
		// instance/etc
		uint64_t route_index = ADDR_OF(&route_config->route_indexes
		)[route_list->start + packet->hash % route_list->count];

		struct route *route =
			ADDR_OF(&route_config->routes) + route_index;

		struct config_gen_ectx *config_gen_ectx =
			ADDR_OF(&module_ectx->config_gen_ectx);

		uint64_t device_id = module_ectx_encode_device(
			module_ectx, route->device_id
		);

		struct device_ectx *device_ectx =
			config_gen_ectx_get_device(config_gen_ectx, device_id);
		if (device_ectx == NULL) {
			packet_front_drop(packet_front, packet);
			continue;
		}

		route_set_packet_destination(packet, route);
		device_ectx_process_output(
			dp_worker, device_ectx, packet_front, packet
		);
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
