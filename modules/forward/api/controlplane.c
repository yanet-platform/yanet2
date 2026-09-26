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
forward_module_config_destroy(struct cp_module *cp_module) {
	struct forward_module_config *config = container_of(
		cp_module, struct forward_module_config, cp_module
	);

	memory_bfree(
		&cp_module->memory_context,
		ADDR_OF(&config->targets),
		sizeof(struct forward_target) * config->target_count
	);

	// The filters go before the classifiers, keeping the ordering of
	// the composition; every free below is safe on a zeroed struct, so
	// a partially built config of a failed update walks the same path.
	vline_free(&config->filter_vlan.rule_map);
	memset(&config->filter_vlan, 0, sizeof(config->filter_vlan));

	value_table_free(&config->filter_ip4.root_joint);
	vline_free(&config->filter_ip4.rule_map);
	memset(&config->filter_ip4, 0, sizeof(config->filter_ip4));

	value_table_free(&config->filter_ip6.root_joint);
	vline_free(&config->filter_ip6.rule_map);
	memset(&config->filter_ip6, 0, sizeof(config->filter_ip6));

	struct fwd_classifier_core *core = &config->classifier_core;
	classify_attr_device_free(&cp_module->memory_context, &core->dev_attr);
	classify_attr_line_free(&cp_module->memory_context, &core->vlan_attr);
	value_table_free(&core->joint);
	memset(core, 0, sizeof(*core));

	struct fwd_classifier_net4 *net4 = &config->classifier_net4;
	classify_attr_net4_free(&cp_module->memory_context, &net4->src_attr);
	classify_attr_net4_free(&cp_module->memory_context, &net4->dst_attr);
	value_table_free(&net4->joint);
	memset(net4, 0, sizeof(*net4));

	struct fwd_classifier_net6 *net6 = &config->classifier_net6;
	classify_attr_net6_free(&cp_module->memory_context, &net6->src_attr);
	classify_attr_net6_free(&cp_module->memory_context, &net6->dst_attr);
	value_table_free(&net6->joint);
	memset(net6, 0, sizeof(*net6));

	// Capture agent before fini zeroes it.
	struct agent *agent = ADDR_OF(&cp_module->agent);

	cp_module_fini(cp_module);

	memory_bfree(
		&agent->memory_context,
		config,
		sizeof(struct forward_module_config)
	);
}

struct cp_module *
forward_module_config_init(
	struct agent *agent, const char *name, yanet_error **err
) {
	struct forward_module_config *config =
		(struct forward_module_config *)memory_balloc(
			&agent->memory_context,
			sizeof(struct forward_module_config)
		);
	if (config == NULL) {
		yanet_error_add(err, "failed to allocate config");
		return NULL;
	}

	if (cp_module_init(&config->cp_module, agent, "forward", name, err)) {
		yanet_error_add(err, "failed to init module");
		memory_bfree(
			&agent->memory_context,
			config,
			sizeof(struct forward_module_config)
		);
		return NULL;
	}

	SET_OFFSET_OF(&config->targets, NULL);
	config->target_count = 0;

	// The classifiers and the filters are embedded by value, so the
	// region is zeroed as a whole; every free of the destroy walk is
	// safe on a zeroed struct.
	memset(&config->classifier_core, 0, sizeof(config->classifier_core));
	memset(&config->classifier_net4, 0, sizeof(config->classifier_net4));
	memset(&config->classifier_net6, 0, sizeof(config->classifier_net6));
	memset(&config->filter_vlan, 0, sizeof(config->filter_vlan));
	memset(&config->filter_ip4, 0, sizeof(config->filter_ip4));
	memset(&config->filter_ip6, 0, sizeof(config->filter_ip6));

	return &config->cp_module;
}

int
forward_module_config_free(struct cp_module *cp_module, yanet_error **err) {
	if (cp_module_try_destroy(cp_module, err)) {
		return -1;
	}

	forward_module_config_destroy(cp_module);
	return 0;
}

