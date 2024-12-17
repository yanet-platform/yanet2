#include "dataplane.h"

#include "dataplane/module/module.h"
#include "dataplane/packet/packet.h"

#include "lpm.h"

#include <rte_ether.h>
#include <rte_ip.h>

#define ROUTE_TYPE_IP4 4
#define ROUTE_TYPE_IP6 6

struct route {
	/*
	 * TODO: Assuming this is only about directly routed networks there
	 * is nothing to handle except the neighbour ethernet address.
	 */
	struct rte_ether_addr dst_addr;
	struct rte_ether_addr src_addr;
};

struct route_list {
	uint32_t *start;
	uint32_t count;
};

struct route_module_config {
	struct module_config config;

	struct lpm lpm_v6;
	struct lpm lpm_v4;

	// All known good routes
	struct route *routes;
	uint32_t route_count;

	// List of route indexes applicable for some destination network
	struct route_list *route_lists;
	uint32_t route_list_count;

	// Just to store the start of route indexes array
	// TODO: this could be replaced by start of the first list entry
	uint32_t *route_indexes;
};

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

	memcpy(ether_hdr->dst_addr.addr_bytes,
	       route->dst_addr.addr_bytes,
	       sizeof(route->dst_addr));

	memcpy(ether_hdr->src_addr.addr_bytes,
	       route->src_addr.addr_bytes,
	       sizeof(route->src_addr));
}

static void
route_handle_packets(
	struct module *module,
	struct module_config *config,
	struct packet_front *packet_front
) {
	(void)module;
	struct route_module_config *route_config =
		container_of(config, struct route_module_config, config);

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
			packet_front_drop(packet_front, packet);
			continue;
		}

		struct route_list *route_list =
			route_config->route_lists + route_list_id;
		if (route_list->count == 0) {
			packet_front_drop(packet_front, packet);
			continue;
		}

		/*
		 * TODO: Route selection should be based on hash/NUMA/etc
		 */
		struct route *route = route_config->routes + *route_list->start;
		route_set_packet_destination(packet, route);
		packet_front_output(packet_front, packet);
	}
}

static int
route_handle_configure(
	struct module *module,
	const void *config_data,
	size_t config_data_size,
	struct module_config **new_config
) {
	(void)module;
	(void)config_data;
	(void)config_data_size;
	(void)new_config;

	struct route_module_config *config = (struct route_module_config *)
		malloc(sizeof(struct route_module_config));

	lpm_init(&config->lpm_v4);
	lpm_init(&config->lpm_v6);

	lpm_insert(
		&config->lpm_v4,
		4,
		(uint8_t[4]){0, 0, 0, 0},
		(uint8_t[4]){0xff, 0xff, 0xff, 0xff},
		0
	);

	lpm_insert(
		&config->lpm_v6,
		16,
		(uint8_t[16]){0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		(uint8_t[16]){0xff,
			      0xff,
			      0xff,
			      0xff,
			      0xff,
			      0xff,
			      0xff,
			      0xff,
			      0xff,
			      0xff,
			      0xff,
			      0xff,
			      0xff,
			      0xff,
			      0xff,
			      0xff},
		1
	);
	config->route_indexes = (uint32_t *)malloc(sizeof(uint32_t) * 2);
	config->route_indexes[0] = 0;
	config->route_indexes[1] = 1;

	config->routes = (struct route *)malloc(sizeof(struct route) * 2);
	memcpy(config->routes[0].dst_addr.addr_bytes,
	       (uint8_t[6]){0x01, 0x02, 0x03, 0x04, 0x05, 0x06},
	       6);
	memcpy(config->routes[0].src_addr.addr_bytes,
	       (uint8_t[6]){0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c},
	       6);

	memcpy(config->routes[1].dst_addr.addr_bytes,
	       (uint8_t[6]){0xa1, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6},
	       6);
	memcpy(config->routes[1].src_addr.addr_bytes,
	       (uint8_t[6]){0xa7, 0xa8, 0xa9, 0xaa, 0xab, 0xac},
	       6);

	config->route_lists =
		(struct route_list *)malloc(sizeof(struct route_list) * 2);
	config->route_lists[0] =
		(struct route_list){config->route_indexes + 0, 1};
	config->route_lists[1] =
		(struct route_list){config->route_indexes + 1, 1};

	*new_config = &config->config;

	return 0;
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
	module->module.config_handler = route_handle_configure;

	return &module->module;
}
