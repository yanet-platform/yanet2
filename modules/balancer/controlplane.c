#include "controlplane.h"
#include "common/memory_address.h"
#include "config.h"
#include "defines.h"

#include "common/exp_array.h"
#include "common/memory.h"

#include "dataplane/config/zone.h"

#include "controlplane/agent/agent.h"
#include "vs.h"

////////////////////////////////////////////////////////////////////////////////

struct balancer_real_config {
	uint64_t type;
	uint16_t weight;
	uint8_t dst_addr[16];

	// FIXME: why we need these two?
	uint8_t src_addr[16];
	uint8_t src_mask[16];
};

struct balancer_src_prefix {
	uint8_t start_addr[16];
	uint8_t end_addr[16];
};

struct balancer_service_config {
	uint64_t type;
	uint8_t address[16];
	struct balancer_vs_port_range *port_ranges;
	uint64_t port_range_count;
	uint64_t prefixes_count;
	struct balancer_src_prefix *prefixes;
	uint64_t real_count;
	struct balancer_real_config reals[];
};

static void
set_default_timeout_if_empty(uint32_t *value) {
	if (*value == 0) {
		*value = STATE_TIMEOUT_DEFAULT;
	}
}

static int
config_data_init(
	struct balancer_module_config *config,
	struct memory_context *mctx,
	size_t workers_cnt
) {
	set_default_timeout_if_empty(&config->state_config.tcp_syn_ack_timeout);
	set_default_timeout_if_empty(&config->state_config.tcp_syn_timeout);
	set_default_timeout_if_empty(&config->state_config.tcp_fin_timeout);
	set_default_timeout_if_empty(&config->state_config.tcp_timeout);
	set_default_timeout_if_empty(&config->state_config.udp_timeout);
	set_default_timeout_if_empty(&config->state_config.default_timeout);

	int ret = v4_vs_lookup_init(config, mctx, NULL, 0);
	if (ret < 0) {
		return -1;
	}
	ret = v6_vs_lookup_init(config, mctx, NULL, 0);
	if (ret < 0) {
		return -1;
	}
	return balancer_state_init(
		&config->state,
		workers_cnt,
		config->state_config.sessions_to_reserve,
		mctx
	);
}

struct cp_module *
balancer_module_config_init(struct agent *agent, const char *name) {
	struct balancer_module_config *config =
		(struct balancer_module_config *)memory_balloc(
			&agent->memory_context,
			sizeof(struct balancer_module_config)
		);
	if (config == NULL)
		return NULL;

	if (cp_module_init(
		    &config->cp_module,
		    agent,
		    "balancer",
		    name,
		    balancer_module_config_free
	    )) {
		memory_bfree(
			&agent->memory_context,
			config,
			sizeof(struct balancer_module_config)
		);

		return NULL;
	}

	config_data_init(
		config,
		&config->cp_module.memory_context,
		ADDR_OF(&agent->dp_config)->worker_count
	);

	return &config->cp_module;
}

int
balancer_module_config_update_real_weight(
	struct cp_module *cp_module,
	uint64_t service_idx,
	uint64_t real_idx,
	uint16_t weight
) {
	struct balancer_module_config *config = container_of(
		cp_module, struct balancer_module_config, cp_module
	);

	if (service_idx >= config->service_count) {
		return -1;
	}
	struct balancer_vs *service =
		ADDR_OF(&ADDR_OF(&config->services)[service_idx]);

	if (real_idx >= service->real_count) {
		return -1;
	}

	if (ring_change_weight(&service->real_ring, real_idx, weight)) {
		return -1;
	}
	ADDR_OF(&config->reals)[service->real_start + real_idx].weight = weight;
	return 0;
}

void
balancer_module_config_free(struct cp_module *cp_module) {
	struct balancer_module_config *config = container_of(
		cp_module, struct balancer_module_config, cp_module
	);

	struct agent *agent = ADDR_OF(&cp_module->agent);

	mem_array_free_exp(
		&agent->memory_context,
		ADDR_OF(&config->reals),
		sizeof(struct balancer_rs),
		config->real_count
	);

	for (uint64_t service_idx = 0; service_idx < config->service_count;
	     service_idx++) {
		struct balancer_vs **vs_ptr =
			ADDR_OF(&config->services) + service_idx;
		struct balancer_vs *vs = ADDR_OF(vs_ptr);
		lpm_free(&vs->src_filter);
		memory_bfree(
			&agent->memory_context, vs, sizeof(struct balancer_vs)
		);
	}

	mem_array_free_exp(
		&agent->memory_context,
		ADDR_OF(&config->services),
		sizeof(struct balancer_vs),
		config->service_count
	);

	v4_vs_lookup_free(config);
	v6_vs_lookup_free(config);

	balancer_state_free(&config->state);

	memory_bfree(
		&agent->memory_context,
		config,
		sizeof(struct balancer_module_config)
	);
}

