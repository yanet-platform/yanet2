#include <errno.h>

#include "controlplane.h"

#include "config.h"

#include "common/container_of.h"

#include "controlplane/agent/agent.h"

struct cp_module *
forward_module_config_init(struct agent *agent, const char *name) {
	struct forward_module_config *config =
		(struct forward_module_config *)memory_balloc(
			&agent->memory_context,
			sizeof(struct forward_module_config)
		);
	if (config == NULL) {
		errno = ENOMEM;
		return NULL;
	}

	if (cp_module_init(
		    &config->cp_module,
		    agent,
		    "forward",
		    name,
		    forward_module_config_free
	    )) {
		goto fail;
	}

	SET_OFFSET_OF(&config->devices, NULL);
	config->device_count = 0;

	return &config->cp_module;

fail: {
	int prev_errno = errno;
	forward_module_config_free(&config->cp_module);
	errno = prev_errno;
	return NULL;
}
}

void
forward_module_config_free(struct cp_module *cp_module) {
	struct forward_module_config *config = container_of(
		cp_module, struct forward_module_config, cp_module
	);

	struct forward_device_config **devices = ADDR_OF(&config->devices);
	for (uint64_t idx = 0; idx < config->device_count; ++idx) {
		struct forward_device_config *device = ADDR_OF(devices + idx);
		if (device == NULL)
			continue;

		lpm_free(&device->lpm_v4);
		lpm_free(&device->lpm_v6);
		memory_bfree(
			&cp_module->memory_context,
			ADDR_OF(&device->targets),
			sizeof(struct forward_target) * device->target_count
		);

		memory_bfree(
			&cp_module->memory_context,
			device,
			sizeof(struct forward_device_config)
		);
	}

	memory_bfree(
		&cp_module->memory_context,
		devices,
		sizeof(struct forward_device_config *) * config->device_count
	);

	struct agent *agent = ADDR_OF(&cp_module->agent);
	// FIXME: remove the check as agent should be assigned
	if (agent != NULL) {
		memory_bfree(
			&agent->memory_context,
			config,
			sizeof(struct forward_module_config)
		);
	}
}

static inline struct forward_target *
forward_module_new_target(
	struct memory_context *memory_context,
	struct forward_device_config *device
) {
	struct forward_target *old_targets = ADDR_OF(&device->targets);

	struct forward_target *new_targets =
		(struct forward_target *)memory_brealloc(
			memory_context,
			old_targets,
			sizeof(struct forward_target) * (device->target_count),
			sizeof(struct forward_target) *
				(device->target_count + 1)
		);

	if (new_targets == NULL)
		return NULL;

	SET_OFFSET_OF(&device->targets, new_targets);
	return new_targets + device->target_count++;
}

static struct forward_device_config *
forward_module_ensure_device(
	struct forward_module_config *config, uint64_t device_index
) {
	struct forward_device_config **devices = ADDR_OF(&config->devices);

	if (device_index >= config->device_count) {
		struct forward_device_config **new_devices =
			(struct forward_device_config **)memory_balloc(
				&config->cp_module.memory_context,
				sizeof(struct forward_device_config *) *
					(device_index + 1)
			);
		if (new_devices == NULL)
			NULL;

		memset(new_devices,
		       0,
		       sizeof(struct forward_device_config *) *
			       (device_index + 1));
		for (uint64_t idx = 0; idx < config->device_count; ++idx) {
			SET_OFFSET_OF(
				new_devices + idx, ADDR_OF(devices + idx)
			);
		}

		SET_OFFSET_OF(&config->devices, new_devices);
		memory_bfree(
			&config->cp_module.memory_context,
			devices,
			sizeof(struct forward_device_config *) *
				config->device_count
		);
		config->device_count = device_index + 1;

		devices = new_devices;
	}

	struct forward_device_config *device = ADDR_OF(devices + device_index);
	if (device != NULL)
		return device;

	device = (struct forward_device_config *)memory_balloc(
		&config->cp_module.memory_context,
		sizeof(struct forward_device_config)
	);
	if (device == NULL)
		return NULL;

	if (lpm_init(&device->lpm_v4, &config->cp_module.memory_context)) {
		goto error_lpm_v4;
	}

	if (lpm_init(&device->lpm_v6, &config->cp_module.memory_context)) {
		goto error_lpm_v6;
	}

	device->l2_target_id = LPM_VALUE_INVALID;
	device->target_count = 0;
	SET_OFFSET_OF(&device->targets, NULL);

	SET_OFFSET_OF(devices + device_index, device);

	return device;

error_lpm_v6:
	lpm_free(&device->lpm_v4);

error_lpm_v4:
	memory_bfree(
		&config->cp_module.memory_context,
		device,
		sizeof(struct forward_device_config)
	);
	return NULL;
}

