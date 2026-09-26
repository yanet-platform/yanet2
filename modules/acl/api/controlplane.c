#include <errno.h>
#include <stdint.h>
#include <stdlib.h>
#include <time.h>

#include "controlplane.h"

#include "../dataplane/config.h"
#include "common/memory_address.h"
#include "lib/errors/errors.h"
#include "lib/logging/log.h"
#include "objects/fwstate/api/fwstate_map_v4_object.h"
#include "objects/fwstate/api/fwstate_map_v6_object.h"

#include "common/container_of.h"

#include "lib/controlplane/agent/agent.h"

#include "lib/classify/classifiers/line.h"
#include "lib/classify/classifiers/port.h"
#include "lib/classify/compiler/device.h"
#include "lib/classify/compiler/helper.h"
#include "lib/classify/compiler/ipfrag.h"
#include "lib/classify/compiler/line.h"
#include "lib/classify/compiler/net4.h"
#include "lib/classify/compiler/net6.h"
#include "lib/classify/compiler/u16_ranges.h"

static void
acl_module_config_destroy(struct cp_module *cp_module) {
	struct acl_module_config *config =
		container_of(cp_module, struct acl_module_config, cp_module);
	struct memory_context *memory_context = &cp_module->memory_context;

	memory_bfree(
		memory_context,
		ADDR_OF(&config->targets),
		sizeof(struct acl_target) * config->target_count
	);

	// The filters go before the classifiers, keeping the ordering of
	// the composition - the family root joints before the class
	// sources they join; every free below is safe on a zeroed struct,
	// so a partially built config of a failed update walks the same
	// path.
	vline_free(&config->filter_l2.rule_map);
	memset(&config->filter_l2, 0, sizeof(config->filter_l2));

	value_table_free(&config->filter_ip4.root_joint);
	vline_free(&config->filter_ip4.rule_map);
	memset(&config->filter_ip4, 0, sizeof(config->filter_ip4));

	value_table_free(&config->filter_ip4_tcp.root_joint);
	vline_free(&config->filter_ip4_tcp.rule_map);
	memset(&config->filter_ip4_tcp, 0, sizeof(config->filter_ip4_tcp));

	value_table_free(&config->filter_ip4_udp.root_joint);
	vline_free(&config->filter_ip4_udp.rule_map);
	memset(&config->filter_ip4_udp, 0, sizeof(config->filter_ip4_udp));

	value_table_free(&config->filter_ip4_icmp.root_joint);
	vline_free(&config->filter_ip4_icmp.rule_map);
	memset(&config->filter_ip4_icmp, 0, sizeof(config->filter_ip4_icmp));

	value_table_free(&config->filter_ip6.root_joint);
	vline_free(&config->filter_ip6.rule_map);
	memset(&config->filter_ip6, 0, sizeof(config->filter_ip6));

	value_table_free(&config->filter_ip6_tcp.root_joint);
	vline_free(&config->filter_ip6_tcp.rule_map);
	memset(&config->filter_ip6_tcp, 0, sizeof(config->filter_ip6_tcp));

	value_table_free(&config->filter_ip6_udp.root_joint);
	vline_free(&config->filter_ip6_udp.rule_map);
	memset(&config->filter_ip6_udp, 0, sizeof(config->filter_ip6_udp));

	value_table_free(&config->filter_ip6_icmp.root_joint);
	vline_free(&config->filter_ip6_icmp.rule_map);
	memset(&config->filter_ip6_icmp, 0, sizeof(config->filter_ip6_icmp));

	struct acl_classifier_l2 *l2 = &config->classifier_l2;
	classify_attr_device_free(memory_context, &l2->dev_attr);
	memset(l2, 0, sizeof(*l2));

	struct acl_classifier_core4 *core4 = &config->classifier_core4;
	classify_attr_device_free(memory_context, &core4->dev_attr);
	classify_attr_net4_free(memory_context, &core4->net4_src_attr);
	classify_attr_net4_free(memory_context, &core4->net4_dst_attr);
	classify_attr_line_free(memory_context, &core4->ipproto_attr);
	value_table_free(&core4->nets_joint);
	value_table_free(&core4->mid_joint);
	value_table_free(&core4->proto_joint);
	memset(core4, 0, sizeof(*core4));

	classify_attr_ipfrag_free(
		memory_context, &config->classifier_frag4.frag_attr
	);
	memset(&config->classifier_frag4, 0, sizeof(config->classifier_frag4));

	struct acl_classifier_ports *ports4 = &config->classifier_ports4;
	classify_attr_port_free(memory_context, &ports4->src_attr);
	classify_attr_port_free(memory_context, &ports4->dst_attr);
	value_table_free(&ports4->joint);
	memset(ports4, 0, sizeof(*ports4));

	classify_attr_line_free(
		memory_context, &config->classifier_tcp4.flags_attr
	);
	value_table_free(&config->classifier_tcp4.flags_joint);
	memset(&config->classifier_tcp4, 0, sizeof(config->classifier_tcp4));

	classify_attr_line_free(
		memory_context, &config->classifier_icmp4.type_attr
	);
	memset(&config->classifier_icmp4, 0, sizeof(config->classifier_icmp4));

	struct acl_classifier_core6 *core6 = &config->classifier_core6;
	classify_attr_device_free(memory_context, &core6->dev_attr);
	classify_attr_net6_free(memory_context, &core6->net6_src_attr);
	classify_attr_net6_free(memory_context, &core6->net6_dst_attr);
	classify_attr_line_free(memory_context, &core6->ipproto_attr);
	value_table_free(&core6->nets_joint);
	value_table_free(&core6->mid_joint);
	value_table_free(&core6->proto_joint);
	memset(core6, 0, sizeof(*core6));

	classify_attr_ipfrag_free(
		memory_context, &config->classifier_frag6.frag_attr
	);
	memset(&config->classifier_frag6, 0, sizeof(config->classifier_frag6));

	struct acl_classifier_ports *ports6 = &config->classifier_ports6;
	classify_attr_port_free(memory_context, &ports6->src_attr);
	classify_attr_port_free(memory_context, &ports6->dst_attr);
	value_table_free(&ports6->joint);
	memset(ports6, 0, sizeof(*ports6));

	classify_attr_line_free(
		memory_context, &config->classifier_tcp6.flags_attr
	);
	value_table_free(&config->classifier_tcp6.flags_joint);
	memset(&config->classifier_tcp6, 0, sizeof(config->classifier_tcp6));

	classify_attr_line_free(
		memory_context, &config->classifier_icmp6.type_attr
	);
	memset(&config->classifier_icmp6, 0, sizeof(config->classifier_icmp6));

	// Capture agent before fini zeroes it.
	struct agent *agent = ADDR_OF(&cp_module->agent);

	cp_module_fini(cp_module);

	memory_bfree(
		&agent->memory_context,
		cp_module,
		sizeof(struct acl_module_config)
	);
}