typedef int (*forward_rule_check_func)(const struct forward_rule *forward_rule);

/*
 * Field selectors of the forward rule: the module rule is handed to
 * the classify compilers directly, every attribute through its getter.
 */
static inline void
fwd_rule_get_devices(
	const struct classifier_rule *rule, struct filter_devices *devices
) {
	const struct forward_rule *fwd_rule =
		container_of(rule, struct forward_rule, rule);
	*devices = fwd_rule->devices;
}

static inline void
fwd_rule_get_vlan_ranges(
	const struct classifier_rule *rule, struct classify_line_ranges *ranges
) {
	const struct forward_rule *fwd_rule =
		container_of(rule, struct forward_rule, rule);
	*ranges = fwd_rule->vlan_ranges;
}

static inline void
fwd_rule_get_net4_srcs(
	const struct classifier_rule *rule, struct filter_net4s *nets
) {
	const struct forward_rule *fwd_rule =
		container_of(rule, struct forward_rule, rule);
	*nets = fwd_rule->src_net4s;
}

static inline void
fwd_rule_get_net4_dsts(
	const struct classifier_rule *rule, struct filter_net4s *nets
) {
	const struct forward_rule *fwd_rule =
		container_of(rule, struct forward_rule, rule);
	*nets = fwd_rule->dst_net4s;
}

static inline void
fwd_rule_get_net6_srcs(
	const struct classifier_rule *rule, struct filter_net6s *nets
) {
	const struct forward_rule *fwd_rule =
		container_of(rule, struct forward_rule, rule);
	*nets = fwd_rule->src_net6s;
}

static inline void
fwd_rule_get_net6_dsts(
	const struct classifier_rule *rule, struct filter_net6s *nets
) {
	const struct forward_rule *fwd_rule =
		container_of(rule, struct forward_rule, rule);
	*nets = fwd_rule->dst_net6s;
}

CLASSIFY_DEVICE_COMPILE(fwd_device, fwd_rule_get_devices)
CLASSIFY_LINE_COMPILE(
	fwd_vlan, struct classify_attr_line, 4096, fwd_rule_get_vlan_ranges
)
CLASSIFY_NET4_COMPILE(fwd_net4_src, fwd_rule_get_net4_srcs)
CLASSIFY_NET4_COMPILE(fwd_net4_dst, fwd_rule_get_net4_dsts)
CLASSIFY_NET6_COMPILE(fwd_net6_src, fwd_rule_get_net6_srcs)
CLASSIFY_NET6_COMPILE(fwd_net6_dst, fwd_rule_get_net6_dsts)

// Spells a projection of the ruleset: every rule the check rejects
// keeps a NULL slot, so the compile stages skip it.
static uint32_t
project_forward_rules(
	struct forward_rule *forward_rules,
	uint32_t forward_rule_count,
	const struct classifier_rule **rule_ptrs,
	forward_rule_check_func check
) {
	uint32_t rule_idx = 0;
	for (uint32_t idx = 0; idx < forward_rule_count; ++idx) {
		if (!check(forward_rules + idx)) {
			rule_ptrs[idx] = NULL;
		} else {
			rule_ptrs[idx] = &forward_rules[idx].rule;
			++rule_idx;
		}
	}

	return rule_idx;
}

// Spells a projection of the ruleset through a fresh array: one slot
// per rule, NULL for the rules absent from the projection. Returns
// NULL when the array cannot be allocated.
static const struct classifier_rule **
forward_rule_ptrs_project(
	struct forward_rule *forward_rules,
	uint32_t forward_rule_count,
	forward_rule_check_func check
) {
	const struct classifier_rule **rule_ptrs =
		(const struct classifier_rule **)malloc(
			sizeof(struct classifier_rule *) *
			(forward_rule_count ? forward_rule_count : 1)
		);
	if (rule_ptrs == NULL) {
		return NULL;
	}

	project_forward_rules(
		forward_rules, forward_rule_count, rule_ptrs, check
	);
	return rule_ptrs;
}

