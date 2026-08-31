#include "controlplane.h"

#include "config.h"

#include <stdlib.h>
#include <string.h>

#include <lib/filter/compiler.h>

#include "common/container_of.h"
#include "common/memory.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_module.h"

// Compiler counterparts of the dataplane query signatures (see process.h).
// Source filter: source network + destination (service) port.
FILTER_COMPILER_DECLARE(L3B_SOURCE_FILTER_IP4_TAG, net4_src, port_dst);
FILTER_COMPILER_DECLARE(L3B_SOURCE_FILTER_IP6_TAG, net6_src, port_dst);
// Destination filter: destination network + protocol, action is the virtual
// service index.
FILTER_COMPILER_DECLARE(L3B_DESTINATION_FILTER_IP4_TAG, net4_dst, proto_range);
FILTER_COMPILER_DECLARE(L3B_DESTINATION_FILTER_IP6_TAG, net6_dst, proto_range);

struct cp_module *
l3b_module_config_new(
	struct agent *agent, const char *name, yanet_error **error
) {
	struct module_config *config = (struct module_config *)memory_balloc(
		&agent->memory_context, sizeof(struct module_config)
	);
	if (config == NULL) {
		yanet_error_add(error, "failed to allocate config");
		return NULL;
	}

	if (cp_module_init(&config->cp_module, agent, "l3b", name, error)) {
		yanet_error_add(error, "failed to init module");
		memory_bfree(
			&agent->memory_context,
			config,
			sizeof(struct module_config)
		);
		return NULL;
	}

	config->virtual_service_count = 0;
	SET_OFFSET_OF(&config->virtual_service_links, NULL);

	config->virtual_service_index_count = 0;
	SET_OFFSET_OF(&config->virtual_service_indexes, NULL);

	memset(&config->filter_ip6, 0, sizeof(config->filter_ip6));
	memset(&config->filter_ip4, 0, sizeof(config->filter_ip4));

	return &config->cp_module;
}

static void
l3b_module_config_destroy(struct cp_module *cp_module) {
	struct module_config *config =
		container_of(cp_module, struct module_config, cp_module);

	filter_free(&config->filter_ip4, L3B_DESTINATION_FILTER_IP4_TAG);
	filter_free(&config->filter_ip6, L3B_DESTINATION_FILTER_IP6_TAG);

	struct memory_context *memory_context = &cp_module->memory_context;
	uint64_t *virtual_service_links =
		ADDR_OF(&config->virtual_service_links);
	if (virtual_service_links != NULL) {
		memory_bfree(
			memory_context,
			virtual_service_links,
			sizeof(uint64_t) * config->virtual_service_count
		);
	}

	uint32_t *virtual_service_indexes =
		ADDR_OF(&config->virtual_service_indexes);
	if (virtual_service_indexes != NULL) {
		memory_bfree(
			memory_context,
			virtual_service_indexes,
			sizeof(uint32_t) * config->virtual_service_index_count
		);
	}

	// Capture agent before fini zeroes it.
	struct agent *agent = ADDR_OF(&config->cp_module.agent);

	cp_module_fini(&config->cp_module);
	memory_bfree(
		&agent->memory_context, config, sizeof(struct module_config)
	);
}

int
l3b_module_config_free(struct cp_module *cp_module, yanet_error **err) {
	if (cp_module_try_destroy(cp_module, err)) {
		return -1;
	}

	l3b_module_config_destroy(cp_module);
	return 0;
}

// Translate the source filter rules into classifier filter_rule descriptors.
static void
make_source_filter_rules(
	const struct l3b_source_filter_rule *source_filter_rules,
	uint32_t source_filter_rule_count,
	struct filter_rule *filter_rules
) {
	for (uint32_t idx = 0; idx < source_filter_rule_count; ++idx) {
		const struct l3b_source_filter_rule *rule =
			&source_filter_rules[idx];
		struct filter_rule *filter_rule = &filter_rules[idx];

		filter_rule->net6.src_count = rule->net6s.count;
		filter_rule->net6.srcs = rule->net6s.items;
		filter_rule->net4.src_count = rule->net4s.count;
		filter_rule->net4.srcs = rule->net4s.items;
		filter_rule->transport.dst_count = rule->port_ranges.count;
		filter_rule->transport.dsts = rule->port_ranges.items;
		filter_rule->action = 0;
	}
}