static int
acl_module_compile_rules(
	struct cp_module *cp_module,
	struct acl_rule *acl_rules,
	uint32_t rule_count,
	const char *fw4_map_name,
	const char *fw6_map_name,
	yanet_error **err
);

struct cp_module *
acl_module_config_init(
	struct agent *agent,
	const char *name,
	struct acl_rule *acl_rules,
	uint32_t rule_count,
	const char *fw4_map_name,
	const char *fw6_map_name,
	yanet_error **err
) {
	struct acl_module_config *config =
		(struct acl_module_config *)memory_balloc(
			&agent->memory_context, sizeof(struct acl_module_config)
		);
	if (config == NULL) {
		yanet_error_add(err, "failed to allocate config");
		return NULL;
	}

	if (cp_module_init(&config->cp_module, agent, "acl", name, err)) {
		yanet_error_add(err, "failed to init module");
		memory_bfree(
			&agent->memory_context,
			config,
			sizeof(struct acl_module_config)
		);
		return NULL;
	}

	SET_OFFSET_OF(&config->targets, NULL);
	config->abs_targets = NULL;
	config->target_count = 0;

	// The classifiers and the filters are embedded by value, so both
	// regions are zeroed as a whole; every free of the destroy walk is
	// safe on a zeroed struct.
	memset(&config->classifier_l2, 0, sizeof(config->classifier_l2));
	memset(&config->classifier_core4, 0, sizeof(config->classifier_core4));
	memset(&config->classifier_frag4, 0, sizeof(config->classifier_frag4));
	memset(&config->classifier_ports4, 0, sizeof(config->classifier_ports4)
	);
	memset(&config->classifier_tcp4, 0, sizeof(config->classifier_tcp4));
	memset(&config->classifier_icmp4, 0, sizeof(config->classifier_icmp4));
	memset(&config->classifier_core6, 0, sizeof(config->classifier_core6));
	memset(&config->classifier_frag6, 0, sizeof(config->classifier_frag6));
	memset(&config->classifier_ports6, 0, sizeof(config->classifier_ports6)
	);
	memset(&config->classifier_tcp6, 0, sizeof(config->classifier_tcp6));
	memset(&config->classifier_icmp6, 0, sizeof(config->classifier_icmp6));

	memset(&config->filter_l2, 0, sizeof(config->filter_l2));
	memset(&config->filter_ip4, 0, sizeof(config->filter_ip4));
	memset(&config->filter_ip4_tcp, 0, sizeof(config->filter_ip4_tcp));
	memset(&config->filter_ip4_udp, 0, sizeof(config->filter_ip4_udp));
	memset(&config->filter_ip4_icmp, 0, sizeof(config->filter_ip4_icmp));
	memset(&config->filter_ip6, 0, sizeof(config->filter_ip6));
	memset(&config->filter_ip6_tcp, 0, sizeof(config->filter_ip6_tcp));
	memset(&config->filter_ip6_udp, 0, sizeof(config->filter_ip6_udp));
	memset(&config->filter_ip6_icmp, 0, sizeof(config->filter_ip6_icmp));

	config->v4_object_link_idx = ACL_OBJECT_LINK_NONE;
	config->v6_object_link_idx = ACL_OBJECT_LINK_NONE;

	// Register module-level counters
	struct {
		const char *name;
		uint64_t size;
		uint64_t *dst;
	} counters[] = {
		{"acl_no_match", 1, &config->no_match_counter_id},
		{"acl_action_allow", 1, &config->action_allow_counter_id},
		{"acl_action_deny", 1, &config->action_deny_counter_id},
		{"acl_action_check_pass",
		 1,
		 &config->action_check_pass_counter_id},
		{"acl_action_check_miss",
		 1,
		 &config->action_check_miss_counter_id},
		{"acl_action_create_state",
		 1,
		 &config->action_create_state_counter_id},
		{"acl_action_invalid", 1, &config->action_invalid_counter_id},
		{"acl_action_non_term", 1, &config->action_non_term_counter_id},

		{"acl_sync_sent", 2, &config->sync_sent_counter_id},
	};

	for (size_t i = 0; i < sizeof(counters) / sizeof(counters[0]); ++i) {
		uint64_t id = counter_registry_register(
			&config->cp_module.counter_registry,
			counters[i].name,
			counters[i].size,
			err
		);
		if (id == (uint64_t)-1) {
			yanet_error_add(
				err,
				"failed to register counter '%s'",
				counters[i].name
			);
			// Frees directly instead of going through the type
			// destructor.
			//
			// A failed configuration-data setup never reaches a
			// state its own teardown could safely walk. No
			// reference beyond the caller's own has been taken, and
			// no registry has observed the module yet, so nothing
			// is lost by freeing the block here.
			cp_module_fini(&config->cp_module);
			memory_bfree(
				&agent->memory_context,
				config,
				sizeof(struct acl_module_config)
			);
			return NULL;
		}
		*counters[i].dst = id;
	}

	// A config handle is built exactly once: the ruleset is compiled
	// before the module is ever visible to a registry. A compile
	// failure tears the whole module down through the registered
	// destructor, which frees every partial allocation the compile
	// made — the same state a caller-side Free of a failed update
	// used to reach.
	if (acl_module_compile_rules(
		    &config->cp_module,
		    acl_rules,
		    rule_count,
		    fw4_map_name,
		    fw6_map_name,
		    err
	    )) {
		acl_module_config_destroy(&config->cp_module);
		return NULL;
	}

	return &config->cp_module;
}

int
acl_module_config_free(struct cp_module *cp_module, yanet_error **err) {
	if (cp_module_try_destroy(cp_module, err)) {
		return -1;
	}

	acl_module_config_destroy(cp_module);
	return 0;
}

typedef int (*acl_rule_check_func)(const struct acl_rule *acl_rule);

/*
 * Field selectors of the acl rule: the module rule is handed to the
 * classify compilers directly, every attribute through its getter.
 */
static inline void
acl_rule_get_devices(
	const struct classifier_rule *rule, struct filter_devices *devices
) {
	const struct acl_rule *acl_rule =
		container_of(rule, struct acl_rule, rule);
	*devices = acl_rule->devices;
}

static inline void
acl_rule_get_net4_srcs(
	const struct classifier_rule *rule, struct filter_net4s *nets
) {
	const struct acl_rule *acl_rule =
		container_of(rule, struct acl_rule, rule);
	*nets = acl_rule->src_net4s;
}

static inline void
acl_rule_get_net4_dsts(
	const struct classifier_rule *rule, struct filter_net4s *nets
) {
	const struct acl_rule *acl_rule =
		container_of(rule, struct acl_rule, rule);
	*nets = acl_rule->dst_net4s;
}

static inline void
acl_rule_get_net6_srcs(
	const struct classifier_rule *rule, struct filter_net6s *nets
) {
	const struct acl_rule *acl_rule =
		container_of(rule, struct acl_rule, rule);
	*nets = acl_rule->src_net6s;
}

static inline void
acl_rule_get_net6_dsts(
	const struct classifier_rule *rule, struct filter_net6s *nets
) {
	const struct acl_rule *acl_rule =
		container_of(rule, struct acl_rule, rule);
	*nets = acl_rule->dst_net6s;
}

