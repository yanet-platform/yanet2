#include <errno.h>

#include "controlplane.h"

#include "config.h"

#include "common/container_of.h"

#include "controlplane/agent/agent.h"

struct cp_module *
forward_module_config_init(struct agent *agent, const char *name) {
	struct dp_config *dp_config = ADDR_OF(&agent->dp_config);

	uint64_t device_count = dp_config->dp_topology.device_count;
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

	if (cp_module_init(
		    &config->cp_module,
		    agent,
		    "forward",
		    name,
		    forward_module_config_free
	    )) {
		goto fail;
	}

	struct memory_context *memory_context =
		&config->cp_module.memory_context;

	config->device_count = device_count;

	// FIXME: Ensure that there are no more than UINT16_MAX devices.
	for (uint16_t dev_idx = 0; dev_idx < config->device_count; ++dev_idx) {
		struct forward_device_config *device_forward =
			config->device_forwards + dev_idx;
		// dev_idx is the source device ID. By default, there is no
		// forwarding between devices, and all incoming traffic to the
		// device goes back through the same device.
		device_forward->l2_dst_device_id = dev_idx;
		if (lpm_init(&device_forward->lpm_v4, memory_context)) {
			goto fail;
		}
		if (lpm_init(&device_forward->lpm_v6, memory_context)) {
			goto fail;
		}

		device_forward->target_count = 0;
		device_forward->targets = NULL;
		device_forward->l2_counter_id = -1;
	}

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

	struct agent *agent = ADDR_OF(&cp_module->agent);

	for (uint64_t device_idx = 0; device_idx < config->device_count;
	     ++device_idx) {
		struct forward_device_config *device_config =
			config->device_forwards + device_idx;
		lpm_free(&device_config->lpm_v4);
		lpm_free(&device_config->lpm_v6);

		memory_bfree(
			&agent->memory_context,
			ADDR_OF(&device_config->targets),
			sizeof(struct forward_target) *
				device_config->target_count
		);
	}

	if (agent != NULL) {
		memory_bfree(
			&agent->memory_context,
			config,
			sizeof(struct forward_module_config) +
				sizeof(struct forward_device_config) *
					config->device_count
		);
	}
}

static inline struct forward_target *
forward_module_new_target(
	struct memory_context *memory_context,
	struct forward_device_config *device_config
) {
	struct forward_target *old_targets = ADDR_OF(&device_config->targets);

	struct forward_target *new_targets =
		(struct forward_target *)memory_brealloc(
			memory_context,
			old_targets,
			sizeof(struct forward_target) *
				(device_config->target_count),
			sizeof(struct forward_target) *
				(device_config->target_count + 1)
		);

	if (new_targets == NULL)
		return NULL;

	SET_OFFSET_OF(&device_config->targets, new_targets);
	return new_targets + device_config->target_count++;
}

int
forward_module_config_enable_v4(
	struct cp_module *cp_module,
	const uint8_t *from,
	const uint8_t *to,
	uint16_t src_device_id,
	uint16_t dst_device_id,
	const char *counter_name
) {
	struct forward_module_config *config = container_of(
		cp_module, struct forward_module_config, cp_module
	);

	struct agent *agent = ADDR_OF(&cp_module->agent);

	if (src_device_id >= config->device_count) {
		errno = ENODEV;
		return -1;
	}
	if (dst_device_id >= config->device_count) {
		errno = ENODEV;
		return -1;
	}

	struct forward_device_config *device_config =
		config->device_forwards + src_device_id;

	struct forward_target *new_target = forward_module_new_target(
		&agent->memory_context, device_config
	);

	if (new_target == NULL)
		return -1;

	new_target->device_id = dst_device_id;
	new_target->counter_id = counter_registry_register(
		&cp_module->counters, counter_name, 2
	);

	return lpm_insert(
		&config->device_forwards[src_device_id].lpm_v4,
		4,
		from,
		to,
		device_config->target_count - 1
	);
}

int
forward_module_config_enable_v6(
	struct cp_module *cp_module,
	const uint8_t *from,
	const uint8_t *to,
	uint16_t src_device_id,
	uint16_t dst_device_id,
	const char *counter_name
) {
	struct forward_module_config *config = container_of(
		cp_module, struct forward_module_config, cp_module
	);

	struct agent *agent = ADDR_OF(&cp_module->agent);

	if (src_device_id >= config->device_count) {
		errno = ENODEV;
		return -1;
	}
	if (dst_device_id >= config->device_count) {
		errno = ENODEV;
		return -1;
	}

	struct forward_device_config *device_config =
		config->device_forwards + src_device_id;

	struct forward_target *new_target = forward_module_new_target(
		&agent->memory_context, device_config
	);

	if (new_target == NULL)
		return -1;

	new_target->device_id = dst_device_id;
	new_target->counter_id = counter_registry_register(
		&cp_module->counters, counter_name, 2
	);

	return lpm_insert(
		&config->device_forwards[src_device_id].lpm_v6,
		16,
		from,
		to,
		device_config->target_count - 1
	);
}

int
forward_module_config_enable_l2(
	struct cp_module *cp_module,
	uint16_t src_device_id,
	uint16_t dst_device_id,
	const char *counter_name
) {
	struct forward_module_config *config = container_of(
		cp_module, struct forward_module_config, cp_module
	);

	if (src_device_id >= config->device_count) {
		errno = ENODEV;
		return -1;
	}
	if (dst_device_id >= config->device_count) {
		errno = ENODEV;
		return -1;
	}

	config->device_forwards[src_device_id].l2_counter_id =
		counter_registry_register(
			&cp_module->counters, counter_name, 2
		);

	config->device_forwards[src_device_id].l2_dst_device_id = dst_device_id;

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
