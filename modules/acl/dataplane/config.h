#pragma once

#include "lib/controlplane/config/cp_module.h"

#include "lib/filter/classifiers/net6.h"
#include "lib/filter/filter.h"

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

struct acl_module_config {
	struct cp_module cp_module;

	struct filter filter_ip4;
	struct filter filter_ip4_port;
	struct filter filter_ip6;
	struct filter filter_ip6_port;
	struct filter filter_vlan;

	uint64_t target_count;
	struct acl_target *targets;

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

	// Shared v6 half-address classification for the two v6 filters.
	//
	// Built only when both filter_ip6 and filter_ip6_port compiled
	// non-empty, so a single union trie walk classifies the address
	// halves for both of them. Left all-zero, including a NULL
	// net6_share_src.remap_hi_a, when there is no shared classification
	// to use.
	struct net6_share_dir net6_share_src;
	struct net6_share_dir net6_share_dst;
};
