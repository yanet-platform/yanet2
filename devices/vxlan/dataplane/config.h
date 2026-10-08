#pragma once

#include <stdint.h>

#include "lib/controlplane/config/cp_device.h"

// The UDP destination port IANA assigned to VXLAN.
#define VXLAN_UDP_PORT 4789

// The largest VXLAN network identifier: the field is 24 bits wide.
#define VXLAN_VNI_MAX ((UINT32_C(1) << 24) - 1)

// Tunnel parameters of one vxlan device, read by the Rust dataplane.
//
// The Rust device crate mirrors this layout and its build fails when the
// two disagree. Addresses are stored in network byte order, the VNI in
// host byte order. The body is frozen once the device is published.
struct vxlan_device_config {
	uint8_t local_mac[6];
	uint8_t remote_mac[6];
	uint8_t local_ip[4];
	uint8_t remote_ip[4];
	uint32_t vni;
};

struct cp_device_vxlan {
	struct cp_device cp_device;
	struct vxlan_device_config config;
};
