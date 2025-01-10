#pragma once

#include <stdint.h>

#include "common/network.h"

struct decap_module_config {
	uint32_t v4_prefix_count;
	struct net4 v4_prefixes;

	uint32_t v6_prefix_count;
	struct net6 v6_prefixes;
};
