#include "vs.h"
#include "module.h"

#include "common/lpm.h"
#include "common/memory.h"

#include "../dataplane/module.h"
#include "../dataplane/real.h"
#include "../dataplane/vs.h"

#include "filter/filter.h"
#include "filter/rule.h"

#include <string.h>

////////////////////////////////////////////////////////////////////////////////

struct addr_range {
	uint8_t start_addr[16];
	uint8_t end_addr[16];
};

// Represents config of the virtual service
struct balancer_vs_config {
	vs_flags_t flags;
	uint8_t address[16];
	uint16_t port;
	uint8_t proto;
	size_t allowed_src_count;
	struct addr_range *allowed_src;
	size_t real_count;
	struct real reals[];
};

////////////////////////////////////////////////////////////////////////////////

static int
vs_v4_table_init(
	struct balancer_module_config *config,
	struct balancer_vs_config *vs_configs,
	size_t count
) {
	size_t ipv4_count = 0;
	for (size_t i = 0; i < count; ++i) {
		struct balancer_vs_config *vs_config = &vs_configs[i];
		if (!(vs_config->flags & VS_IPV6_FLAG)) {
			++ipv4_count;
		}
	}
	if (ipv4_count == 0) {
		return 0;
	}
	struct rule_holder {
		struct net4 vs_addr;
		struct filter_port_range vs_ports;
	};
	struct rule_holder *holders = memory_balloc(
		&config->cp_module.memory_context,
		sizeof(struct rule_holder) * ipv4_count
	);
	if (holders == NULL) {
		return -1;
	}
	struct filter_rule *rules = memory_balloc(
		&config->cp_module.memory_context,
		sizeof(struct filter_rule) * ipv4_count
	);
	if (rules == NULL) {
		goto free_holders;
	}
	for (size_t i = 0, j = 0; i < count; ++i) {
		struct balancer_vs_config *vs_config = &vs_configs[i];
		if (vs_config->flags & VS_IPV6_FLAG) {
			continue;
		}
		struct rule_holder *holder = &holders[j];
		struct filter_rule *rule = &rules[j];
		memcpy(holder->vs_addr.addr, vs_config, NET4_LEN);
		memset(holder->vs_addr.mask, 0xFF, NET4_LEN);
		if (vs_config->flags & VS_OPS_FLAG) {
			holder->vs_ports.from = 0;
			holder->vs_ports.to = -1;
		} else {
			holder->vs_ports.from = vs_config->port;
			holder->vs_ports.to = vs_config->port;
		}
		rule->action = j;
		rule->net4.dst_count = 1;
		rule->net4.dsts = &holder->vs_addr;
		rule->transport.proto =
			(struct filter_proto){.proto = vs_config->proto, 0, 0};
		rule->transport.dst_count = 1;
		rule->transport.dsts = &holder->vs_ports;
		++j;
	}

	int res = FILTER_INIT(
		&config->vs_v4_table,
		VS_V4_TABLE_TAG,
		rules,
		ipv4_count,
		&config->cp_module.memory_context
	);
	if (res < 0) {
		goto free_rules;
	}
	return 0;

free_rules:
	memory_bfree(
		&config->cp_module.memory_context,
		rules,
		sizeof(struct filter_rule) * ipv4_count
	);

free_holders:
	memory_bfree(
		&config->cp_module.memory_context,
		holders,
		sizeof(struct rule_holder) * ipv4_count
	);

	return -1;
}

