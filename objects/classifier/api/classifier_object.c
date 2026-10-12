#include <errno.h>
#include <stdlib.h>

#include "classifier_object.h"

#include "classifier_lookup.h"

#include "lib/classify/compiler/device.h"
#include "lib/classify/compiler/helper.h"
#include "lib/classify/compiler/line.h"
#include "lib/classify/compiler/net4.h"
#include "lib/classify/compiler/net6.h"

#include "common/container_of.h"
#include "common/strutils.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_object.h"

#include "lib/dataplane/object/object.h"

static struct classifier_object *
classifier_object_of(const struct cp_object *cp_object) {
	return container_of(cp_object, struct classifier_object, cp_object);
}

// Releases the compiled bundle and the device table, without touching
// the embedded cp_object: the mirror of the module config destroys,
// every free below is safe on a zeroed struct, so a partially built
// object of a failed compile walks the same total release.
void
classifier_object_fini_data(struct classifier_object *self) {
	struct memory_context *memory_context = &self->cp_object.memory_context;

	vline_free(&self->filter_l2.rule_map);
	memset(&self->filter_l2, 0, sizeof(self->filter_l2));

	value_table_free(&self->filter_ip4.root_joint);
	vline_free(&self->filter_ip4.rule_map);
	memset(&self->filter_ip4, 0, sizeof(self->filter_ip4));

	value_table_free(&self->filter_ip6.root_joint);
	vline_free(&self->filter_ip6.rule_map);
	memset(&self->filter_ip6, 0, sizeof(self->filter_ip6));

	classify_attr_device_free(memory_context, &self->core.dev_attr);
	classify_attr_line_free(memory_context, &self->core.vlan_attr);
	value_table_free(&self->core.joint);
	memset(&self->core, 0, sizeof(self->core));

	classify_attr_net4_free(memory_context, &self->net4.src_attr);
	classify_attr_net4_free(memory_context, &self->net4.dst_attr);
	value_table_free(&self->net4.joint);
	memset(&self->net4, 0, sizeof(self->net4));

	classify_attr_net6_free(memory_context, &self->net6.src_attr);
	classify_attr_net6_free(memory_context, &self->net6.dst_attr);
	value_table_free(&self->net6.joint);
	memset(&self->net6, 0, sizeof(self->net6));

	struct classifier_object_device *devices = ADDR_OF(&self->devices);
	if (devices != NULL) {
		memory_bfree(
			memory_context,
			devices,
			sizeof(struct classifier_object_device) *
				(self->device_count ? self->device_count : 1)
		);
	}
	SET_OFFSET_OF(&self->devices, NULL);
	self->device_count = 0;

	self->rule_count = 0;
	self->subtype = CLASSIFIER_OBJECT_SUBTYPE_NONE;
}

/*
 * Field selectors of the object rule: the rule is handed to the
 * classify compilers directly, every attribute through its getter.
 */
static inline void
cl_rule_get_devices(
	const struct classifier_rule *rule, struct filter_devices *devices
) {
	const struct classifier_object_rule *cl_rule =
		container_of(rule, struct classifier_object_rule, rule);
	*devices = cl_rule->devices;
}

static inline void
cl_rule_get_vlan_ranges(
	const struct classifier_rule *rule, struct classify_line_ranges *ranges
) {
	const struct classifier_object_rule *cl_rule =
		container_of(rule, struct classifier_object_rule, rule);
	*ranges = cl_rule->vlan_ranges;
}

static inline void
cl_rule_get_net4_srcs(
	const struct classifier_rule *rule, struct filter_net4s *nets
) {
	const struct classifier_object_rule *cl_rule =
		container_of(rule, struct classifier_object_rule, rule);
	*nets = cl_rule->src_net4s;
}

static inline void
cl_rule_get_net4_dsts(
	const struct classifier_rule *rule, struct filter_net4s *nets
) {
	const struct classifier_object_rule *cl_rule =
		container_of(rule, struct classifier_object_rule, rule);
	*nets = cl_rule->dst_net4s;
}

