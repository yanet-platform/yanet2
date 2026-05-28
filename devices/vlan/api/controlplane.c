#include <stdlib.h>

#include "controlplane.h"

#include "config.h"

#include "common/container_of.h"

#include "controlplane/agent/agent.h"

#include "lib/errors/errors.h"

struct cp_device *
cp_device_vlan_create(
	struct agent *agent,
	const struct cp_device_vlan_config *config,
	yanet_error **err
) {
	struct cp_device_vlan *cp_device_vlan =
		(struct cp_device_vlan *)memory_balloc(
			&agent->memory_context, sizeof(struct cp_device_vlan)
		);
	if (cp_device_vlan == NULL) {
		yanet_error_add(err, "memory allocation failed");
		return NULL;
	}

	memset(cp_device_vlan, 0, sizeof(struct cp_device_vlan));
	SET_OFFSET_OF(
		&cp_device_vlan->cp_device.parent_memory_context,
		&agent->memory_context
	);
	cp_device_vlan->cp_device.alloc_size = sizeof(struct cp_device_vlan);

	if (cp_device_init(
		    &cp_device_vlan->cp_device,
		    agent,
		    &config->cp_device_config,
		    err
	    )) {
		memory_bfree(
			&agent->memory_context,
			cp_device_vlan,
			sizeof(struct cp_device_vlan)
		);
		return NULL;
	}

	cp_device_vlan->vlan = config->vlan;

	return &cp_device_vlan->cp_device;
}

void
cp_device_vlan_free(struct cp_device *cp_device) {
	cp_device_fini(cp_device);
	cp_device_free(cp_device);
}

struct cp_device_vlan_config *
cp_device_vlan_config_create(
	const char *name,
	uint64_t input_count,
	uint64_t output_count,
	uint16_t vlan,
	yanet_error **err
) {
	struct cp_device_vlan_config *cp_device_vlan_config =
		(struct cp_device_vlan_config *)malloc(
			sizeof(struct cp_device_vlan_config)
		);
	if (cp_device_vlan_config == NULL) {
		yanet_error_add(err, "memory allocation failed");
		return NULL;
	}

	if (cp_device_config_init(
		    &cp_device_vlan_config->cp_device_config,
		    "vlan",
		    name,
		    input_count,
		    output_count,
		    err
	    )) {
		goto error_init;
	}

	cp_device_vlan_config->vlan = vlan;

	return cp_device_vlan_config;

error_init:
	free(cp_device_vlan_config);

	return NULL;
}

int
cp_device_vlan_config_set_input_pipeline(
	struct cp_device_vlan_config *cp_device_vlan_config,
	uint64_t index,
	const char *name,
	uint64_t weight
) {
	return cp_device_config_set_input_pipeline(
		&cp_device_vlan_config->cp_device_config, index, name, weight
	);
}

int
cp_device_vlan_config_set_output_pipeline(
	struct cp_device_vlan_config *cp_device_vlan_config,
	uint64_t index,
	const char *name,
	uint64_t weight
) {
	return cp_device_config_set_output_pipeline(
		&cp_device_vlan_config->cp_device_config, index, name, weight
	);
}

void
cp_device_vlan_config_free(struct cp_device_vlan_config *cp_device_vlan_config
) {
	cp_device_config_fini(&cp_device_vlan_config->cp_device_config);
	free(cp_device_vlan_config);
}