static inline enum filter_ip_fragment
acl_rule_get_fragment(const struct classifier_rule *rule) {
	const struct acl_rule *acl_rule =
		container_of(rule, struct acl_rule, rule);
	return acl_rule->fragment;
}

/*
 * Field selectors of the protocol line attributes: the control plane
 * that authors the rules derives the domain intervals out of the
 * authored 16 bit protocol ranges - the protocol number spans for the
 * shared core, the low byte sub-spans for the transport leaves - so
 * the rule carries them and the getters hand the views out.
 */
static inline void
acl_rule_get_ipproto_ranges(
	const struct classifier_rule *rule, struct classify_line_ranges *ranges
) {
	const struct acl_rule *acl_rule =
		container_of(rule, struct acl_rule, rule);
	*ranges = acl_rule->ipproto_ranges;
}

CLASSIFY_DEVICE_COMPILE(acl_device, acl_rule_get_devices)
CLASSIFY_NET4_COMPILE(acl_net4_src, acl_rule_get_net4_srcs)
CLASSIFY_NET4_COMPILE(acl_net4_dst, acl_rule_get_net4_dsts)
CLASSIFY_NET6_COMPILE(acl_net6_src, acl_rule_get_net6_srcs)
CLASSIFY_NET6_COMPILE(acl_net6_dst, acl_rule_get_net6_dsts)
CLASSIFY_IPFRAG_COMPILE(acl_ipfrag, acl_rule_get_fragment)
CLASSIFY_LINE_COMPILE(
	acl_ipproto,
	struct classify_attr_line,
	0x100,
	acl_rule_get_ipproto_ranges
)
static inline void
acl_rule_get_tcp_flags_ranges(
	const struct classifier_rule *rule, struct classify_line_ranges *ranges
) {
	const struct acl_rule *acl_rule =
		container_of(rule, struct acl_rule, rule);
	*ranges = acl_rule->tcp_flags_ranges;
}

static inline void
acl_rule_get_icmp_type_ranges(
	const struct classifier_rule *rule, struct classify_line_ranges *ranges
) {
	const struct acl_rule *acl_rule =
		container_of(rule, struct acl_rule, rule);
	*ranges = acl_rule->icmp_type_ranges;
}

CLASSIFY_LINE_COMPILE(
	acl_tcp_flags,
	struct classify_attr_line,
	0x100,
	acl_rule_get_tcp_flags_ranges
)
CLASSIFY_LINE_COMPILE(
	acl_icmp_type,
	struct classify_attr_line,
	0x100,
	acl_rule_get_icmp_type_ranges
)
static inline void
acl_rule_get_src_port_ranges(
	const struct classifier_rule *rule, struct filter_u16_ranges *ranges
) {
	const struct acl_rule *acl_rule =
		container_of(rule, struct acl_rule, rule);
	ranges->count = acl_rule->src_port_ranges.count;
	ranges->items =
		(const struct filter_u16_span *)acl_rule->src_port_ranges.items;
}

static inline void
acl_rule_get_dst_port_ranges(
	const struct classifier_rule *rule, struct filter_u16_ranges *ranges
) {
	const struct acl_rule *acl_rule =
		container_of(rule, struct acl_rule, rule);
	ranges->count = acl_rule->dst_port_ranges.count;
	ranges->items =
		(const struct filter_u16_span *)acl_rule->dst_port_ranges.items;
}

CLASSIFY_U16_RANGES_COMPILE(
	acl_src_port, struct classify_attr_port, acl_rule_get_src_port_ranges
)
CLASSIFY_U16_RANGES_COMPILE(
	acl_dst_port, struct classify_attr_port, acl_rule_get_dst_port_ranges
)

// Spells a projection of the ruleset: every rule the check rejects
// keeps a NULL slot, so the compile stages skip it.
static uint32_t
project_acl_rules(
	struct acl_rule *acl_rules,
	uint32_t acl_rule_count,
	const struct classifier_rule **rule_ptrs,
	acl_rule_check_func check
) {
	uint32_t rule_idx = 0;
	for (uint32_t idx = 0; idx < acl_rule_count; ++idx) {
		if (!check(acl_rules + idx)) {
			rule_ptrs[idx] = NULL;
		} else {
			rule_ptrs[idx] = &acl_rules[idx].rule;
			++rule_idx;
		}
	}

	return rule_idx;
}

// Spells a projection of the ruleset through a fresh array: one slot
// per rule, NULL for the rules absent from the projection. Returns
// NULL when the array cannot be allocated.
static const struct classifier_rule **
acl_rule_ptrs_project(
	struct acl_rule *acl_rules,
	uint32_t acl_rule_count,
	acl_rule_check_func check
) {
	const struct classifier_rule **rule_ptrs =
		(const struct classifier_rule **)malloc(
			sizeof(struct classifier_rule *) *
			(acl_rule_count ? acl_rule_count : 1)
		);
	if (rule_ptrs == NULL) {
		return NULL;
	}

	project_acl_rules(acl_rules, acl_rule_count, rule_ptrs, check);
	return rule_ptrs;
}

static int
check_acl_rule_l2(const struct acl_rule *acl_rule) {
	return !acl_rule->src_net6s.count && !acl_rule->dst_net6s.count &&
	       !acl_rule->src_net4s.count && !acl_rule->dst_net4s.count;
}

static int
check_has_ip4(const struct acl_rule *acl_rule) {
	return acl_rule->src_net4s.count && acl_rule->dst_net4s.count;
}

static int
check_has_ip6(const struct acl_rule *acl_rule) {
	return acl_rule->src_net6s.count && acl_rule->dst_net6s.count;
}

/*
 * The projection checks of the family paths: the control plane that
 * authors the rules decides the membership alongside the derived
 * line intervals - which rules match the packets without a transport
 * header, which discriminate through the ports or the transport
 * specific byte - so the checks read the decision fields of the rule
 * and the compile stays free of the authored protocol semantics.
 */
static int
check_acl_rule_plain(const struct acl_rule *acl_rule) {
	return acl_rule->path_plain;
}

static int
check_acl_rule_tcp(const struct acl_rule *acl_rule) {
	return acl_rule->path_tcp;
}

static int
check_acl_rule_udp(const struct acl_rule *acl_rule) {
	return acl_rule->path_udp;
}

// The union of the tcp and the udp projections: the rules the shared
// ports pair compiles over.
static int
acl_rule_has_ports_path(const struct acl_rule *acl_rule) {
	return acl_rule->path_tcp || acl_rule->path_udp;
}

static int
check_acl_rule_icmp(const struct acl_rule *acl_rule) {
	return acl_rule->path_icmp;
}

/*
 * Builds the l2 classifier: the device attribute alone over the l2
 * projection - the rules without networks, which match every packet of
 * their devices regardless of the protocol family. A single attribute
 * needs no joint: the device classes are the final classes the decoder
 * resolves, so the build hands its stage out for the derivation.
 *
 * The stages write straight into the classifier embedded in the
 * config, and a failed stage leaves its own outputs zeroed or
 * released, so the destroy walk finishes a partially built config
 * without per stage cleanup here; the stage stays owned by the
 * caller.
 */