// Translate the destination filter rules into classifier filter_rule
// descriptors. The filter query returns the matched rule's index; the mapping
// to a virtual service index is kept separately in the module config.
static void
make_destination_filter_rules(
	const struct l3b_destination_filter_rule *destination_filter_rules,
	uint32_t destination_filter_rule_count,
	struct filter_rule *filter_rules
) {
	for (uint32_t idx = 0; idx < destination_filter_rule_count; ++idx) {
		const struct l3b_destination_filter_rule *rule =
			&destination_filter_rules[idx];
		struct filter_rule *filter_rule = &filter_rules[idx];

		filter_rule->net6.dst_count = rule->net6s.count;
		filter_rule->net6.dsts = rule->net6s.items;
		filter_rule->net4.dst_count = rule->net4s.count;
		filter_rule->net4.dsts = rule->net4s.items;
		filter_rule->transport.proto_count = rule->proto_ranges.count;
		filter_rule->transport.protos = rule->proto_ranges.items;
	}
}

// Build a heap array of pointers to filter_rules for filter_init. Returns NULL
// when count is zero (filter_init accepts a NULL rule list).
static const struct filter_rule **
make_filter_rule_ptrs(
	const struct filter_rule *filter_rules, uint32_t rule_count
) {
	if (rule_count == 0) {
		return NULL;
	}

	const struct filter_rule **ptrs = malloc(sizeof(*ptrs) * rule_count);
	if (ptrs == NULL) {
		return NULL;
	}

	for (uint32_t idx = 0; idx < rule_count; ++idx) {
		ptrs[idx] = &filter_rules[idx];
	}
	return ptrs;
}

// Compile both per-family source filters of a virtual service. On failure any
// partially built filter is released.
static int
build_source_filters(
	struct virtual_service *virtual_service,
	const struct l3b_source_filter_rule *source_filter_rules,
	uint32_t source_filter_rule_count,
	struct memory_context *memory_context,
	yanet_error **err
) {
	struct filter_rule *filter_rules =
		calloc(source_filter_rule_count, sizeof(struct filter_rule));
	if (filter_rules == NULL && source_filter_rule_count > 0) {
		yanet_error_add(err, "failed to allocate source filter rules");
		return -1;
	}

	make_source_filter_rules(
		source_filter_rules, source_filter_rule_count, filter_rules
	);

	const struct filter_rule **filter_rule_ptrs =
		make_filter_rule_ptrs(filter_rules, source_filter_rule_count);

	int rc = -1;
	if (filter_rule_ptrs == NULL && source_filter_rule_count > 0) {
		yanet_error_add(
			err, "failed to allocate source filter rule ptrs"
		);
		goto out;
	}

	if (filter_init(
		    &virtual_service->filter_ip4,
		    L3B_SOURCE_FILTER_IP4_TAG,
		    filter_rule_ptrs,
		    source_filter_rule_count,
		    memory_context,
		    "source_filter_ip4",
		    err
	    )) {
		yanet_error_add(err, "failed to init source filter_ip4");
		goto out;
	}

	if (filter_init(
		    &virtual_service->filter_ip6,
		    L3B_SOURCE_FILTER_IP6_TAG,
		    filter_rule_ptrs,
		    source_filter_rule_count,
		    memory_context,
		    "source_filter_ip6",
		    err
	    )) {
		yanet_error_add(err, "failed to init source filter_ip6");
		filter_free(
			&virtual_service->filter_ip4, L3B_SOURCE_FILTER_IP4_TAG
		);
		goto out;
	}

	rc = 0;

out:
	free(filter_rule_ptrs);
	free(filter_rules);
	return rc;
}

