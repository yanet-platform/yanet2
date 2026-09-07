#include "controlplane.h"

#include "config.h"

#include <stdlib.h>
#include <string.h>

#include <lib/filter/compiler.h>

#include "common/container_of.h"
#include "common/memory.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_module.h"

#include "objects/l3b/api/l3b_virtual_service_object.h"

// Compiler counterpart of the dataplane query signature (see process.h).
// Destination filter: destination network + protocol.
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

	config->destination_filter_rule_count = 0;
	SET_OFFSET_OF(&config->rule_object_links, NULL);

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
	uint64_t *rule_object_links = ADDR_OF(&config->rule_object_links);
	if (rule_object_links != NULL) {
		memory_bfree(
			memory_context,
			rule_object_links,
			sizeof(uint64_t) * config->destination_filter_rule_count
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
	yanet_error **err
) {
	struct module_config *config =
		container_of(cp_module, struct module_config, cp_module);
	struct memory_context *memory_context = &cp_module->memory_context;

	// Link the virtual service each rule names and record the per-rule link
	// index; rules naming the same service share one link, and the
	// dataplane resolves the link at execution time to reach the object.
	//
	// The module is freshly constructed, so no earlier links exist.
	if (destination_filter_rule_count > 0) {
		uint64_t *link_array = (uint64_t *)memory_balloc(
			memory_context,
			sizeof(uint64_t) * destination_filter_rule_count
		);
		if (link_array == NULL) {
			yanet_error_add(
				err, "failed to allocate rule object links"
			);
			return -1;
		}

		for (uint32_t idx = 0; idx < destination_filter_rule_count;
		     ++idx) {
			if (cp_module_link_object(
				    cp_module,
				    L3B_VIRTUAL_SERVICE_OBJECT_TYPE,
				    destination_filter_rules[idx]
					    .virtual_service,
				    &link_array[idx],
				    err
			    )) {
				memory_bfree(
					memory_context,
					link_array,
					sizeof(uint64_t) *
						destination_filter_rule_count
				);
				return -1;
			}
		}
		SET_OFFSET_OF(&config->rule_object_links, link_array);
	} else {
		SET_OFFSET_OF(&config->rule_object_links, NULL);
	}
	config->destination_filter_rule_count = destination_filter_rule_count;

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
