#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>

#include <rte_ether.h>

#include "common/container_of.h"
#include "controlplane/config/econtext.h"
#include "dataplane/module/module.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/packet.h"

#include "config.h"
#include "dataplane.h"
#include "process.h"

static void
l3b_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
) {
	(void)dp_worker;

	struct module_config *config = container_of(
		ADDR_OF(&module_ectx->cp_module),
		struct module_config,
		cp_module
	);

	uint32_t input_count = packet_front_input_count(packet_front);

	struct packet *ip4_packets[input_count];
	uint32_t ip4_result[input_count];
	uint64_t ip4_idx = 0;

	struct packet *ip6_packets[input_count];
	uint32_t ip6_result[input_count];
	uint64_t ip6_idx = 0;

	for (struct packet *packet = packet_list_first(&packet_front->input);
	     packet != NULL;
	     packet = packet->next) {
		if (packet->network_header.type ==
			    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4) &&
		    (packet->transport_header.type == IPPROTO_TCP ||
		     packet->transport_header.type == IPPROTO_UDP)) {
			ip4_packets[ip4_idx++] = packet;
		}

		if (packet->network_header.type ==
			    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6) &&
		    (packet->transport_header.type == IPPROTO_TCP ||
		     packet->transport_header.type == IPPROTO_UDP)) {
			ip6_packets[ip6_idx++] = packet;
		}
	}

	// The module-level filters stay zeroed until the first virtual service
	// is published; querying a zeroed filter is undefined (value_table_get
	// dereferences a relative pointer via ADDR_OF_NONNULL). Skip the query
	// and treat every TCP/UDP packet as unmatched while there are no
	// services.
	if (config->virtual_service_count > 0) {
		filter_query(
			&config->filter_ip4,
			l3b_filter_ip4,
			ip4_packets,
			ip4_result,
			ip4_idx
		);

		filter_query(
			&config->filter_ip6,
			l3b_filter_ip6,
			ip6_packets,
			ip6_result,
			ip6_idx
		);
	}

	ip4_idx = 0;
	ip6_idx = 0;

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		uint16_t type = packet->network_header.type;
		bool tcp_udp = packet->transport_header.type == IPPROTO_TCP ||
			       packet->transport_header.type == IPPROTO_UDP;

		if (!((type == rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4) ||
		       type == rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) &&
		      tcp_udp)) {
			packet_front_output(packet_front, packet);
			continue;
		}

		uint32_t action = FILTER_RULE_INVALID;
		if (config->virtual_service_count > 0) {
			if (type == rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
				action = ip4_result[ip4_idx];
			} else {
				action = ip6_result[ip6_idx];
			}
		}

		if (type == rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
			ip4_idx++;
		} else {
			ip6_idx++;
		}

		if (action != FILTER_RULE_INVALID &&
		    action < config->virtual_service_count) {
			struct virtual_service **virtual_services =
				ADDR_OF(&config->virtual_services);
			int result = l3b_virtual_service_process(
				ADDR_OF(&virtual_services[action]), packet
			);
			if (result == 0) {
				packet_front_output(packet_front, packet);
			} else {
				packet_front_drop(packet_front, packet);
			}
		} else {
			packet_front_drop(packet_front, packet);
		}
	}
}

struct module *
new_module_l3b() {
	struct module *module = (struct module *)malloc(sizeof(*module));

	if (module == NULL) {
		return NULL;
	}

	snprintf(module->name, sizeof(module->name), "%s", "l3b");
	module->handler = l3b_handle_packets;

	return module;
}