struct cp_object *
l3b_virtual_service_create(
	struct agent *agent,
	const char *name,
	const struct l3b_virtual_service *virtual_service,
	yanet_error **err
) {
	struct l3b_virtual_service_object *object =
		(struct l3b_virtual_service_object *)memory_balloc(
			&agent->memory_context,
			sizeof(struct l3b_virtual_service_object)
		);
	if (object == NULL) {
		yanet_error_add(
			err, "failed to allocate virtual service object"
		);
		return NULL;
	}

	if (cp_object_init(
		    &object->cp_object,
		    agent,
		    L3B_VIRTUAL_SERVICE_OBJECT_TYPE,
		    name,
		    err
	    )) {
		yanet_error_add(err, "failed to init virtual service object");
		memory_bfree(
			&agent->memory_context,
			object,
			sizeof(struct l3b_virtual_service_object)
		);
		return NULL;
	}

	// Every allocation below is attributed to the object's own memory
	// context so the accounting travels with the object.
	struct memory_context *memory_context =
		&object->cp_object.memory_context;
	struct virtual_service *vs = &object->virtual_service;

	vs->scheduler_hash_mask = virtual_service->hash_mask;
	vs->scheduler_index_mask = virtual_service->index_mask;
	vs->real_server_count = virtual_service->real_server_count;

	// Backends.
	if (virtual_service->real_server_count > 0) {
		struct real_server *real_servers =
			(struct real_server *)memory_balloc(
				memory_context,
				sizeof(struct real_server) *
					virtual_service->real_server_count
			);
		if (real_servers == NULL) {
			yanet_error_add(err, "failed to allocate real servers");
			goto error_vs;
		}

		for (uint32_t idx = 0; idx < virtual_service->real_server_count;
		     ++idx) {
			real_servers[idx].type =
				virtual_service->real_servers[idx].type;
			real_servers[idx].destination_addr =
				virtual_service->real_servers[idx]
					.destination_addr;
			real_servers[idx].source_net =
				virtual_service->real_servers[idx].source_net;
			real_servers[idx].state = real_state_enabled;
		}
		SET_OFFSET_OF(&vs->real_servers, real_servers);
	}

	// Real server ring: allocate capacity slots, count starts empty and is
	// populated later via l3b_virtual_service_update_ring.
	uint32_t ring_capacity = virtual_service->ring_capacity;
	if (ring_capacity > 0) {
		uint32_t *server_indexes = (uint32_t *)memory_balloc(
			memory_context, sizeof(uint32_t) * ring_capacity
		);
		if (server_indexes == NULL) {
			yanet_error_add(
				err, "failed to allocate real server ring"
			);
			goto error_real_servers;
		}
		SET_OFFSET_OF(&vs->real_ring.server_indexes, server_indexes);
	}
	vs->real_ring.capacity = ring_capacity;
	vs->real_ring.count = 0;

	// Per-service source filters.
	if (build_source_filters(
		    vs,
		    virtual_service->source_filter_rules,
		    virtual_service->source_filter_rule_count,
		    memory_context,
		    err
	    )) {
		goto error_ring;
	}

	return &object->cp_object;

error_ring:
	if (vs->real_ring.capacity > 0) {
		memory_bfree(
			memory_context,
			ADDR_OF(&vs->real_ring.server_indexes),
			sizeof(uint32_t) * vs->real_ring.capacity
		);
	}

error_real_servers:
	if (vs->real_server_count > 0) {
		memory_bfree(
			memory_context,
			ADDR_OF(&vs->real_servers),
			sizeof(struct real_server) * vs->real_server_count
		);
	}

error_vs:
	cp_object_fini(&object->cp_object);
	memory_bfree(
		&agent->memory_context,
		object,
		sizeof(struct l3b_virtual_service_object)
	);
	return NULL;
}

