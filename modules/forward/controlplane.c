#include <errno.h>

#include "controlplane.h"

#include "config.h"

#include "common/container_of.h"
#include "common/strutils.h"

#include "controlplane/agent/agent.h"

struct module_data *
forward_module_config_init(
	struct agent *agent, const char *name, uint16_t device_count
) {
	struct dp_config *dp_config = ADDR_OF(&agent->dp_config);
	uint64_t index;
	if (dp_config_lookup_module(dp_config, "forward", &index)) {
		errno = ENXIO;
		return NULL;
	}

	if (device_count > dp_config->dp_topology.device_count) {
		errno = EINVAL;
		return NULL;
	}

	struct forward_module_config *config =
		(struct forward_module_config *)memory_balloc(
			&agent->memory_context,
			sizeof(struct forward_module_config
			) + sizeof(struct forward_device_config) * device_count
		);
	if (config == NULL) {
		errno = ENOMEM;
		return NULL;
	}

	config->module_data.index = index;
	strtcpy(config->module_data.name, name, sizeof(config->module_data.name)
	);
	memory_context_init_from(
		&config->module_data.memory_context,
		&agent->memory_context,
		name
	);
	SET_OFFSET_OF(&config->module_data.agent, agent);
	config->module_data.free_handler = forward_module_config_free;

	struct memory_context *memory_context =
		&config->module_data.memory_context;

	config->device_count = device_count;

	for (uint16_t dev_idx = 0; dev_idx < device_count; ++dev_idx) {
		struct forward_device_config *device_forward =
			config->device_forwards + dev_idx;
		device_forward->l2_forward_device_id = dev_idx;
		if (lpm_init(&device_forward->lpm_v4, memory_context)) {
			goto fail;
		}
		if (lpm_init(&device_forward->lpm_v6, memory_context)) {
			goto fail;
		}
	}

	return &config->module_data;

fail: {
	int prev_errno = errno;
	forward_module_config_free(&config->module_data);
	errno = prev_errno;
	return NULL;
}
}

void
forward_module_config_free(struct module_data *module_data) {
	struct forward_module_config *config = container_of(
		module_data, struct forward_module_config, module_data
	);

	for (uint64_t device_idx = 0; device_idx < config->device_count;
	     ++device_idx) {
		struct forward_device_config *device_config =
			config->device_forwards + device_idx;
		lpm_free(&device_config->lpm_v4);
		lpm_free(&device_config->lpm_v6);
	}

	struct agent *agent = ADDR_OF(&module_data->agent);
	memory_bfree(
		&agent->memory_context,
		config,
		sizeof(struct forward_module_config
		) + sizeof(struct forward_device_config) * config->device_count
	);
}

int
forward_module_config_enable_v4(
	struct module_data *module_data,
	const uint8_t *from,
	const uint8_t *to,
	uint16_t src_device_id,
	uint16_t dst_device_id
) {
	struct forward_module_config *config = container_of(
		module_data, struct forward_module_config, module_data
	);

	if (src_device_id >= config->device_count) {
		errno = ENODEV;
		return -1;
	}
	if (dst_device_id >= config->device_count) {
		errno = ENODEV;
		return -1;
	}

	return lpm_insert(
		&config->device_forwards[src_device_id].lpm_v4,
		4,
		from,
		to,
		dst_device_id
	);
}

int
forward_module_config_enable_v6(
	struct module_data *module_data,
	const uint8_t *from,
	const uint8_t *to,
	uint16_t src_device_id,
	uint16_t dst_device_id
) {
	struct forward_module_config *config = container_of(
		module_data, struct forward_module_config, module_data
	);

	if (src_device_id >= config->device_count) {
		errno = ENODEV;
		return -1;
	}
	if (dst_device_id >= config->device_count) {
		errno = ENODEV;
		return -1;
	}

	return lpm_insert(
		&config->device_forwards[src_device_id].lpm_v6,
		16,
		from,
		to,
		dst_device_id
	);
}

int
forward_module_config_enable_l2(
	struct module_data *module_data,
	uint16_t src_device_id,
	uint16_t dst_device_id
) {
	struct forward_module_config *config = container_of(
		module_data, struct forward_module_config, module_data
	);

	if (src_device_id >= config->device_count) {
		errno = ENODEV;
		return -1;
	}
	if (dst_device_id >= config->device_count) {
		errno = ENODEV;
		return -1;
	}

	config->device_forwards[src_device_id].l2_forward_device_id =
		dst_device_id;
	return 0;
}
