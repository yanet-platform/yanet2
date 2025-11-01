#include "vs.h"
#include "common/memory_address.h"
#include "common/network.h"
#include "module.h"

#include "common/lpm.h"
#include "common/memory.h"

#include "../dataplane/module.h"
#include "../dataplane/real.h"
#include "../dataplane/ring.h"
#include "../dataplane/vs.h"

#include "lib/controlplane/agent/agent.h"

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
	struct memory_context *mctx;
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
	struct balancer_vs_config **vs_configs,
	size_t count
) {
	size_t ipv4_count = 0;
	for (size_t i = 0; i < count; ++i) {
		struct balancer_vs_config *vs_config = vs_configs[i];
		if (!(vs_config->flags & BALANCER_VS_IPV6_FLAG)) {
			++ipv4_count;
		}
	}
	struct rule_holder {
		struct net4 vs_addr;
		struct filter_port_range vs_ports;
	};
	struct rule_holder *holders = memory_balloc(
		&config->cp_module.memory_context,
		sizeof(struct rule_holder) * ipv4_count
	);
	if (holders == NULL && ipv4_count > 0) {
		return -1;
	}
	struct filter_rule *rules = memory_balloc(
		&config->cp_module.memory_context,
		sizeof(struct filter_rule) * ipv4_count
	);
	if (rules == NULL && ipv4_count > 0) {
		goto free_holders;
	}
	for (size_t i = 0, j = 0; i < count; ++i) {
		struct balancer_vs_config *vs_config = vs_configs[i];
		if (vs_config->flags & BALANCER_VS_IPV6_FLAG) {
			continue;
		}
		struct rule_holder *holder = &holders[j];
		struct filter_rule *rule = &rules[j];
		rule->net4.dst_count = 1;
		rule->net4.dsts = &holder->vs_addr;
		memcpy(rule->net4.dsts[0].addr, vs_config->address, NET4_LEN);
		memset(rule->net4.dsts[0].mask, 0xFF, NET4_LEN);
		rule->transport.dst_count = 1;
		rule->transport.dsts = &holder->vs_ports;

		if (vs_config->flags & BALANCER_VS_PURE_L3_FLAG) {
			rule->transport.dsts[0] =
				(struct filter_port_range){0, 0xFFFF};
		} else {
			rule->transport.dsts[0] = (struct filter_port_range
			){vs_config->port, vs_config->port};
		}

		rule->transport.proto =
			(struct filter_proto){vs_config->proto, 0, 0};

		rule->action = i;
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
	struct balancer_vs_config **vs_configs,
	size_t count
) {
	size_t ipv6_count = 0;
	for (size_t i = 0; i < count; ++i) {
		struct balancer_vs_config *vs_config = vs_configs[i];
		if (vs_config->flags & BALANCER_VS_IPV6_FLAG) {
			++ipv6_count;
		}
	}
	struct rule_holder {
		struct net6 vs_addr;
		struct filter_port_range vs_ports;
	};
	struct rule_holder *holders = memory_balloc(
		&config->cp_module.memory_context,
		sizeof(struct rule_holder) * ipv6_count
	);
	if (holders == NULL && ipv6_count > 0) {
		return -1;
	}
	struct filter_rule *rules = memory_balloc(
		&config->cp_module.memory_context,
		sizeof(struct filter_rule) * ipv6_count
	);
	if (rules == NULL && ipv6_count > 0) {
		goto free_holders;
	}
	for (size_t i = 0, j = 0; i < count; ++i) {
		struct balancer_vs_config *vs_config = vs_configs[i];
		if (!(vs_config->flags & BALANCER_VS_IPV6_FLAG)) {
			continue;
		}
		struct rule_holder *holder = &holders[j];
		struct filter_rule *rule = &rules[j];
		rule->net6.dst_count = 1;
		rule->net6.dsts = &holder->vs_addr;
		memcpy(rule->net6.dsts[0].addr, vs_config->address, NET6_LEN);
		memset(rule->net6.dsts[0].mask, 0xFF, NET6_LEN);
		rule->transport.dst_count = 1;
		rule->transport.dsts = &holder->vs_ports;

		if (vs_config->flags & BALANCER_VS_PURE_L3_FLAG) {
			rule->transport.dsts[0] =
				(struct filter_port_range){0, 0xFFFF};
		} else {
			rule->transport.dsts[0] = (struct filter_port_range
			){vs_config->port, vs_config->port};
		}

		rule->transport.proto =
			(struct filter_proto){vs_config->proto, 0, 0};

		rule->action = i;
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
	struct balancer_vs_config **vs_configs
) {
	size_t real_count = 0;
	for (size_t i = 0; i < vs_count; ++i) {
		real_count += vs_configs[i]->real_count;
	}
	config->real_count = real_count;
	config->vs_count = vs_count;

	struct virtual_service *config_vs = memory_balloc(
		&config->cp_module.memory_context,
		config->vs_count * sizeof(struct virtual_service)
	);
	if (config_vs == NULL && vs_count > 0) {
		return -1;
	}
	SET_OFFSET_OF(&config->vs, config_vs);

	struct real *config_reals = memory_balloc(
		&config->cp_module.memory_context,
		config->real_count * sizeof(struct real)
	);
	if (config_reals == NULL && real_count > 0) {
		goto free_vs;
	}
	SET_OFFSET_OF(&config->reals, config_reals);

	size_t real_idx = 0;

	size_t initialized_vs_count;
	for (initialized_vs_count = 0; initialized_vs_count < vs_count;
	     ++initialized_vs_count) {
		struct balancer_vs_config *vs_config =
			vs_configs[initialized_vs_count];
		struct virtual_service *vs = &config_vs[initialized_vs_count];
		vs->round_robin_counter = 0;
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
		for (size_t real = 0; real < vs->real_count; ++real) {
			config_reals[real_idx + real] = vs_config->reals[real];
			res = ring_change_weight(
				&vs->real_ring,
				real,
				vs_config->reals[real].weight
			);
			if (res < 0) {
				goto free_initalized_vs;
			}
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
				(vs->flags & BALANCER_VS_IPV6_FLAG) ? 16 : 4,
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
		memcpy(config_reals,
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
		struct virtual_service *vs = &config_vs[i];
		ring_free(&vs->real_ring);
		lpm_free(&vs->src_filter);
	}

free_vs:
	memory_bfree(
		&config->cp_module.memory_context,
		config_vs,
		config->vs_count * sizeof(struct virtual_service)
	);

	return -1;
}

////////////////////////////////////////////////////////////////////////////////

struct balancer_vs_config *
balancer_vs_config_create(
	struct agent *agent,
	uint64_t flags,
	uint8_t *ip,
	uint16_t port,
	uint8_t proto,
	size_t real_count,
	size_t allowed_src_count
) {
	if ((flags & BALANCER_VS_PURE_L3_FLAG) || port == 0) {
		port = 0;
		flags |= BALANCER_VS_PURE_L3_FLAG;
	}

	uint8_t *memory = memory_balloc(
		&agent->memory_context,
		sizeof(struct balancer_vs_config) +
			sizeof(struct real) * real_count +
			sizeof(struct addr_range) * allowed_src_count
	);
	if (memory == NULL) {
		return NULL;
	}
	struct balancer_vs_config *vs_config =
		(struct balancer_vs_config *)memory;
	vs_config->mctx = &agent->memory_context;
	vs_config->real_count = real_count;
	vs_config->allowed_src_count = allowed_src_count;
	vs_config->allowed_src =
		(struct addr_range *)(memory +
				      sizeof(struct balancer_vs_config) +
				      sizeof(struct real) * real_count);
	vs_config->flags = (vs_flags_t)flags;
	if (vs_config->flags & BALANCER_VS_IPV6_FLAG) {
		memcpy(vs_config->address, ip, NET6_LEN);
	} else {
		memcpy(vs_config->address, ip, NET4_LEN);
	}
	if (vs_config->flags & BALANCER_VS_PURE_L3_FLAG) {
		vs_config->port = 0;
	} else {
		vs_config->port = port;
	}
	vs_config->proto = proto;
	return vs_config;
}

////////////////////////////////////////////////////////////////////////////////

void
balancer_vs_config_free(struct balancer_vs_config *vs_config) {
	memory_bfree(
		vs_config->mctx,
		vs_config,
		sizeof(struct balancer_vs_config) +
			sizeof(struct real) * vs_config->real_count +
			sizeof(struct addr_range) * vs_config->allowed_src_count
	);
}

////////////////////////////////////////////////////////////////////////////////

void
balancer_vs_config_set_real(
	struct balancer_vs_config *vs_config,
	size_t index,
	uint64_t flags,
	uint16_t weight,
	uint8_t *dst_addr,
	uint8_t *src_addr,
	uint8_t *src_mask
) {
	struct real *real = &vs_config->reals[index];
	real->flags = (real_flags_t)flags;
	real->weight = weight;
	size_t len =
		(real->flags & BALANCER_REAL_IPV6_FLAG) ? NET6_LEN : NET4_LEN;
	memcpy(real->dst_addr, dst_addr, len);
	memcpy(real->src_addr, src_addr, len);
	memcpy(real->src_mask, src_mask, len);
	for (size_t i = 0; i < len; ++i) {
		real->src_addr[i] &= real->src_mask[i];
	}
}

void
balancer_vs_config_set_allowed_src_range(
	struct balancer_vs_config *vs_config,
	size_t index,
	uint8_t *from,
	uint8_t *to
) {
	struct addr_range *addr_range = &vs_config->allowed_src[index];
	size_t len = (vs_config->flags & BALANCER_VS_IPV6_FLAG) ? NET6_LEN
								: NET4_LEN;
	memcpy(addr_range->start_addr, from, len);
	memcpy(addr_range->end_addr, to, len);
}