static void
l3b_virtual_service_object_destroy(struct cp_object *cp_object) {
	struct l3b_virtual_service_object *object = container_of(
		cp_object, struct l3b_virtual_service_object, cp_object
	);
	struct virtual_service *vs = &object->virtual_service;
	struct memory_context *memory_context = &cp_object->memory_context;

	filter_free(&vs->filter_ip4, L3B_SOURCE_FILTER_IP4_TAG);
	filter_free(&vs->filter_ip6, L3B_SOURCE_FILTER_IP6_TAG);

	if (vs->real_ring.capacity > 0) {
		memory_bfree(
			memory_context,
			ADDR_OF(&vs->real_ring.server_indexes),
			sizeof(uint32_t) * vs->real_ring.capacity
		);
	}

	if (vs->real_server_count > 0) {
		memory_bfree(
			memory_context,
			ADDR_OF(&vs->real_servers),
			sizeof(struct real_server) * vs->real_server_count
		);
	}

	// Capture agent before fini zeroes it.
	struct agent *agent = ADDR_OF(&cp_object->agent);

	cp_object_fini(cp_object);
	memory_bfree(
		&agent->memory_context,
		object,
		sizeof(struct l3b_virtual_service_object)
	);
}

int
l3b_virtual_service_free(struct cp_object *cp_object, yanet_error **err) {
	if (cp_object_try_destroy(cp_object, err)) {
		return -1;
	}

	l3b_virtual_service_object_destroy(cp_object);
	return 0;
}

static struct virtual_service *
l3b_virtual_service_of(struct cp_object *cp_object) {
	return &container_of(
			cp_object, struct l3b_virtual_service_object, cp_object
	)
			->virtual_service;
}

int
l3b_virtual_service_update_ring(
	struct cp_object *cp_object,
	const uint32_t *server_indexes,
	uint32_t server_index_count,
	yanet_error **err
) {
	struct virtual_service *virtual_service =
		l3b_virtual_service_of(cp_object);
	if (server_index_count > virtual_service->real_ring.capacity) {
		yanet_error_add(err, "ring count exceeds capacity");
		return -1;
	}

	for (uint32_t idx = 0; idx < server_index_count; ++idx) {
		if (server_indexes[idx] >= virtual_service->real_server_count) {
			yanet_error_add(err, "invalid real server index");
			return -1;
		}
	}

	uint32_t *ring_indexes =
		ADDR_OF(&virtual_service->real_ring.server_indexes);
	for (uint32_t idx = 0; idx < server_index_count; ++idx) {
		ring_indexes[idx] = server_indexes[idx];
	}

	// Release the count after the index writes so the dataplane, on
	// acquiring it, observes the populated indexes.
	__atomic_store_n(
		&virtual_service->real_ring.count,
		server_index_count,
		__ATOMIC_RELEASE
	);
	return 0;
}

int
l3b_virtual_service_set_real_server_state(
	struct cp_object *cp_object,
	uint32_t real_server_index,
	bool enabled,
	yanet_error **err
) {
	struct virtual_service *virtual_service =
		l3b_virtual_service_of(cp_object);
	if (real_server_index >= virtual_service->real_server_count) {
		yanet_error_add(err, "invalid real server index");
		return -1;
	}

	struct real_server *real_servers =
		ADDR_OF(&virtual_service->real_servers);
	real_servers[real_server_index].state =
		enabled ? real_state_enabled : real_state_disabled;
	return 0;
}

