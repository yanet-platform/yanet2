#include "controlplane.h"
#include "config.h"

#include "common/container_of.h"
#include "common/strutils.h"

struct balancer_real_config {
	uint64_t type;
	uint8_t dst_addr[16];
	uint8_t src_addr[16];
	uint8_t src_mask[16];
};

struct balancer_service_config {
	uint64_t type;
	uint8_t address[16];
	uint64_t real_count;
	struct balancer_real_config reals[];
};

struct module_data *
balancer_module_config_init(struct agent *agent, const char *name) {
	struct dp_config *dp_config = ADDR_OF(&agent->dp_config);
	uint64_t index;
	if (dp_config_lookup_module(dp_config, "balancer", &index)) {
		return NULL;
	}

	struct balancer_module_config *config =
		(struct balancer_module_config *)memory_balloc(
			&agent->memory_context,
			sizeof(struct balancer_module_config)
		);
	if (config == NULL)
		return NULL;

	config->module_data.index = index;
	strtcpy(config->module_data.name, name, sizeof(config->module_data.name)
	);
	memory_context_init_from(
		&config->module_data.memory_context,
		&agent->memory_context,
		name
	);

	struct memory_context *memory_context =
		&config->module_data.memory_context;
	lpm_init(&config->v4_service_lookup, memory_context);
	lpm_init(&config->v6_service_lookup, memory_context);

	return &config->module_data;
}

int
balancer_module_config_add_service(
	struct module_data *module_data, struct balancer_service_config *service
) {
	struct balancer_module_config *config = container_of(
		module_data, struct balancer_module_config, module_data
	);

	uint64_t real_start = config->real_count;

	struct balancer_rs *reals = ADDR_OF(&config->reals);

	for (uint64_t real_idx = 0; real_idx < service->real_count;
	     ++real_idx) {
		if (mem_array_expand_exp(
			    &config->module_data.memory_context,
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
	}

	SET_OFFSET_OF(&config->reals, reals);

	struct balancer_vs *services = ADDR_OF(&config->services);

	if (mem_array_expand_exp(
		    &config->module_data.memory_context,
		    (void **)&services,
		    sizeof(*services),
		    &config->service_count
	    )) {
		return -1;
	}

	services[config->service_count - 1].type = service->type;
	memcpy(services[config->service_count - 1].address, service->address, 16
	);
	services[config->service_count - 1].real_start = real_start;
	services[config->service_count - 1].real_count = service->real_count;
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

	SET_OFFSET_OF(&config->services, services);
	return 0;
}

struct balancer_service_config *
balancer_service_config_create(
	uint64_t type, uint8_t *address, uint64_t real_count
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
	free(config);
}

void
balancer_service_config_set_real(
	struct balancer_service_config *service_config,
	uint64_t index,
	uint64_t type,
	uint8_t *dst_addr,
	uint8_t *src_addr,
	uint8_t *src_mask
) {
	struct balancer_real_config *real_config =
		service_config->reals + index;
	real_config->type = type;
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
