#include "controlplane.h"

#include "config.h"

#include <stdlib.h>

#include "lib/classify/compiler/helper.h"
#include "lib/classify/compiler/net4.h"
#include "lib/classify/compiler/net6.h"
#include "lib/classify/compiler/u16_ranges.h"

#include "common/container_of.h"
#include "common/memory.h"
#include "common/network_mask.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_module.h"
#include "lib/controlplane/config/zone.h"
#include "lib/counters/counters.h"

#include "objects/l3b/api/l3b_virtual_service_object.h"

/*
 * Destination attributes borrow their networks and ranges from module rules.
 */
static inline void
l3b_rule_get_net4_dsts(
	const struct classifier_rule *rule, struct filter_net4s *nets
) {
	const struct l3b_destination_filter_rule *destination_rule =
		container_of(rule, struct l3b_destination_filter_rule, rule);
	*nets = destination_rule->net4s;
}

static inline void
l3b_rule_get_net6_dsts(
	const struct classifier_rule *rule, struct filter_net6s *nets
) {
	const struct l3b_destination_filter_rule *destination_rule =
		container_of(rule, struct l3b_destination_filter_rule, rule);
	*nets = destination_rule->net6s;
}

static inline void
l3b_rule_get_proto_ranges(
	const struct classifier_rule *rule, struct filter_u16_ranges *ranges
) {
	const struct l3b_destination_filter_rule *destination_rule =
		container_of(rule, struct l3b_destination_filter_rule, rule);
	ranges->count = destination_rule->proto_ranges.count;
	ranges->items = (const struct filter_u16_span *)
				destination_rule->proto_ranges.items;
}

CLASSIFY_NET4_COMPILE(l3b_net4_dst, l3b_rule_get_net4_dsts)
CLASSIFY_NET6_COMPILE(l3b_net6_dst, l3b_rule_get_net6_dsts)
CLASSIFY_U16_RANGES_COMPILE(
	l3b_proto, struct classify_attr_port, l3b_rule_get_proto_ranges
)

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
	SET_OFFSET_OF(&config->rule_link_counter_ids, NULL);

	// Zero initialization makes partial-construction cleanup safe.
	memset(&config->classifier_ip6, 0, sizeof(config->classifier_ip6));
	memset(&config->classifier_ip4, 0, sizeof(config->classifier_ip4));

	return &config->cp_module;
}

// Releases one family destination classifier; safe on a zeroed one.
static void
l3b_destination_classifier_ip4_free(
	struct memory_context *memory_context,
	struct l3b_destination_classifier_ip4 *cls
) {
	classify_attr_net4_free(memory_context, &cls->dst_attr);
	classify_attr_port_free(memory_context, &cls->proto_attr);
	value_table_free(&cls->root_joint);
	vline_free(&cls->rule_map);
	memset(cls, 0, sizeof(*cls));
}

static void
l3b_destination_classifier_ip6_free(
	struct memory_context *memory_context,
	struct l3b_destination_classifier_ip6 *cls
) {
	classify_attr_net6_free(memory_context, &cls->dst_attr);
	classify_attr_port_free(memory_context, &cls->proto_attr);
	value_table_free(&cls->root_joint);
	vline_free(&cls->rule_map);
	memset(cls, 0, sizeof(*cls));
}

static void
l3b_module_config_destroy(struct cp_module *cp_module) {
	struct module_config *config =
		container_of(cp_module, struct module_config, cp_module);

	struct memory_context *memory_context =
		&cp_module->entity.memory_context;

	l3b_destination_classifier_ip4_free(
		memory_context, &config->classifier_ip4
	);
	l3b_destination_classifier_ip6_free(
		memory_context, &config->classifier_ip6
	);

	uint64_t *rule_object_links = ADDR_OF(&config->rule_object_links);
	if (rule_object_links != NULL) {
		memory_bfree(
			memory_context,
			rule_object_links,
			sizeof(uint64_t) * config->destination_filter_rule_count
		);
	}

	uint64_t *rule_link_counter_ids =
		ADDR_OF(&config->rule_link_counter_ids);
	if (rule_link_counter_ids != NULL) {
		memory_bfree(
			memory_context,
			rule_link_counter_ids,
			sizeof(uint64_t) * config->destination_filter_rule_count
		);
	}

	// Capture agent before fini zeroes it.
	struct agent *agent = ADDR_OF(&config->cp_module.entity.agent);

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

	struct agent *owner = ADDR_OF(&cp_module->entity.agent);

	l3b_module_config_destroy(cp_module);
	agent_destroy_released(owner);
	return 0;
}

