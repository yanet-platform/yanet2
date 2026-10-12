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
#include "lib/dataplane/worker/worker.h"

static void
mirror_clone(
	struct dp_worker *worker,
	struct packet *packet,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front,
	void (*route)(
		struct module_ectx *,
		struct packet_front *,
		struct device_entry_ectx *,
		struct packet *
	),
	struct device_entry_ectx *entry_ectx,
	uint16_t device_id
) {
	struct packet *clone = worker_clone_packet(
		worker, packet, module_ectx->packet_recirc_limit
	);
	if (clone == NULL) {
		return;
	}

	clone->tx_device_id = device_id;
	route(module_ectx, packet_front, entry_ectx, clone);
}

static void
mirror_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
) {
	struct mirror_module_config *mirror_config = container_of(
		module_ectx->abs_cp_module,
		struct mirror_module_config,
		cp_module
	);
	struct mirror_prepared *prepared =
		ADDR_OF(&module_ectx->module_prepared);

	// The worker force-polls every module each tick, so an empty front here
	// is normal. Zero-sizing the arrays below is undefined behavior.
	uint64_t count = packet_front_input_count(packet_front);
	if (count == 0) {
		return;
	}

	struct packet *packets[count];
	uint32_t results[count];

	uint64_t collect_idx = 0;
	for (struct packet *packet = packet_list_first(&packet_front->input);
	     packet != NULL;
	     packet = packet->next) {
		packets[collect_idx++] = packet;
	}

	// The classification is the linked classifier object's: the public
	// handler of the object type runs the bundle the object's subtype
	// selected, over the whole batch at once. A context whose binding
	// resolved nothing classifies every packet to no rule.
	classifier_object_classify(
		prepared->classifier,
		prepared->device_class,
		(const struct packet **)packets,
		results,
		count
	);

	struct packet *packet;
	uint64_t idx = 0;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		struct mirror_target *target = NULL;

		uint32_t action = results[idx++];

		// The decision of the matched rule resolves the result; the
		// bound guards a ruleset that changed under a module the
		// owner did not rebuild, by mirroring nothing for the rule
		// indices that no longer resolve.
		if (action != CLASSIFY_RULE_INVALID &&
		    action < mirror_config->target_count) {
			target = ADDR_OF(&mirror_config->targets) + action;
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

			if (device_target != NULL) {
				if (target->mode == MIRROR_MODE_IN) {
					mirror_clone(
						dp_worker,
						packet,
						module_ectx,
						packet_front,
						module_ectx_route_input,
						device_target->abs_input_entry,
						device_target->device_id
					);
				} else if (target->mode == MIRROR_MODE_OUT) {
					mirror_clone(
						dp_worker,
						packet,
						module_ectx,
						packet_front,
						module_ectx_route_output,
						device_target->abs_output_entry,
						device_target->device_id
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

// Resolves the module's binding to the classifier object: the linked
// object's prepared classification handles, and the class of the
// executing device in the object's device attribute - the device the
// link pass identified through the object's device names.
static void
mirror_module_commit_ectx(
	struct module_ectx *module_ectx, struct cp_module *cp_module
) {
	struct mirror_module_config *config =
		container_of(cp_module, struct mirror_module_config, cp_module);
	struct mirror_prepared *prepared =
		ADDR_OF(&module_ectx->module_prepared);

	prepared->classifier = NULL;
	prepared->device_class = 0;

	if (config->classifier_link_idx == MIRROR_CLASSIFIER_LINK_NONE) {
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
mirror_module_commit(struct dp_config *dp_config, struct cp_module *cp_module) {
	(void)dp_config;
	(void)cp_module;
}

struct module *
new_module_mirror() {
	struct mirror_module *module =
		(struct mirror_module *)malloc(sizeof(struct mirror_module));

	if (module == NULL) {
		return NULL;
	}

	// The loader copies every field of the returned descriptor, so
	// heap garbage must not survive in the ones this constructor
	// leaves unset.
	memset(module, 0, sizeof(*module));

	snprintf(
		module->module.name, sizeof(module->module.name), "%s", "mirror"
	);
	module->module.handler = mirror_handle_packets;
	module->module.commit_handler = mirror_module_commit;
	module->module.commit_ectx_handler = mirror_module_commit_ectx;
	module->module.prepared_size = sizeof(struct mirror_prepared);

	return &module->module;
}