static inline void
cl_rule_get_net6_srcs(
	const struct classifier_rule *rule, struct filter_net6s *nets
) {
	const struct classifier_object_rule *cl_rule =
		container_of(rule, struct classifier_object_rule, rule);
	*nets = cl_rule->src_net6s;
}

static inline void
cl_rule_get_net6_dsts(
	const struct classifier_rule *rule, struct filter_net6s *nets
) {
	const struct classifier_object_rule *cl_rule =
		container_of(rule, struct classifier_object_rule, rule);
	*nets = cl_rule->dst_net6s;
}

CLASSIFY_DEVICE_COMPILE(cl_device, cl_rule_get_devices)
CLASSIFY_LINE_COMPILE(
	cl_vlan, struct classify_attr_line, 4096, cl_rule_get_vlan_ranges
)
CLASSIFY_NET4_COMPILE(cl_net4_src, cl_rule_get_net4_srcs)
CLASSIFY_NET4_COMPILE(cl_net4_dst, cl_rule_get_net4_dsts)
CLASSIFY_NET6_COMPILE(cl_net6_src, cl_rule_get_net6_srcs)
CLASSIFY_NET6_COMPILE(cl_net6_dst, cl_rule_get_net6_dsts)

// Spells a projection of the ruleset: every rule the check rejects
// keeps a NULL slot, so the compile stages skip it.
static uint32_t
cl_project_rules(
	struct classifier_object_rule *rules,
	uint32_t rule_count,
	const struct classifier_rule **rule_ptrs,
	int (*check)(const struct classifier_object_rule *)
) {
	uint32_t projected = 0;
	for (uint32_t idx = 0; idx < rule_count; ++idx) {
		if (!check(rules + idx)) {
			rule_ptrs[idx] = NULL;
		} else {
			rule_ptrs[idx] = &rules[idx].rule;
			++projected;
		}
	}

	return projected;
}

// Spells a projection of the ruleset through a fresh array: one slot
// per rule, NULL for the rules absent from the projection. Returns
// NULL when the array cannot be allocated.
static const struct classifier_rule **
cl_rule_ptrs_project(
	struct classifier_object_rule *rules,
	uint32_t rule_count,
	int (*check)(const struct classifier_object_rule *)
) {
	const struct classifier_rule **rule_ptrs =
		(const struct classifier_rule **)malloc(
			sizeof(struct classifier_rule *) *
			(rule_count ? rule_count : 1)
		);
	if (rule_ptrs == NULL) {
		return NULL;
	}

	cl_project_rules(rules, rule_count, rule_ptrs, check);
	return rule_ptrs;
}

static int
cl_check_rule_l2(const struct classifier_object_rule *rule) {
	return !rule->src_net6s.count && !rule->dst_net6s.count &&
	       !rule->src_net4s.count && !rule->dst_net4s.count;
}

static int
cl_check_has_ip4(const struct classifier_object_rule *rule) {
	return rule->src_net4s.count && rule->dst_net4s.count;
}

static int
cl_check_has_ip6(const struct classifier_object_rule *rule) {
	return rule->src_net6s.count && rule->dst_net6s.count;
}

static int
cl_check_rule_ip4(const struct classifier_object_rule *rule) {
	return cl_check_has_ip4(rule);
}

static int
cl_check_rule_ip6(const struct classifier_object_rule *rule) {
	return cl_check_has_ip6(rule);
}

static int
cl_check_rule_any(const struct classifier_object_rule *rule) {
	return cl_check_rule_l2(rule) || cl_check_has_ip4(rule) ||
	       cl_check_has_ip6(rule);
}

/*
 * Builds the object's device table: the any slot first, then every
 * device name the rules carry, each finding or taking the next table
 * index. The assigned indices are written back into the rule device
 * items, so the device compile below addresses the table exactly the
 * way a module's device links address the module table.
 */
