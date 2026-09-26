#include <errno.h>

#include "controlplane.h"

#include "config.h"

#include "lib/classify/compiler/device.h"
#include "lib/classify/compiler/helper.h"
#include "lib/classify/compiler/line.h"
#include "lib/classify/compiler/net4.h"
#include "lib/classify/compiler/net6.h"

#include "common/container_of.h"
#include "lib/errors/errors.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_module.h"

static void
mirror_module_config_destroy(struct cp_module *cp_module) {
	struct mirror_module_config *config =
		container_of(cp_module, struct mirror_module_config, cp_module);
	struct memory_context *memory_context = &cp_module->memory_context;

	memory_bfree(
		memory_context,
		ADDR_OF(&config->targets),
		sizeof(struct mirror_target) * config->target_count
	);

	// Every free below is safe on a zeroed struct, so a partially
	// built config of a failed update walks the same total destroy.
	struct mirror_classifier_vlan *vlan = &config->classifier_vlan;
	classify_attr_device_free(memory_context, &vlan->dev_attr);
	classify_attr_line_free(memory_context, &vlan->vlan_attr);
	value_table_free(&vlan->joint);
	vline_free(&vlan->rule_map);
	memset(vlan, 0, sizeof(*vlan));

	struct mirror_classifier_ip4 *ip4 = &config->classifier_ip4;
	classify_attr_device_free(memory_context, &ip4->dev_attr);
	classify_attr_line_free(memory_context, &ip4->vlan_attr);
	classify_attr_net4_free(memory_context, &ip4->net4_src_attr);
	classify_attr_net4_free(memory_context, &ip4->net4_dst_attr);
	value_table_free(&ip4->dev_vlan_joint);
	value_table_free(&ip4->nets_joint);
	value_table_free(&ip4->root_joint);
	vline_free(&ip4->rule_map);
	memset(ip4, 0, sizeof(*ip4));

	struct mirror_classifier_ip6 *ip6 = &config->classifier_ip6;
	classify_attr_device_free(memory_context, &ip6->dev_attr);
	classify_attr_line_free(memory_context, &ip6->vlan_attr);
	classify_attr_net6_free(memory_context, &ip6->net6_src_attr);
	classify_attr_net6_free(memory_context, &ip6->net6_dst_attr);
	value_table_free(&ip6->dev_vlan_joint);
	value_table_free(&ip6->nets_joint);
	value_table_free(&ip6->root_joint);
	vline_free(&ip6->rule_map);
	memset(ip6, 0, sizeof(*ip6));

	// Capture agent before fini zeroes it.
	struct agent *agent = ADDR_OF(&cp_module->agent);

	cp_module_fini(cp_module);

	memory_bfree(
		&agent->memory_context,
		config,
		sizeof(struct mirror_module_config)
	);
}

struct cp_module *
mirror_module_config_init(
	struct agent *agent, const char *name, yanet_error **err
) {
	struct mirror_module_config *config =
		(struct mirror_module_config *)memory_balloc(
			&agent->memory_context,
			sizeof(struct mirror_module_config)
		);
	if (config == NULL) {
		yanet_error_add(err, "failed to allocate config");
		return NULL;
	}

	if (cp_module_init(&config->cp_module, agent, "mirror", name, err)) {
		yanet_error_add(err, "failed to init module");
		memory_bfree(
			&agent->memory_context,
			config,
			sizeof(struct mirror_module_config)
		);
		return NULL;
	}

	SET_OFFSET_OF(&config->targets, NULL);
	config->target_count = 0;

	// The classifiers are embedded by value with their decoders, so
	// the region is zeroed as a whole; every free of the destroy walk
	// is safe on a zeroed struct.
	memset(&config->classifier_vlan, 0, sizeof(config->classifier_vlan));
	memset(&config->classifier_ip4, 0, sizeof(config->classifier_ip4));
	memset(&config->classifier_ip6, 0, sizeof(config->classifier_ip6));

	return &config->cp_module;
}

int
mirror_module_config_free(struct cp_module *cp_module, yanet_error **err) {
	if (cp_module_try_destroy(cp_module, err)) {
		return -1;
	}

	mirror_module_config_destroy(cp_module);
	return 0;
}

typedef int (*mirror_rule_check_func)(const struct mirror_rule *mirror_rule);

/*
 * Field selectors of the mirror rule: the module rule is handed to the
 * classify compilers directly, every attribute through its getter.
 */
