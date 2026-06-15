#include "config.h"

#include <rte_ether.h>
#include <rte_ip.h>

#include <filter/query.h>

#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/module/module.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/pipeline/econtext.h"
#include "lib/dataplane/pipeline/pipeline.h"
#include "lib/dataplane/worker/worker.h"

FILTER_QUERY_DECLARE(filter_vlan, device, vlan);

FILTER_QUERY_DECLARE(filter_ip4, device, vlan, net4_src, net4_dst);

FILTER_QUERY_DECLARE(filter_ip6, device, vlan, net6_src, net6_dst);

static void
mirror_clone(
	struct dp_worker *worker,
	struct packet *packet,
	struct packet_list *pending,
	uint16_t device_id
) {
	struct packet *clone = worker_clone_packet(worker, packet);
	if (clone == NULL) {
		return;
	}

	clone->tx_device_id = device_id;
	packet_list_add(pending, clone);
}

static void
mirror_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
) {
	struct mirror_module_config *mirror_config = container_of(
		ADDR_OF(&module_ectx->cp_module),
		struct mirror_module_config,
		cp_module
	);

	struct packet *vlan_packets[packet_list_count(&packet_front->input)];
	uint32_t vlan_result[packet_list_count(&packet_front->input)];
	uint64_t vlan_idx = 0;

	struct packet *ip4_packets[packet_list_count(&packet_front->input)];
	uint32_t ip4_result[packet_list_count(&packet_front->input)];
	uint64_t ip4_idx = 0;

	struct packet *ip6_packets[packet_list_count(&packet_front->input)];
	uint32_t ip6_result[packet_list_count(&packet_front->input)];
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

	filter_query(
		&mirror_config->filter_vlan,
		filter_vlan,
		vlan_packets,
		vlan_result,
		vlan_idx
	);

	filter_query(
		&mirror_config->filter_ip4,
		filter_ip4,
		ip4_packets,
		ip4_result,
		ip4_idx
	);

	filter_query(
		&mirror_config->filter_ip6,
		filter_ip6,
		ip6_packets,
		ip6_result,
		ip6_idx
	);

	vlan_idx = 0;
	ip4_idx = 0;
	ip6_idx = 0;

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		struct mirror_target *target = NULL;

		uint32_t action = vlan_result[vlan_idx];
		++vlan_idx;

		if (packet->network_header.type ==
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
			if (ip4_result[ip4_idx] < action)
				action = ip4_result[ip4_idx];
			++ip4_idx;
		} else if (packet->network_header.type ==
			   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
			if (ip6_result[ip6_idx] < action)
				action = ip6_result[ip6_idx];
			++ip6_idx;
		}

		if (action != FILTER_RULE_INVALID)
			target = ADDR_OF(&mirror_config->targets) + action;

		if (target != NULL) {
			uint64_t *counters = counter_get_address(
				target->counter_id,
				dp_worker->idx,
				ADDR_OF(&module_ectx->counter_storage)
			);
			counters[0] += 1;
			counters[1] += packet_data_len(packet);

			uint16_t device_id = module_ectx_encode_device(
				module_ectx, target->device_id
			);

			if (device_id != (uint16_t)-1) {
				if (target->mode == MIRROR_MODE_IN) {
					mirror_clone(
						dp_worker,
						packet,
						&packet_front->pending_input,
						device_id
					);
				} else if (target->mode == MIRROR_MODE_OUT) {
					mirror_clone(
						dp_worker,
						packet,
						&packet_front->pending_output,
						device_id
					);
				}
			}
		}

		packet_front_output(packet_front, packet);
	}
}

struct mirror_module {
	struct module module;
};

struct module *
new_module_mirror() {
	struct mirror_module *module =
		(struct mirror_module *)malloc(sizeof(struct mirror_module));

	if (module == NULL) {
		return NULL;
	}

	snprintf(
		module->module.name, sizeof(module->module.name), "%s", "mirror"
	);
	module->module.handler = mirror_handle_packets;

	return &module->module;
}