static int
check_forward_rule_l2(const struct forward_rule *forward_rule) {
	return !forward_rule->src_net6s.count &&
	       !forward_rule->dst_net6s.count &&
	       !forward_rule->src_net4s.count && !forward_rule->dst_net4s.count;
}

static int
check_has_ip4(const struct forward_rule *forward_rule) {
	return forward_rule->src_net4s.count && forward_rule->dst_net4s.count;
}

static int
check_has_ip6(const struct forward_rule *forward_rule) {
	return forward_rule->src_net6s.count && forward_rule->dst_net6s.count;
}

static int
check_forward_rule_ip4(const struct forward_rule *forward_rule) {
	return check_has_ip4(forward_rule);
}

static int
check_forward_rule_ip6(const struct forward_rule *forward_rule) {
	return check_has_ip6(forward_rule);
}

static int
check_forward_rule_any(const struct forward_rule *forward_rule) {
	return check_forward_rule_l2(forward_rule) ||
	       check_has_ip4(forward_rule) || check_has_ip6(forward_rule);
}

/*
 * Builds the shared core classifier: the device and vlan attributes
 * over the union of the family projections, joined into the core
 * classes of the stage argument - the classes the l2 decoder and the
 * family root joints of both families resolve through.
 *
 * The stages write straight into the classifier embedded in the
 * config, and a failed stage leaves its own outputs zeroed or
 * released, so the destroy walk finishes a partially built config
 * without per stage cleanup here; the local stages are released on
 * the common exit of both success and failure, the core stage stays
 * owned by the caller.
 */
static int
forward_module_build_core(
	struct cp_module *cp_module,
	struct classifier *core_stage,
	struct forward_rule *forward_rules,
	uint32_t forward_rule_count,
	yanet_error **err
) {
	struct forward_module_config *config = container_of(
		cp_module, struct forward_module_config, cp_module
	);
	struct memory_context *memory_context = &cp_module->memory_context;
	struct fwd_classifier_core *cls = &config->classifier_core;
	const struct classifier_rule **rule_ptrs = forward_rule_ptrs_project(
		forward_rules, forward_rule_count, check_forward_rule_any
	);

	struct classifier stage_dev = {0};
	struct classifier stage_vlan = {0};

	int rc = -1;

	if (rule_ptrs == NULL) {
		yanet_error_add(err, "failed to build the forward core");
		return -1;
	}
	if (classify_fwd_device_compile(
		    memory_context,
		    rule_ptrs,
		    forward_rule_count,
		    &cls->dev_attr,
		    &stage_dev
	    )) {
		goto error;
	}
	if (classify_fwd_vlan_compile(
		    memory_context,
		    rule_ptrs,
		    forward_rule_count,
		    &cls->vlan_attr,
		    &stage_vlan
	    )) {
		goto error;
	}
	if (classify_join(
		    memory_context,
		    &stage_dev,
		    &stage_vlan,
		    forward_rule_count,
		    &cls->joint,
		    core_stage
	    )) {
		goto error;
	}

	rc = 0;

error:
	classifier_fini(&stage_dev, memory_context, forward_rule_count);
	classifier_fini(&stage_vlan, memory_context, forward_rule_count);
	free(rule_ptrs);

	if (rc != 0) {
		yanet_error_add(err, "failed to build the forward core");
	}
	return rc;
}

/*
 * Derives the l2 filter decoder: the rule map of the l2 projection
 * resolved out of the core classes of the build.
 *
 * The core stage stays owned by the caller - the family root joints
 * resolve through it after this derivation.
 */