static inline void
mirror_rule_get_devices(
	const struct classifier_rule *rule, struct filter_devices *devices
) {
	const struct mirror_rule *mirror_rule =
		container_of(rule, struct mirror_rule, rule);
	*devices = mirror_rule->devices;
}

static inline void
mirror_rule_get_vlan_ranges(
	const struct classifier_rule *rule, struct classify_line_ranges *ranges
) {
	const struct mirror_rule *mirror_rule =
		container_of(rule, struct mirror_rule, rule);
	*ranges = mirror_rule->vlan_ranges;
}

static inline void
mirror_rule_get_net4_srcs(
	const struct classifier_rule *rule, struct filter_net4s *nets
) {
	const struct mirror_rule *mirror_rule =
		container_of(rule, struct mirror_rule, rule);
	*nets = mirror_rule->src_net4s;
}

static inline void
mirror_rule_get_net4_dsts(
	const struct classifier_rule *rule, struct filter_net4s *nets
) {
	const struct mirror_rule *mirror_rule =
		container_of(rule, struct mirror_rule, rule);
	*nets = mirror_rule->dst_net4s;
}

static inline void
mirror_rule_get_net6_srcs(
	const struct classifier_rule *rule, struct filter_net6s *nets
) {
	const struct mirror_rule *mirror_rule =
		container_of(rule, struct mirror_rule, rule);
	*nets = mirror_rule->src_net6s;
}

static inline void
mirror_rule_get_net6_dsts(
	const struct classifier_rule *rule, struct filter_net6s *nets
) {
	const struct mirror_rule *mirror_rule =
		container_of(rule, struct mirror_rule, rule);
	*nets = mirror_rule->dst_net6s;
}

CLASSIFY_DEVICE_COMPILE(mirror_device, mirror_rule_get_devices)
CLASSIFY_LINE_COMPILE(
	mirror_vlan,
	struct classify_attr_line,
	4096,
	mirror_rule_get_vlan_ranges
)
CLASSIFY_NET4_COMPILE(mirror_net4_src, mirror_rule_get_net4_srcs)
CLASSIFY_NET4_COMPILE(mirror_net4_dst, mirror_rule_get_net4_dsts)
CLASSIFY_NET6_COMPILE(mirror_net6_src, mirror_rule_get_net6_srcs)
CLASSIFY_NET6_COMPILE(mirror_net6_dst, mirror_rule_get_net6_dsts)

// Spells a projection of the ruleset: every rule the check rejects
// keeps a NULL slot, so the compile stages skip it.
static uint32_t
project_mirror_rules(
	struct mirror_rule *mirror_rules,
	uint32_t mirror_rule_count,
	const struct classifier_rule **rule_ptrs,
	mirror_rule_check_func check
) {
	uint32_t rule_idx = 0;
	for (uint32_t idx = 0; idx < mirror_rule_count; ++idx) {
		if (!check(mirror_rules + idx)) {
			rule_ptrs[idx] = NULL;
		} else {
			rule_ptrs[idx] = &mirror_rules[idx].rule;
			++rule_idx;
		}
	}

	return rule_idx;
}

// Spells a projection of the ruleset through a fresh array: one slot
// per rule, NULL for the rules absent from the projection. Returns
// NULL when the array cannot be allocated.
static const struct classifier_rule **
mirror_rule_ptrs_project(
	struct mirror_rule *mirror_rules,
	uint32_t mirror_rule_count,
	mirror_rule_check_func check
) {
	const struct classifier_rule **rule_ptrs =
		(const struct classifier_rule **)malloc(
			sizeof(struct classifier_rule *) *
			(mirror_rule_count ? mirror_rule_count : 1)
		);
	if (rule_ptrs == NULL) {
		return NULL;
	}

	project_mirror_rules(mirror_rules, mirror_rule_count, rule_ptrs, check);
	return rule_ptrs;
}

static int
check_mirror_rule_l2(const struct mirror_rule *mirror_rule) {
	return !mirror_rule->src_net6s.count && !mirror_rule->dst_net6s.count &&
	       !mirror_rule->src_net4s.count && !mirror_rule->dst_net4s.count;
}

static int
check_has_ip4(const struct mirror_rule *mirror_rule) {
	return mirror_rule->src_net4s.count && mirror_rule->dst_net4s.count;
}

static int
check_has_ip6(const struct mirror_rule *mirror_rule) {
	return mirror_rule->src_net6s.count && mirror_rule->dst_net6s.count;
}

static int
check_mirror_rule_ip4(const struct mirror_rule *mirror_rule) {
	return check_has_ip4(mirror_rule);
}

