#pragma once

#include <stddef.h>
#include <stdint.h>

#include "filter/rule.h"
#include "modules/balancer2/dataplane/types/session.h"

struct balancer_handle;
struct agent;
struct filter;

enum ipnet { ip4, ip6 };

struct filter *
balancer_compile_vs_matcher(
	struct agent *agent, struct filter_rule *rules, enum ipnet ipnet
);

struct balancer_handle *
balancer_create(
	struct agent *agent,
	const char *name,
	size_t vs_count,
	size_t st_capacity,
	struct filter *vs_ip4_matcher,
	struct filter *vs_ip6_matcher,
	struct balancer_session_timeouts *timeouts
);

struct filter *
balancer_vs_matcher(struct balancer_handle *balancer, enum ipnet ipnet);

int
balancer_install(struct agent *agent, struct balancer_handle *handle);

struct balancer_vs_handle;

struct filter *
balancer_compile_vs_acl(
	struct agent *agent, struct filter_rule *rules, enum ipnet ipnet
);

uint64_t *
balancer_register_acl_rules(
	struct agent *agent, const char **tags, size_t count
);

struct balancer_vs_handle *
balancer_create_vs(
	struct balancer_handle *handle,
	uint32_t idx,
	uint64_t id,
	uint16_t flags,
	uint64_t *rule_counters,
	struct filter *acl,
	size_t reals_count,
	uint32_t *real_weights
);

struct balancer_real_update {
	uint32_t weight;
	bool enabled;
};

int
balancer_vs_update_reals(
	struct balancer_vs_handle *vs,
	struct balancer_real_update *updates,
	size_t update_count
);

struct balancer_real_handle;

struct balancer_real_handle *
balancer_vs_create_real(
	struct balancer_vs_handle *vs,
	uint32_t idx,
	uint64_t id,
	uint16_t flags,
	struct net src,
	struct net_addr addr
);

uint64_t
balancer_real_active_sessions(
	struct balancer_real_handle *real, uint32_t *last_packet_timestamp
);

const char *balancer_vs_counter_prefix;
const char *balancer_vs_acl_counter_prefix;
const char *balancer_real_counter_prefix;
const char *balancer_common_counter_name;
const char *balancer_l4_counter_name;
