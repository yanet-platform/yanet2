#pragma once

#include <stdint.h>

struct dp_topology {
	uint64_t device_count;

	// Phy <-> Virt device mapping
	uint16_t *forward_map;
};