void
balancer_module_config_set_state_config(
	struct cp_module *cp_module,
	uint32_t tcp_syn_ack_timeout,
	uint32_t tcp_syn_timeout,
	uint32_t tcp_fin_timeout,
	uint32_t tcp_timeout,
	uint32_t udp_timeout,
	uint32_t default_timeout,
	uint32_t sessions_to_reserve
) {
	struct balancer_module_config *config = container_of(
		cp_module, struct balancer_module_config, cp_module
	);

	config->state_config.tcp_syn_ack_timeout = tcp_syn_ack_timeout;
	config->state_config.tcp_syn_timeout = tcp_syn_timeout;
	config->state_config.tcp_fin_timeout = tcp_fin_timeout;
	config->state_config.tcp_timeout = tcp_timeout;
	config->state_config.udp_timeout = udp_timeout;
	config->state_config.default_timeout = default_timeout;
	config->state_config.sessions_to_reserve = sessions_to_reserve;
}

static int
build_v4_service_lookup(
	struct balancer_module_config *config, struct memory_context *mctx
) {
	struct balancer_vs **services = ADDR_OF(&config->services);
	size_t v4_service_count = 0;
	for (size_t i = 0; i < config->service_count; ++i) {
		struct balancer_vs *service = ADDR_OF(&services[i]);
		if (service->flags & VS_TYPE_V4) {
			++v4_service_count;
		}
	}
	struct filter_rule *rules = memory_balloc(
		mctx, sizeof(struct filter_rule) * v4_service_count
	);
	if (rules == NULL) {
		goto free_on_error;
	}

	size_t v4_service_index = 0;
	for (size_t i = 0; i < config->service_count; ++i) {
		struct balancer_vs *service = ADDR_OF(&services[i]);
		if (service->flags & VS_TYPE_V4) {
			struct filter_rule *rule = &rules[v4_service_index];
			rule->net4.dst_count = 1;
			rule->net4.dsts =
				memory_balloc(mctx, sizeof(struct net4));
			if (rule->net4.dsts == NULL) {
				goto free_on_error;
			}
			memcpy(rule->net4.dsts[0].addr, service->address, 4);
			memset(rule->net4.dsts[0].mask, 0xFF, 4);
			rule->transport.dst_count = 1;
			rule->transport.dsts = memory_balloc(
				mctx,
				sizeof(struct filter_port_range) *
					service->port_range_count
			);
			if (rule->transport.dsts == NULL) {
				goto free_on_error;
			}
			for (size_t k = 0; k < service->port_range_count; ++k) {
				rule->transport.dsts[k].from =
					service->port_ranges[k].from;
				rule->transport.dsts[k].to =
					service->port_ranges[k].to;
			}
			rule->action = v4_service_index;
			++v4_service_index;
		}
	}

	v4_vs_lookup_free(config);
	int ret = v4_vs_lookup_init(config, mctx, rules, v4_service_count);
	if (ret < 0) {
		goto free_on_error;
	}
	/// @todo: free
	return 0;
free_on_error:
	/// @todo: free
	return -1;
}

