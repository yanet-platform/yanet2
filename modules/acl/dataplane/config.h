#pragma once

#include "lib/controlplane/config/cp_module.h"

#include "lib/classify/classify.h"
#include "lib/statemap/fwtable.h"

struct classifier;

struct counter_value_handle;

#define ACTION_ALLOW 0
#define ACTION_DENY 1
#define ACTION_COUNT 2
#define ACTION_CHECK_STATE 3
#define ACTION_CREATE_STATE 4
#define ACTION_LOG 5

#define ACL_MAX_ACTIONS 8

// Sentinel for "no object link at this slot". object_link_get_address
// returns NULL for any index >= object_link_count, so a config with no
// map link at this slot resolves to a NULL fwtable.
#define ACL_OBJECT_LINK_NONE UINT64_MAX

struct acl_target {
	// FIXME: use dynamic allocation
	uint64_t actions[ACL_MAX_ACTIONS];
	uint64_t action_count;
	uint64_t counter_id;
};

// Per-worker absolutes for the packet hot path, derived once per
// module execution context by the module's execution-context commit
// handler.
//
// The module counter addresses, the per-rule counter handle array and
// the linked state tables; the state tables are NULL for a family
// with no object link, in which case CHECK_STATE finds no state for
// that family.
struct acl_prepared {
	uint64_t *allow_cnt;
	uint64_t *deny_cnt;
	uint64_t *check_pass_cnt;
	uint64_t *check_miss_cnt;
	uint64_t *create_cnt;
	uint64_t *sync_cnt;
	uint64_t *invalid_cnt;
	uint64_t *non_term_cnt;
	uint64_t *no_match_cnt;
	struct counter_value_handle **rules_handles;
	fwtable_t *fw4table;
	fwtable_t *fw6table;
};

struct acl_module_config {
	struct cp_module cp_module;

	struct classify_filter filter_ip4;
	struct classify_filter filter_ip4_port;
	struct classify_filter filter_ip6;
	struct classify_filter filter_ip6_port;
	struct classify_filter filter_vlan;
	// Control plane only: the classifier trees the filters borrow their
	// tapes from, released after the filters at config destroy. The ip
	// classifiers cover the full ip signature over the union of both
	// projections of their family, and the port scoped filters join a
	// ports classifier onto them; the joins hold their own references.
	struct classifier *classifier_vlan;
	struct classifier *classifier_ip4;
	struct classifier *classifier_ip4_frag;
	struct classifier *classifier_ip4_port;
	struct classifier *classifier_ip6;
	struct classifier *classifier_ip6_frag;
	struct classifier *classifier_ip6_port;

	uint64_t target_count;
	struct acl_target *targets;
	// The targets array, as an absolute address for the packet hot
	// path.
	//
	// The commit handler copies it from the relative field above once
	// per published generation; the array is the config's own memory,
	// so the derivation is generation-invariant. It is zero until
	// then.
	struct acl_target *abs_targets;

	// Index of the per-rule "rules" counter registry within
	// cp_module.runtime_counter_registries. Each per-rule counter_id is
	// resolved against this registry's per-worker storage.
	uint64_t rules_registry_idx;

	// Object link indices for the v4 and v6 fwtables, declared by
	// acl_module_config_update via cp_module_link_object and resolved at
	// ectx build time into per-worker object_ectx entries. The
	// ACL_OBJECT_LINK_NONE sentinel marks an absent link, and CHECK_STATE
	// then finds no state for that family.
	uint64_t v4_object_link_idx;
	uint64_t v6_object_link_idx;
	// Metrics
	uint64_t compilation_time_ns;
	uint64_t filter_rule_count_ip4;
	uint64_t filter_rule_count_ip4_port;
	uint64_t filter_rule_count_ip6;
	uint64_t filter_rule_count_ip6_port;
	uint64_t filter_rule_count_vlan;

	// Module-level counters, registered by acl_module_config_init
	uint64_t no_match_counter_id;
	uint64_t action_allow_counter_id;
	uint64_t action_deny_counter_id;
	uint64_t action_check_pass_counter_id;
	uint64_t action_check_miss_counter_id;
	uint64_t action_create_state_counter_id;
	uint64_t action_invalid_counter_id;
	uint64_t action_non_term_counter_id;
	uint64_t sync_sent_counter_id;
};
