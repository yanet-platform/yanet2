#include "controlplane.h"
#include "clock.h"
#include "common/memory_address.h"
#include "config.h"

#include "common/exp_array.h"
#include "common/memory.h"

#include "dataplane/config/zone.h"

#include "controlplane/agent/agent.h"
#include "rs_def.h"
#include "rule.h"
#include "session.h"
#include "state.h"
#include "vs.h"
#include "vs_def.h"

////////////////////////////////////////////////////////////////////////////////

struct balancer_real_config {
	uint64_t flags;
	uint16_t weight;
	uint8_t dst_addr[16];

	uint8_t src_addr[16];
	uint8_t src_mask[16];
};

struct balancer_src_prefix {
	uint8_t start_addr[16];
	uint8_t end_addr[16];
};

struct balancer_vs_config {
	uint64_t flags;
	uint8_t address[16];
	uint16_t port;
	uint8_t proto;
	uint64_t prefixes_count;
	struct balancer_src_prefix *prefixes;
	uint64_t real_count;
	struct balancer_real_config reals[];
};

////////////////////////////////////////////////////////////////////////////////

struct balancer_state *
balancer_state_init(struct agent *agent, size_t sessions_to_reserve) {
	const size_t align = alignof(struct balancer_state);
	uint8_t *memory = memory_balloc(
		&agent->memory_context, sizeof(struct balancer_state) + align
	);
	if (memory == NULL) {
		return NULL;
	}
	memory += (align - ((uintptr_t)memory) % align) % align;
	assert((uintptr_t)memory % align == 0);
	struct balancer_state *state = (struct balancer_state *)memory;
	SET_OFFSET_OF(&state->mctx, &agent->memory_context);
	state->current_gen = 0;
	state->workers_cnt = ADDR_OF(&agent->dp_config)->worker_count;
	int res = TTLMAP_INIT(
		&state->generations[0].session_table,
		&agent->memory_context,
		struct balancer_session_id,
		struct balancer_session_state,
		sessions_to_reserve
	);
	if (res != 0) {
		memory_bfree(
			&agent->memory_context,
			memory,
			sizeof(struct balancer_state) + align
		);
		return NULL;
	}
	for (size_t i = 0; i < state->workers_cnt; ++i) {
		struct worker_info *worker_info =
			&state->generations[0].worker_info[i];
		worker_info_init(worker_info);
	}
	clock_init(&state->clock);
	return state;
}

////////////////////////////////////////////////////////////////////////////////

void
balancer_state_free(struct balancer_state *state) {
	struct balancer_session_table_gen *cur_storage =
		balancer_get_cur_storage_gen(state);
	if (balancer_session_table_capacity(cur_storage) > 0) {
		TTLMAP_FREE(&cur_storage->session_table);
	}

	struct balancer_session_table_gen *prev_storage =
		balancer_get_prev_storage_gen(state);
	if (balancer_session_table_capacity(prev_storage) > 0) {
		TTLMAP_FREE(&prev_storage->session_table);
	}
}

////////////////////////////////////////////////////////////////////////////////

int
config_data_init(
	struct balancer_module_config *config,
	struct memory_context *mctx,
	struct balancer_state *state
) {
	config->services = NULL;
	config->vs_count = 0;

	config->real_count = 0;
	config->reals = NULL;

	int ret = balancer_vsv4_table_init(config, mctx, NULL, 0);
	if (ret < 0) {
		return -1;
	}

	ret = balancer_vsv6_table_init(config, mctx, NULL, 0);
	if (ret < 0) {
		return -1;
	}

	SET_OFFSET_OF(&config->state, state);
	return 0;
}

