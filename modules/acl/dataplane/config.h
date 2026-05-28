#pragma once

#include "controlplane/config/cp_module.h"

#include "filter/filter.h"
#include "lib/fwstate/config.h"

#define ACTION_ALLOW 0
#define ACTION_DENY 1
#define ACTION_COUNT 2
#define ACTION_CHECK_STATE 3
#define ACTION_CREATE_STATE 4
#define ACTION_LOG 5

#define ACL_MAX_ACTIONS 8

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
	uint64_t action_check_pass_counter_id;
	uint64_t action_check_miss_counter_id;
	uint64_t action_create_state_counter_id;
	uint64_t action_invalid_counter_id;
	uint64_t action_non_term_counter_id;
	uint64_t sync_sent_counter_id;
};
