#pragma once

#include <stdint.h>

#include "devices/vxlan/dataplane/config.h"
#include "lib/controlplane/config/cp_device.h"

struct agent;
struct cp_device;
struct yanet_error;

struct cp_device_vxlan_config {
	struct cp_device_config cp_device_config;
	struct vxlan_device_config vxlan;
};

// Create a dangling vxlan device in the agent's memory.
//
// Returns NULL with an error when the VNI does not fit 24 bits or an
// allocation fails.
struct cp_device *
cp_device_vxlan_new(
	struct agent *agent,
	const struct cp_device_vxlan_config *config,
	yanet_error **err
);

// Destroy the device when it is dangling, per cp_device_try_destroy.
//
// Returns -1 with errno EAGAIN while a live generation still references
// the device; the caller must keep its handle and retry later.
int
cp_device_vxlan_free(struct cp_device *cp_device, yanet_error **err);

struct cp_device_vxlan_config *
cp_device_vxlan_config_new(
	const char *name,
	uint64_t input_count,
	uint64_t output_count,
	const struct vxlan_device_config *vxlan,
	yanet_error **err
);

int
cp_device_vxlan_config_set_input_pipeline(
	struct cp_device_vxlan_config *cp_device_vxlan_config,
	uint64_t index,
	const char *name,
	uint64_t weight
);

int
cp_device_vxlan_config_set_output_pipeline(
	struct cp_device_vxlan_config *cp_device_vxlan_config,
	uint64_t index,
	const char *name,
	uint64_t weight
);

void
cp_device_vxlan_config_free(
	struct cp_device_vxlan_config *cp_device_vxlan_config
);