int
forward_module_config_enable_v4(
	struct cp_module *cp_module,
	const uint8_t *from,
	const uint8_t *to,
	const char *src_name,
	const char *dst_name,
	const char *counter_name
) {
	struct forward_module_config *config = container_of(
		cp_module, struct forward_module_config, cp_module
	);

	uint64_t src_device_index;
	if (cp_module_link_device(cp_module, src_name, &src_device_index)) {
		return -1;
	}

	uint64_t dst_device_index;
	if (cp_module_link_device(cp_module, dst_name, &dst_device_index)) {
		return -1;
	}

	struct forward_device_config *device =
		forward_module_ensure_device(config, src_device_index);
	if (device == NULL)
		return -1;

	struct forward_target *new_target =
		forward_module_new_target(&cp_module->memory_context, device);

	if (new_target == NULL)
		return -1;

	new_target->device_id = dst_device_index;
	new_target->counter_id = counter_registry_register(
		&cp_module->counter_registry, counter_name, 2
	);

	return lpm_insert(
		&device->lpm_v4, 4, from, to, device->target_count - 1
	);
}

int
forward_module_config_enable_v6(
	struct cp_module *cp_module,
	const uint8_t *from,
	const uint8_t *to,
	const char *src_name,
	const char *dst_name,
	const char *counter_name
) {
	struct forward_module_config *config = container_of(
		cp_module, struct forward_module_config, cp_module
	);

	uint64_t src_device_index;
	if (cp_module_link_device(cp_module, src_name, &src_device_index)) {
		return -1;
	}

	uint64_t dst_device_index;
	if (cp_module_link_device(cp_module, dst_name, &dst_device_index)) {
		return -1;
	}

	struct forward_device_config *device =
		forward_module_ensure_device(config, src_device_index);
	if (device == NULL)
		return -1;

	struct forward_target *new_target =
		forward_module_new_target(&cp_module->memory_context, device);

	if (new_target == NULL)
		return -1;

	new_target->device_id = dst_device_index;
	new_target->counter_id = counter_registry_register(
		&cp_module->counter_registry, counter_name, 2
	);

	return lpm_insert(
		&device->lpm_v6, 16, from, to, device->target_count - 1
	);
}

int
forward_module_config_enable_l2(
	struct cp_module *cp_module,
	const char *src_name,
	const char *dst_name,
	const char *counter_name
) {
	struct forward_module_config *config = container_of(
		cp_module, struct forward_module_config, cp_module
	);

	uint64_t src_device_index;
	if (cp_module_link_device(cp_module, src_name, &src_device_index)) {
		return -1;
	}

	uint64_t dst_device_index;
	if (cp_module_link_device(cp_module, dst_name, &dst_device_index)) {
		return -1;
	}

	struct forward_device_config *device =
		forward_module_ensure_device(config, src_device_index);
	if (device == NULL)
		return -1;

	struct forward_target *new_target =
		forward_module_new_target(&cp_module->memory_context, device);

	if (new_target == NULL)
		return -1;

	new_target->device_id = dst_device_index;
	new_target->counter_id = counter_registry_register(
		&cp_module->counter_registry, counter_name, 2
	);

	device->l2_target_id = device->target_count - 1;

	return 0;
}

uint64_t
forward_module_topology_device_count(struct agent *agent) {
	struct dp_config *dp_config = ADDR_OF(&agent->dp_config);
	return dp_config->dp_topology.device_count;
}

int
forward_module_config_delete(struct cp_module *cp_module) {
	return agent_delete_module(
		cp_module->agent, "forward", cp_module->name
	);
}
