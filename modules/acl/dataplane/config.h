#pragma once

#include "controlplane/config/cp_module.h"

#include "filter/filter.h"
#include "lib/fwstate/config.h"

#define ACL_ACTION_ALLOW 0
#define ACL_ACTION_DENY 1
#define ACL_ACTION_COUNT 2
#define ACL_ACTION_CHECK_STATE 3
#define ACL_ACTION_CREATE_STATE 4

struct acl_target {
	uint64_t action;
	uint64_t counter_id;
};

// FIXME: make the structure private?
struct acl_module_config {
	struct cp_module cp_module;

	struct filter filter_ip4;
	struct filter filter_ip4_port;
	struct filter filter_ip6;
	struct filter filter_ip6_port;
	struct filter filter_vlan;

	uint64_t target_count;
	struct acl_target *targets;

	struct fwstate_config fwstate_cfg;

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
	uint64_t action_count_counter_id;
	uint64_t action_check_state_counter_id;
	uint64_t action_create_state_counter_id;
	uint64_t action_unknown_counter_id;
	uint64_t state_miss_counter_id;
	uint64_t sync_sent_counter_id;
};