static int
check_mirror_rule_ip6(const struct mirror_rule *mirror_rule) {
	return check_has_ip6(mirror_rule);
}

// Rule group mapping rows of the compile scratch of the l2 build
// below: the two attributes and their joint.
/*
 * Compiles the l2 projection: the device and vlan attributes over the
 * rules without networks - the only projection they can match through
 * - joined into the l2 classes and decoded over the same projection.
 *
 * The stages write straight into the classifier embedded in the
 * config, and a failed stage leaves its own outputs zeroed or
 * released, so the destroy walk finishes a partially built config
 * without per stage cleanup here; the registries and the rule group
 * mappings are the only scratch, released on the common exit of both
 * success and failure.
 */
static int
mirror_module_init_l2(
	struct cp_module *cp_module,
	struct mirror_rule *mirror_rules,
	uint32_t mirror_rule_count,
	yanet_error **err
) {
	struct mirror_module_config *config =
		container_of(cp_module, struct mirror_module_config, cp_module);
	struct memory_context *memory_context = &cp_module->memory_context;
	struct mirror_classifier_vlan *cls = &config->classifier_vlan;
	const struct classifier_rule **rule_ptrs = mirror_rule_ptrs_project(
		mirror_rules, mirror_rule_count, check_mirror_rule_l2
	);

	struct classifier stage_dev = {0};
	struct classifier stage_vlan = {0};
	struct classifier stage_l2 = {0};

	int rc = -1;

	if (rule_ptrs == NULL) {
		yanet_error_add(err, "failed to init filter_vlan");
		return -1;
	}

	if (classify_mirror_device_compile(
		    memory_context,
		    rule_ptrs,
		    mirror_rule_count,
		    &cls->dev_attr,
		    &stage_dev
	    )) {
		goto error;
	}
	if (classify_mirror_vlan_compile(
		    memory_context,
		    rule_ptrs,
		    mirror_rule_count,
		    &cls->vlan_attr,
		    &stage_vlan
	    )) {
		goto error;
	}
	if (classify_join(
		    memory_context,
		    &stage_dev,
		    &stage_vlan,
		    mirror_rule_count,
		    &cls->joint,
		    &stage_l2
	    )) {
		goto error;
	}
	if (classify_decode(
		    memory_context,
		    &stage_l2,
		    rule_ptrs,
		    mirror_rule_count,
		    &cls->rule_map
	    )) {
		goto error;
	}

	rc = 0;

error:
	classifier_fini(&stage_dev, memory_context, mirror_rule_count);
	classifier_fini(&stage_vlan, memory_context, mirror_rule_count);
	classifier_fini(&stage_l2, memory_context, mirror_rule_count);
	free(rule_ptrs);

	if (rc != 0) {
		yanet_error_add(err, "failed to init filter_vlan");
	}
	return rc;
}

// Rule group mapping rows of the compile scratch of the ip4 build
// below: the four attributes, the two inner joints and the family root
// joint with the decoder stage.
/*
 * Compiles the ip4 projection: the four attributes over the rules
 * holding an IPv4 network pair, joined in the pairwise order - device
 * with vlan, the network pair, the two partial results into the root -
 * and decoded over the same projection.
 *
 * The stages write straight into the classifier embedded in the
 * config, and a failed stage leaves its own outputs zeroed or
 * released, so the destroy walk finishes a partially built config
 * without per stage cleanup here; the registries and the rule group
 * mappings are the only scratch, released on the common exit of both
 * success and failure.
 */
