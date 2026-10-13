#pragma once

#include <stdint.h>

#include "lib/controlplane/config/zone.h"

/*
 * Shared-memory configuration of one vxlan device instance.
 *
 * vni and dst_port are in host byte order, the IP addresses in network
 * order, and the MAC addresses are raw wire bytes. pad keeps the field
 * layout deterministic for CGO marshalling.
 */
struct cp_device_vxlan {
	struct cp_device cp_device;
	uint32_t vni;
	uint16_t dst_port;
	uint16_t pad;
	uint8_t src_mac[6];
	uint8_t dst_mac[6];
	uint32_t src_ip;
	uint32_t dst_ip;
};