static int
forward_module_derive_l2(
	struct cp_module *cp_module,
	const struct classifier *core_stage,
	struct forward_rule *forward_rules,
	uint32_t forward_rule_count,
	yanet_error **err
) {
	struct forward_module_config *config = container_of(
		cp_module, struct forward_module_config, cp_module
	);
	struct memory_context *memory_context = &cp_module->memory_context;
	const struct classifier_rule **rule_ptrs = forward_rule_ptrs_project(
		forward_rules, forward_rule_count, check_forward_rule_l2
	);
	if (rule_ptrs == NULL) {
		yanet_error_add(err, "failed to init filter_vlan");
		return -1;
	}

	if (classify_decode(
		    memory_context,
		    core_stage,
		    rule_ptrs,
		    forward_rule_count,
		    &config->filter_vlan.rule_map
	    )) {
		free(rule_ptrs);
		yanet_error_add(err, "failed to init filter_vlan");
		return -1;
	}

	free(rule_ptrs);
	return 0;
}

/*
 * Compiles the net4 classification: the network pair classifier over
 * the ip4 family projection, the family root joint joining the pair
 * classes onto the core classes of the stage argument, and the family
 * filter decoding the same projection out of the root classes.
 *
 * The core groups come from the union projection of the l2 build
 * while the network groups come from the family one, so a rule absent
 * from either side keeps no group there, the join skips it, and its
 * classes resolve to no rule - the rules only the other projection
 * holds never leak into the filter. The stages write straight into
 * the classifier and the filter embedded in the config, and a failed
 * stage leaves its own outputs zeroed or released, so the destroy
 * walk finishes a partially built config without per stage cleanup
 * here; the registries and the rule group mappings are the only
 * scratch, released on the common exit of both success and failure.
 */
static int
forward_module_build_net4(
	struct cp_module *cp_module,
	struct classifier *core_stage,
	struct forward_rule *forward_rules,
	uint32_t forward_rule_count,
	struct classifier *ip4_stage,
	yanet_error **err
) {
	struct forward_module_config *config = container_of(
		cp_module, struct forward_module_config, cp_module
	);
	struct memory_context *memory_context = &cp_module->memory_context;
	struct fwd_classifier_net4 *cls = &config->classifier_net4;
	const struct classifier_rule **rule_ptrs = forward_rule_ptrs_project(
		forward_rules, forward_rule_count, check_forward_rule_ip4
	);

	struct classifier stage_src = {0};
	struct classifier stage_dst = {0};
	struct classifier stage_nets = {0};

	int rc = -1;

	if (rule_ptrs == NULL) {
		yanet_error_add(err, "failed to init filter_ip4");
		return -1;
	}

	if (classify_fwd_net4_src_compile(
		    memory_context,
		    rule_ptrs,
		    forward_rule_count,
		    &cls->src_attr,
		    &stage_src
	    )) {
		goto error;
	}
	if (classify_fwd_net4_dst_compile(
		    memory_context,
		    rule_ptrs,
		    forward_rule_count,
		    &cls->dst_attr,
		    &stage_dst
	    )) {
		goto error;
	}
	if (classify_join(
		    memory_context,
		    &stage_src,
		    &stage_dst,
		    forward_rule_count,
		    &cls->joint,
		    &stage_nets
	    )) {
		goto error;
	}
	if (classify_join(
		    memory_context,
		    core_stage,
		    &stage_nets,
		    forward_rule_count,
		    &config->filter_ip4.root_joint,
		    ip4_stage
	    )) {
		goto error;
	}

	rc = 0;

error:
	classifier_fini(&stage_src, memory_context, forward_rule_count);
	classifier_fini(&stage_dst, memory_context, forward_rule_count);
	classifier_fini(&stage_nets, memory_context, forward_rule_count);
	free(rule_ptrs);

	if (rc != 0) {
		yanet_error_add(err, "failed to init filter_ip4");
	}
	return rc;
}

/*
 * Derives the ip4 family filter decoder: the rule map of the family
 * projection resolved out of the root classes of the build. The stage
 * argument is the root stage the build handed out; the derivation
 * consumes it, so it is released here.
 */
