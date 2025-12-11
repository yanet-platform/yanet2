#pragma once

#include "controlplane/config/cp_module.h"
#include <stddef.h>
#include <time.h>

////////////////////////////////////////////////////////////////////////////////

struct my_module_config {
	struct cp_module cp_module;
	size_t packet_counter;
	uint64_t last_packet_timestamp;
};