static int
mirror_module_init_ip4(
	struct cp_module *cp_module,
	struct mirror_rule *mirror_rules,
	uint32_t mirror_rule_count,
	yanet_error **err
) {
	struct mirror_module_config *config =
		container_of(cp_module, struct mirror_module_config, cp_module);
	struct memory_context *memory_context = &cp_module->memory_context;
	struct mirror_classifier_ip4 *cls = &config->classifier_ip4;
	const struct classifier_rule **rule_ptrs = mirror_rule_ptrs_project(
		mirror_rules, mirror_rule_count, check_mirror_rule_ip4
	);

	struct classifier stage_dev = {0};
	struct classifier stage_vlan = {0};
	struct classifier stage_dev_vlan = {0};
	struct classifier stage_n4_src = {0};
	struct classifier stage_n4_dst = {0};
	struct classifier stage_nets = {0};
	struct classifier stage_ip4 = {0};

	int rc = -1;

	if (rule_ptrs == NULL) {
		yanet_error_add(err, "failed to init filter_ip4");
		return -1;
	}

	if (classify_mirror_device_compile(
		    memory_context,
		    rule_ptrs,
		    mirror_rule_count,
		    &cls->dev_attr,
		    &stage_dev
	    )) {
		goto error;
	}
	if (classify_mirror_vlan_compile(
		    memory_context,
		    rule_ptrs,
		    mirror_rule_count,
		    &cls->vlan_attr,
		    &stage_vlan
	    )) {
		goto error;
	}
	if (classify_join(
		    memory_context,
		    &stage_dev,
		    &stage_vlan,
		    mirror_rule_count,
		    &cls->dev_vlan_joint,
		    &stage_dev_vlan
	    )) {
		goto error;
	}
	if (classify_mirror_net4_src_compile(
		    memory_context,
		    rule_ptrs,
		    mirror_rule_count,
		    &cls->net4_src_attr,
		    &stage_n4_src
	    )) {
		goto error;
	}
	if (classify_mirror_net4_dst_compile(
		    memory_context,
		    rule_ptrs,
		    mirror_rule_count,
		    &cls->net4_dst_attr,
		    &stage_n4_dst
	    )) {
		goto error;
	}
	if (classify_join(
		    memory_context,
		    &stage_n4_src,
		    &stage_n4_dst,
		    mirror_rule_count,
		    &cls->nets_joint,
		    &stage_nets
	    )) {
		goto error;
	}
	if (classify_join(
		    memory_context,
		    &stage_dev_vlan,
		    &stage_nets,
		    mirror_rule_count,
		    &cls->root_joint,
		    &stage_ip4
	    )) {
		goto error;
	}
	if (classify_decode(
		    memory_context,
		    &stage_ip4,
		    rule_ptrs,
		    mirror_rule_count,
		    &cls->rule_map
	    )) {
		goto error;
	}

	rc = 0;

error:
	classifier_fini(&stage_dev, memory_context, mirror_rule_count);
	classifier_fini(&stage_vlan, memory_context, mirror_rule_count);
	classifier_fini(&stage_dev_vlan, memory_context, mirror_rule_count);
	classifier_fini(&stage_n4_src, memory_context, mirror_rule_count);
	classifier_fini(&stage_n4_dst, memory_context, mirror_rule_count);
	classifier_fini(&stage_nets, memory_context, mirror_rule_count);
	classifier_fini(&stage_ip4, memory_context, mirror_rule_count);
	free(rule_ptrs);

	if (rc != 0) {
		yanet_error_add(err, "failed to init filter_ip4");
	}
	return rc;
}

// Rule group mapping rows of the compile scratch of the ip6 build
// below: the four attributes, the two inner joints and the family root
// joint with the decoder stage.
/*
 * Compiles the ip6 projection: the four attributes over the rules
 * holding an IPv6 network pair, joined in the pairwise order - device
 * with vlan, the network pair, the two partial results into the root -
 * and decoded over the same projection.
 *
 * The stages write straight into the classifier embedded in the
 * config, and a failed stage leaves its own outputs zeroed or
 * released, so the destroy walk finishes a partially built config
 * without per stage cleanup here; the registries and the rule group
 * mappings are the only scratch, released on the common exit of both
 * success and failure.
 */