static int
build_v6_service_lookup(
	struct balancer_module_config *config, struct memory_context *mctx
) {
	struct balancer_vs **services = ADDR_OF(&config->services);
	size_t v6_service_count = 0;
	for (size_t i = 0; i < config->service_count; ++i) {
		struct balancer_vs *service = ADDR_OF(&services[i]);
		if (service->flags & VS_TYPE_V6) {
			++v6_service_count;
		}
	}
	struct filter_rule *rules = memory_balloc(
		mctx, sizeof(struct filter_rule) * v6_service_count
	);
	if (rules == NULL) {
		goto free_on_error;
	}

	size_t v6_service_index = 0;
	for (size_t i = 0; i < config->service_count; ++i) {
		struct balancer_vs *service = ADDR_OF(&services[i]);
		if (service->flags & VS_TYPE_V6) {
			struct filter_rule *rule = &rules[v6_service_index];
			rule->net6.dst_count = 1;
			rule->net6.dsts =
				memory_balloc(mctx, sizeof(struct net6));
			if (rule->net6.dsts == NULL) {
				goto free_on_error;
			}
			memcpy(rule->net6.dsts[0].addr, service->address, 16);
			memset(rule->net6.dsts[0].mask, 0xFF, 16);
			rule->transport.dst_count = 1;
			rule->transport.dsts = memory_balloc(
				mctx,
				sizeof(struct filter_port_range) *
					service->port_range_count
			);
			if (rule->transport.dsts == NULL) {
				goto free_on_error;
			}
			for (size_t k = 0; k < service->port_range_count; ++k) {
				rule->transport.dsts[k].from =
					service->port_ranges[k].from;
				rule->transport.dsts[k].to =
					service->port_ranges[k].to;
			}
			rule->action = v6_service_index;
			++v6_service_index;
		}
	}
	v6_vs_lookup_free(config);
	int ret = v6_vs_lookup_init(config, mctx, rules, v6_service_count);
	if (ret < 0) {
		goto free_on_error;
	}
	return 0;
free_on_error:
	/// @todo: free
	return -1;
}

int
balancer_module_config_add_service(
	struct cp_module *cp_module, struct balancer_service_config *service
) {
	struct balancer_module_config *config = container_of(
		cp_module, struct balancer_module_config, cp_module
	);

	uint64_t real_start = config->real_count;

	struct balancer_rs *reals = ADDR_OF(&config->reals);

	for (uint64_t real_idx = 0; real_idx < service->real_count;
	     ++real_idx) {
		if (mem_array_expand_exp(
			    &config->cp_module.memory_context,
			    (void **)&reals,
			    sizeof(*reals),
			    &config->real_count
		    )) {
			return -1;
		}

		reals[config->real_count - 1].flags =
			service->reals[real_idx].type;
		reals[config->real_count - 1].weight =
			service->reals[real_idx].weight;
		memcpy(reals[config->real_count - 1].dst_addr,
		       service->reals[real_idx].dst_addr,
		       16);
		memcpy(reals[config->real_count - 1].src_addr,
		       service->reals[real_idx].src_addr,
		       16);
		memcpy(reals[config->real_count - 1].src_mask,
		       service->reals[real_idx].src_mask,
		       16);
		for (uint8_t i = 0; i < 16; i++) {
			service->reals[real_idx].src_addr[i] &=
				service->reals[real_idx].src_mask[i];
		}
	}

	SET_OFFSET_OF(&config->reals, reals);

	struct balancer_vs **services = ADDR_OF(&config->services);

	for (uint64_t service_idx = 0; service_idx < config->service_count;
	     service_idx++) {
		services[service_idx] = ADDR_OF(&services[service_idx]);
	}

	if (mem_array_expand_exp(
		    &config->cp_module.memory_context,
		    (void **)&services,
		    sizeof(struct balancer_vs *),
		    &config->service_count
	    )) {
		return -1;
	}

	struct balancer_vs *balancer_service =
		(struct balancer_vs *)memory_balloc(
			&config->cp_module.memory_context,
			sizeof(struct balancer_vs)
		);

	if (balancer_service == NULL)
		return -1;

	if (ring_init(
		    &balancer_service->real_ring,
		    &config->cp_module.memory_context,
		    service->real_count
	    )) {
		return -1;
	}

	for (uint64_t real_idx = 0; real_idx < service->real_count;
	     ++real_idx) {
		if (ring_change_weight(
			    &balancer_service->real_ring,
			    real_idx,
			    service->reals[real_idx].weight
		    )) {
			return -1;
		}
	}
	services[config->service_count - 1] = balancer_service;

	for (uint64_t service_idx = 0; service_idx < config->service_count;
	     service_idx++) {
		SET_OFFSET_OF(&services[service_idx], services[service_idx]);
	}

	balancer_service->flags = service->type;
	memcpy(balancer_service->address, service->address, 16);
	balancer_service->real_start = real_start;
	balancer_service->real_count = service->real_count;
	if (service->type & VS_TYPE_V4) {
		build_v4_service_lookup(
			config, &config->cp_module.memory_context
		);
	} else if (service->type & VS_TYPE_V6) {
		build_v6_service_lookup(
			config, &config->cp_module.memory_context
		);
	}
	lpm_init(
		&balancer_service->src_filter, &config->cp_module.memory_context
	);

	for (uint64_t prefix_idx = 0; prefix_idx < service->prefixes_count;
	     ++prefix_idx) {
		struct balancer_src_prefix prefix =
			service->prefixes[prefix_idx];
		if (service->type & VS_TYPE_V4) {
			lpm_insert(
				&balancer_service->src_filter,
				4,
				prefix.start_addr,
				prefix.end_addr,
				1
			);
		} else if (service->type & VS_TYPE_V6) {
			lpm_insert(
				&balancer_service->src_filter,
				16,
				prefix.start_addr,
				prefix.end_addr,
				1
			);
		}
	}

	SET_OFFSET_OF(&config->services, services);
	return 0;
}