// Missing-family networks and empty protocol ranges must never match.
//
// Excluded rules keep their slots so decoded indices retain rule order.
static const struct classifier_rule **
l3b_rule_ptrs_project(
	const struct l3b_destination_filter_rule *destination_filter_rules,
	uint32_t rule_count,
	bool family_is_ip4
) {
	const struct classifier_rule **rule_ptrs =
		(const struct classifier_rule **)malloc(
			sizeof(struct classifier_rule *) *
			(rule_count ? rule_count : 1)
		);
	if (rule_ptrs == NULL) {
		return NULL;
	}

	for (uint32_t idx = 0; idx < rule_count; ++idx) {
		const struct filter_net4s *net4s =
			&destination_filter_rules[idx].net4s;
		const struct filter_net6s *net6s =
			&destination_filter_rules[idx].net6s;
		if (destination_filter_rules[idx].proto_ranges.count == 0) {
			rule_ptrs[idx] = NULL;
		} else if (family_is_ip4) {
			rule_ptrs[idx] =
				net4s->count > 0
					? &destination_filter_rules[idx].rule
					: NULL;
		} else {
			rule_ptrs[idx] =
				net6s->count > 0
					? &destination_filter_rules[idx].rule
					: NULL;
		}
	}

	return rule_ptrs;
}

/*
 * Destination classification combines network and transport restrictions
 * within one address family while preserving rule order.
 *
 * Persistent outputs belong to the module config, whose destruction
 * releases partial construction after failure. Temporary registries and
 * rule mappings are released here on both success and failure.
 */
static int
l3b_module_init_destination(
	struct module_config *config,
	const struct l3b_destination_filter_rule *destination_filter_rules,
	uint32_t rule_count,
	bool family_is_ip4,
	yanet_error **err
) {
	struct memory_context *memory_context =
		&config->cp_module.entity.memory_context;
	const struct classifier_rule **rule_ptrs = l3b_rule_ptrs_project(
		destination_filter_rules, rule_count, family_is_ip4
	);

	struct classifier stage_net = {0};
	struct classifier stage_proto = {0};
	struct classifier stage_destination = {0};

	int rc = -1;

	if (rule_ptrs == NULL) {
		yanet_error_add(
			err, "failed to allocate destination rule ptrs"
		);
		return -1;
	}

	if (family_is_ip4) {
		struct l3b_destination_classifier_ip4 *cls =
			&config->classifier_ip4;
		if (classify_l3b_net4_dst_compile(
			    memory_context,
			    "ip4:net4_dst",
			    rule_ptrs,
			    rule_count,
			    &cls->dst_attr,
			    &stage_net
		    )) {
			goto error;
		}
	} else {
		struct l3b_destination_classifier_ip6 *cls =
			&config->classifier_ip6;
		if (classify_l3b_net6_dst_compile(
			    memory_context,
			    "ip6:net6_dst",
			    rule_ptrs,
			    rule_count,
			    &cls->dst_attr,
			    &stage_net
		    )) {
			goto error;
		}
	}

	if (classify_l3b_proto_compile(
		    memory_context,
		    family_is_ip4 ? "ip4:ipproto" : "ip6:ipproto",
		    rule_ptrs,
		    rule_count,
		    family_is_ip4 ? &config->classifier_ip4.proto_attr
				  : &config->classifier_ip6.proto_attr,
		    &stage_proto
	    )) {
		goto error;
	}

	if (classify_join(
		    memory_context,
		    family_is_ip4 ? "ip4:root_joint" : "ip6:root_joint",
		    &stage_net,
		    &stage_proto,
		    rule_count,
		    family_is_ip4 ? &config->classifier_ip4.root_joint
				  : &config->classifier_ip6.root_joint,
		    &stage_destination
	    )) {
		goto error;
	}

	if (classify_decode(
		    memory_context,
		    family_is_ip4 ? "ip4:rules" : "ip6:rules",
		    &stage_destination,
		    rule_ptrs,
		    rule_count,
		    family_is_ip4 ? &config->classifier_ip4.rule_map
				  : &config->classifier_ip6.rule_map
	    )) {
		goto error;
	}

	rc = 0;

error:
	classifier_fini(&stage_net, memory_context, rule_count);
	classifier_fini(&stage_proto, memory_context, rule_count);
	classifier_fini(&stage_destination, memory_context, rule_count);
	free(rule_ptrs);

	if (rc != 0) {
		yanet_error_add(
			err, "failed to compile destination classifier"
		);
	}
	return rc;
}

