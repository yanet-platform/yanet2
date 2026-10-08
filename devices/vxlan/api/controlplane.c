#include <stdlib.h>

#include "controlplane.h"

#include "lib/controlplane/agent/agent.h"

#include "lib/errors/errors.h"

// Destroy a vxlan device: base teardown, then the wrapper allocation.
static void
cp_device_vxlan_destroy(struct cp_device *cp_device) {
	struct agent *agent = ADDR_OF(&cp_device->agent);
	cp_device_fini(cp_device);
	memory_bfree(
		&agent->memory_context,
		cp_device,
		sizeof(struct cp_device_vxlan)
	);
}

struct cp_device *
cp_device_vxlan_new(
	struct agent *agent,
	const struct cp_device_vxlan_config *config,
	yanet_error **err
) {
	if (config->vxlan.vni > VXLAN_VNI_MAX) {
		yanet_error_add(
			err,
			"vxlan VNI %u does not fit 24 bits",
			config->vxlan.vni
		);
		return NULL;
	}

	struct cp_device_vxlan *cp_device_vxlan =
		(struct cp_device_vxlan *)memory_balloc(
			&agent->memory_context, sizeof(struct cp_device_vxlan)
		);
	if (cp_device_vxlan == NULL) {
		yanet_error_add(err, "memory allocation failed");
		return NULL;
	}

	memset(cp_device_vxlan, 0, sizeof(struct cp_device_vxlan));

	if (cp_device_init(
		    &cp_device_vxlan->cp_device,
		    agent,
		    &config->cp_device_config,
		    err
	    )) {
		memory_bfree(
			&agent->memory_context,
			cp_device_vxlan,
			sizeof(struct cp_device_vxlan)
		);
		return NULL;
	}

	cp_device_vxlan->config = config->vxlan;

	return &cp_device_vxlan->cp_device;
}

int
cp_device_vxlan_free(struct cp_device *cp_device, yanet_error **err) {
	if (cp_device_try_destroy(cp_device, err)) {
		return -1;
	}

	cp_device_vxlan_destroy(cp_device);
	return 0;
}

struct cp_device_vxlan_config *
cp_device_vxlan_config_new(
	const char *name,
	uint64_t input_count,
	uint64_t output_count,
	const struct vxlan_device_config *vxlan,
	yanet_error **err
) {
	struct cp_device_vxlan_config *cp_device_vxlan_config =
		(struct cp_device_vxlan_config *)malloc(
			sizeof(struct cp_device_vxlan_config)
		);
	if (cp_device_vxlan_config == NULL) {
		yanet_error_add(err, "memory allocation failed");
		return NULL;
	}

	if (cp_device_config_init(
		    &cp_device_vxlan_config->cp_device_config,
		    "vxlan",
		    name,
		    input_count,
		    output_count,
		    err
	    )) {
		free(cp_device_vxlan_config);
		return NULL;
	}

	cp_device_vxlan_config->vxlan = *vxlan;

	return cp_device_vxlan_config;
}

int
cp_device_vxlan_config_set_input_pipeline(
	struct cp_device_vxlan_config *cp_device_vxlan_config,
	uint64_t index,
	const char *name,
	uint64_t weight
) {
	return cp_device_config_set_input_pipeline(
		&cp_device_vxlan_config->cp_device_config, index, name, weight
	);
}

int
cp_device_vxlan_config_set_output_pipeline(
	struct cp_device_vxlan_config *cp_device_vxlan_config,
	uint64_t index,
	const char *name,
	uint64_t weight
) {
	return cp_device_config_set_output_pipeline(
		&cp_device_vxlan_config->cp_device_config, index, name, weight
	);
}

void
cp_device_vxlan_config_free(
	struct cp_device_vxlan_config *cp_device_vxlan_config
) {
	cp_device_config_fini(&cp_device_vxlan_config->cp_device_config);
	free(cp_device_vxlan_config);
}
