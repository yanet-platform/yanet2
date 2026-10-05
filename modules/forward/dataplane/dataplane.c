#include "config.h"

#include <string.h>

#include <rte_ether.h>
#include <rte_ip.h>

#include "filter_lookup.h"

#include "lib/controlplane/config/econtext.h"

#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/module/module.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/pipeline/pipeline.h"

// One fixed batch of the input front processed by the handler.
#define FORWARD_HANDLE_MAX_BATCH 64

static void
forward_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
) {
	(void)dp_worker;

	struct forward_module_config *forward_config = container_of(
		module_ectx->abs_cp_module,
		struct forward_module_config,
		cp_module
	);

	// The scratch holds one fixed batch of the input front.
	//
	// A front handed to the handler has no size ceiling: routed fronts
	// accumulate packets from other entries and cloning modules grow
	// them further. The burst is processed batch by batch: each batch
	// is classified, then its verdict consumes exactly the packets the
	// classification covered, so the remainder of the input list stays
	// listed for the next iteration. The vlan stage runs over the
	// batch itself and needs no separate pointer array.
	struct packet *packets[FORWARD_HANDLE_MAX_BATCH];
	uint32_t vlan_result[FORWARD_HANDLE_MAX_BATCH];

	struct packet *ip4_packets[FORWARD_HANDLE_MAX_BATCH];
	uint32_t ip4_result[FORWARD_HANDLE_MAX_BATCH];
	uint32_t ip4_pos[FORWARD_HANDLE_MAX_BATCH];

	struct packet *ip6_packets[FORWARD_HANDLE_MAX_BATCH];
	uint32_t ip6_result[FORWARD_HANDLE_MAX_BATCH];
	uint32_t ip6_pos[FORWARD_HANDLE_MAX_BATCH];

	uint32_t core_classes[FORWARD_HANDLE_MAX_BATCH];
	uint32_t core4_classes[FORWARD_HANDLE_MAX_BATCH];
	uint32_t net4_classes[FORWARD_HANDLE_MAX_BATCH];
	uint32_t core6_classes[FORWARD_HANDLE_MAX_BATCH];
	uint32_t net6_classes[FORWARD_HANDLE_MAX_BATCH];

	struct packet *packet;
	uint32_t count;
	while ((count = packet_front_collect_input(
			packet_front, packets, FORWARD_HANDLE_MAX_BATCH
		)) != 0) {
		uint32_t ip4_idx = 0;
		uint32_t ip6_idx = 0;

		for (uint32_t idx = 0; idx < count; ++idx) {
			packet = packets[idx];

			if (packet->network_header.type ==
			    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
				// The position of the family packet inside
				// the batch: the shared core classes are
				// gathered through it.
				ip4_pos[ip4_idx] = idx;
				ip4_packets[ip4_idx++] = packet;
			}

			if (packet->network_header.type ==
			    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
				ip6_pos[ip6_idx] = idx;
				ip6_packets[ip6_idx++] = packet;
			}
		}

		// The family filters share the core classification: the core
		// classes are computed once per batch, the vlan filter
		// resolves them through its decoder and the family results
		// combine them with the network pair classes through the root
		// joints.
		fwd_classify_core(
			&forward_config->classifier_core,
			module_ectx->module_device_id,
			(const struct packet **)packets,
			core_classes,
			count
		);
		classify_resolve(
			&forward_config->filter_vlan.rule_map,
			core_classes,
			vlan_result,
			count
		);

		for (uint32_t idx = 0; idx < ip4_idx; ++idx) {
			core4_classes[idx] = core_classes[ip4_pos[idx]];
		}
		fwd_classify_net4(
			&forward_config->classifier_net4,
			(const struct packet **)ip4_packets,
			net4_classes,
			ip4_idx
		);
		classify_combine(
			&forward_config->filter_ip4.root_joint,
			&forward_config->filter_ip4.rule_map,
			core4_classes,
			net4_classes,
			ip4_result,
			ip4_idx
		);

		for (uint32_t idx = 0; idx < ip6_idx; ++idx) {
			core6_classes[idx] = core_classes[ip6_pos[idx]];
		}
		fwd_classify_net6(
			&forward_config->classifier_net6,
			(const struct packet **)ip6_packets,
			net6_classes,
			ip6_idx
		);
		classify_combine(
			&forward_config->filter_ip6.root_joint,
			&forward_config->filter_ip6.rule_map,
			core6_classes,
			net6_classes,
			ip6_result,
			ip6_idx
		);

		ip4_idx = 0;
		ip6_idx = 0;

		for (uint32_t idx = 0; idx < count; ++idx) {
			packet = packet_list_pop(&packet_front->input);
			struct forward_target *target = NULL;

			uint32_t action = vlan_result[idx];

			if (packet->network_header.type ==
			    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
				if (ip4_result[ip4_idx] < action) {
					action = ip4_result[ip4_idx];
				}
				++ip4_idx;
			} else if (packet->network_header.type ==
				   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
				if (ip6_result[ip6_idx] < action) {
					action = ip6_result[ip6_idx];
				}
				++ip6_idx;
			}

			if (action != CLASSIFY_RULE_INVALID) {
				target = ADDR_OF(&forward_config->targets) +
					 action;
			}

			if (target != NULL) {
				uint64_t *counters = counter_get_address(
					target->counter_id,
					module_ectx->abs_counter_storage
				);
				counters[0] += 1;
				counters[1] += packet_data_len(packet);

				struct module_device_target *device_target =
					module_ectx_device_target(
						module_ectx, target->device_id
					);

				if (device_target == NULL) {
					packet_front_drop(packet_front, packet);
					continue;
				}

				if (target->mode == FORWARD_MODE_IN) {
					packet->tx_device_id =
						device_target->device_id;
					module_ectx_route_input(
						module_ectx,
						packet_front,
						device_target->abs_input_entry,
						packet
					);
				} else if (target->mode == FORWARD_MODE_OUT) {
					packet->tx_device_id =
						device_target->device_id;
					module_ectx_route_output(
						module_ectx,
						packet_front,
						device_target->abs_output_entry,
						packet
					);
				} else {
					packet_front_output(
						packet_front, packet
					);
				}

			} else {
				// If the forwarding module doesn't modify the
				// target device_id, the packet should be
				// placed in the output queue, which will be
				// the input queue for the next module.
				packet_front_output(packet_front, packet);
			}
		}
	}
}

struct forward_module {
	struct module module;
};

static void
forward_module_commit_ectx(
	struct module_ectx *module_ectx, struct cp_module *cp_module
) {
	(void)module_ectx;
	(void)cp_module;
}

static void
forward_module_commit(
	struct dp_config *dp_config, struct cp_module *cp_module
) {
	(void)dp_config;
	(void)cp_module;
}

struct module *
new_module_forward() {
	struct forward_module *module =
		(struct forward_module *)malloc(sizeof(struct forward_module));

	if (module == NULL) {
		return NULL;
	}

	// The loader copies every field of the returned descriptor, so
	// heap garbage must not survive in the ones this constructor
	// leaves unset.
	memset(module, 0, sizeof(*module));

	snprintf(
		module->module.name,
		sizeof(module->module.name),
		"%s",
		"forward"
	);
	module->module.handler = forward_handle_packets;
	module->module.commit_handler = forward_module_commit;
	module->module.commit_ectx_handler = forward_module_commit_ectx;
	module->module.prepared_size = sizeof(struct forward_prepared);

	return &module->module;
}
