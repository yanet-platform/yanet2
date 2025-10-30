#pragma once

#include <stdint.h>

#include "controlplane/config/zone.h"

struct cp_device_vlan {
	struct cp_device cp_device;
	uint16_t vlan;
};