// Copy the service's packet-counter ID while its published generation is
// pinned.
static uint64_t
l3b_linked_service_counter_id(
	const struct cp_module *cp_module, const char *name
) {
	struct agent *agent = ADDR_OF(&cp_module->entity.agent);
	if (agent == NULL) {
		return COUNTER_INVALID;
	}

	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);
	struct cp_config_gen *config_gen = cp_config_gen_acquire(cp_config);
	uint64_t counter_id = COUNTER_INVALID;
	{
		struct cp_object *linked = cp_object_registry_lookup(
			&config_gen->object_registry,
			L3B_VIRTUAL_SERVICE_OBJECT_TYPE,
			name
		);
		if (linked != NULL) {
			counter_id = counter_registry_lookup_index(
				&linked->link_counter_registry,
				L3B_LINK_COUNTER_PACKETS
			);
		}
	}
	cp_config_gen_release(cp_config, config_gen);
	return counter_id;
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
	struct memory_context *memory_context =
		&cp_module->entity.memory_context;

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

		uint64_t *counter_id_array = (uint64_t *)memory_balloc(
			memory_context,
			sizeof(uint64_t) * destination_filter_rule_count
		);
		if (counter_id_array == NULL) {
			yanet_error_add(
				err, "failed to allocate rule counter ids"
			);
			memory_bfree(
				memory_context,
				link_array,
				sizeof(uint64_t) * destination_filter_rule_count
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
					counter_id_array,
					sizeof(uint64_t) *
						destination_filter_rule_count
				);
				memory_bfree(
					memory_context,
					link_array,
					sizeof(uint64_t) *
						destination_filter_rule_count
				);
				return -1;
			}

			// Resolve the service's link packets counter in the
			// live generation's registry: ids are stable across
			// the per-generation registry copies, so the
			// per-worker link storages spawned at install time
			// answer them.
			counter_id_array[idx] = l3b_linked_service_counter_id(
				cp_module,
				destination_filter_rules[idx].virtual_service
			);
		}
		SET_OFFSET_OF(&config->rule_object_links, link_array);
		SET_OFFSET_OF(&config->rule_link_counter_ids, counter_id_array);
	} else {
		SET_OFFSET_OF(&config->rule_object_links, NULL);
		SET_OFFSET_OF(&config->rule_link_counter_ids, NULL);
	}
	config->destination_filter_rule_count = destination_filter_rule_count;

	// Reject unrepresentable masks before classifier construction.
	//
	// Earlier link and array allocations remain owned by the module config.
	for (uint32_t rule_idx = 0; rule_idx < destination_filter_rule_count;
	     ++rule_idx) {
		const struct l3b_destination_filter_rule *destination_rule =
			&destination_filter_rules[rule_idx];
		if (!network_net4_masks_are_valid(
			    destination_rule->net4s.items,
			    destination_rule->net4s.count
		    )) {
			yanet_error_add(
				err,
				"destination filter rule %u has a "
				"non-contiguous IPv4 network mask",
				rule_idx
			);
			return -1;
		}
		if (!network_net6_masks_are_valid(
			    destination_rule->net6s.items,
			    destination_rule->net6s.count
		    )) {
			yanet_error_add(
				err,
				"destination filter rule %u has an IPv6 "
				"network mask that is not bi-contiguous",
				rule_idx
			);
			return -1;
		}
	}

	// A ruleset without rules keeps the classifiers zeroed: the dataplane
	// queries them only while the rule count is positive.
	if (destination_filter_rule_count > 0) {
		if (l3b_module_init_destination(
			    config,
			    destination_filter_rules,
			    destination_filter_rule_count,
			    true,
			    err
		    ) ||
		    l3b_module_init_destination(
			    config,
			    destination_filter_rules,
			    destination_filter_rule_count,
			    false,
			    err
		    )) {
			return -1;
		}
	}

	return 0;
}