// Compile both per-family destination filters of the module config. On failure
// any partially built filter is released.
static int
build_destination_filters(
	struct module_config *config,
	const struct l3b_destination_filter_rule *destination_filter_rules,
	uint32_t destination_filter_rule_count,
	struct memory_context *memory_context,
	yanet_error **err
) {
	struct filter_rule *filter_rules =
		calloc(destination_filter_rule_count,
		       sizeof(struct filter_rule));
	if (filter_rules == NULL && destination_filter_rule_count > 0) {
		yanet_error_add(
			err, "failed to allocate destination filter rules"
		);
		return -1;
	}

	make_destination_filter_rules(
		destination_filter_rules,
		destination_filter_rule_count,
		filter_rules
	);

	const struct filter_rule **filter_rule_ptrs = make_filter_rule_ptrs(
		filter_rules, destination_filter_rule_count
	);

	int rc = -1;
	if (filter_rule_ptrs == NULL && destination_filter_rule_count > 0) {
		yanet_error_add(
			err, "failed to allocate destination filter ptrs"
		);
		goto out;
	}

	if (filter_init(
		    &config->filter_ip4,
		    L3B_DESTINATION_FILTER_IP4_TAG,
		    filter_rule_ptrs,
		    destination_filter_rule_count,
		    memory_context,
		    "destination_filter_ip4",
		    err
	    )) {
		yanet_error_add(err, "failed to init destination filter_ip4");
		goto out;
	}

	if (filter_init(
		    &config->filter_ip6,
		    L3B_DESTINATION_FILTER_IP6_TAG,
		    filter_rule_ptrs,
		    destination_filter_rule_count,
		    memory_context,
		    "destination_filter_ip6",
		    err
	    )) {
		yanet_error_add(err, "failed to init destination filter_ip6");
		filter_free(
			&config->filter_ip4, L3B_DESTINATION_FILTER_IP4_TAG
		);
		goto out;
	}

	rc = 0;

out:
	free(filter_rule_ptrs);
	free(filter_rules);
	return rc;
}

int
l3b_module_config_update(
	struct cp_module *cp_module,
	const struct l3b_destination_filter_rule *destination_filter_rules,
	uint32_t destination_filter_rule_count,
	const char *const *service_names,
	uint32_t service_count,
	yanet_error **err
) {
	struct module_config *config =
		container_of(cp_module, struct module_config, cp_module);
	struct memory_context *memory_context = &cp_module->memory_context;

	// Link each named virtual service object and record the link index in
	// service-slot order; the dataplane resolves the link at execution
	// time to reach the object.
	//
	// The module is freshly constructed, so no earlier links exist.
	if (service_count > 0) {
		uint64_t *link_array = (uint64_t *)memory_balloc(
			memory_context, sizeof(uint64_t) * service_count
		);
		if (link_array == NULL) {
			yanet_error_add(
				err, "failed to allocate virtual service links"
			);
			return -1;
		}

		for (uint32_t idx = 0; idx < service_count; ++idx) {
			if (cp_module_link_object(
				    cp_module,
				    L3B_VIRTUAL_SERVICE_OBJECT_TYPE,
				    service_names[idx],
				    &link_array[idx],
				    err
			    )) {
				memory_bfree(
					memory_context,
					link_array,
					sizeof(uint64_t) * service_count
				);
				return -1;
			}
		}
		SET_OFFSET_OF(&config->virtual_service_links, link_array);
	} else {
		SET_OFFSET_OF(&config->virtual_service_links, NULL);
	}
	config->virtual_service_count = service_count;

	// Map each destination filter rule index to its virtual service index;
	// the filter query returns the rule index, the dataplane looks the
	// virtual service up through this array.
	if (destination_filter_rule_count > 0) {
		uint32_t *virtual_service_indexes = (uint32_t *)memory_balloc(
			memory_context,
			sizeof(uint32_t) * destination_filter_rule_count
		);
		if (virtual_service_indexes == NULL) {
			yanet_error_add(
				err,
				"failed to allocate virtual service indexes"
			);
			return -1;
		}

		for (uint32_t idx = 0; idx < destination_filter_rule_count;
		     ++idx) {
			virtual_service_indexes[idx] =
				destination_filter_rules[idx]
					.virtual_service_index;
		}
		SET_OFFSET_OF(
			&config->virtual_service_indexes,
			virtual_service_indexes
		);
	} else {
		SET_OFFSET_OF(&config->virtual_service_indexes, NULL);
	}
	config->virtual_service_index_count = destination_filter_rule_count;

	if (build_destination_filters(
		    config,
		    destination_filter_rules,
		    destination_filter_rule_count,
		    memory_context,
		    err
	    )) {
		return -1;
	}

	return 0;
}
