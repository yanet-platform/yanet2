#include "config.h"

#include <rte_ether.h>
#include <rte_ip.h>

#include <filter/query.h>

#include "controlplane/config/econtext.h"

#include "dataplane/config/zone.h"
#include "dataplane/module/module.h"
#include "dataplane/packet/packet.h"
#include "dataplane/pipeline/pipeline.h"

FILTER_QUERY_DECLARE(FWD_FILTER_VLAN_TAG, device, vlan);

FILTER_QUERY_DECLARE(FWD_FILTER_IP4_TAG, device, vlan, net4_src, net4_dst);

FILTER_QUERY_DECLARE(FWD_FILTER_IP6_TAG, device, vlan, net6_src, net6_dst);

static void
forward_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
) {
	struct forward_module_config *forward_config = container_of(
		ADDR_OF(&module_ectx->cp_module),
		struct forward_module_config,
		cp_module
	);

	struct packet *vlan_packets[packet_list_count(&packet_front->input)];
	const struct value_range
		*vlan_result[packet_list_count(&packet_front->input)];
	uint64_t vlan_idx = 0;

	struct packet *ip4_packets[packet_list_count(&packet_front->input)];
	const struct value_range
		*ip4_result[packet_list_count(&packet_front->input)];
	uint64_t ip4_idx = 0;

	struct packet *ip6_packets[packet_list_count(&packet_front->input)];
	const struct value_range
		*ip6_result[packet_list_count(&packet_front->input)];
	uint64_t ip6_idx = 0;

	for (struct packet *packet = packet_list_first(&packet_front->input);
	     packet != NULL;
	     packet = packet->next) {

		vlan_packets[vlan_idx++] = packet;

		if (packet->network_header.type ==
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
			ip4_packets[ip4_idx++] = packet;
		}

		if (packet->network_header.type ==
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
			ip6_packets[ip6_idx++] = packet;
		}
	}

	FILTER_QUERY(
		&forward_config->filter_vlan,
		FWD_FILTER_VLAN_TAG,
		vlan_packets,
		vlan_result,
		vlan_idx
	);

	FILTER_QUERY(
		&forward_config->filter_ip4,
		FWD_FILTER_IP4_TAG,
		ip4_packets,
		ip4_result,
		ip4_idx
	);

	FILTER_QUERY(
		&forward_config->filter_ip6,
		FWD_FILTER_IP6_TAG,
		ip6_packets,
		ip6_result,
		ip6_idx
	);

	vlan_idx = 0;
	ip4_idx = 0;
	ip6_idx = 0;

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		struct forward_target *target = NULL;

		const uint32_t *actions = NULL;
		uint32_t action_count = 0;

		// Set vlan as default
		actions = ADDR_OF(&vlan_result[vlan_idx]->values);
		action_count = vlan_result[vlan_idx]->count;
		++vlan_idx;

		if (packet->network_header.type ==
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
			const uint32_t *ip4_actions =
				ADDR_OF(&ip4_result[ip4_idx]->values);
			uint32_t ip4_action_count = ip4_result[ip4_idx]->count;
			++ip4_idx;

			if (ip4_action_count && (action_count == 0 ||
						 ip4_actions[0] < actions[0])) {
				actions = ip4_actions;
				action_count = ip4_action_count;
			}
		} else if (packet->network_header.type ==
			   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
			const uint32_t *ip6_actions =
				ADDR_OF(&ip6_result[ip6_idx]->values);
			uint32_t ip6_action_count = ip6_result[ip6_idx]->count;
			++ip6_idx;

			if (ip6_action_count && (action_count == 0 ||
						 ip6_actions[0] < actions[0])) {
				actions = ip6_actions;
				action_count = ip6_action_count;
			}
		}

		if (action_count)
			target = ADDR_OF(&forward_config->targets) + actions[0];

		if (target != NULL) {
			uint64_t *counters = counter_get_address(
				target->counter_id,
				dp_worker->idx,
				ADDR_OF(&module_ectx->counter_storage)
			);
			counters[0] += 1;
			counters[1] += packet_data_len(packet);

			struct config_gen_ectx *config_gen_ectx =
				ADDR_OF(&module_ectx->config_gen_ectx);

			uint64_t device_id = module_ectx_encode_device(
				module_ectx, target->device_id
			);

			struct device_ectx *device_ectx =
				config_gen_ectx_get_device(
					config_gen_ectx, device_id
				);
			if (device_ectx == NULL) {
				packet_front_drop(packet_front, packet);
				continue;
			}

			if (target->mode == FORWARD_MODE_IN) {
				device_ectx_process_input(
					dp_worker,
					device_ectx,
					packet_front,
					packet
				);
			} else if (target->mode == FORWARD_MODE_OUT) {
				device_ectx_process_output(
					dp_worker,
					device_ectx,
					packet_front,
					packet
				);
			} else {
				packet_front_output(packet_front, packet);
			}

		} else {
			// If the forwarding module doesn't modify the target
			// device_id, the packet should be placed in the output
			// queue, which will be the input queue for the next
			// module.
			packet_front_output(packet_front, packet);
		}
	}
}

struct forward_module {
	struct module module;
};

struct module *
new_module_forward() {
	struct forward_module *module =
		(struct forward_module *)malloc(sizeof(struct forward_module));

	if (module == NULL) {
		return NULL;
	}

	snprintf(
		module->module.name,
		sizeof(module->module.name),
		"%s",
		"forward"
	);
	module->module.handler = forward_handle_packets;

	return &module->module;
}