static int
mirror_module_init_ip6(
	struct cp_module *cp_module,
	struct mirror_rule *mirror_rules,
	uint32_t mirror_rule_count,
	yanet_error **err
) {
	struct mirror_module_config *config =
		container_of(cp_module, struct mirror_module_config, cp_module);
	struct memory_context *memory_context = &cp_module->memory_context;
	struct mirror_classifier_ip6 *cls = &config->classifier_ip6;
	const struct classifier_rule **rule_ptrs = mirror_rule_ptrs_project(
		mirror_rules, mirror_rule_count, check_mirror_rule_ip6
	);

	struct classifier stage_dev = {0};
	struct classifier stage_vlan = {0};
	struct classifier stage_dev_vlan = {0};
	struct classifier stage_n6_src = {0};
	struct classifier stage_n6_dst = {0};
	struct classifier stage_nets = {0};
	struct classifier stage_ip6 = {0};

	int rc = -1;

	if (rule_ptrs == NULL) {
		yanet_error_add(err, "failed to init filter_ip6");
		return -1;
	}

	if (classify_mirror_device_compile(
		    memory_context,
		    rule_ptrs,
		    mirror_rule_count,
		    &cls->dev_attr,
		    &stage_dev
	    )) {
		goto error;
	}
	if (classify_mirror_vlan_compile(
		    memory_context,
		    rule_ptrs,
		    mirror_rule_count,
		    &cls->vlan_attr,
		    &stage_vlan
	    )) {
		goto error;
	}
	if (classify_join(
		    memory_context,
		    &stage_dev,
		    &stage_vlan,
		    mirror_rule_count,
		    &cls->dev_vlan_joint,
		    &stage_dev_vlan
	    )) {
		goto error;
	}
	if (classify_mirror_net6_src_compile(
		    memory_context,
		    rule_ptrs,
		    mirror_rule_count,
		    &cls->net6_src_attr,
		    &stage_n6_src
	    )) {
		goto error;
	}
	if (classify_mirror_net6_dst_compile(
		    memory_context,
		    rule_ptrs,
		    mirror_rule_count,
		    &cls->net6_dst_attr,
		    &stage_n6_dst
	    )) {
		goto error;
	}
	if (classify_join(
		    memory_context,
		    &stage_n6_src,
		    &stage_n6_dst,
		    mirror_rule_count,
		    &cls->nets_joint,
		    &stage_nets
	    )) {
		goto error;
	}
	if (classify_join(
		    memory_context,
		    &stage_dev_vlan,
		    &stage_nets,
		    mirror_rule_count,
		    &cls->root_joint,
		    &stage_ip6
	    )) {
		goto error;
	}
	if (classify_decode(
		    memory_context,
		    &stage_ip6,
		    rule_ptrs,
		    mirror_rule_count,
		    &cls->rule_map
	    )) {
		goto error;
	}

	rc = 0;

error:
	classifier_fini(&stage_dev, memory_context, mirror_rule_count);
	classifier_fini(&stage_vlan, memory_context, mirror_rule_count);
	classifier_fini(&stage_dev_vlan, memory_context, mirror_rule_count);
	classifier_fini(&stage_n6_src, memory_context, mirror_rule_count);
	classifier_fini(&stage_n6_dst, memory_context, mirror_rule_count);
	classifier_fini(&stage_nets, memory_context, mirror_rule_count);
	classifier_fini(&stage_ip6, memory_context, mirror_rule_count);
	free(rule_ptrs);

	if (rc != 0) {
		yanet_error_add(err, "failed to init filter_ip6");
	}
	return rc;
}

int
mirror_module_config_update(
	struct cp_module *cp_module,
	struct mirror_rule *mirror_rules,
	uint32_t rule_count,
	yanet_error **err
) {
	if (rule_count == 0) {
		yanet_error_add(
			err, "mirror config must contain at least one rule"
		);
		return -1;
	}

	struct mirror_module_config *config =
		container_of(cp_module, struct mirror_module_config, cp_module);

	struct mirror_target *targets = (struct mirror_target *)memory_balloc(
		&cp_module->memory_context,
		sizeof(struct mirror_target) * rule_count
	);
	if (targets == NULL) {
		goto error;
	}

	SET_OFFSET_OF(&config->targets, targets);
	config->target_count = rule_count;

	// Just collect and link all devices
	for (uint32_t idx = 0; idx < rule_count; ++idx) {
		struct mirror_rule *rule = mirror_rules + idx;

		if (cp_module_link_device(
			    cp_module,
			    rule->target,
			    &targets[idx].device_id,
			    err
		    )) {
			goto error_target;
		}

		targets[idx].mode = rule->mode;

		if ((targets[idx].counter_id = counter_registry_register(
			     &cp_module->counter_registry, rule->counter, 2, err
		     )) == (uint64_t)-1) {
			goto error_target;
		}

		for (uint32_t idx = 0; idx < rule->devices.count; ++idx) {
			if (cp_module_link_device(
				    cp_module,
				    rule->devices.items[idx].name,
				    &rule->devices.items[idx].id,
				    err
			    )) {
				goto error_target;
			}
		}
	}

	if (mirror_module_init_l2(cp_module, mirror_rules, rule_count, err) ||
	    mirror_module_init_ip4(cp_module, mirror_rules, rule_count, err) ||
	    mirror_module_init_ip6(cp_module, mirror_rules, rule_count, err)) {
		goto error_target;
	}

	return 0;

error_target:
	memory_bfree(
		&cp_module->memory_context,
		targets,
		sizeof(struct mirror_target) * rule_count
	);
	SET_OFFSET_OF(&config->targets, NULL);
	config->target_count = 0;

error:

	return -1;
}
