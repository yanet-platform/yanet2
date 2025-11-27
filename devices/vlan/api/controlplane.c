#include <errno.h>

#include "controlplane.h"

#include "config.h"

#include "common/container_of.h"

#include "controlplane/agent/agent.h"

struct cp_device *
cp_device_vlan_create(
	struct agent *agent, const struct cp_device_vlan_config *config
) {
	struct cp_device_vlan *cp_device_vlan =
		(struct cp_device_vlan *)memory_balloc(
			&agent->memory_context, sizeof(struct cp_device_vlan)
		);
	if (cp_device_vlan == NULL) {
		errno = ENOMEM;
		return NULL;
	}

	if (cp_device_init(
		    &cp_device_vlan->cp_device, agent, &config->cp_device_config
	    )) {
		goto fail;
	}

	cp_device_vlan->vlan = config->vlan;

	return &cp_device_vlan->cp_device;

fail: {
	int prev_errno = errno;
	cp_device_vlan_free(&cp_device_vlan->cp_device);
	errno = prev_errno;
	return NULL;
}
}

void
cp_device_vlan_free(struct cp_device *cp_device) {
	struct cp_device_vlan *cp_device_vlan =
		container_of(cp_device, struct cp_device_vlan, cp_device);

	struct agent *agent = ADDR_OF(&cp_device->agent);
	// FIXME: remove the check as agent should be assigned
	if (agent != NULL) {
		cp_device_destroy(&agent->memory_context, cp_device);
		memory_bfree(
			&agent->memory_context,
			cp_device_vlan,
			sizeof(struct cp_device_vlan)
		);
	}
}

struct cp_device_vlan_config *
cp_device_vlan_config_create(
	const char *name,
	uint64_t input_count,
	uint64_t output_count,
	uint16_t vlan
) {
	struct cp_device_vlan_config *cp_device_vlan_config =
		(struct cp_device_vlan_config *)malloc(
			sizeof(struct cp_device_vlan_config)
		);
	if (cp_device_vlan_config == NULL) {
		goto error;
	}

	if (cp_device_config_init(
		    &cp_device_vlan_config->cp_device_config,
		    "vlan",
		    name,
		    input_count,
		    output_count
	    )) {
		goto error_init;
	}

	cp_device_vlan_config->vlan = vlan;

	return cp_device_vlan_config;

error_init:
	free(cp_device_vlan_config);

error:
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
	free(cp_device_vlan_config);
}