static int
vs_v6_table_init(
	struct balancer_module_config *config,
	struct balancer_vs_config *vs_configs,
	size_t count
) {
	size_t ipv6_count = 0;
	for (size_t i = 0; i < count; ++i) {
		struct balancer_vs_config *vs_config = &vs_configs[i];
		if (vs_config->flags & VS_IPV6_FLAG) {
			++ipv6_count;
		}
	}
	if (ipv6_count == 0) {
		return 0;
	}
	struct rule_holder {
		struct net6 vs_addr;
		struct filter_port_range vs_ports;
	};
	struct rule_holder *holders = memory_balloc(
		&config->cp_module.memory_context,
		sizeof(struct rule_holder) * ipv6_count
	);
	if (holders == NULL) {
		return -1;
	}
	struct filter_rule *rules = memory_balloc(
		&config->cp_module.memory_context,
		sizeof(struct filter_rule) * ipv6_count
	);
	if (rules == NULL) {
		goto free_holders;
	}
	for (size_t i = 0, j = 0; i < count; ++i) {
		struct balancer_vs_config *vs_config = &vs_configs[i];
		if (!(vs_config->flags & VS_IPV6_FLAG)) {
			continue;
		}
		struct rule_holder *holder = &holders[j];
		struct filter_rule *rule = &rules[j];
		memcpy(holder->vs_addr.addr, vs_config, NET4_LEN);
		memset(holder->vs_addr.mask, 0xFF, NET4_LEN);
		if (vs_config->flags & VS_OPS_FLAG) {
			holder->vs_ports.from = 0;
			holder->vs_ports.to = -1;
		} else {
			holder->vs_ports.from = vs_config->port;
			holder->vs_ports.to = vs_config->port;
		}
		rule->action = j;
		rule->net6.dst_count = 1;
		rule->net6.dsts = &holder->vs_addr;
		rule->transport.proto =
			(struct filter_proto){.proto = vs_config->proto, 0, 0};
		rule->transport.dst_count = 1;
		rule->transport.dsts = &holder->vs_ports;
		++j;
	}

	int res = FILTER_INIT(
		&config->vs_v6_table,
		VS_V6_TABLE_TAG,
		rules,
		ipv6_count,
		&config->cp_module.memory_context
	);
	if (res < 0) {
		goto free_rules;
	}
	return 0;

free_rules:
	memory_bfree(
		&config->cp_module.memory_context,
		rules,
		sizeof(struct filter_rule) * ipv6_count
	);

free_holders:
	memory_bfree(
		&config->cp_module.memory_context,
		holders,
		sizeof(struct rule_holder) * ipv6_count
	);

	return -1;
}

int
balancer_vs_init(
	struct balancer_module_config *config,
	size_t vs_count,
	struct balancer_vs_config *vs_configs
) {
	size_t real_count = 0;
	for (size_t i = 0; i < vs_count; ++i) {
		real_count += vs_configs[i].real_count;
	}
	config->real_count = real_count;
	config->vs_count = vs_count;

	config->vs = memory_balloc(
		&config->cp_module.memory_context,
		config->vs_count * sizeof(struct virtual_service)
	);
	if (config->vs == NULL) {
		return -1;
	}

	config->reals = memory_balloc(
		&config->cp_module.memory_context,
		config->real_count * sizeof(struct real)
	);
	if (config->reals == NULL) {
		goto free_vs;
	}

	size_t real_idx = 0;

	size_t initialized_vs_count;
	for (initialized_vs_count = 0; initialized_vs_count < vs_count;
	     ++initialized_vs_count) {
		struct balancer_vs_config *vs_config =
			&vs_configs[initialized_vs_count];
		struct virtual_service *vs = &config->vs[initialized_vs_count];
		vs->flags = vs_config->flags;
		memcpy(vs->address, vs_config->address, NET6_LEN);
		vs->port = vs_config->port;
		vs->proto = vs_config->proto;
		vs->real_start = real_idx;
		vs->real_count = vs_config->real_count;
		int res = ring_init(
			&vs->real_ring,
			&config->cp_module.memory_context,
			vs->real_count
		);
		if (res < 0) {
			goto free_initalized_vs;
		}
		res = lpm_init(
			&vs->src_filter, &config->cp_module.memory_context
		);
		if (res < 0) {
			ring_free(&vs->real_ring);
			goto free_initalized_vs;
		}
		for (size_t i = 0; i < vs_config->allowed_src_count; ++i) {
			res = lpm_insert(
				&vs->src_filter,
				(vs->flags & VS_IPV6_FLAG) ? 16 : 4,
				vs_config->allowed_src[i].start_addr,
				vs_config->allowed_src[i].end_addr,
				1
			);
			if (res < 0) {
				ring_free(&vs->real_ring);
				lpm_free(&vs->src_filter);
				goto free_initalized_vs;
			}
		}
		memcpy(config->reals,
		       vs_config->reals,
		       sizeof(struct real) * vs->real_count);
		real_idx += vs->real_count;
	}

	// Init tables of virtual services

	int res = vs_v4_table_init(config, vs_configs, vs_count);
	if (res < 0) {
		goto free_initalized_vs;
	}

	res = vs_v6_table_init(config, vs_configs, vs_count);
	if (res < 0) {
		FILTER_FREE(&config->vs_v4_table, VS_V4_TABLE_TAG)
		goto free_initalized_vs;
	}

	return 0;

free_initalized_vs:
	for (size_t i = 0; i < initialized_vs_count; ++i) {
		struct virtual_service *vs = &config->vs[initialized_vs_count];
		ring_free(&vs->real_ring);
		lpm_free(&vs->src_filter);
	}

free_vs:
	memory_bfree(
		&config->cp_module.memory_context,
		config->vs,
		config->vs_count * sizeof(struct virtual_service)
	);

	return -1;
}
