#pragma once

#include <stdbool.h>
#include <stdint.h>

#include "lib/classify/classify.h"
#include "lib/classify/rule.h"

#include "lib/counters/counters.h"

#include "lib/errors/errors.h"

enum acl_rule_action_kind {
	ACL_RULE_ACTION_KIND_ALLOW,
	ACL_RULE_ACTION_KIND_DENY,
	ACL_RULE_ACTION_KIND_COUNT,
	ACL_RULE_ACTION_KIND_CHECK_STATE,
	ACL_RULE_ACTION_KIND_CREATE_STATE,
	ACL_RULE_ACTION_KIND_LOG
};

struct agent;
struct cp_module;
// Destroy the module when it is dangling, per cp_module_try_destroy.
//
// Returns -1 with errno EAGAIN while a live generation still references
// the module; the caller must keep its handle and retry later.
int
acl_module_config_free(struct cp_module *cp_module, yanet_error **err);

struct acl_action {
	enum acl_rule_action_kind kind;
};

struct acl_rule {
	struct classifier_rule rule;

	struct acl_action *actions;
	uint64_t action_count;

	char counter[COUNTER_NAME_LEN];

	struct filter_devices devices;

	struct filter_net4s src_net4s;
	struct filter_net4s dst_net4s;

	struct filter_net6s src_net6s;
	struct filter_net6s dst_net6s;

	// The derived protocol line intervals: the authored 16 bit
	// protocol ranges carry the IP protocol number in the high byte
	// and the transport specific byte in the low one, and the control
	// plane that authors the rules splits them - the protocol number
	// spans for the shared core, the low byte sub-spans of TCP for the
	// flags leaf and of ICMP and ICMPv6 for the shared type leaf - so
	// the compile consumes the intervals without the authored
	// encoding.
	struct classify_line_ranges ipproto_ranges;
	struct classify_line_ranges tcp_flags_ranges;
	struct classify_line_ranges icmp_type_ranges;

	// The family path membership decided alongside the interval
	// split: the plain path takes the rules matching the packets
	// without a transport header (fragments of a whole transport
	// byte), the protocol paths take the rules that discriminate
	// through their leaves. The union of the tcp and the udp flags
	// spells the rules the shared ports pair compiles over.
	bool path_plain;
	bool path_tcp;
	bool path_udp;
	bool path_icmp;

	struct filter_port_ranges src_port_ranges;
	struct filter_port_ranges dst_port_ranges;

	enum filter_ip_fragment fragment;
};

// Allocate the ACL module config and compile the rules into it in one
// step: a config handle is fully built at construction and never updated
// afterwards.
//
// rules and rule_count describe the ruleset to compile (rule_count may be
// zero). fw4_map_name and fw6_map_name name standalone fwstate_map_v4 /
// fwstate_map_v6 objects whose fwtables the module uses for state
// lookups. Either may be NULL or empty, in which case no link is
// declared and CHECK_STATE finds no state for that family.
// Returns NULL with err set on failure; nothing is left allocated.
struct cp_module *
acl_module_config_init(
	struct agent *agent,
	const char *name,
	struct acl_rule *rules,
	uint32_t rule_count,
	const char *fw4_map_name,
	const char *fw6_map_name,
	yanet_error **err
);

struct acl_config_info {
	uint64_t compilation_time_ns;
	uint64_t filter_rule_count_l2;
	uint64_t filter_rule_count_ip4;
	uint64_t filter_rule_count_ip4_tcp;
	uint64_t filter_rule_count_ip4_udp;
	uint64_t filter_rule_count_ip4_icmp;
	uint64_t filter_rule_count_ip6;
	uint64_t filter_rule_count_ip6_tcp;
	uint64_t filter_rule_count_ip6_udp;
	uint64_t filter_rule_count_ip6_icmp;
};

void
acl_module_config_get_info(
	struct cp_module *cp_module, struct acl_config_info *info
);
