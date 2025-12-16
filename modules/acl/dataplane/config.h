#pragma once

#include "controlplane/config/zone.h"

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
};
