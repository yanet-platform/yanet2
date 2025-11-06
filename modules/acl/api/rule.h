#pragma once

#include <assert.h>
#include <stddef.h>

#include <filter/rule.h>

////////////////////////////////////////////////////////////////////////////////

#define ACL_RULE_LOG_FLAG ((uint8_t)(1ull << 0))
#define ACL_RULE_KEEP_STATE_FLAG ((uint8_t)(1ull << 1))
#define ACL_RULE_NON_TERMINATE_FLAG ((uint8_t)(1ull << 2))

////////////////////////////////////////////////////////////////////////////////

typedef struct filter_rule acl_rule_t;

enum acl_action {
	acl_action_pass,
	acl_action_deny,
	acl_action_action_count,
	acl_action_check_state,
	/// @todo: add more actions here
	acl_actions_count, // number of actions, dont move
};

int
acl_rule_fill(
	acl_rule_t *rule,
	struct filter_net4 net4,
	struct filter_net6 net6,
	struct filter_transport transport,
	size_t device_count,
	const char **devices,
	enum acl_action action,
	uint8_t action_flags
);