struct balancer_service_config *
balancer_service_config_create(
	uint64_t type,
	uint8_t *address,
	uint64_t port_range_count,
	uint64_t real_count,
	uint64_t prefixes_count
) {
	if (port_range_count == 0 || prefixes_count == 0) {
		return NULL;
	}

	struct balancer_service_config *config =
		(struct balancer_service_config *)malloc(
			sizeof(struct balancer_service_config) +
			sizeof(struct balancer_real_config) * real_count
		);
	if (config == NULL) {
		return NULL;
	}
	memset(config,
	       0,
	       sizeof(struct balancer_service_config) +
		       sizeof(struct balancer_real_config) * real_count);

	config->port_ranges = (struct balancer_vs_port_range *)malloc(
		sizeof(struct balancer_vs_port_range) * port_range_count
	);
	if (config->port_ranges == NULL) {
		return NULL;
	}
	for (size_t i = 0; i < port_range_count; ++i) {
		config->port_ranges[i].from = config->port_ranges[i].to = 0;
	}
	config->port_range_count = port_range_count;

	config->prefixes = (struct balancer_src_prefix *)malloc(
		sizeof(struct balancer_src_prefix) * prefixes_count
	);
	if (config->prefixes == NULL) {
		return NULL;
	}
	memset(config->prefixes,
	       0,
	       sizeof(struct balancer_src_prefix) * prefixes_count);
	config->prefixes_count = prefixes_count;

	config->type = type;
	if (type & VS_TYPE_V4) {
		memcpy(config->address, address, 4);
	} else if (type & VS_TYPE_V6) {
		memcpy(config->address, address, 16);
	}
	config->real_count = real_count;

	return config;
}

void
balancer_service_config_set_port_range(
	struct balancer_service_config *service_config,
	uint64_t index,
	uint16_t from,
	uint16_t to
) {
	service_config->port_ranges[index].from = from;
	service_config->port_ranges[index].to = to;
}

void
balancer_service_config_free(struct balancer_service_config *config) {
	free(config->prefixes);
	free(config);
}

void
balancer_service_config_set_real(
	struct balancer_service_config *service_config,
	uint64_t index,
	uint64_t type,
	uint16_t weight,
	uint8_t *dst_addr,
	uint8_t *src_addr,
	uint8_t *src_mask
) {
	struct balancer_real_config *real_config =
		service_config->reals + index;
	real_config->type = type;
	real_config->weight = weight;
	if (type & RS_TYPE_V4) {
		memcpy(real_config->dst_addr, dst_addr, 4);
		memcpy(real_config->src_addr, src_addr, 4);
		memcpy(real_config->src_mask, src_mask, 4);
	} else if (type & RS_TYPE_V6) {
		memcpy(real_config->dst_addr, dst_addr, 16);
		memcpy(real_config->src_addr, src_addr, 16);
		memcpy(real_config->src_mask, src_mask, 16);
	}
}

void
balancer_service_config_set_src_prefix(
	struct balancer_service_config *service_config,
	uint64_t index,
	uint8_t *start_addr,
	uint8_t *end_addr
) {
	struct balancer_src_prefix *src_prefix =
		service_config->prefixes + index;
	if (service_config->type & VS_TYPE_V6) {
		memcpy(src_prefix->start_addr, start_addr, 16);
		memcpy(src_prefix->end_addr, end_addr, 16);
	} else if (service_config->type & VS_TYPE_V4) {
		memcpy(src_prefix->start_addr, start_addr, 4);
		memcpy(src_prefix->end_addr, end_addr, 4);
	}
}