static int
acl_module_build_l2(
	struct cp_module *cp_module,
	struct acl_rule *acl_rules,
	uint32_t acl_rule_count,
	struct classifier *dev_stage,
	yanet_error **err
) {
	struct acl_module_config *config =
		container_of(cp_module, struct acl_module_config, cp_module);
	struct memory_context *memory_context = &cp_module->memory_context;
	struct acl_classifier_l2 *cls = &config->classifier_l2;
	const struct classifier_rule **rule_ptrs = acl_rule_ptrs_project(
		acl_rules, acl_rule_count, check_acl_rule_l2
	);

	int rc = -1;

	if (rule_ptrs == NULL) {
		yanet_error_add(err, "failed to init filter_l2");
		return -1;
	}

	if (classify_acl_device_compile(
		    memory_context,
		    rule_ptrs,
		    acl_rule_count,
		    &cls->dev_attr,
		    dev_stage
	    )) {
		goto error;
	}

	rc = 0;

error:
	free(rule_ptrs);
	if (rc != 0) {
		yanet_error_add(err, "failed to init filter_l2");
	}
	return rc;
}

/*
 * Derives the l2 filter decoder: the rule count and the rule map of
 * the l2 projection resolved out of the device classes of the build.
 * The stage argument is the stage the build handed out; the
 * derivation consumes it, so it is released here.
 */
static int
acl_module_derive_l2(
	struct cp_module *cp_module,
	struct classifier *dev_stage,
	struct acl_rule *acl_rules,
	uint32_t acl_rule_count,
	yanet_error **err
) {
	struct acl_module_config *config =
		container_of(cp_module, struct acl_module_config, cp_module);
	struct memory_context *memory_context = &cp_module->memory_context;
	const struct classifier_rule **rule_ptrs = acl_rule_ptrs_project(
		acl_rules, acl_rule_count, check_acl_rule_l2
	);
	if (rule_ptrs == NULL) {
		classifier_fini(dev_stage, memory_context, acl_rule_count);
		yanet_error_add(err, "failed to init filter_l2");
		return -1;
	}

	config->filter_rule_count_l2 = project_acl_rules(
		acl_rules, acl_rule_count, rule_ptrs, check_acl_rule_l2
	);
	if (classify_decode(
		    memory_context,
		    dev_stage,
		    rule_ptrs,
		    acl_rule_count,
		    &config->filter_l2.rule_map
	    )) {
		free(rule_ptrs);
		classifier_fini(dev_stage, memory_context, acl_rule_count);
		yanet_error_add(err, "failed to init filter_l2");
		return -1;
	}

	free(rule_ptrs);
	classifier_fini(dev_stage, memory_context, acl_rule_count);
	return 0;
}

/*
 * Compiles the ip4 family composition: the core classifier over the
 * union of the plain and the port scoped projections of the family,
 * the fragment suffix over the plain projection alone, and the plain
 * family filter joining the suffix classes onto the core classes
 * through its root joint and decoding over the plain projection.
 *
 * The core groups come from the union projection while the fragment
 * groups come from the plain one, so a rule absent from either side
 * keeps no group there, the join skips it, and its classes resolve to
 * no rule - the rules only the other projection holds never leak into
 * the filter. The stages write straight into the classifiers and the
 * filter embedded in the config, and a failed stage leaves its own
 * outputs zeroed or released, so the destroy walk finishes a partially
 * built config without per stage cleanup here. The root stage registry
 * with its row outlives this build through the core stage argument;
 * the remaining registries and the rule group mappings are local
 * scratch, released on the common exit of both success and failure.
 */
static int
acl_module_build_ip4_classifier(
	struct cp_module *cp_module,
	struct classifier *core_stage,
	struct acl_rule *acl_rules,
	uint32_t acl_rule_count,
	struct classifier *plain_stage
) {
	struct acl_module_config *config =
		container_of(cp_module, struct acl_module_config, cp_module);
	struct memory_context *memory_context = &cp_module->memory_context;
	struct acl_classifier_core4 *cls = &config->classifier_core4;
	const struct classifier_rule **rule_ptrs =
		acl_rule_ptrs_project(acl_rules, acl_rule_count, check_has_ip4);

	struct classifier stage_dev = {0};
	struct classifier stage_n4_src = {0};
	struct classifier stage_n4_dst = {0};
	struct classifier stage_nets = {0};
	struct classifier stage_mid = {0};
	struct classifier stage_ipproto = {0};
	struct classifier stage_frag = {0};

	int rc = -1;

	if (rule_ptrs == NULL) {
		return -1;
	}

	// The core attributes and their joints over the union of both
	// projections of the family: the core classes feed the plain and
	// the port scoped root joints.

	if (classify_acl_device_compile(
		    memory_context,
		    rule_ptrs,
		    acl_rule_count,
		    &cls->dev_attr,
		    &stage_dev
	    )) {
		goto error;
	}
	if (classify_acl_net4_src_compile(
		    memory_context,
		    rule_ptrs,
		    acl_rule_count,
		    &cls->net4_src_attr,
		    &stage_n4_src
	    )) {
		goto error;
	}
	if (classify_acl_net4_dst_compile(
		    memory_context,
		    rule_ptrs,
		    acl_rule_count,
		    &cls->net4_dst_attr,
		    &stage_n4_dst
	    )) {
		goto error;
	}
	if (classify_join(
		    memory_context,
		    &stage_n4_src,
		    &stage_n4_dst,
		    acl_rule_count,
		    &cls->nets_joint,
		    &stage_nets
	    )) {
		goto error;
	}
	if (classify_join(
		    memory_context,
		    &stage_dev,
		    &stage_nets,
		    acl_rule_count,
		    &cls->mid_joint,
		    &stage_mid
	    )) {
		goto error;
	}
	if (classify_acl_ipproto_compile(
		    memory_context,
		    rule_ptrs,
		    acl_rule_count,
		    &cls->ipproto_attr,
		    &stage_ipproto
	    )) {
		goto error;
	}
	if (classify_join(
		    memory_context,
		    &stage_mid,
		    &stage_ipproto,
		    acl_rule_count,
		    &cls->proto_joint,
		    core_stage
	    )) {
		goto error;
	}

	// The plain family filter: the fragment suffix compiles over the
	// plain projection, and the family root joint joins its classes
	// onto the core classes - the derivation resolves the plain
	// projection out of the joined classes.
	project_acl_rules(
		acl_rules, acl_rule_count, rule_ptrs, check_acl_rule_plain
	);

	if (classify_acl_ipfrag_compile(
		    memory_context,
		    rule_ptrs,
		    acl_rule_count,
		    &config->classifier_frag4.frag_attr,
		    &stage_frag
	    )) {
		goto error;
	}
	if (classify_join(
		    memory_context,
		    core_stage,
		    &stage_frag,
		    acl_rule_count,
		    &config->filter_ip4.root_joint,
		    plain_stage
	    )) {
		goto error;
	}

	rc = 0;

error:
	classifier_fini(&stage_dev, memory_context, acl_rule_count);
	classifier_fini(&stage_n4_src, memory_context, acl_rule_count);
	classifier_fini(&stage_n4_dst, memory_context, acl_rule_count);
	classifier_fini(&stage_nets, memory_context, acl_rule_count);
	classifier_fini(&stage_mid, memory_context, acl_rule_count);
	classifier_fini(&stage_frag, memory_context, acl_rule_count);
	free(rule_ptrs);
	return rc;
}

