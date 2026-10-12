#include "config.h"

#include <string.h>

#include <rte_ether.h>
#include <rte_ip.h>

#include "common/container_of.h"

#include "lib/controlplane/config/econtext.h"

#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/module/module.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/pipeline/econtext.h"
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
	struct forward_prepared *prepared =
		ADDR_OF(&module_ectx->module_prepared);

	// The scratch holds one fixed batch of the input front.
	//
	// A front handed to the handler has no size ceiling: routed fronts
	// accumulate packets from other entries and cloning modules grow
	// them further. The burst is processed batch by batch: each batch
	// is classified, then its verdict consumes exactly the packets the
	// classification covered, so the remainder of the input list stays
	// listed for the next iteration.
	struct packet *packets[FORWARD_HANDLE_MAX_BATCH];
	uint32_t results[FORWARD_HANDLE_MAX_BATCH];

	struct packet *packet;
	uint32_t count;
	while ((count = packet_front_collect_input(
			packet_front, packets, FORWARD_HANDLE_MAX_BATCH
		)) != 0) {
		// The classification is the linked classifier object's: the
		// public handler of the object type runs the bundle the
		// object's subtype selected, over the whole batch at once. A
		// context whose binding resolved nothing classifies every
		// packet to no rule.
		classifier_object_classify(
			prepared->classifier,
			prepared->device_class,
			(const struct packet **)packets,
			results,
			count
		);

		for (uint32_t idx = 0; idx < count; ++idx) {
			// The collect is a peek: each packet leaves the input
			// list here, in the same order the classification
			// covered it.
			packet = packet_list_pop(&packet_front->input);
			struct forward_target *target = NULL;

			uint32_t action = results[idx];

			// The decision of the matched rule resolves the
			// result; the bound guards a ruleset that changed
			// under a module the owner did not rebuild, by
			// passing the rule indices that no longer resolve
			// through unrouted.
			if (action != CLASSIFY_RULE_INVALID &&
			    action < forward_config->target_count) {
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

// Resolves the module's binding to the classifier object: the linked
// object's prepared classification handles, and the class of the
// executing device in the object's device attribute - the device the
// link pass identified through the object's device names.
static void
forward_module_commit_ectx(
	struct module_ectx *module_ectx, struct cp_module *cp_module
) {
	struct forward_module_config *config = container_of(
		cp_module, struct forward_module_config, cp_module
	);
	struct forward_prepared *prepared =
		ADDR_OF(&module_ectx->module_prepared);

	prepared->classifier = NULL;
	prepared->device_class = 0;

	if (config->classifier_link_idx == FORWARD_CLASSIFIER_LINK_NONE) {
		return;
	}

	struct module_object_link_ectx *link = object_link_get_address(
		module_ectx, config->classifier_link_idx
	);
	if (link == NULL || link->abs_object_ectx == NULL) {
		return;
	}

	struct cp_object *cp_object = link->abs_object_ectx->abs_cp_object;
	if (cp_object == NULL) {
		return;
	}

	prepared->classifier = link->abs_object_ectx->abs_object_prepared;

	// The module device id is the executing device's slot in the
	// module's device table, which carries the object's device names;
	// a device outside the table keeps the any slot's name.
	uint32_t module_device_id = module_ectx->module_device_id;
	if (module_device_id >= cp_module->device_count) {
		module_device_id = 0;
	}
	struct cp_module_device *devices = ADDR_OF(&cp_module->devices);
	prepared->device_class = classifier_object_device_class(
		cp_object, devices[module_device_id].name
	);
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