static int
forward_module_derive_net4(
	struct cp_module *cp_module,
	struct classifier *ip4_stage,
	struct forward_rule *forward_rules,
	uint32_t forward_rule_count,
	yanet_error **err
) {
	struct forward_module_config *config = container_of(
		cp_module, struct forward_module_config, cp_module
	);
	struct memory_context *memory_context = &cp_module->memory_context;
	const struct classifier_rule **rule_ptrs = forward_rule_ptrs_project(
		forward_rules, forward_rule_count, check_forward_rule_ip4
	);
	if (rule_ptrs == NULL) {
		classifier_fini(ip4_stage, memory_context, forward_rule_count);
		yanet_error_add(err, "failed to init filter_ip4");
		return -1;
	}
	if (classify_decode(
		    memory_context,
		    ip4_stage,
		    rule_ptrs,
		    forward_rule_count,
		    &config->filter_ip4.rule_map
	    )) {
		free(rule_ptrs);
		classifier_fini(ip4_stage, memory_context, forward_rule_count);
		yanet_error_add(err, "failed to init filter_ip4");
		return -1;
	}

	free(rule_ptrs);
	classifier_fini(ip4_stage, memory_context, forward_rule_count);
	return 0;
}

// The net6 classification, in the same shape as the net4 one.
static int
forward_module_build_net6(
	struct cp_module *cp_module,
	struct classifier *core_stage,
	struct forward_rule *forward_rules,
	uint32_t forward_rule_count,
	struct classifier *ip6_stage,
	yanet_error **err
) {
	struct forward_module_config *config = container_of(
		cp_module, struct forward_module_config, cp_module
	);
	struct memory_context *memory_context = &cp_module->memory_context;
	struct fwd_classifier_net6 *cls = &config->classifier_net6;
	const struct classifier_rule **rule_ptrs = forward_rule_ptrs_project(
		forward_rules, forward_rule_count, check_forward_rule_ip6
	);

	struct classifier stage_src = {0};
	struct classifier stage_dst = {0};
	struct classifier stage_nets = {0};

	int rc = -1;

	if (rule_ptrs == NULL) {
		yanet_error_add(err, "failed to init filter_ip6");
		return -1;
	}

	if (classify_fwd_net6_src_compile(
		    memory_context,
		    rule_ptrs,
		    forward_rule_count,
		    &cls->src_attr,
		    &stage_src
	    )) {
		goto error;
	}
	if (classify_fwd_net6_dst_compile(
		    memory_context,
		    rule_ptrs,
		    forward_rule_count,
		    &cls->dst_attr,
		    &stage_dst
	    )) {
		goto error;
	}
	if (classify_join(
		    memory_context,
		    &stage_src,
		    &stage_dst,
		    forward_rule_count,
		    &cls->joint,
		    &stage_nets
	    )) {
		goto error;
	}
	if (classify_join(
		    memory_context,
		    core_stage,
		    &stage_nets,
		    forward_rule_count,
		    &config->filter_ip6.root_joint,
		    ip6_stage
	    )) {
		goto error;
	}

	rc = 0;

error:
	classifier_fini(&stage_src, memory_context, forward_rule_count);
	classifier_fini(&stage_dst, memory_context, forward_rule_count);
	classifier_fini(&stage_nets, memory_context, forward_rule_count);
	free(rule_ptrs);

	if (rc != 0) {
		yanet_error_add(err, "failed to init filter_ip6");
	}
	return rc;
}

/*
 * Derives the ip6 family filter decoder: the rule map of the family
 * projection resolved out of the root classes of the build. The stage
 * argument is the root stage the build handed out; the derivation
 * consumes it, so it is released here.
 */