/*
 * Derives the plain ip4 family filter decoder: the rule count and
 * the rule map of the family projection resolved out of the root
 * classes of the build. The stage argument is the root stage the build
 * handed out; the derivation consumes it, so it is released here.
 */
static int
acl_module_derive_ip4(
	struct cp_module *cp_module,
	struct classifier *plain_stage,
	struct acl_rule *acl_rules,
	uint32_t acl_rule_count
) {
	struct acl_module_config *config =
		container_of(cp_module, struct acl_module_config, cp_module);
	struct memory_context *memory_context = &cp_module->memory_context;
	const struct classifier_rule **rule_ptrs = acl_rule_ptrs_project(
		acl_rules, acl_rule_count, check_acl_rule_plain
	);
	if (rule_ptrs == NULL) {
		classifier_fini(plain_stage, memory_context, acl_rule_count);
		return -1;
	}

	config->filter_rule_count_ip4 = project_acl_rules(
		acl_rules, acl_rule_count, rule_ptrs, check_acl_rule_plain
	);
	if (classify_decode(
		    memory_context,
		    plain_stage,
		    rule_ptrs,
		    acl_rule_count,
		    &config->filter_ip4.rule_map
	    )) {
		free(rule_ptrs);
		classifier_fini(plain_stage, memory_context, acl_rule_count);
		return -1;
	}

	free(rule_ptrs);
	classifier_fini(plain_stage, memory_context, acl_rule_count);
	return 0;
}

// The ip6 family composition, in the same shape as the ip4 one.
static int
acl_module_build_ip6_classifier(
	struct cp_module *cp_module,
	struct classifier *core_stage,
	struct acl_rule *acl_rules,
	uint32_t acl_rule_count,
	struct classifier *plain_stage
) {
	struct acl_module_config *config =
		container_of(cp_module, struct acl_module_config, cp_module);
	struct memory_context *memory_context = &cp_module->memory_context;
	struct acl_classifier_core6 *cls = &config->classifier_core6;
	const struct classifier_rule **rule_ptrs =
		acl_rule_ptrs_project(acl_rules, acl_rule_count, check_has_ip6);

	struct classifier stage_dev = {0};
	struct classifier stage_n6_src = {0};
	struct classifier stage_n6_dst = {0};
	struct classifier stage_nets = {0};
	struct classifier stage_mid = {0};
	struct classifier stage_ipproto = {0};
	struct classifier stage_frag = {0};

	int rc = -1;

	if (rule_ptrs == NULL) {
		return -1;
	}

	// The core attributes and their joints over the union of both
	// projections of the family: the core classes feed the plain and
	// the port scoped root joints.

	if (classify_acl_device_compile(
		    memory_context,
		    rule_ptrs,
		    acl_rule_count,
		    &cls->dev_attr,
		    &stage_dev
	    )) {
		goto error;
	}
	if (classify_acl_net6_src_compile(
		    memory_context,
		    rule_ptrs,
		    acl_rule_count,
		    &cls->net6_src_attr,
		    &stage_n6_src
	    )) {
		goto error;
	}
	if (classify_acl_net6_dst_compile(
		    memory_context,
		    rule_ptrs,
		    acl_rule_count,
		    &cls->net6_dst_attr,
		    &stage_n6_dst
	    )) {
		goto error;
	}
	if (classify_join(
		    memory_context,
		    &stage_n6_src,
		    &stage_n6_dst,
		    acl_rule_count,
		    &cls->nets_joint,
		    &stage_nets
	    )) {
		goto error;
	}
	if (classify_join(
		    memory_context,
		    &stage_dev,
		    &stage_nets,
		    acl_rule_count,
		    &cls->mid_joint,
		    &stage_mid
	    )) {
		goto error;
	}
	if (classify_acl_ipproto_compile(
		    memory_context,
		    rule_ptrs,
		    acl_rule_count,
		    &cls->ipproto_attr,
		    &stage_ipproto
	    )) {
		goto error;
	}
	if (classify_join(
		    memory_context,
		    &stage_mid,
		    &stage_ipproto,
		    acl_rule_count,
		    &cls->proto_joint,
		    core_stage
	    )) {
		goto error;
	}

	// The plain family filter: the fragment suffix compiles over the
	// plain projection, and the family root joint joins its classes
	// onto the core classes - the derivation resolves the plain
	// projection out of the joined classes.
	project_acl_rules(
		acl_rules, acl_rule_count, rule_ptrs, check_acl_rule_plain
	);

	if (classify_acl_ipfrag_compile(
		    memory_context,
		    rule_ptrs,
		    acl_rule_count,
		    &config->classifier_frag6.frag_attr,
		    &stage_frag
	    )) {
		goto error;
	}
	if (classify_join(
		    memory_context,
		    core_stage,
		    &stage_frag,
		    acl_rule_count,
		    &config->filter_ip6.root_joint,
		    plain_stage
	    )) {
		goto error;
	}

	rc = 0;

error:
	classifier_fini(&stage_dev, memory_context, acl_rule_count);
	classifier_fini(&stage_n6_src, memory_context, acl_rule_count);
	classifier_fini(&stage_n6_dst, memory_context, acl_rule_count);
	classifier_fini(&stage_nets, memory_context, acl_rule_count);
	classifier_fini(&stage_mid, memory_context, acl_rule_count);
	classifier_fini(&stage_frag, memory_context, acl_rule_count);
	free(rule_ptrs);
	return rc;
}

/*
 * Derives the plain ip6 family filter decoder: the rule count and
 * the rule map of the family projection resolved out of the root
 * classes of the build. The stage argument is the root stage the build
 * handed out; the derivation consumes it, so it is released here.
 */
static int
acl_module_derive_ip6(
	struct cp_module *cp_module,
	struct classifier *plain_stage,
	struct acl_rule *acl_rules,
	uint32_t acl_rule_count
) {
	struct acl_module_config *config =
		container_of(cp_module, struct acl_module_config, cp_module);
	struct memory_context *memory_context = &cp_module->memory_context;
	const struct classifier_rule **rule_ptrs =
		acl_rule_ptrs_project(acl_rules, acl_rule_count, check_has_ip6);
	if (rule_ptrs == NULL) {
		classifier_fini(plain_stage, memory_context, acl_rule_count);
		return -1;
	}

	config->filter_rule_count_ip6 = project_acl_rules(
		acl_rules, acl_rule_count, rule_ptrs, check_acl_rule_plain
	);
	if (classify_decode(
		    memory_context,
		    plain_stage,
		    rule_ptrs,
		    acl_rule_count,
		    &config->filter_ip6.rule_map
	    )) {
		free(rule_ptrs);
		classifier_fini(plain_stage, memory_context, acl_rule_count);
		return -1;
	}

	free(rule_ptrs);
	classifier_fini(plain_stage, memory_context, acl_rule_count);
	return 0;
}

