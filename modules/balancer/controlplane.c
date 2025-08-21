#include "controlplane.h"
#include "config.h"

#include "common/container_of.h"
#include "common/exp_array.h"
#include "common/memory.h"
#include "common/strutils.h"

#include "dataplane/config/zone.h"

#include "controlplane/agent/agent.h"

struct balancer_real_config {
	uint64_t type;
	uint16_t weight;
	uint8_t dst_addr[16];
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
	uint64_t prefixes_count;
	struct balancer_src_prefix *prefixes;
	uint64_t real_count;
	struct balancer_real_config reals[];
};

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

	balancer_module_config_data_init(
		config, &config->cp_module.memory_context
	);

	return &config->cp_module;
}

void
balancer_module_config_data_init(
	struct balancer_module_config *config,
	struct memory_context *memory_context
) {
	lpm_init(&config->v4_service_lookup, memory_context);
	lpm_init(&config->v6_service_lookup, memory_context);
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
		lpm_free(&vs->src);
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

	lpm_free(&config->v4_service_lookup);
	lpm_free(&config->v6_service_lookup);

	memory_bfree(
		&agent->memory_context,
		config,
		sizeof(struct balancer_module_config)
	);
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

		reals[config->real_count - 1].type =
			service->reals[real_idx].type;
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

	balancer_service->type = service->type;
	memcpy(balancer_service->address, service->address, 16);
	balancer_service->real_start = real_start;
	balancer_service->real_count = service->real_count;
	if (service->type & VS_TYPE_V4) {
		lpm_insert(
			&config->v4_service_lookup,
			4,
			service->address,
			service->address,
			config->service_count - 1
		);
	} else if (service->type & VS_TYPE_V6) {
		lpm_insert(
			&config->v6_service_lookup,
			16,
			service->address,
			service->address,
			config->service_count - 1
		);
	}
	lpm_init(&balancer_service->src, &config->cp_module.memory_context);

	for (uint64_t prefix_idx = 0; prefix_idx < service->prefixes_count;
	     ++prefix_idx) {
		struct balancer_src_prefix prefix =
			service->prefixes[prefix_idx];
		if (service->type & VS_TYPE_V4) {
			lpm_insert(
				&balancer_service->src,
				4,
				prefix.start_addr,
				prefix.end_addr,
				1
			);
		} else if (service->type & VS_TYPE_V6) {
			lpm_insert(
				&balancer_service->src,
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
	uint64_t real_count,
	uint64_t prefixes_count
) {
	struct balancer_service_config *config =
		(struct balancer_service_config *)malloc(
			sizeof(struct balancer_service_config) +
			sizeof(struct balancer_real_config) * real_count
		);
	if (config == NULL)
		return NULL;
	memset(config,
	       0,
	       sizeof(struct balancer_service_config) +
		       sizeof(struct balancer_real_config) * real_count);

	config->prefixes = (struct balancer_src_prefix *)malloc(
		sizeof(struct balancer_src_prefix) * prefixes_count
	);
	if (config->prefixes == NULL)
		return NULL;
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