static int
forward_module_derive_net6(
	struct cp_module *cp_module,
	struct classifier *ip6_stage,
	struct forward_rule *forward_rules,
	uint32_t forward_rule_count,
	yanet_error **err
) {
	struct forward_module_config *config = container_of(
		cp_module, struct forward_module_config, cp_module
	);
	struct memory_context *memory_context = &cp_module->memory_context;
	const struct classifier_rule **rule_ptrs = forward_rule_ptrs_project(
		forward_rules, forward_rule_count, check_forward_rule_ip6
	);
	if (rule_ptrs == NULL) {
		classifier_fini(ip6_stage, memory_context, forward_rule_count);
		yanet_error_add(err, "failed to init filter_ip6");
		return -1;
	}
	if (classify_decode(
		    memory_context,
		    ip6_stage,
		    rule_ptrs,
		    forward_rule_count,
		    &config->filter_ip6.rule_map
	    )) {
		free(rule_ptrs);
		classifier_fini(ip6_stage, memory_context, forward_rule_count);
		yanet_error_add(err, "failed to init filter_ip6");
		return -1;
	}

	free(rule_ptrs);
	classifier_fini(ip6_stage, memory_context, forward_rule_count);
	return 0;
}

int
forward_module_config_update(
	struct cp_module *cp_module,
	struct forward_rule *forward_rules,
	uint32_t rule_count,
	yanet_error **err
) {
	struct forward_module_config *config = container_of(
		cp_module, struct forward_module_config, cp_module
	);

	struct forward_target *targets = (struct forward_target *)memory_balloc(
		&cp_module->memory_context,
		sizeof(struct forward_target) * rule_count
	);
	if (targets == NULL) {
		goto error;
	}

	SET_OFFSET_OF(&config->targets, targets);
	config->target_count = rule_count;

	// Just collect and link all devices
	for (uint32_t idx = 0; idx < rule_count; ++idx) {
		struct forward_rule *rule = forward_rules + idx;

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

	// The core class stage of the driver: the l2 build fills it
	// through its root joint, and the net4 and the net6 builds join
	// onto it before the stage is released.
	struct classifier core_stage = {0};

	if (forward_module_build_core(
		    cp_module, &core_stage, forward_rules, rule_count, err
	    ) ||
	    forward_module_derive_l2(
		    cp_module, &core_stage, forward_rules, rule_count, err
	    )) {
		classifier_fini(
			&core_stage, &cp_module->memory_context, rule_count
		);
		goto error_target;
	}

	// Every family build hands its root stage straight to its own
	// derivation, so a failed step leaves no stage behind.
	{
		struct classifier ip4_stage = {0};
		if (forward_module_build_net4(
			    cp_module,
			    &core_stage,
			    forward_rules,
			    rule_count,
			    &ip4_stage,
			    err
		    ) ||
		    forward_module_derive_net4(
			    cp_module,
			    &ip4_stage,
			    forward_rules,
			    rule_count,
			    err
		    )) {
			classifier_fini(
				&ip4_stage,
				&cp_module->memory_context,
				rule_count
			);
			classifier_fini(
				&core_stage,
				&cp_module->memory_context,
				rule_count
			);
			goto error_target;
		}
	}

	{
		struct classifier ip6_stage = {0};
		if (forward_module_build_net6(
			    cp_module,
			    &core_stage,
			    forward_rules,
			    rule_count,
			    &ip6_stage,
			    err
		    ) ||
		    forward_module_derive_net6(
			    cp_module,
			    &ip6_stage,
			    forward_rules,
			    rule_count,
			    err
		    )) {
			classifier_fini(
				&ip6_stage,
				&cp_module->memory_context,
				rule_count
			);
			classifier_fini(
				&core_stage,
				&cp_module->memory_context,
				rule_count
			);
			goto error_target;
		}
	}

	classifier_fini(&core_stage, &cp_module->memory_context, rule_count);

	return 0;

error_target:
	memory_bfree(
		&cp_module->memory_context,
		targets,
		sizeof(struct forward_target) * rule_count
	);
	SET_OFFSET_OF(&config->targets, NULL);
	config->target_count = 0;

error:

	return -1;
}