/*
 * Targets of the protocol path filter builds of one family: the ports
 * classifier shared by the tcp and the udp paths, the path leaf
 * classifiers, and the three path root joints, filled by the build
 * below into the config fields the family names; the projection
 * decoders and the rule counts of the projections, filled by the
 * derivations.
 */
struct acl_path_filters_build {
	struct acl_classifier_ports *ports;
	struct acl_classifier_tcp *tcp;
	struct acl_classifier_icmp *icmp;

	struct value_table *tcp_root_joint;
	struct vline *tcp_rule_map;
	uint64_t *tcp_rule_count;

	struct value_table *udp_root_joint;
	struct vline *udp_rule_map;
	uint64_t *udp_rule_count;

	struct value_table *icmp_root_joint;
	struct vline *icmp_rule_map;
	uint64_t *icmp_rule_count;
};

/*
 * Compiles the port scoped filter of a family: the ports pair over
 * the port scoped projection, the family root joint joining the ports
 * classes onto the core classes of the core stage argument, and the
 * decoder of the same projection.
 *
 * The core groups come from the union projection of the family while
 * the ports groups come from the port scoped one, so a rule absent
 * from either side keeps no group there and the join skips it. The
 * stages write straight into the targets of the build descriptor, and
 * a failed stage leaves its own outputs zeroed or released, so the
 * destroy walk finishes a partially built config without per stage
 * cleanup here; the registries and the rule group mappings are the
 * only scratch, released on the common exit of both success and
 * failure.
 */
/*
 * Compiles the protocol paths of one family: the ports pair over the
 * union of the tcp and the udp projections - both paths join its
 * classes through their own root joints - the TCP flags leaf over the
 * tcp projection, and the ICMP type leaf over the icmp projection,
 * each path root joint joining the path suffix classes onto the core
 * classes of the stage argument and handing its stage out.
 *
 * Every stage writes straight into the classifiers and the filters
 * embedded in the config, and a failed stage leaves its own outputs
 * zeroed or released, so the destroy walk finishes a partially built
 * config without per stage cleanup here; the handed out stages stay
 * owned by the caller, the local stages are released on the common
 * exit of both success and failure.
 */
static int
acl_module_build_paths(
	struct cp_module *cp_module,
	struct classifier *core_stage,
	const struct acl_path_filters_build *build,
	struct acl_rule *acl_rules,
	uint32_t acl_rule_count,
	struct classifier *tcp_stage,
	struct classifier *udp_stage,
	struct classifier *icmp_stage
) {
	struct memory_context *memory_context = &cp_module->memory_context;

	struct classifier stage_src = {0};
	struct classifier stage_dst = {0};
	struct classifier stage_ports = {0};
	struct classifier stage_flags = {0};
	struct classifier stage_tcp_mid = {0};
	struct classifier stage_type = {0};

	int rc = -1;

	// The ports pair over the union of both port carrying paths.
	{
		const struct classifier_rule **rule_ptrs =
			acl_rule_ptrs_project(
				acl_rules,
				acl_rule_count,
				acl_rule_has_ports_path
			);
		if (rule_ptrs == NULL) {
			return -1;
		}

		if (classify_acl_src_port_compile(
			    memory_context,
			    rule_ptrs,
			    acl_rule_count,
			    &build->ports->src_attr,
			    &stage_src
		    )) {
			free(rule_ptrs);
			goto error;
		}
		if (classify_acl_dst_port_compile(
			    memory_context,
			    rule_ptrs,
			    acl_rule_count,
			    &build->ports->dst_attr,
			    &stage_dst
		    )) {
			free(rule_ptrs);
			goto error;
		}
		if (classify_join(
			    memory_context,
			    &stage_src,
			    &stage_dst,
			    acl_rule_count,
			    &build->ports->joint,
			    &stage_ports
		    )) {
			free(rule_ptrs);
			goto error;
		}
		free(rule_ptrs);
	}

	// The udp path: the ports classes join onto the core classes
	// directly, the path has no transport specific leaf.
	if (classify_join(
		    memory_context,
		    core_stage,
		    &stage_ports,
		    acl_rule_count,
		    build->udp_root_joint,
		    udp_stage
	    )) {
		goto error;
	}

	// The tcp path: the flags leaf joins onto the ports classes
	// first - the small sides cross before the core join - and the
	// joined classes join onto the core classes.
	{
		const struct classifier_rule **rule_ptrs =
			acl_rule_ptrs_project(
				acl_rules, acl_rule_count, check_acl_rule_tcp
			);
		if (rule_ptrs == NULL) {
			goto error;
		}

		if (classify_acl_tcp_flags_compile(
			    memory_context,
			    rule_ptrs,
			    acl_rule_count,
			    &build->tcp->flags_attr,
			    &stage_flags
		    )) {
			free(rule_ptrs);
			goto error;
		}
		free(rule_ptrs);
	}
	if (classify_join(
		    memory_context,
		    &stage_ports,
		    &stage_flags,
		    acl_rule_count,
		    &build->tcp->flags_joint,
		    &stage_tcp_mid
	    )) {
		goto error;
	}
	if (classify_join(
		    memory_context,
		    core_stage,
		    &stage_tcp_mid,
		    acl_rule_count,
		    build->tcp_root_joint,
		    tcp_stage
	    )) {
		goto error;
	}

	// The icmp path: the type leaf joins onto the core classes.
	{
		const struct classifier_rule **rule_ptrs =
			acl_rule_ptrs_project(
				acl_rules, acl_rule_count, check_acl_rule_icmp
			);
		if (rule_ptrs == NULL) {
			goto error;
		}

		if (classify_acl_icmp_type_compile(
			    memory_context,
			    rule_ptrs,
			    acl_rule_count,
			    &build->icmp->type_attr,
			    &stage_type
		    )) {
			free(rule_ptrs);
			goto error;
		}
		free(rule_ptrs);
	}
	if (classify_join(
		    memory_context,
		    core_stage,
		    &stage_type,
		    acl_rule_count,
		    build->icmp_root_joint,
		    icmp_stage
	    )) {
		goto error;
	}

	rc = 0;

error:
	classifier_fini(&stage_src, memory_context, acl_rule_count);
	classifier_fini(&stage_dst, memory_context, acl_rule_count);
	classifier_fini(&stage_ports, memory_context, acl_rule_count);
	classifier_fini(&stage_flags, memory_context, acl_rule_count);
	classifier_fini(&stage_tcp_mid, memory_context, acl_rule_count);
	classifier_fini(&stage_type, memory_context, acl_rule_count);
	return rc;
}