struct cp_module *
balancer_module_config_create(
	struct agent *agent, struct balancer_state *state, const char *name
) {
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

	config_data_init(config, &config->cp_module.memory_context, state);

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

	if (service_idx >= config->vs_count) {
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

	for (uint64_t service_idx = 0; service_idx < config->vs_count;
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
		config->vs_count
	);

	balancer_vsv4_table_free(config);
	v6_vs_lookup_free(config);

	memory_bfree(
		&agent->memory_context,
		config,
		sizeof(struct balancer_module_config)
	);
}

void
balancer_module_config_set_timeouts(
	struct cp_module *cp_module,
	uint32_t tcp_syn_ack_timeout,
	uint32_t tcp_syn_timeout,
	uint32_t tcp_fin_timeout,
	uint32_t tcp_timeout,
	uint32_t udp_timeout,
	uint32_t default_timeout
) {
	struct balancer_module_config *config = container_of(
		cp_module, struct balancer_module_config, cp_module
	);

	struct balancer_session_timeouts *timeouts = &config->timeouts;
	timeouts->tcp_syn_ack_timeout = tcp_syn_ack_timeout;
	timeouts->tcp_syn_timeout = tcp_syn_timeout;
	timeouts->tcp_fin_timeout = tcp_fin_timeout;
	timeouts->tcp_timeout = tcp_timeout;
	timeouts->udp_timeout = udp_timeout;
	timeouts->default_timeout = default_timeout;
}

static int
build_v4_service_lookup(
	struct balancer_module_config *config, struct memory_context *mctx
) {
	struct balancer_vs **services = ADDR_OF(&config->services);
	size_t v4_service_count = 0;
	for (size_t i = 0; i < config->vs_count; ++i) {
		struct balancer_vs *service = ADDR_OF(&services[i]);
		if (!(service->flags & BALANCER_VS_IPV6_FLAG)) {
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
	for (size_t i = 0; i < config->vs_count; ++i) {
		struct balancer_vs *service = ADDR_OF(&services[i]);
		if (!(service->flags & BALANCER_VS_IPV6_FLAG)) {
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
				mctx, sizeof(struct filter_port_range)
			);
			if (rule->transport.dsts == NULL) {
				goto free_on_error;
			}

			if (service->port == 0) {
				rule->transport.dsts[0] =
					(struct filter_port_range){0, 0xFFFF};
			} else {
				rule->transport.dsts[0] =
					(struct filter_port_range
					){service->port, service->port};
			}

			rule->transport.proto =
				(struct filter_proto){service->proto, 0, 0};

			rule->action = i;
			++v4_service_index;
		}
	}

	balancer_vsv4_table_free(config);
	int ret =
		balancer_vsv4_table_init(config, mctx, rules, v4_service_count);
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
	for (size_t i = 0; i < config->vs_count; ++i) {
		struct balancer_vs *service = ADDR_OF(&services[i]);
		if (service->flags & BALANCER_VS_IPV6_FLAG) {
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
	for (size_t i = 0; i < config->vs_count; ++i) {
		struct balancer_vs *service = ADDR_OF(&services[i]);
		if (service->flags & BALANCER_VS_IPV6_FLAG) {
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
				mctx, sizeof(struct filter_port_range)
			);
			if (service->port == 0) {
				rule->transport.dsts[0] =
					(struct filter_port_range){0, 0xFFFF};
			} else {
				rule->transport.dsts[0] =
					(struct filter_port_range
					){service->port, service->port};
			}

			rule->transport.proto =
				(struct filter_proto){service->proto, 0, 0};

			rule->action = i;
			++v6_service_index;
		}
	}
	v6_vs_lookup_free(config);
	int ret =
		balancer_vsv6_table_init(config, mctx, rules, v6_service_count);
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
	struct cp_module *cp_module, struct balancer_vs_config *service_config
) {
	struct balancer_module_config *config = container_of(
		cp_module, struct balancer_module_config, cp_module
	);

	uint64_t real_start = config->real_count;

	struct balancer_rs *reals = ADDR_OF(&config->reals);

	for (uint64_t real_idx = 0; real_idx < service_config->real_count;
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
			service_config->reals[real_idx].flags;
		reals[config->real_count - 1].weight =
			service_config->reals[real_idx].weight;
		memcpy(reals[config->real_count - 1].dst_addr,
		       service_config->reals[real_idx].dst_addr,
		       16);
		memcpy(reals[config->real_count - 1].src_addr,
		       service_config->reals[real_idx].src_addr,
		       16);
		memcpy(reals[config->real_count - 1].src_mask,
		       service_config->reals[real_idx].src_mask,
		       16);
		for (uint8_t i = 0; i < 16; i++) {
			service_config->reals[real_idx].src_addr[i] &=
				service_config->reals[real_idx].src_mask[i];
		}
	}

	SET_OFFSET_OF(&config->reals, reals);

	struct balancer_vs **services = ADDR_OF(&config->services);

	for (uint64_t service_idx = 0; service_idx < config->vs_count;
	     service_idx++) {
		services[service_idx] = ADDR_OF(&services[service_idx]);
	}

	if (mem_array_expand_exp(
		    &config->cp_module.memory_context,
		    (void **)&services,
		    sizeof(struct balancer_vs *),
		    &config->vs_count
	    )) {
		return -1;
	}

	SET_OFFSET_OF(&config->services, services);

	struct balancer_vs *service = (struct balancer_vs *)memory_balloc(
		&config->cp_module.memory_context, sizeof(struct balancer_vs)
	);

	if (service == NULL)
		return -1;

	memcpy(service->address, service_config->address, 16);
	service->port = service_config->port;
	service->proto = service_config->proto;
	service->flags = service_config->flags;

	service->real_count = service_config->real_count;
	service->real_start = real_start;

	if (ring_init(
		    &service->real_ring,
		    &config->cp_module.memory_context,
		    service_config->real_count
	    )) {
		return -1;
	}

	for (uint64_t real_idx = 0; real_idx < service_config->real_count;
	     ++real_idx) {
		if (ring_change_weight(
			    &service->real_ring,
			    real_idx,
			    service_config->reals[real_idx].weight
		    )) {
			return -1;
		}
	}
	services[config->vs_count - 1] = service;

	for (uint64_t service_idx = 0; service_idx < config->vs_count;
	     service_idx++) {
		SET_OFFSET_OF(&services[service_idx], services[service_idx]);
	}

	if (!(service_config->flags & BALANCER_VS_IPV6_FLAG)) {
		build_v4_service_lookup(
			config, &config->cp_module.memory_context
		);
	} else if (service_config->flags & BALANCER_VS_IPV6_FLAG) {
		build_v6_service_lookup(
			config, &config->cp_module.memory_context
		);
	}
	lpm_init(&service->src_filter, &config->cp_module.memory_context);

	for (uint64_t prefix_idx = 0;
	     prefix_idx < service_config->prefixes_count;
	     ++prefix_idx) {
		struct balancer_src_prefix prefix =
			service_config->prefixes[prefix_idx];
		if (!(service_config->flags & BALANCER_VS_IPV6_FLAG)) {
			lpm_insert(
				&service->src_filter,
				4,
				prefix.start_addr,
				prefix.end_addr,
				1
			);
		} else if (service_config->flags & BALANCER_VS_IPV6_FLAG) {
			lpm_insert(
				&service->src_filter,
				16,
				prefix.start_addr,
				prefix.end_addr,
				1
			);
		}
	}

	return 0;
}

struct balancer_vs_config *
balancer_service_config_create(
	balancer_vs_flags_t flags,
	uint8_t *address,
	uint16_t port,
	uint8_t proto,
	uint64_t real_count,
	uint64_t prefixes_count
) {
	if (prefixes_count == 0) {
		return NULL;
	}

	if ((flags & BALANCER_VS_PURE_L3_FLAG) || port == 0) {
		port = 0;
		flags |= BALANCER_VS_PURE_L3_FLAG;
	}

	struct balancer_vs_config *config = (struct balancer_vs_config *)malloc(
		sizeof(struct balancer_vs_config) +
		sizeof(struct balancer_real_config) * real_count
	);
	if (config == NULL) {
		return NULL;
	}
	memset(config,
	       0,
	       sizeof(struct balancer_vs_config) +
		       sizeof(struct balancer_real_config) * real_count);
	config->port = port;
	config->proto = proto;

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

	config->flags = flags;
	if (!(flags & BALANCER_VS_IPV6_FLAG)) {
		memcpy(config->address, address, 4);
	} else { // IPv6
		memcpy(config->address, address, 16);
	}
	config->real_count = real_count;

	return config;
}

void
balancer_service_config_free(struct balancer_vs_config *config) {
	free(config->prefixes);
	free(config);
}

void
balancer_service_config_set_real(
	struct balancer_vs_config *service_config,
	uint64_t index,
	balancer_rs_flags_t flags,
	uint16_t weight,
	uint8_t *dst_addr,
	uint8_t *src_addr,
	uint8_t *src_mask
) {
	struct balancer_real_config *real_config =
		service_config->reals + index;
	real_config->flags = flags;
	real_config->weight = weight;
	if (flags & BALANCER_RS_IPV6_FLAG) {
		for (size_t i = 0; i < 16; ++i) {
			src_addr[i] &= src_mask[i];
		}
		memcpy(real_config->dst_addr, dst_addr, 16);
		memcpy(real_config->src_addr, src_addr, 16);
		memcpy(real_config->src_mask, src_mask, 16);
	} else {
		for (size_t i = 0; i < 4; ++i) {
			src_addr[i] &= src_mask[i];
		}
		memcpy(real_config->dst_addr, dst_addr, 4);
		memcpy(real_config->src_addr, src_addr, 4);
		memcpy(real_config->src_mask, src_mask, 4);
	}
}

void
balancer_service_config_set_src_prefix(
	struct balancer_vs_config *service_config,
	uint64_t index,
	uint8_t *start_addr,
	uint8_t *end_addr
) {
	struct balancer_src_prefix *src_prefix =
		service_config->prefixes + index;
	if (service_config->flags & BALANCER_VS_IPV6_FLAG) {
		memcpy(src_prefix->start_addr, start_addr, 16);
		memcpy(src_prefix->end_addr, end_addr, 16);
	} else if (!(service_config->flags & BALANCER_VS_IPV6_FLAG)) {
		memcpy(src_prefix->start_addr, start_addr, 4);
		memcpy(src_prefix->end_addr, end_addr, 4);
	}
}

////////////////////////////////////////////////////////////////////////////////

void
balancer_module_config_update_current_time(struct cp_module *cp_module) {
	struct balancer_module_config *config = container_of(
		cp_module, struct balancer_module_config, cp_module
	);
	clock_update_time(&config->state->clock);
}