#pragma once

#include "api/vs.h"
#include <stddef.h>

struct vs_state {
	struct vs_identifier identifier;
	char pad[40];
	size_t registry_idx; // index of the virtual service in the registry
};