/*
 * Derives the protocol path filter decoders of one family: the rule
 * counts and the rule maps of the path projections resolved out of
 * the handed stages. Every derivation consumes its stage, so the
 * stages are released here.
 */

// One protocol path of a family derivation: the stage the build
// handed out, and the decoder outputs the derivation fills.
struct acl_path_stages {
	struct classifier *stage;
	struct vline *rule_map;
	uint64_t *rule_count;
	acl_rule_check_func check;
};

// Releases the stages a failed derivation leaves behind: the path
// the failure stopped at and the ones after it - the stages before
// it were consumed by their own iterations already.
static void
acl_derive_paths_error(
	const struct acl_path_stages *paths,
	uint32_t path,
	struct memory_context *memory_context,
	uint32_t acl_rule_count
) {
	for (uint32_t left = path; left < 3; ++left) {
		classifier_fini(
			paths[left].stage, memory_context, acl_rule_count
		);
	}
}

static int
acl_module_derive_paths(
	struct cp_module *cp_module,
	const struct acl_path_filters_build *build,
	struct classifier *tcp_stage,
	struct classifier *udp_stage,
	struct classifier *icmp_stage,
	struct acl_rule *acl_rules,
	uint32_t acl_rule_count
) {
	struct memory_context *memory_context = &cp_module->memory_context;

	struct acl_path_stages paths[3] = {
		{tcp_stage,
		 build->tcp_rule_map,
		 build->tcp_rule_count,
		 check_acl_rule_tcp},
		{udp_stage,
		 build->udp_rule_map,
		 build->udp_rule_count,
		 check_acl_rule_udp},
		{icmp_stage,
		 build->icmp_rule_map,
		 build->icmp_rule_count,
		 check_acl_rule_icmp},
	};

	for (uint32_t path = 0; path < 3; ++path) {
		const struct classifier_rule **rule_ptrs =
			acl_rule_ptrs_project(
				acl_rules, acl_rule_count, paths[path].check
			);
		if (rule_ptrs == NULL) {
			acl_derive_paths_error(
				paths, path, memory_context, acl_rule_count
			);
			return -1;
		}

		*paths[path].rule_count = project_acl_rules(
			acl_rules, acl_rule_count, rule_ptrs, paths[path].check
		);
		if (classify_decode(
			    memory_context,
			    paths[path].stage,
			    rule_ptrs,
			    acl_rule_count,
			    paths[path].rule_map
		    )) {
			free(rule_ptrs);
			acl_derive_paths_error(
				paths, path, memory_context, acl_rule_count
			);
			return -1;
		}

		free(rule_ptrs);
		classifier_fini(
			paths[path].stage, memory_context, acl_rule_count
		);
	}

	return 0;
}