static int
cl_build_device_table(
	struct classifier_object *self,
	struct classifier_object_rule *rules,
	uint32_t rule_count,
	yanet_error **err
) {
	struct memory_context *memory_context = &self->cp_object.memory_context;

	struct classifier_object_device *devices =
		(struct classifier_object_device *)memory_balloc(
			memory_context, sizeof(struct classifier_object_device)
		);
	if (devices == NULL) {
		yanet_error_add(
			err,
			"failed to allocate the device table of classifier "
			"object '%s'",
			self->cp_object.name
		);
		return -1;
	}
	// The any slot: a device the rules never named resolves to it,
	// mirroring the module device attribute's zero slot.
	devices[0].name[0] = '\0';
	SET_OFFSET_OF(&self->devices, devices);
	self->device_count = 1;

	for (uint32_t idx = 0; idx < rule_count; ++idx) {
		struct filter_devices *rule_devices = &rules[idx].devices;
		for (uint32_t dev_idx = 0; dev_idx < rule_devices->count;
		     ++dev_idx) {
			struct filter_device *device =
				rule_devices->items + dev_idx;

			uint64_t slot = 0;
			for (; slot < self->device_count; ++slot) {
				if (!strncmp(
					    devices[slot].name,
					    device->name,
					    CP_DEVICE_NAME_LEN
				    )) {
					break;
				}
			}

			if (slot == self->device_count) {
				devices = (struct classifier_object_device *)
					memory_brealloc(
						memory_context,
						devices,
						sizeof(
							struct
							classifier_object_device
						) * self->device_count,
						sizeof(
							struct
							classifier_object_device
						) * (self->device_count + 1)
					);
				if (devices == NULL) {
					yanet_error_add(
						err,
						"failed to grow the device "
						"table of classifier object "
						"'%s'",
						self->cp_object.name
					);
					return -1;
				}
				strtcpy(devices[self->device_count].name,
					device->name,
					CP_DEVICE_NAME_LEN);
				SET_OFFSET_OF(&self->devices, devices);
				++self->device_count;
			}

			device->id = slot;
		}
	}

	return 0;
}

/*
 * Builds the shared core classifier: the device and vlan attributes
 * over the union of the family projections, joined into the core
 * classes of the stage argument - the classes the l2 decoder and the
 * family root joints of both families resolve through.
 *
 * The stages write straight into the classifier embedded in the
 * object, and a failed stage leaves its own outputs zeroed or
 * released, so the total release finishes a partially built object
 * without per stage cleanup here; the local stages are released on
 * the common exit of both success and failure, the core stage stays
 * owned by the caller.
 */
