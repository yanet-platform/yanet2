#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>

#include <rte_ether.h>

#include <lib/filter/query.h>

#include "common/container_of.h"
#include "lib/controlplane/config/econtext.h"
#include "lib/dataplane/module/module.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/pipeline/pipeline.h"

#include "config.h"
#include "dataplane.h"
#include "process.h"

// Whether the packet carries a usable transport header for the source and
// destination classifiers. Non-initial fragments keep the TCP/UDP protocol
// number of their flow but hold arbitrary payload where the ports should be,
// so they are forwarded untouched instead of being classified.
static bool
l3b_packet_has_transport(const struct packet *packet) {
	return packet->fragment_offset == 0 &&
	       (packet->transport_header.type == IPPROTO_TCP ||
		packet->transport_header.type == IPPROTO_UDP);
}

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

	// A force-polled tick can hand this module an empty front, and sizing
	// the arrays below directly by that count would then declare them with
	// zero length, which is undefined behavior. The early return is only
	// safe because nothing runs after this handler's final packet loop —
	// any code added after that loop must run before the return.
	uint32_t input_count = packet_front_input_count(packet_front);
	if (input_count == 0) {
		return;
	}

	struct packet *ip4_packets[input_count];
	uint32_t ip4_result[input_count];
	uint64_t ip4_idx = 0;

	struct packet *ip6_packets[input_count];
	uint32_t ip6_result[input_count];
	uint64_t ip6_idx = 0;

	for (struct packet *packet = packet_list_first(&packet_front->input);
	     packet != NULL;
	     packet = packet->next) {
		if (!l3b_packet_has_transport(packet)) {
			continue;
		}

		if (packet->network_header.type ==
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
			ip4_packets[ip4_idx++] = packet;
		}

		if (packet->network_header.type ==
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
			ip6_packets[ip6_idx++] = packet;
		}
	}

	// The module-level filters stay zeroed until the first destination
	// rule is installed; querying a zeroed filter is undefined
	// (value_table_get dereferences a relative pointer via
	// ADDR_OF_NONNULL). Skip the query and treat every TCP/UDP packet as
	// unmatched while there are no rules.
	if (config->destination_filter_rule_count > 0) {
		filter_query(
			&config->filter_ip4,
			l3b_destination_filter_ip4,
			ip4_packets,
			ip4_result,
			ip4_idx
		);

		filter_query(
			&config->filter_ip6,
			l3b_destination_filter_ip6,
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
		bool classified =
			(type == rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4) ||
			 type == rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) &&
			l3b_packet_has_transport(packet);

		if (!classified) {
			packet_front_output(packet_front, packet);
			continue;
		}

		uint32_t rule_index = FILTER_RULE_INVALID;
		if (config->destination_filter_rule_count > 0) {
			if (type == rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
				rule_index = ip4_result[ip4_idx];
			} else {
				rule_index = ip6_result[ip6_idx];
			}
		}

		if (type == rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
			ip4_idx++;
		} else {
			ip6_idx++;
		}

		// Each rule carries the object link of the virtual service it
		// routes to.
		if (rule_index != FILTER_RULE_INVALID &&
		    rule_index < config->destination_filter_rule_count) {
			uint64_t *rule_object_links =
				ADDR_OF(&config->rule_object_links);
			struct virtual_service *virtual_service =
				l3b_module_ectx_virtual_service(
					module_ectx,
					rule_object_links[rule_index]
				);
			if (virtual_service == NULL) {
				packet_front_drop(packet_front, packet);
				continue;
			}

			int result = l3b_virtual_service_process(
				virtual_service, packet
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