// Compile the ruleset into a freshly initialized ACL module config.
//
// Static on purpose: the only entry point is acl_module_config_init,
// which owns the config's whole lifetime — a second update of a built
// config is not part of the module's contract.
static int
acl_module_compile_rules(
	struct cp_module *cp_module,
	struct acl_rule *acl_rules,
	uint32_t rule_count,
	const char *fw4_map_name,
	const char *fw6_map_name,
	yanet_error **err
) {
	struct acl_module_config *config =
		container_of(cp_module, struct acl_module_config, cp_module);

	for (uint64_t idx = 0; idx < rule_count; ++idx) {
		struct acl_rule *rule = acl_rules + idx;
		for (uint64_t dev_idx = 0; dev_idx < rule->devices.count;
		     ++dev_idx) {
			if (cp_module_link_device(
				    cp_module,
				    rule->devices.items[dev_idx].name,
				    &rule->devices.items[dev_idx].id,
				    err
			    )) {
				goto error;
			}
		}
	}

	struct acl_target *targets = NULL;
	if (rule_count > 0) {
		targets = (struct acl_target *)memory_balloc(
			&cp_module->memory_context,
			sizeof(struct acl_target) * rule_count
		);
		if (targets == NULL) {
			goto error;
		}
	}

	SET_OFFSET_OF(&config->targets, targets);
	config->target_count = rule_count;

	// Per-rule counters live in a dedicated "rules" registry so they are
	// separated from the module's predefined counters in the per-worker
	// storages and the counter storage registry.
	uint64_t rules_registry_idx;
	struct counter_registry *rules_registry = cp_module_counter_registry(
		cp_module, "rules", &rules_registry_idx, err
	);
	if (rules_registry == NULL) {
		goto error_target;
	}
	config->rules_registry_idx = rules_registry_idx;

	for (uint32_t idx = 0; idx < rule_count; ++idx) {
		struct acl_rule *acl_rule = acl_rules + idx;

		uint64_t action_count = acl_rule->action_count;
		if (action_count > ACL_MAX_ACTIONS) {
			/*
			 * Could not reach a terminal one action
			 */
			goto error_target;
		}
		for (uint64_t action_idx = 0; action_idx < action_count;
		     ++action_idx) {
			uint64_t *action = targets[idx].actions + action_idx;
			switch (acl_rule->actions[action_idx].kind) {
			case ACL_RULE_ACTION_KIND_ALLOW:
				*action = ACTION_ALLOW;
				break;
			case ACL_RULE_ACTION_KIND_DENY:
				*action = ACTION_DENY;
				break;
			case ACL_RULE_ACTION_KIND_COUNT:
				*action = ACTION_COUNT;
				break;
			case ACL_RULE_ACTION_KIND_CHECK_STATE:
				*action = ACTION_CHECK_STATE;
				break;
			case ACL_RULE_ACTION_KIND_CREATE_STATE:
				*action = ACTION_CREATE_STATE;
				break;
			case ACL_RULE_ACTION_KIND_LOG:
				*action = ACTION_LOG;
				break;
			default:
				goto error_target;
			}
		}
		targets[idx].action_count = action_count;

		const char *counter_name = acl_rule->counter;
		char default_counter[COUNTER_NAME_LEN];
		if (counter_name[0] == '\0') {
			snprintf(
				default_counter,
				sizeof(default_counter),
				"rule %d",
				idx
			);
			counter_name = default_counter;
		}
		if ((targets[idx].counter_id = counter_registry_register(
			     rules_registry, counter_name, 2, err
		     )) == (uint64_t)-1) {
			goto error_target;
		}
	}

	// The core class stages of the two families: every family build
	// fills the stage of its family through its root joint, and the
	// port scoped build of the same family joins onto it before the
	// stage is released.
	struct classifier core4_stage = {0};
	struct classifier plain4_stage = {0};
	struct classifier core6_stage = {0};
	struct classifier plain6_stage = {0};

	struct timespec ts_start, ts_end;
	clock_gettime(CLOCK_MONOTONIC, &ts_start);

	// Every build hands its final stage straight to its own
	// derivation, so a failed step leaves no stage behind.
	{
		struct classifier dev_stage = {0};
		if (acl_module_build_l2(
			    cp_module, acl_rules, rule_count, &dev_stage, err
		    ) ||
		    acl_module_derive_l2(
			    cp_module, &dev_stage, acl_rules, rule_count, err
		    )) {
			goto error_target;
		}
	}

	if (acl_module_build_ip4_classifier(
		    cp_module,
		    &core4_stage,
		    acl_rules,
		    rule_count,
		    &plain4_stage
	    ) ||
	    acl_module_derive_ip4(
		    cp_module, &plain4_stage, acl_rules, rule_count
	    )) {
		yanet_error_add(err, "failed to init filter_ip4");
		goto error_target;
	}

	{
		struct acl_path_filters_build path4_build = {
			.ports = &config->classifier_ports4,
			.tcp = &config->classifier_tcp4,
			.icmp = &config->classifier_icmp4,
			.tcp_root_joint = &config->filter_ip4_tcp.root_joint,
			.tcp_rule_map = &config->filter_ip4_tcp.rule_map,
			.tcp_rule_count = &config->filter_rule_count_ip4_tcp,
			.udp_root_joint = &config->filter_ip4_udp.root_joint,
			.udp_rule_map = &config->filter_ip4_udp.rule_map,
			.udp_rule_count = &config->filter_rule_count_ip4_udp,
			.icmp_root_joint = &config->filter_ip4_icmp.root_joint,
			.icmp_rule_map = &config->filter_ip4_icmp.rule_map,
			.icmp_rule_count = &config->filter_rule_count_ip4_icmp,
		};
		struct classifier tcp4_stage = {0};
		struct classifier udp4_stage = {0};
		struct classifier icmp4_stage = {0};
		if (acl_module_build_paths(
			    cp_module,
			    &core4_stage,
			    &path4_build,
			    acl_rules,
			    rule_count,
			    &tcp4_stage,
			    &udp4_stage,
			    &icmp4_stage
		    ) ||
		    acl_module_derive_paths(
			    cp_module,
			    &path4_build,
			    &tcp4_stage,
			    &udp4_stage,
			    &icmp4_stage,
			    acl_rules,
			    rule_count
		    )) {
			yanet_error_add(err, "failed to init the ip4 paths");
			goto error_target;
		}
	}

	if (acl_module_build_ip6_classifier(
		    cp_module,
		    &core6_stage,
		    acl_rules,
		    rule_count,
		    &plain6_stage
	    ) ||
	    acl_module_derive_ip6(
		    cp_module, &plain6_stage, acl_rules, rule_count
	    )) {
		yanet_error_add(err, "failed to init filter_ip6");
		goto error_target;
	}

	{
		struct acl_path_filters_build path6_build = {
			.ports = &config->classifier_ports6,
			.tcp = &config->classifier_tcp6,
			.icmp = &config->classifier_icmp6,
			.tcp_root_joint = &config->filter_ip6_tcp.root_joint,
			.tcp_rule_map = &config->filter_ip6_tcp.rule_map,
			.tcp_rule_count = &config->filter_rule_count_ip6_tcp,
			.udp_root_joint = &config->filter_ip6_udp.root_joint,
			.udp_rule_map = &config->filter_ip6_udp.rule_map,
			.udp_rule_count = &config->filter_rule_count_ip6_udp,
			.icmp_root_joint = &config->filter_ip6_icmp.root_joint,
			.icmp_rule_map = &config->filter_ip6_icmp.rule_map,
			.icmp_rule_count = &config->filter_rule_count_ip6_icmp,
		};
		struct classifier tcp6_stage = {0};
		struct classifier udp6_stage = {0};
		struct classifier icmp6_stage = {0};
		if (acl_module_build_paths(
			    cp_module,
			    &core6_stage,
			    &path6_build,
			    acl_rules,
			    rule_count,
			    &tcp6_stage,
			    &udp6_stage,
			    &icmp6_stage
		    ) ||
		    acl_module_derive_paths(
			    cp_module,
			    &path6_build,
			    &tcp6_stage,
			    &udp6_stage,
			    &icmp6_stage,
			    acl_rules,
			    rule_count
		    )) {
			yanet_error_add(err, "failed to init the ip6 paths");
			goto error_target;
		}
	}

	classifier_fini(&core4_stage, &cp_module->memory_context, rule_count);
	classifier_fini(&core6_stage, &cp_module->memory_context, rule_count);

	clock_gettime(CLOCK_MONOTONIC, &ts_end);
	config->compilation_time_ns =
		(uint64_t)((int64_t)(ts_end.tv_sec - ts_start.tv_sec) *
				   1000000000LL +
			   (ts_end.tv_nsec - ts_start.tv_nsec));

	// Link the fwstate-map objects whose fwtables back state lookups.
	// The module is freshly constructed, so no earlier links exist.
	config->v4_object_link_idx = ACL_OBJECT_LINK_NONE;
	config->v6_object_link_idx = ACL_OBJECT_LINK_NONE;

	if (fw4_map_name != NULL && fw4_map_name[0] != '\0') {
		if (cp_module_link_object(
			    cp_module,
			    FWSTATE_MAP_V4_OBJECT_TYPE,
			    fw4_map_name,
			    &config->v4_object_link_idx,
			    err
		    )) {
			goto error_target;
		}
	}

	if (fw6_map_name != NULL && fw6_map_name[0] != '\0') {
		if (cp_module_link_object(
			    cp_module,
			    FWSTATE_MAP_V6_OBJECT_TYPE,
			    fw6_map_name,
			    &config->v6_object_link_idx,
			    err
		    )) {
			goto error_target;
		}
	}

	return 0;

error_target:
	classifier_fini(&core4_stage, &cp_module->memory_context, rule_count);
	classifier_fini(&core6_stage, &cp_module->memory_context, rule_count);

	if (targets != NULL) {
		memory_bfree(
			&cp_module->memory_context,
			targets,
			sizeof(struct acl_target) * rule_count
		);
	}
	SET_OFFSET_OF(&config->targets, NULL);
	config->target_count = 0;

error:
	return -1;
}

void
acl_module_config_get_info(
	struct cp_module *cp_module, struct acl_config_info *info
) {
	struct acl_module_config *config =
		container_of(cp_module, struct acl_module_config, cp_module);

	info->compilation_time_ns = config->compilation_time_ns;
	info->filter_rule_count_l2 = config->filter_rule_count_l2;
	info->filter_rule_count_ip4 = config->filter_rule_count_ip4;
	info->filter_rule_count_ip4_tcp = config->filter_rule_count_ip4_tcp;
	info->filter_rule_count_ip4_udp = config->filter_rule_count_ip4_udp;
	info->filter_rule_count_ip4_icmp = config->filter_rule_count_ip4_icmp;
	info->filter_rule_count_ip6 = config->filter_rule_count_ip6;
	info->filter_rule_count_ip6_tcp = config->filter_rule_count_ip6_tcp;
	info->filter_rule_count_ip6_udp = config->filter_rule_count_ip6_udp;
	info->filter_rule_count_ip6_icmp = config->filter_rule_count_ip6_icmp;
}