static int
cl_build_core(
	struct classifier_object *self,
	struct classifier *core_stage,
	struct classifier_object_rule *rules,
	uint32_t rule_count,
	yanet_error **err
) {
	struct memory_context *memory_context = &self->cp_object.memory_context;
	struct cl_classifier_core *cls = &self->core;
	const struct classifier_rule **rule_ptrs =
		cl_rule_ptrs_project(rules, rule_count, cl_check_rule_any);

	struct classifier stage_dev = {0};
	struct classifier stage_vlan = {0};

	int rc = -1;

	if (rule_ptrs == NULL) {
		yanet_error_add(err, "failed to build the classifier core");
		return -1;
	}
	if (classify_cl_device_compile(
		    memory_context,
		    "l2:device",
		    rule_ptrs,
		    rule_count,
		    &cls->dev_attr,
		    &stage_dev
	    )) {
		goto error;
	}
	if (classify_cl_vlan_compile(
		    memory_context,
		    "l2:vlan",
		    rule_ptrs,
		    rule_count,
		    &cls->vlan_attr,
		    &stage_vlan
	    )) {
		goto error;
	}
	if (classify_join(
		    memory_context,
		    "l2:joint",
		    &stage_dev,
		    &stage_vlan,
		    rule_count,
		    &cls->joint,
		    core_stage
	    )) {
		goto error;
	}

	rc = 0;

error:
	classifier_fini(&stage_dev, memory_context, rule_count);
	classifier_fini(&stage_vlan, memory_context, rule_count);
	free(rule_ptrs);

	if (rc != 0) {
		yanet_error_add(err, "failed to build the classifier core");
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
cl_derive_l2(
	struct classifier_object *self,
	const struct classifier *core_stage,
	struct classifier_object_rule *rules,
	uint32_t rule_count,
	yanet_error **err
) {
	struct memory_context *memory_context = &self->cp_object.memory_context;
	const struct classifier_rule **rule_ptrs =
		cl_rule_ptrs_project(rules, rule_count, cl_check_rule_l2);
	if (rule_ptrs == NULL) {
		yanet_error_add(
			err, "failed to build the classifier l2 filter"
		);
		return -1;
	}

	if (classify_decode(
		    memory_context,
		    "l2:rules",
		    core_stage,
		    rule_ptrs,
		    rule_count,
		    &self->filter_l2.rule_map
	    )) {
		free(rule_ptrs);
		yanet_error_add(
			err, "failed to build the classifier l2 filter"
		);
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
 * the classifier and the filter embedded in the object, and a failed
 * stage leaves its own outputs zeroed or released, so the total
 * release finishes a partially built object without per stage cleanup
 * here; the registries and the rule group mappings are the only
 * scratch, released on the common exit of both success and failure.
 */
static int
cl_build_net4(
	struct classifier_object *self,
	struct classifier *core_stage,
	struct classifier_object_rule *rules,
	uint32_t rule_count,
	struct classifier *ip4_stage,
	yanet_error **err
) {
	struct memory_context *memory_context = &self->cp_object.memory_context;
	struct cl_classifier_net4 *cls = &self->net4;
	const struct classifier_rule **rule_ptrs =
		cl_rule_ptrs_project(rules, rule_count, cl_check_rule_ip4);

	struct classifier stage_src = {0};
	struct classifier stage_dst = {0};
	struct classifier stage_nets = {0};

	int rc = -1;

	if (rule_ptrs == NULL) {
		yanet_error_add(
			err, "failed to build the classifier ip4 filter"
		);
		return -1;
	}

	if (classify_cl_net4_src_compile(
		    memory_context,
		    "ip4:net4_src",
		    rule_ptrs,
		    rule_count,
		    &cls->src_attr,
		    &stage_src
	    )) {
		goto error;
	}
	if (classify_cl_net4_dst_compile(
		    memory_context,
		    "ip4:net4_dst",
		    rule_ptrs,
		    rule_count,
		    &cls->dst_attr,
		    &stage_dst
	    )) {
		goto error;
	}
	if (classify_join(
		    memory_context,
		    "ip4:nets_joint",
		    &stage_src,
		    &stage_dst,
		    rule_count,
		    &cls->joint,
		    &stage_nets
	    )) {
		goto error;
	}
	if (classify_join(
		    memory_context,
		    "ip4:root_joint",
		    core_stage,
		    &stage_nets,
		    rule_count,
		    &self->filter_ip4.root_joint,
		    ip4_stage
	    )) {
		goto error;
	}

	rc = 0;

error:
	classifier_fini(&stage_src, memory_context, rule_count);
	classifier_fini(&stage_dst, memory_context, rule_count);
	classifier_fini(&stage_nets, memory_context, rule_count);
	free(rule_ptrs);

	if (rc != 0) {
		yanet_error_add(
			err, "failed to build the classifier ip4 filter"
		);
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
cl_derive_net4(
	struct classifier_object *self,
	struct classifier *ip4_stage,
	struct classifier_object_rule *rules,
	uint32_t rule_count,
	yanet_error **err
) {
	struct memory_context *memory_context = &self->cp_object.memory_context;
	const struct classifier_rule **rule_ptrs =
		cl_rule_ptrs_project(rules, rule_count, cl_check_rule_ip4);
	if (rule_ptrs == NULL) {
		classifier_fini(ip4_stage, memory_context, rule_count);
		yanet_error_add(
			err, "failed to build the classifier ip4 filter"
		);
		return -1;
	}
	if (classify_decode(
		    memory_context,
		    "ip4:rules",
		    ip4_stage,
		    rule_ptrs,
		    rule_count,
		    &self->filter_ip4.rule_map
	    )) {
		free(rule_ptrs);
		classifier_fini(ip4_stage, memory_context, rule_count);
		yanet_error_add(
			err, "failed to build the classifier ip4 filter"
		);
		return -1;
	}

	free(rule_ptrs);
	classifier_fini(ip4_stage, memory_context, rule_count);
	return 0;
}

// The net6 classification, in the same shape as the net4 one.
static int
cl_build_net6(
	struct classifier_object *self,
	struct classifier *core_stage,
	struct classifier_object_rule *rules,
	uint32_t rule_count,
	struct classifier *ip6_stage,
	yanet_error **err
) {
	struct memory_context *memory_context = &self->cp_object.memory_context;
	struct cl_classifier_net6 *cls = &self->net6;
	const struct classifier_rule **rule_ptrs =
		cl_rule_ptrs_project(rules, rule_count, cl_check_rule_ip6);

	struct classifier stage_src = {0};
	struct classifier stage_dst = {0};
	struct classifier stage_nets = {0};

	int rc = -1;

	if (rule_ptrs == NULL) {
		yanet_error_add(
			err, "failed to build the classifier ip6 filter"
		);
		return -1;
	}

	if (classify_cl_net6_src_compile(
		    memory_context,
		    "ip6:net6_src",
		    rule_ptrs,
		    rule_count,
		    &cls->src_attr,
		    &stage_src
	    )) {
		goto error;
	}
	if (classify_cl_net6_dst_compile(
		    memory_context,
		    "ip6:net6_dst",
		    rule_ptrs,
		    rule_count,
		    &cls->dst_attr,
		    &stage_dst
	    )) {
		goto error;
	}
	if (classify_join(
		    memory_context,
		    "ip6:nets_joint",
		    &stage_src,
		    &stage_dst,
		    rule_count,
		    &cls->joint,
		    &stage_nets
	    )) {
		goto error;
	}
	if (classify_join(
		    memory_context,
		    "ip6:root_joint",
		    core_stage,
		    &stage_nets,
		    rule_count,
		    &self->filter_ip6.root_joint,
		    ip6_stage
	    )) {
		goto error;
	}

	rc = 0;

error:
	classifier_fini(&stage_src, memory_context, rule_count);
	classifier_fini(&stage_dst, memory_context, rule_count);
	classifier_fini(&stage_nets, memory_context, rule_count);
	free(rule_ptrs);

	if (rc != 0) {
		yanet_error_add(
			err, "failed to build the classifier ip6 filter"
		);
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
cl_derive_net6(
	struct classifier_object *self,
	struct classifier *ip6_stage,
	struct classifier_object_rule *rules,
	uint32_t rule_count,
	yanet_error **err
) {
	struct memory_context *memory_context = &self->cp_object.memory_context;
	const struct classifier_rule **rule_ptrs =
		cl_rule_ptrs_project(rules, rule_count, cl_check_rule_ip6);
	if (rule_ptrs == NULL) {
		classifier_fini(ip6_stage, memory_context, rule_count);
		yanet_error_add(
			err, "failed to build the classifier ip6 filter"
		);
		return -1;
	}
	if (classify_decode(
		    memory_context,
		    "ip6:rules",
		    ip6_stage,
		    rule_ptrs,
		    rule_count,
		    &self->filter_ip6.rule_map
	    )) {
		free(rule_ptrs);
		classifier_fini(ip6_stage, memory_context, rule_count);
		yanet_error_add(
			err, "failed to build the classifier ip6 filter"
		);
		return -1;
	}

	free(rule_ptrs);
	classifier_fini(ip6_stage, memory_context, rule_count);
	return 0;
}

/*
 * Compiles the ruleset into the bundle the subtype names.
 *
 * Requires the embedded cp_object initialized - the bundle allocates
 * from its memory context - and nothing else: a contextless consumer
 * may hand-init the memory context and run this directly.
 */
int
classifier_object_compile(
	struct classifier_object *self,
	enum classifier_object_subtype subtype,
	struct classifier_object_rule *rules,
	uint32_t rule_count,
	yanet_error **err
) {
	if (subtype == CLASSIFIER_OBJECT_SUBTYPE_L2) {
		// The l2 bundle classifies through the device and vlan
		// attributes alone, so a network condition would widen
		// no class the compile could ever resolve.
		for (uint32_t idx = 0; idx < rule_count; ++idx) {
			if (!cl_check_rule_l2(rules + idx)) {
				yanet_error_add(
					err,
					"rule %u of classifier object '%s' "
					"carries network conditions the l2 "
					"subtype does not classify",
					idx,
					self->cp_object.name
				);
				return -1;
			}
		}
	}

	if (cl_build_device_table(self, rules, rule_count, err)) {
		return -1;
	}

	self->rule_count = rule_count;
	self->subtype = subtype;

	// The core class stage of the driver: the l2 build fills it
	// through its root joint, and the net4 and the net6 builds join
	// onto it before the stage is released.
	struct classifier core_stage = {0};

	if (cl_build_core(self, &core_stage, rules, rule_count, err) ||
	    cl_derive_l2(self, &core_stage, rules, rule_count, err)) {
		classifier_fini(
			&core_stage, &self->cp_object.memory_context, rule_count
		);
		return -1;
	}

	if (subtype == CLASSIFIER_OBJECT_SUBTYPE_L3) {
		// Every family build hands its root stage straight to its
		// own derivation, so a failed step leaves no stage behind.
		struct classifier ip4_stage = {0};
		if (cl_build_net4(
			    self,
			    &core_stage,
			    rules,
			    rule_count,
			    &ip4_stage,
			    err
		    ) ||
		    cl_derive_net4(self, &ip4_stage, rules, rule_count, err)) {
			classifier_fini(
				&ip4_stage,
				&self->cp_object.memory_context,
				rule_count
			);
			classifier_fini(
				&core_stage,
				&self->cp_object.memory_context,
				rule_count
			);
			return -1;
		}

		struct classifier ip6_stage = {0};
		if (cl_build_net6(
			    self,
			    &core_stage,
			    rules,
			    rule_count,
			    &ip6_stage,
			    err
		    ) ||
		    cl_derive_net6(self, &ip6_stage, rules, rule_count, err)) {
			classifier_fini(
				&ip6_stage,
				&self->cp_object.memory_context,
				rule_count
			);
			classifier_fini(
				&core_stage,
				&self->cp_object.memory_context,
				rule_count
			);
			return -1;
		}
	}

	classifier_fini(
		&core_stage, &self->cp_object.memory_context, rule_count
	);

	return 0;
}

// Typed destructor: tears the object down and returns its storage to
// the agent arena. Runs only on a dangling object, after
// classifier_object_config_free wins the try-destroy race.
static void
classifier_object_destroy(struct cp_object *cp_object) {
	struct classifier_object *self = classifier_object_of(cp_object);

	struct agent *agent = ADDR_OF(&cp_object->agent);

	classifier_object_fini_data(self);
	cp_object_fini(cp_object);

	memory_bfree(
		&agent->memory_context, self, sizeof(struct classifier_object)
	);
}

static struct cp_object *
classifier_object_config_new(
	struct agent *agent,
	const char *name,
	enum classifier_object_subtype subtype,
	struct classifier_object_rule *rules,
	uint32_t rule_count,
	yanet_error **err
) {
	struct classifier_object *self =
		(struct classifier_object *)memory_balloc(
			&agent->memory_context, sizeof(struct classifier_object)
		);
	if (self == NULL) {
		yanet_error_add(
			err, "failed to allocate classifier object '%s'", name
		);
		return NULL;
	}

	if (cp_object_init(
		    &self->cp_object, agent, CLASSIFIER_OBJECT_TYPE, name, err
	    )) {
		yanet_error_add(err, "failed to init classifier object");
		memory_bfree(
			&agent->memory_context,
			self,
			sizeof(struct classifier_object)
		);
		return NULL;
	}

	if (classifier_object_compile(self, subtype, rules, rule_count, err)) {
		yanet_error_add(err, "failed to compile classifier object");
		classifier_object_destroy(&self->cp_object);
		return NULL;
	}

	return &self->cp_object;
}

struct cp_object *
classifier_object_config_new_l2(
	struct agent *agent,
	const char *name,
	struct classifier_object_rule *rules,
	uint32_t rule_count,
	yanet_error **err
) {
	return classifier_object_config_new(
		agent,
		name,
		CLASSIFIER_OBJECT_SUBTYPE_L2,
		rules,
		rule_count,
		err
	);
}

struct cp_object *
classifier_object_config_new_l3(
	struct agent *agent,
	const char *name,
	struct classifier_object_rule *rules,
	uint32_t rule_count,
	yanet_error **err
) {
	return classifier_object_config_new(
		agent,
		name,
		CLASSIFIER_OBJECT_SUBTYPE_L3,
		rules,
		rule_count,
		err
	);
}

int
classifier_object_config_free(struct cp_object *cp_object, yanet_error **err) {
	if (cp_object_try_destroy(cp_object, err)) {
		return -1;
	}

	classifier_object_destroy(cp_object);
	return 0;
}

enum classifier_object_subtype
classifier_object_subtype(const struct cp_object *cp_object) {
	return classifier_object_of(cp_object)->subtype;
}

uint32_t
classifier_object_rule_count(const struct cp_object *cp_object) {
	return classifier_object_of(cp_object)->rule_count;
}

uint64_t
classifier_object_device_count(const struct cp_object *cp_object) {
	return classifier_object_of(cp_object)->device_count;
}

const char *
classifier_object_device_name(const struct cp_object *cp_object, uint64_t idx) {
	struct classifier_object *self = classifier_object_of(cp_object);
	struct classifier_object_device *devices = ADDR_OF(&self->devices);
	return devices[idx].name;
}

uint32_t
classifier_object_device_class(
	const struct cp_object *cp_object, const char *device_name
) {
	struct classifier_object *self = classifier_object_of(cp_object);
	struct classifier_object_device *devices = ADDR_OF(&self->devices);

	// A device the rules never named resolves through the any slot,
	// the table's zero index, the same way a module's device
	// attribute resolves an executing device the module never linked.
	uint64_t slot = 0;
	for (uint64_t idx = 1; idx < self->device_count; ++idx) {
		if (!strncmp(
			    devices[idx].name, device_name, CP_DEVICE_NAME_LEN
		    )) {
			slot = idx;
			break;
		}
	}

	return vline_get(
		(struct vline *)&self->core.dev_attr.line, (uint32_t)slot
	);
}

void
classifier_object_prepare(
	struct classifier_prepared *prepared,
	const struct classifier_object *object
) {
	prepared->subtype = object->subtype;
	prepared->object = object;

	switch (object->subtype) {
	case CLASSIFIER_OBJECT_SUBTYPE_L2:
		prepared->classify_batch = classifier_object_classify_l2;
		return;
	case CLASSIFIER_OBJECT_SUBTYPE_L3:
		prepared->classify_batch = classifier_object_classify_l3;
		return;
	default:
		// A subtype no create API produces selects no handle: the
		// public handler resolves every packet to no rule through
		// the NULL left here.
		prepared->classify_batch = NULL;
		return;
	}
}

// The object's execution-context commit: the handles of the object's
// subtype into the buffer the control plane allocated for this
// context. The single-writer pass re-derives from the authoritative
// object fields, so a repeated pass rewrites the same selection.
static void
classifier_object_commit_ectx(
	struct object_ectx *object_ectx, struct cp_object *cp_object
) {
	struct classifier_prepared *prepared = object_ectx->abs_object_prepared;
	if (prepared == NULL) {
		return;
	}

	classifier_object_prepare(prepared, classifier_object_of(cp_object));
}

struct object *
new_object_classifier() {
	struct object *object =
		(struct object *)calloc(1, sizeof(struct object));
	if (object == NULL) {
		return NULL;
	}

	strtcpy(object->name, CLASSIFIER_OBJECT_TYPE, sizeof(object->name));
	object->commit_ectx_handler = classifier_object_commit_ectx;
	object->prepared_size = sizeof(struct classifier_prepared);

	return object;
}
