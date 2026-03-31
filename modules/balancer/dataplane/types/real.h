#pragma once

#include <stdbool.h>

#include "common/network.h"

struct balancer_real {
	struct net_addr addr;
	struct net_addr src;
	bool enabled;
};