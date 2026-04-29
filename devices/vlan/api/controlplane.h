#pragma once

#include <stdint.h>

#include "controlplane/config/cp_device.h"

struct agent;
struct cp_device;
struct yanet_error;

struct cp_device_vlan_config {
	struct cp_device_config cp_device_config;
	uint16_t vlan;
};

struct cp_device *
cp_device_vlan_create(
	struct agent *agent,
	const struct cp_device_vlan_config *config,
	yanet_error **err
);

void
cp_device_vlan_free(struct cp_device *cp_device);

struct cp_device_vlan_config *
cp_device_vlan_config_create(
	const char *name,
	uint64_t input_count,
	uint64_t output_count,
	uint16_t vlan,
	yanet_error **err
);

int
cp_device_vlan_config_set_input_pipeline(
	struct cp_device_vlan_config *cp_device_vlan_config,
	uint64_t index,
	const char *name,
	uint64_t weight
);

int
cp_device_vlan_config_set_output_pipeline(
	struct cp_device_vlan_config *cp_device_vlan_config,
	uint64_t index,
	const char *name,
	uint64_t weight
);

void
cp_device_vlan_config_free(struct cp_device_vlan_config *cp_device_vlan_config);
