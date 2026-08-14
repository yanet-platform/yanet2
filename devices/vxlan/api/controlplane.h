#pragma once

#include <stdint.h>

#include "lib/controlplane/config/cp_device.h"

struct agent;
struct cp_device;
struct yanet_error;

/*
 * Tunables of one vxlan device instance, copied verbatim into the
 * shared-memory configuration.
 *
 * vni and dst_port are in host byte order, the IP addresses in network
 * order, and the MAC addresses are raw wire bytes. pad keeps the layout
 * deterministic for CGO marshalling.
 */
struct cp_device_vxlan_settings {
	uint32_t vni;
	uint16_t dst_port;
	uint16_t pad;
	uint8_t src_mac[6];
	uint8_t dst_mac[6];
	uint32_t src_ip;
	uint32_t dst_ip;
};

struct cp_device_vxlan_config {
	struct cp_device_config cp_device_config;
	struct cp_device_vxlan_settings settings;
};

struct cp_device *
cp_device_vxlan_new(
	struct agent *agent,
	const struct cp_device_vxlan_config *config,
	yanet_error **err
);

void
cp_device_vxlan_free(struct cp_device *cp_device);

struct cp_device_vxlan_config *
cp_device_vxlan_config_new(
	const char *name,
	uint64_t input_count,
	uint64_t output_count,
	const struct cp_device_vxlan_settings *settings,
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
